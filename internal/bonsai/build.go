package bonsai

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// staleTempAge is how old a leftover build temp must be before a later run reclaims it. Generous
// so the sweep never races a concurrent build or removes a warm MCP server's artifacts before
// they're loaded — by this age the file is long done being written and already parsed into memory.
const staleTempAge = time.Hour

var sweepOnce sync.Once

// sweepStaleTemps best-effort removes bonsai build temps left behind by interrupted or killed runs
// (a SIGKILL or panic-exit skips the deferred cleanup, so files accumulate). Age-gated and run once
// per process, just before the first build.
func sweepStaleTemps() {
	sweepOnce.Do(func() { sweepTempsIn(os.TempDir(), time.Now().Add(-staleTempAge)) })
}

// sweepTempsIn removes bonsai-bin-*/bonsai-dumpdep-* files in dir whose mtime is before cutoff.
// Split out from sweepStaleTemps so the age/prefix logic is testable without the process-wide Once.
func sweepTempsIn(dir string, cutoff time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "bonsai-bin-") && !strings.HasPrefix(name, "bonsai-dumpdep-") {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// buildArtifacts is the output of building the analysis target from source: the compiled
// (unstripped) binary plus the linker's symbol-dependency dump captured from `-dumpdep`.
type buildArtifacts struct {
	Binary  string // path to the compiled unstripped binary
	Dumpdep string // path to the captured `-ldflags=-dumpdep` output (post-DCE reachability)
	Command string // human-readable build command (GOOS/GOARCH + user flags), for the progress UI
}

// buildForAnalysis compiles target in dir into a temporary unstripped binary and captures
// the linker's `-dumpdep` symbol-reachability graph alongside it. Building ourselves means
// we always have matching source + binary, never need to locate a checkout or rebuild a
// stripped artifact, and get the exact post-dead-code-elimination reference graph the
// linker actually used. p selects the build cell (GOOS/GOARCH/tags); b carries persisted build
// defaults (env, extra tags, freeform args). Zero values are the host toolchain. Returns the
// artifacts and a cleanup func that removes both temps.
func buildForAnalysis(dir, target string, p Platform, b BuildSettings) (buildArtifacts, func(), error) {
	sweepStaleTemps() // reclaim temps from earlier interrupted/killed runs before adding our own
	binF, err := os.CreateTemp("", "bonsai-bin-*")
	if err != nil {
		return buildArtifacts{}, func() {}, err
	}
	binF.Close()
	ddF, err := os.CreateTemp("", "bonsai-dumpdep-*")
	if err != nil {
		os.Remove(binF.Name())
		return buildArtifacts{}, func() {}, err
	}
	cleanup := func() {
		os.Remove(binF.Name())
		os.Remove(ddF.Name())
	}

	// -dumpdep writes the symbol dependency graph to stderr during the link step; capture
	// it to a file. On a build failure the link step never runs, so stderr holds only the
	// compiler error, which we surface for diagnostics.
	// user args first, then bonsai's own -o/-ldflags=-dumpdep/-tags so they can't be clobbered:
	// the analysis build must stay unstripped and emit the dumpdep graph regardless of what the
	// user persisted. as a result -tags/-o/-ldflags inside b.Args are overridden — put build tags
	// in the tags/build.tags field, not args.
	args := []string{"build"}
	args = append(args, splitArgs(b.Args)...)
	args = append(args, splitArgs(p.Args)...)
	args = append(args, "-o", binF.Name(), "-ldflags=-dumpdep")
	if tags := tagsArg(effectiveTags(b, p)); tags != "" {
		args = append(args, tags)
	}
	args = append(args, target)
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = platformEnv(p, b)
	cmd.Stderr = ddF
	runErr := cmd.Run()
	ddF.Close()
	if runErr != nil {
		msg := tailFile(ddF.Name(), 4096)
		cleanup()
		return buildArtifacts{}, func() {}, fmt.Errorf("building %s in %s: %w\n%s", target, dir, runErr, msg)
	}

	return buildArtifacts{Binary: binF.Name(), Dumpdep: ddF.Name(), Command: formatBuildCommand(p, b, args)}, cleanup, nil
}

// formatBuildCommand renders the exact `go build` invocation bonsai ran, for the progress UI and
// debugging: the effective GOOS/GOARCH (host values when the cell leaves them blank) and env
// overrides as a prefix, then the literal argv — including bonsai's own -o/-ldflags=-dumpdep and
// the user's overridden -ldflags, so what you see is what ran. args is the slice passed to `go`.
func formatBuildCommand(p Platform, b BuildSettings, args []string) string {
	goos, goarch := p.GOOS, p.GOARCH
	if goos == "" {
		goos = runtime.GOOS
	}
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	parts := []string{"GOOS=" + goos, "GOARCH=" + goarch}

	env := map[string]string{}
	maps.Copy(env, b.Env) // global first, then cell wins (matches platformEnv)
	maps.Copy(env, p.Env)
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, k+"="+env[k])
	}

	parts = append(parts, "go")
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}

// tailFile returns up to the last limit bytes of the file at path (build diagnostics live at
// the end). Best-effort: returns "" if the file can't be read.
func tailFile(path string, limit int) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > limit {
		data = data[len(data)-limit:]
	}
	return strings.TrimSpace(string(data))
}
