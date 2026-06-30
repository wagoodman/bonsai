package commands

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/anchore/clio"
	"github.com/spf13/cobra"

	"github.com/wagoodman/bonsai/cmd/bonsai/cli/options"
	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/bus"
	"github.com/wagoodman/bonsai/internal/report"
)

type matrixConfig struct {
	options.Format `yaml:",inline" json:",inline" mapstructure:",squash"`
	options.Matrix `yaml:"analysis" json:"analysis" mapstructure:"analysis"`
}

// Matrix is the `bonsai matrix` command: run the analysis across a declared set of build cells
// (GOOS/GOARCH/tags) and report the worst-case go floor — the MAX over every platform you ship,
// which is the number that actually constrains go.mod — plus which modules are universal versus
// platform-specific. Floor-only by default (no builds); --size also builds each cell for size.
func Matrix(app clio.Application) *cobra.Command {
	opts := &matrixConfig{Format: defaultFormat()}

	return app.SetupCommand(&cobra.Command{
		Use:   "matrix [DIR]",
		Short: "report the worst-case go floor and platform divergence across a build matrix",
		Long: "matrix runs the analysis across a set of build cells (GOOS/GOARCH/tags) and reports the worst-case " +
			"go floor (the MAX over every platform you ship, the number to put in go.mod) and which deps pin it, " +
			"plus which modules are universal versus platform-specific. Declare the cells under analysis.matrix in " +
			".bonsai.yaml, or pass --platform for an ad-hoc run. Floor-only by default (no builds, cross-compiles " +
			"without a cgo toolchain) — this is an import-level upper bound that can name deps dead-code elimination " +
			"later drops; --size builds each cell for the exact post-DCE floor and per-cell size.",
		Example: options.FormatPositionalArgsHelp(
			map[string]string{
				pathArg: pathArgHelp,
			},
		),
		Args: chainArgs(
			cobra.MaximumNArgs(1),
			func(_ *cobra.Command, args []string) error {
				if len(args) == 1 {
					opts.Dir = args[0]
				}
				return nil
			},
		),
		RunE: func(_ *cobra.Command, _ []string) error {
			defer bus.Exit()
			return runMatrix(opts)
		},
	}, opts)
}

func runMatrix(opts *matrixConfig) error {
	cfg := opts.Config()
	cells, err := resolveCells(opts, &cfg)
	if err != nil {
		return err
	}

	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = min(len(cells), runtime.GOMAXPROCS(0))
	}

	rep, err := bonsai.Matrix(cfg, cells, opts.Size, jobs)
	if err != nil {
		return err
	}

	buf := &strings.Builder{}
	switch strings.ToLower(opts.Output) {
	case formatTable:
		err = report.WriteMatrixTable(buf, &rep, opts.Wide, colorEnabled())
	case formatMarkdown:
		err = report.WriteMatrixMarkdown(buf, &rep, opts.Wide)
	case formatJSON:
		err = report.WriteJSON(buf, rep)
	default:
		err = fmt.Errorf("unknown format: %s", opts.Output)
	}
	if err != nil {
		return err
	}

	bus.Report(buf.String())
	// every cell failing (e.g. a cgo-only matrix with no cross toolchain) is an error, not an
	// empty "no floor" success — otherwise a CI gate on `bonsai matrix` passes green.
	if rep.SuccessfulCells() == 0 {
		return fmt.Errorf("all %d build cells failed to build", len(cells))
	}
	return nil
}

// resolveCells picks the build cells for this run. Explicit choices win over the auto-detected
// goreleaser matrix, so precedence is: --platform flags, then the configured analysis.matrix, then
// the goreleaser import (resolved at config-load time), then the built-in default set.
func resolveCells(opts *matrixConfig, cfg *bonsai.Config) ([]bonsai.Platform, error) {
	if len(opts.Platforms) > 0 {
		var cells []bonsai.Platform
		for _, s := range opts.Platforms {
			p, err := parsePlatform(s, opts.Tags)
			if err != nil {
				return nil, err
			}
			cells = append(cells, p)
		}
		return cells, nil
	}
	if len(opts.Build.Matrix) > 0 {
		return opts.Build.Matrix, nil
	}
	// len>0 guard, not just != nil: fangs allocates the nil import pointer while walking the config,
	// so a zero-cell import can slip through even when goreleaser isn't in play.
	if imp := opts.GoreleaserImport; imp != nil && len(imp.Cells) > 0 {
		// goreleaser carries its build flags/env per-cell, so the global build settings don't apply.
		cfg.Build = bonsai.BuildSettings{}
		note := fmt.Sprintf("note: matrix derived from %s (%d cells)", imp.File, len(imp.Cells))
		if imp.Builds > 1 {
			note += fmt.Sprintf(" across %d builds", imp.Builds)
		}
		bus.Notify(note)
		return imp.Cells, nil
	}
	bus.Notify("note: no matrix declared; using the default linux/amd64, darwin/arm64, windows/amd64 " +
		"(set analysis.matrix in .bonsai.yaml, add a .goreleaser.yaml, or pass --platform)")
	return defaultMatrix(), nil
}

// parsePlatform parses an "os/arch" or "os/arch+tag,tag" cell, appending extraTags (from --tags).
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
		return p, fmt.Errorf("invalid --platform %q (want \"os/arch\" or \"os/arch+tag,tag\")", s)
	}
	p.GOOS = strings.TrimSpace(osArch[0])
	p.GOARCH = strings.TrimSpace(osArch[1])
	p.Tags = append(p.Tags, extraTags...)
	return p, nil
}

// defaultMatrix is the built-in cell set used when none is declared: the three platforms most
// projects ship, chosen to surface divergence (different OS + a non-amd64 arch).
func defaultMatrix() []bonsai.Platform {
	return []bonsai.Platform{
		{GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "darwin", GOARCH: "arm64"},
		{GOOS: "windows", GOARCH: "amd64"},
	}
}
