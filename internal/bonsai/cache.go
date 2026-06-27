package bonsai

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Building the target with -ldflags=-dumpdep forces a full re-link on every run — the linker
// must actually run to emit the symbol-dependency graph (a side channel Go's build cache can't
// replay), so the link is never served from cache. For large binaries that link dominates.
// We sidestep it by caching the *resolved* analysis inputs (sizes + post-DCE reference graph)
// keyed by the exact source identity, so a second run on the same clean commit skips the
// build, `go list`, and dumpdep parse entirely.

// cacheFormat is bumped whenever the snapshot layout or the meaning of the resolved data
// changes, so older caches are ignored rather than misread.
const cacheFormat = "1"

// buildSnapshot is the gob-serializable form of resolve()'s output: the parsed binary plus the
// fields of the build graph the analysis reads. binaryInfo is all-exported, so it serializes
// directly; the graph is flattened into plain structs (no pointers/cycles).
type buildSnapshot struct {
	Bin binaryInfo

	MainModule   string
	MainModDir   string
	RootPackages []string
	ModulePaths  []string
	DirectMods   []string
	ModuleOfPkg  map[string]string
	Packages     map[string]snapPkg
	Modules      map[string]snapMod
}

type snapPkg struct {
	Name     string
	Standard bool
	Imports  []string
}

type snapMod struct {
	Version   string
	Dir       string
	Main      bool
	GoVersion string
}

func snapshotOf(bin *binaryInfo, g *buildGraph) buildSnapshot {
	snap := buildSnapshot{
		Bin:          *bin,
		MainModule:   g.mainModule,
		MainModDir:   g.mainModDir,
		RootPackages: g.rootPackages,
		ModulePaths:  g.modulePaths,
		ModuleOfPkg:  g.moduleOfPkg,
		Packages:     make(map[string]snapPkg, len(g.packages)),
		Modules:      make(map[string]snapMod, len(g.allModules)),
	}
	for m := range g.directMods {
		snap.DirectMods = append(snap.DirectMods, m)
	}
	for ip, p := range g.packages {
		snap.Packages[ip] = snapPkg{Name: p.Name, Standard: p.Standard, Imports: p.Imports}
	}
	for path, m := range g.allModules {
		snap.Modules[path] = snapMod{Version: m.Version, Dir: m.Dir, Main: m.Main, GoVersion: m.GoVersion}
	}
	return snap
}

// rebuild reconstructs the binaryInfo and buildGraph from a snapshot.
func (snap *buildSnapshot) rebuild() (*binaryInfo, *buildGraph) {
	bin := snap.Bin
	g := &buildGraph{
		mainModule:   snap.MainModule,
		mainModDir:   snap.MainModDir,
		packages:     make(map[string]*listPackage, len(snap.Packages)),
		moduleOfPkg:  snap.ModuleOfPkg,
		modulePaths:  snap.ModulePaths,
		directMods:   make(map[string]bool, len(snap.DirectMods)),
		allModules:   make(map[string]*listModule, len(snap.Modules)),
		rootPackages: snap.RootPackages,
	}
	for ip, sp := range snap.Packages {
		g.packages[ip] = &listPackage{ImportPath: ip, Name: sp.Name, Standard: sp.Standard, Imports: sp.Imports}
	}
	for _, m := range snap.DirectMods {
		g.directMods[m] = true
	}
	for path, sm := range snap.Modules {
		g.allModules[path] = &listModule{Path: path, Version: sm.Version, Dir: sm.Dir, Main: sm.Main, GoVersion: sm.GoVersion}
	}
	return &bin, g
}

// resolveCacheKey derives a content key for the resolved analysis, or ok=false when the result
// can't be safely cached (no git, a dirty tree, or caching disabled). It folds in everything
// that changes the built binary: the source commit, the target, the host platform, the Go
// toolchain, the build env, and the cache format.
func resolveCacheKey(dir, target string, p Platform, b BuildSettings) (string, bool) {
	if os.Getenv("BONSAI_NO_CACHE") != "" {
		return "", false
	}
	commit, clean := gitState(dir)
	if commit == "" || !clean {
		return "", false // only cache reproducible, committed source
	}
	return platformCacheKey(commit, target, p, b), true
}

// platformCacheKey hashes everything that changes the resolved build for a given source commit:
// the target, the effective cell (GOOS/GOARCH/tags, falling back to the host runtime), the
// persisted build env/args, the Go toolchain, and the cache format. Folding the cell + build
// settings in means two matrix cells on the same commit get distinct keys instead of colliding.
func platformCacheKey(commit, target string, p Platform, b BuildSettings) string {
	goos := p.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := p.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	h := sha256.New()
	for _, part := range []string{
		cacheFormat, commit, target,
		goos, goarch, runtime.Version(),
		os.Getenv("GOFLAGS"), os.Getenv("CGO_ENABLED"), os.Getenv("GOEXPERIMENT"),
		strings.Join(effectiveTags(b, p), ","), b.Args, sortedEnvString(b.Env),
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// sortedEnvString flattens an env-override map into a stable "k=v\x00k=v" string for hashing.
func sortedEnvString(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(env[k])
		sb.WriteByte(0)
	}
	return sb.String()
}

// gitState returns dir's HEAD commit and whether the working tree is clean (no modified or
// untracked files). A non-repo or any git error yields ("", false).
func gitState(dir string) (commit string, clean bool) {
	head, err := gitOutput(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", false
	}
	status, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(head), strings.TrimSpace(status) == ""
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...) //nolint:gosec // git subcommands are internally constructed; dir is user-supplied by design
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func cacheDir() string {
	if d := os.Getenv("BONSAI_CACHE_DIR"); d != "" {
		return d
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "bonsai", "resolve")
}

// loadResolveCache returns the cached resolved analysis for key, or an error on a miss.
func loadResolveCache(key string) (*binaryInfo, *buildGraph, error) {
	dir := cacheDir()
	if dir == "" {
		return nil, nil, fmt.Errorf("no cache dir")
	}
	data, err := os.ReadFile(filepath.Join(dir, key+".gob"))
	if err != nil {
		return nil, nil, err
	}
	var snap buildSnapshot
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&snap); err != nil {
		return nil, nil, err
	}
	bin, g := snap.rebuild()
	return bin, g, nil
}

// storeResolveCache writes the resolved analysis for key. Best-effort: failures are ignored
// (the analysis is still correct, just uncached).
func storeResolveCache(key string, bin *binaryInfo, g *buildGraph) {
	dir := cacheDir()
	if dir == "" || os.MkdirAll(dir, 0o755) != nil {
		return
	}
	var buf bytes.Buffer
	if gob.NewEncoder(&buf).Encode(snapshotOf(bin, g)) != nil {
		return
	}
	// write atomically so a concurrent reader never sees a partial file.
	tmp, err := os.CreateTemp(dir, key+"-*.tmp")
	if err != nil {
		return
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	_ = os.Rename(tmp.Name(), filepath.Join(dir, key+".gob"))
}
