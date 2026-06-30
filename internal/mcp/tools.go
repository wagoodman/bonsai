package mcp

import (
	"context"
	"fmt"
	"runtime"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/options"
	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/report"
)

// DiffInput is the input for the diff tool: the whole-binary Input plus the git ref to compare
// against.
type DiffInput struct {
	Input `json:",inline"`
	Ref   string `json:"ref" jsonschema:"git ref to compare against (branch, tag, or commit); the baseline is the merge-base with it"`
}

// MatrixInput is the input for the matrix tool: the whole-binary Input plus the build cells. With
// no platforms given, the cells come from the project's analysis.matrix, then its .goreleaser.yaml,
// then a default set.
type MatrixInput struct {
	Input     `json:",inline"`
	Platforms []string `json:"platforms,omitempty" jsonschema:"build cells \"os/arch\" or \"os/arch+tag,tag\" (replaces the configured matrix)"`
	Tags      []string `json:"tags,omitempty" jsonschema:"build tags applied to every platform cell"`
	Size      bool     `json:"size,omitempty" jsonschema:"build each cell and report per-cell size (default: floor only, no builds)"`
}

// diff builds and analyzes both the working tree and a baseline source state, returning the size
// and go-floor delta this branch makes — the "what did my edit do to the binary" question, against
// a committed ref.
func (s *Server) diff(_ context.Context, _ *mcp.CallToolRequest, in DiffInput) (*mcp.CallToolResult, *bonsai.DiffReport, error) {
	cfg, err := in.config()
	if err != nil {
		return nil, nil, err
	}
	var rep *bonsai.DiffReport
	err = s.cache.serialize(func() error {
		var e error
		rep, e = bonsai.Diff(cfg, in.Ref)
		return e
	})
	return nil, rep, err
}

// check evaluates the project's committed budget (the config's `check:` block) against the built
// target, so an agent can ask "does my change still pass the CI gate?" without re-deriving the
// rules. An absent budget yields Configured=false, not an error.
func (s *Server) check(_ context.Context, _ *mcp.CallToolRequest, in Input) (*mcp.CallToolResult, report.CheckReport, error) {
	b, chk, err := in.load()
	if err != nil {
		return nil, report.CheckReport{}, err
	}
	budget, err := report.ParseBudget(report.BudgetSpec{
		MaxBinarySize: chk.MaxBinarySize,
		MaxGoVersion:  chk.MaxGoVersion,
		Deny:          chk.Deny,
		MaxModuleSize: chk.MaxModuleSize,
		Action:        chk.Action,
	})
	if err != nil {
		return nil, report.CheckReport{}, err
	}
	cfg := b.Config()
	var out report.CheckReport
	err = s.cache.with(cfg, func(r *bonsai.Resolved) error {
		out = report.EvaluateBudget(r.Size(), r.GoFloor(), budget, cfg.Binary != "")
		return nil
	})
	return nil, out, err
}

// matrix runs the analysis across a set of build cells and reports the worst-case go floor (the
// number that constrains go.mod across every platform shipped) plus which modules are universal
// versus platform-specific. Floor-only by default; size builds each cell.
func (s *Server) matrix(_ context.Context, _ *mcp.CallToolRequest, in MatrixInput) (*mcp.CallToolResult, bonsai.MatrixReport, error) {
	b, _, err := in.load()
	if err != nil {
		return nil, bonsai.MatrixReport{}, err
	}
	cfg := b.Config()
	cells, fromGoreleaser, err := matrixCells(b, in.Platforms, in.Tags)
	if err != nil {
		return nil, bonsai.MatrixReport{}, err
	}
	if fromGoreleaser {
		cfg.Build = bonsai.BuildSettings{} // goreleaser cells carry their own per-cell flags/env
	}
	jobs := min(len(cells), runtime.GOMAXPROCS(0))

	var rep bonsai.MatrixReport
	err = s.cache.serialize(func() error {
		var e error
		rep, e = bonsai.Matrix(cfg, cells, in.Size, jobs)
		return e
	})
	if err != nil {
		return nil, bonsai.MatrixReport{}, err
	}
	// every cell failing (e.g. a cgo-only matrix with no cross toolchain) is an error, not an empty
	// "no floor" success.
	if rep.SuccessfulCells() == 0 {
		return nil, rep, fmt.Errorf("all %d build cells failed to build", len(cells))
	}
	return nil, rep, nil
}

// matrixCells picks the build cells, mirroring the CLI precedence: explicit platforms, then the
// configured analysis.matrix, then the goreleaser import, then the built-in default set. The bool
// reports whether the cells came from goreleaser (whose per-cell flags replace the global build
// settings).
func matrixCells(b options.Build, platforms, tags []string) ([]bonsai.Platform, bool, error) {
	if len(platforms) > 0 {
		cells := make([]bonsai.Platform, 0, len(platforms))
		for _, s := range platforms {
			p, err := parsePlatform(s, tags)
			if err != nil {
				return nil, false, err
			}
			cells = append(cells, p)
		}
		return cells, false, nil
	}
	if len(b.Matrix) > 0 {
		return b.Matrix, false, nil
	}
	// len>0 guard, not just != nil: fangs allocates the nil import pointer while walking the config.
	if imp := b.GoreleaserImport; imp != nil && len(imp.Cells) > 0 {
		return imp.Cells, true, nil
	}
	return defaultMatrix(), false, nil
}

// parsePlatform parses an "os/arch" or "os/arch+tag,tag" cell, appending extraTags.
func parsePlatform(s string, extraTags []string) (bonsai.Platform, error) {
	var p bonsai.Platform
	base := s
	if i := strings.IndexByte(s, '+'); i >= 0 {
		base = s[:i]
		for t := range strings.SplitSeq(s[i+1:], ",") {
			if t = strings.TrimSpace(t); t != "" {
				p.Tags = append(p.Tags, t)
			}
		}
	}
	osArch := strings.SplitN(strings.TrimSpace(base), "/", 2)
	if len(osArch) != 2 || strings.TrimSpace(osArch[0]) == "" || strings.TrimSpace(osArch[1]) == "" {
		return p, fmt.Errorf("invalid platform %q (want \"os/arch\" or \"os/arch+tag,tag\")", s)
	}
	p.GOOS = strings.TrimSpace(osArch[0])
	p.GOARCH = strings.TrimSpace(osArch[1])
	p.Tags = append(p.Tags, extraTags...)
	return p, nil
}

// defaultMatrix is the built-in cell set used when none is declared: the three platforms most
// projects ship, chosen to surface divergence.
func defaultMatrix() []bonsai.Platform {
	return []bonsai.Platform{
		{GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "darwin", GOARCH: "arm64"},
		{GOOS: "windows", GOARCH: "amd64"},
	}
}
