package bonsai

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/wagoodman/bonsai/internal/bus"
	"github.com/wagoodman/bonsai/internal/event"
)

// Platform is one build cell: a GOOS/GOARCH target, build tags, and optional per-cell env/args
// overrides. The zero value means "host platform, no extra tags" — exactly the original
// single-build behavior, so every existing call site keeps working unchanged. Env and Args layer
// on top of the global BuildSettings (cell wins), which is how goreleaser cells carry each
// build's own env (e.g. CGO on for darwin only) and flags without a per-build global setting.
type Platform struct {
	GOOS   string            `json:"goos,omitempty" yaml:"goos" mapstructure:"goos"`
	GOARCH string            `json:"goarch,omitempty" yaml:"goarch" mapstructure:"goarch"`
	Tags   []string          `json:"tags,omitempty" yaml:"tags" mapstructure:"tags"`
	Env    map[string]string `json:"env,omitempty" yaml:"env" mapstructure:"env"`
	Args   string            `json:"args,omitempty" yaml:"args" mapstructure:"args"`
}

// Label is the cell's display key: "linux/amd64", or "linux/amd64+cgo,netgo" when tags are set
// (tags sorted so the label is stable). Empty GOOS/GOARCH render as "host". Used as the map key
// and the table column header.
func (p Platform) Label() string {
	goos, goarch := p.GOOS, p.GOARCH
	if goos == "" {
		goos = "host"
	}
	if goarch == "" {
		goarch = "host"
	}
	base := goos + "/" + goarch
	if len(p.Tags) > 0 {
		base += "+" + strings.Join(sortedTags(p.Tags), ",")
	}
	return base
}

// BuildSettings are persisted build defaults applied to every build/list invocation: extra tags
// merged into every cell, env overrides (e.g. CGO_ENABLED=0), and freeform args spliced into
// `go build` (e.g. "-trimpath"). The matrix's per-cell Platform.Tags extend Tags. Args reach
// only `go build`, never `go list` (which rejects build-only flags like -ldflags), and bonsai's
// own -o/-ldflags=-dumpdep are appended after them so user flags can't break the analysis build.
type BuildSettings struct {
	Tags []string          `json:"tags,omitempty" yaml:"tags" mapstructure:"tags"`
	Env  map[string]string `json:"env,omitempty" yaml:"env" mapstructure:"env"`
	Args string            `json:"args,omitempty" yaml:"args" mapstructure:"args"`
}

// sortedTags returns a sorted, de-duplicated copy of tags with blanks removed, so the cache key
// and label are independent of declaration order and duplicates across config + cell collapse.
func sortedTags(tags []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// effectiveTags merges the persisted build tags with a cell's tags into one sorted-unique set.
func effectiveTags(b BuildSettings, p Platform) []string {
	all := make([]string, 0, len(b.Tags)+len(p.Tags))
	all = append(all, b.Tags...)
	all = append(all, p.Tags...)
	return sortedTags(all)
}

// tagsArg renders the -tags flag for the go toolchain, or "" when there are no tags.
func tagsArg(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return "-tags=" + strings.Join(tags, ",")
}

// platformEnv returns the process env with the global env overrides applied, then the cell's own
// env (which wins), then GOOS/GOARCH for the cell (host values when the cell leaves them empty).
func platformEnv(p Platform, b BuildSettings) []string {
	env := os.Environ()
	for k, v := range b.Env {
		env = append(env, k+"="+v)
	}
	for k, v := range p.Env {
		env = append(env, k+"="+v)
	}
	if p.GOOS != "" {
		env = append(env, "GOOS="+p.GOOS)
	}
	if p.GOARCH != "" {
		env = append(env, "GOARCH="+p.GOARCH)
	}
	return env
}

// splitArgs splits a freeform build-args string into fields the way a shell would for the common
// cases: whitespace separates, single and double quotes group (so -ldflags="-s -w" stays one
// argument). Not a full shell parser — no backslash escapes or nested quotes — which is all the
// go build flags here need.
func splitArgs(s string) []string {
	var args []string
	var cur strings.Builder
	var quote rune
	has := false
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			has = true
		case r == ' ' || r == '\t' || r == '\n':
			if has {
				args = append(args, cur.String())
				cur.Reset()
				has = false
			}
		default:
			cur.WriteRune(r)
			has = true
		}
	}
	if has {
		args = append(args, cur.String())
	}
	return args
}

// ResolveGraph is the build-free resolve: it loads only the `go list` import graph for cfg's
// cell, leaving bin nil. Enough for GoFloor (which reads only the graph) and module membership,
// and it cross-compiles without a cgo toolchain. Size/Prune still require a built binary via
// Resolve. Call Close when done (the cleanup is a no-op, but keeps call sites uniform).
func ResolveGraph(cfg Config) (*Resolved, error) {
	dir, target, err := dirTarget(cfg)
	if err != nil {
		return nil, err
	}
	g, err := loadBuildGraph(dir, target, cfg.Platform, cfg.Build)
	if err != nil {
		return nil, err
	}
	return &Resolved{g: g, opts: optsFrom(cfg), cleanup: func() {}}, nil
}

