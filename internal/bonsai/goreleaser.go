package bonsai

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrNoGoreleaserConfig is returned when no .goreleaser.yaml/.yml exists in the directory, so
// callers can distinguish "no goreleaser here" (degrade to a normal build) from a present-but-
// broken config (a real error).
var ErrNoGoreleaserConfig = errors.New("no .goreleaser.yaml or .goreleaser.yml")

// goreleaser support: derive the build matrix (and per-cell tags/env/flags) from a project's
// .goreleaser.yaml instead of hand-declaring it. Each goreleaser `builds` entry expands to its
// GOOS×GOARCH product (minus `ignore`, or its explicit `targets`), and the build's tags/env/flags
// ride those cells. Templated values ({{ .Version }} etc.) are replaced with a dummy, since bonsai
// only needs which files compile and which modules link, not the real version string.

// GoreleaserMatrix is the result of importing a .goreleaser.yaml: the union of build cells across
// every `builds` entry, plus the target package and how many builds contributed (so the caller can
// note when several were merged).
type GoreleaserMatrix struct {
	Cells  []Platform
	Target string // package to build, derived from the first build's dir/main
	Builds int    // number of goreleaser builds that contributed cells
	File   string // the .goreleaser file that was read
}

// glConfig is the subset of a goreleaser config bonsai reads.
type glConfig struct {
	Builds []glBuild `yaml:"builds"`
}

type glBuild struct {
	Main    string       `yaml:"main"`
	Dir     string       `yaml:"dir"`
	Goos    []string     `yaml:"goos"`
	Goarch  []string     `yaml:"goarch"`
	Targets []string     `yaml:"targets"`
	Ignore  []glIgnore   `yaml:"ignore"`
	Tags    glStringList `yaml:"tags"`
	Flags   glStringList `yaml:"flags"`
	Ldflags glStringList `yaml:"ldflags"`
	Env     []string     `yaml:"env"`
}

type glIgnore struct {
	Goos   string `yaml:"goos"`
	Goarch string `yaml:"goarch"`
}

// glStringList accepts a goreleaser field that may be written as a single (possibly multi-line
// block) string or as a YAML sequence — ldflags and flags both allow either form.
type glStringList []string

func (s *glStringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		*s = glStringList{value.Value}
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*s = list
	default:
		// a mapping (or anything else) here is a misconfigured goreleaser file; surface it rather
		// than silently treating the field as empty.
		return fmt.Errorf("line %d: expected a string or list of strings", value.Line)
	}
	return nil
}

// glTemplate matches a goreleaser/Go template action so it can be swapped for a dummy.
var glTemplate = regexp.MustCompile(`\{\{.*?\}\}`)

// dedummy replaces every template action in s with a dummy value. bonsai never needs the real
// substitution (a version string doesn't change which modules link), and a literal go build flag
// like -X main.version={{.Version}} would otherwise fail to parse.
func dedummy(s string) string {
	return glTemplate.ReplaceAllString(s, "0")
}

// FromGoreleaser reads dir's .goreleaser.yaml and derives the build matrix from it. Cells are the
// union of every build's GOOS×GOARCH (minus ignores) or explicit targets, each carrying that
// build's tags/env/flags. Returns an error when no goreleaser file or no builds are found.
func FromGoreleaser(dir string) (GoreleaserMatrix, error) {
	if dir == "" {
		dir = "."
	}
	file, data, err := readGoreleaser(dir)
	if err != nil {
		return GoreleaserMatrix{}, err
	}
	var cfg glConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return GoreleaserMatrix{}, fmt.Errorf("parsing %s: %w", file, err)
	}
	if len(cfg.Builds) == 0 {
		return GoreleaserMatrix{}, fmt.Errorf("%s declares no builds", file)
	}

	var cells []Platform
	seen := map[string]bool{}
	target := ""
	for _, b := range cfg.Builds {
		if target == "" {
			target = glTarget(b.Dir, b.Main)
		}
		for _, c := range cellsForBuild(b) {
			if k := platformKey(c); !seen[k] {
				seen[k] = true
				cells = append(cells, c)
			}
		}
	}
	if len(cells) == 0 {
		return GoreleaserMatrix{}, fmt.Errorf("%s produced no build cells", file)
	}
	return GoreleaserMatrix{Cells: cells, Target: target, Builds: len(cfg.Builds), File: file}, nil
}

// HostBuild selects the build settings for a single (non-matrix) host build from the imported
// matrix: the tags/env/flags of the cell matching goos/goarch — a build's flags are uniform across
// its GOOS×GOARCH cells, so this is that build's settings — falling back to the first cell when
// goreleaser doesn't target this host. Returns those settings plus the derived build target, so the
// single-build subjects (check/diff/anatomy/...) build the way the project ships.
func (g GoreleaserMatrix) HostBuild(goos, goarch string) (BuildSettings, string) {
	pick := g.Cells[0] // FromGoreleaser guarantees at least one cell
	for _, c := range g.Cells {
		if c.GOOS == goos && c.GOARCH == goarch {
			pick = c
			break
		}
	}
	return BuildSettings{Tags: pick.Tags, Env: pick.Env, Args: pick.Args}, g.Target
}