// moduleInfo is a non-main module reachable in the build plus its declared `go` directive.
type moduleInfo struct {
	module    string
	goVersion string
}

// reachableModules returns the non-main modules with at least one reachable package in the
// build, each tagged with its declared `go` directive. The membership basis for the matrix's
// universal-vs-platform-specific split.
func (r *Resolved) reachableModules() []moduleInfo {
	g := r.g
	seen := map[string]bool{}
	for ip := range g.reachable(nil) {
		if mod := g.moduleOfPkg[ip]; mod != "" && mod != g.mainModule {
			seen[mod] = true
		}
	}
	out := make([]moduleInfo, 0, len(seen))
	for mod := range seen {
		out = append(out, moduleInfo{module: mod, goVersion: g.goVersionOf(mod)})
	}
	return out
}

// MatrixReport is the result of running the analysis across a declared set of build cells: the
// worst-case go floor (the number that actually constrains go.mod), which modules are universal
// vs platform-specific, and the per-cell detail. Built from per-cell GoFloor/Size results plus
// set logic — no new analysis math.
type MatrixReport struct {
	Cells     []CellResult   `json:"cells"`            // one per declared platform, in declared order
	WorstGo   GoFloor        `json:"worstFloor"`       // MAX floor across cells (the constraint)
	Universal []string       `json:"universalModules"` // modules present in every successful cell
	Modules   []MatrixModule `json:"modules"`          // union across cells, with per-cell presence + size
	WithSize  bool           `json:"withSize"`         // cells were built and per-cell size computed
}

// SuccessfulCells counts the cells that built/listed without error. Zero means the whole matrix
// failed (e.g. a cgo-only matrix with no cross toolchain), which the caller surfaces as an error
// rather than an empty "no floor" report.
func (m MatrixReport) SuccessfulCells() int {
	n := 0
	for _, c := range m.Cells {
		if c.Err == "" {
			n++
		}
	}
	return n
}

// CellResult is one build cell's outcome. A cell that fails to build/list (e.g. a cgo cell with
// no cross toolchain) records Err and is skipped in the rollups; the rest of the matrix stands.
type CellResult struct {
	Platform Platform    `json:"platform"`
	Label    string      `json:"label"`
	Floor    GoFloor     `json:"floor,omitempty"`
	Size     *SizeReport `json:"size,omitempty"` // only with --size (cell actually built)
	Err      string      `json:"error,omitempty"`
}

// MatrixModule is one module's presence across the matrix: whether it is in every successful
// cell (universal) or only some (platform-specific), and its per-cell size when built.
type MatrixModule struct {
	Module     string            `json:"module"`
	GoVersion  string            `json:"goVersion,omitempty"`
	Universal  bool              `json:"universal"`
	InCells    []string          `json:"inCells"`              // labels of cells that include it
	SizeByCell map[string]uint64 `json:"sizeByCell,omitempty"` // only with --size
}

// cellData is the internal per-cell result the aggregation consumes.
type cellData struct {
	platform Platform
	label    string
	floor    GoFloor
	size     *SizeReport
	modules  []moduleInfo // reachable non-main modules in this cell
	err      error
}

// Matrix runs cfg's analysis across cells and aggregates the results. By default (withSize
// false) each cell is a single build-free `go list` (cheap, cross-compiles without a C
// toolchain); with withSize it builds each cell to attribute per-cell size. Cells run on a
// bounded worker pool of jobs workers; per-cell failures are captured, not fatal.
func Matrix(cfg Config, cells []Platform, withSize bool, jobs int) (MatrixReport, error) {
	if len(cells) == 0 {
		return MatrixReport{}, fmt.Errorf("no platforms declared; pass --platform or add an analysis.matrix to .bonsai.yaml")
	}
	if jobs <= 0 {
		jobs = 1
	}

	// one progress line for the whole matrix: "N/total cells" as each finishes. Increment is
	// atomic, so it's safe to drive from the worker goroutines. (The per-cell build sub-tasks the
	// --size path emits from Resolve are separate; this is the matrix-level rollup.)
	task := bus.PublishTask(event.Title{
		Default:      "Analyze build matrix",
		WhileRunning: "Analyzing build matrix",
		OnSuccess:    "Analyzed build matrix",
	}, "", len(cells))

	data := make([]cellData, len(cells))
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	for i, p := range cells {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, p Platform) {
			defer wg.Done()
			defer func() { <-sem }()
			data[i] = runCell(cfg, p, withSize)
			task.Increment()
			// atomic, so concurrent cells can drive it; shows the just-finished platform.
			task.SetStage(fmt.Sprintf("%d/%d  %s", task.Current(), len(cells), p.Label()))
		}(i, p)
	}
	wg.Wait()
	task.SetStage(fmt.Sprintf("%d platforms", len(cells)))
	task.SetCompleted()

	return aggregateCells(data, withSize), nil
}

// runCell resolves a single cell and extracts its floor, module membership, and (when built)
// size. cfg is taken by value so setting Platform here is local to this goroutine.
func runCell(cfg Config, p Platform, withSize bool) cellData {
	cd := cellData{platform: p, label: p.Label()}
	cfg.Platform = p
	cfg.Binary = "" // matrix always builds from source; a prebuilt binary is a single platform

	var (
		r   *Resolved
		err error
	)
	if withSize {
		r, err = Resolve(cfg)
	} else {
		r, err = ResolveGraph(cfg)
	}
	if err != nil {
		cd.err = err
		return cd
	}
	defer r.Close()

	cd.floor = r.GoFloor()
	cd.modules = r.reachableModules()
	if withSize {
		s := r.Size()
		cd.size = &s
	}
	return cd
}

// aggregateCells reduces per-cell results into the MatrixReport: the worst-case (MAX) floor and
// which cells pin it, the universal-vs-platform-specific module split, and per-cell sizes. Pure
// over cellData, so it is the load-bearing logic under test.
func aggregateCells(cells []cellData, withSize bool) MatrixReport {
	rep := MatrixReport{WithSize: withSize}

	// per-cell results (failures included, in declared order).
	for _, c := range cells {
		cr := CellResult{Platform: c.platform, Label: c.label}
		if c.err != nil {
			cr.Err = c.err.Error()
		} else {
			cr.Floor = c.floor
			cr.Size = c.size
		}
		rep.Cells = append(rep.Cells, cr)
	}

	rep.WorstGo = worstFloor(cells)
	rep.Modules = matrixModules(cells, withSize)
	for _, m := range rep.Modules {
		if m.Universal {
			rep.Universal = append(rep.Universal, m.Module)
		}
	}
	return rep
}

// worstFloor reduces the per-cell floors to the matrix-wide constraint: Version is the MAX over
// successful cells (the number you must put in go.mod), Critical is the union of the pinning
// modules from the cells tied at that max, and NextVersion is the worst-case floor that remains
// after pruning that critical set (each tied cell drops to its own NextVersion).
func worstFloor(cells []cellData) GoFloor {
	var f GoFloor
	for _, c := range cells {
		if c.err != nil {
			continue
		}
		if cmpGo(c.floor.Version, f.Version) > 0 {
			f.Version = c.floor.Version
		}
		if cmpGo(c.floor.OwnedMax, f.OwnedMax) > 0 {
			f.OwnedMax = c.floor.OwnedMax
		}
	}
	if f.Version == "" {
		return f // no cell's deps impose a floor
	}

	crit := map[string]bool{}
	next := ""
	for _, c := range cells {
		if c.err != nil {
			continue
		}
		v := c.floor.Version
		if v == f.Version {
			for _, m := range c.floor.Critical {
				crit[m] = true
			}
			v = c.floor.NextVersion // this cell would drop here once its critical set is pruned
		}
		if cmpGo(v, next) > 0 {
			next = v
		}
	}
	for m := range crit {
		f.Critical = append(f.Critical, m)
	}
	sort.Strings(f.Critical)
	f.NextVersion = next
	return f
}

// matrixModules builds the per-module presence union across successful cells, marking each
// universal (in every successful cell) or platform-specific, with per-cell size when built.
func matrixModules(cells []cellData, withSize bool) []MatrixModule {
	type acc struct {
		goVersion string
		inCells   []string
		sizeByLbl map[string]uint64
	}
	mods := map[string]*acc{}
	successful := 0
	for _, c := range cells {
		if c.err != nil {
			continue
		}
		successful++
		size := cellSizes(c)
		for _, mi := range c.modules {
			a := mods[mi.module]
			if a == nil {
				a = &acc{goVersion: mi.goVersion, sizeByLbl: map[string]uint64{}}
				mods[mi.module] = a
			}
			a.inCells = append(a.inCells, c.label)
			if withSize {
				a.sizeByLbl[c.label] = size[mi.module]
			}
		}
	}

	out := make([]MatrixModule, 0, len(mods))
	for mod, a := range mods {
		mm := MatrixModule{
			Module:    mod,
			GoVersion: a.goVersion,
			Universal: successful > 0 && len(a.inCells) == successful,
			InCells:   a.inCells,
		}
		sort.Strings(mm.InCells)
		if withSize && len(a.sizeByLbl) > 0 {
			mm.SizeByCell = a.sizeByLbl
		}
		out = append(out, mm)
	}
	// platform-specific first, then highest `go` directive (the floor drivers), then name.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Universal != out[j].Universal {
			return !out[i].Universal
		}
		if v := cmpGo(out[i].GoVersion, out[j].GoVersion); v != 0 {
			return v > 0
		}
		return out[i].Module < out[j].Module
	})
	return out
}

// cellSizes maps module -> size for a built cell (empty for floor-only cells).
func cellSizes(c cellData) map[string]uint64 {
	if c.size == nil {
		return nil
	}
	m := make(map[string]uint64, len(c.size.Modules))
	for _, ms := range c.size.Modules {
		m[ms.Module] = ms.Size
	}
	return m
}