// readGoreleaser finds and reads the goreleaser config in dir, trying the standard filenames.
func readGoreleaser(dir string) (string, []byte, error) {
	for _, name := range []string{".goreleaser.yaml", ".goreleaser.yml"} {
		p := filepath.Join(dir, name)
		if data, err := os.ReadFile(p); err == nil {
			return p, data, nil
		}
	}
	return "", nil, fmt.Errorf("%w in %s", ErrNoGoreleaserConfig, dir)
}

// cellsForBuild expands one goreleaser build into its platform cells, attaching the build's
// tags/env/flags to each. Explicit `targets` win; otherwise it's the GOOS×GOARCH product minus
// `ignore`. A build that names neither falls back to a sensible default OS/arch set.
func cellsForBuild(b glBuild) []Platform {
	tags := dedummyTags(b.Tags)
	env := parseEnv(b.Env)
	args := buildArgs(b.Flags, b.Ldflags)

	mk := func(goos, goarch string) Platform {
		return Platform{GOOS: goos, GOARCH: goarch, Tags: tags, Env: env, Args: args}
	}

	if len(b.Targets) > 0 {
		var cells []Platform
		for _, t := range b.Targets {
			parts := strings.Split(t, "_")
			if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
				cells = append(cells, mk(parts[0], parts[1]))
			}
		}
		return cells
	}

	goos := b.Goos
	if len(goos) == 0 {
		goos = []string{"linux", "darwin", "windows"}
	}
	goarch := b.Goarch
	if len(goarch) == 0 {
		goarch = []string{"amd64", "arm64"}
	}
	var cells []Platform
	for _, o := range goos {
		for _, a := range goarch {
			if ignored(b.Ignore, o, a) {
				continue
			}
			cells = append(cells, mk(o, a))
		}
	}
	return cells
}

// ignored reports whether GOOS/GOARCH matches any of the build's ignore rules. A rule with an
// empty field matches any value for that field (e.g. ignore all of one GOOS).
func ignored(rules []glIgnore, goos, goarch string) bool {
	for _, r := range rules {
		if (r.Goos == "" || r.Goos == goos) && (r.Goarch == "" || r.Goarch == goarch) {
			return true
		}
	}
	return false
}

// dedummyTags template-replaces each build tag (tags are rarely templated, but be uniform).
func dedummyTags(tags []string) []string {
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		// a single `tags` entry can itself be space-separated.
		for f := range strings.FieldsSeq(dedummy(t)) {
			out = append(out, f)
		}
	}
	return out
}

// parseEnv turns goreleaser's "KEY=VALUE" env list into a map, template-dummying each value.
func parseEnv(env []string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if k = strings.TrimSpace(k); ok && k != "" {
			out[k] = dedummy(v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildArgs renders a goreleaser build's flags + ldflags as a freeform args string for `go build`.
// ldflags are flattened (block scalars carry newlines) and wrapped in a single -ldflags="..."
// token so splitArgs keeps them together; bonsai's own -ldflags=-dumpdep still wins at link time,
// so these only document intent. Templates are dummied so a -X ...={{.Version}} parses.
func buildArgs(flags, ldflags glStringList) string {
	var parts []string
	for _, f := range flags {
		parts = append(parts, strings.Fields(dedummy(f))...)
	}
	var ld []string
	for _, l := range ldflags {
		ld = append(ld, strings.Fields(dedummy(l))...)
	}
	out := strings.Join(parts, " ")
	if len(ld) > 0 {
		if out != "" {
			out += " "
		}
		out += `-ldflags="` + strings.Join(ld, " ") + `"`
	}
	return out
}

// glTarget derives the package to build from a goreleaser build's dir and main. dir is the build's
// working directory and main the package within it; bonsai builds `go build <target>` from the
// module root, so the combined relative path is the target.
func glTarget(dir, main string) string {
	dir, main = strings.TrimSpace(dir), strings.TrimSpace(main)
	switch {
	case main == "":
		return dir
	case dir == "" || dir == ".":
		return main
	default:
		j := path.Join(dir, main)
		if strings.HasPrefix(dir, "./") && !strings.HasPrefix(j, "./") {
			j = "./" + j
		}
		return j
	}
}

// platformKey is a stable identity for a cell, used to dedup the union across builds.
func platformKey(p Platform) string {
	envKeys := make([]string, 0, len(p.Env))
	for k := range p.Env {
		envKeys = append(envKeys, k+"="+p.Env[k])
	}
	sort.Strings(envKeys)
	return strings.Join([]string{
		p.GOOS, p.GOARCH,
		strings.Join(sortedTags(p.Tags), ","),
		strings.Join(envKeys, ","),
		p.Args,
	}, "|")
}
