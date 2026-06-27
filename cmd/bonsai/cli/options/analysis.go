package options

import (
	"github.com/anchore/fangs"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

var (
	_ interface {
		fangs.FlagAdder
		fangs.FieldDescriber
	} = (*Build)(nil)
	_ interface {
		fangs.FlagAdder
		fangs.PostLoader
		fangs.FieldDescriber
	} = (*Anatomy)(nil)
	_ interface {
		fangs.FlagAdder
		fangs.PostLoader
		fangs.FieldDescriber
	} = (*Prune)(nil)
	_ fangs.FlagAdder = (*Matrix)(nil)
)

const defaultTop = 40

// Build holds the inputs shared by every report subject: how to build (or load) the binary and
// which modules count as 1st-class / locked. The module directory comes from the positional
// argument; Dir mirrors it for the loaded config.
type Build struct {
	Target     string   `yaml:"target" json:"target" mapstructure:"target"`
	Binary     string   `yaml:"binary" json:"binary" mapstructure:"binary"`
	Controlled []string `yaml:"controlled" json:"controlled" mapstructure:"controlled"`
	Lock       []string `yaml:"lock" json:"lock" mapstructure:"lock"`
	Unlock     []string `yaml:"unlock" json:"unlock" mapstructure:"unlock"`

	// BuildSettings are persisted build defaults (tags, env, freeform args) applied to every
	// build/list. Config-only (no flags); read from the analysis.build section.
	BuildSettings bonsai.BuildSettings `yaml:"build" json:"build" mapstructure:"build"`
	// Matrix declares the build cells for `bonsai matrix` (the analysis.matrix section). Other
	// subjects ignore it.
	Matrix []bonsai.Platform `yaml:"matrix" json:"matrix" mapstructure:"matrix"`
	// Goreleaser, when true, derives the matrix and per-cell build flags from the project's
	// .goreleaser.yaml instead of analysis.matrix. Mutually exclusive with matrix/--platform.
	Goreleaser bool `yaml:"goreleaser" json:"goreleaser" mapstructure:"goreleaser"`

	Dir string `yaml:"-" json:"-" mapstructure:"-"` // set from the positional directory argument
}

// Config returns the engine Config fields shared by every report subject: the build/load inputs,
// the class lists, and the persisted build settings. Command-specific fields (Why/Blame/
// HideLocked, Platform) are set by the caller.
func (o *Build) Config() bonsai.Config {
	return bonsai.Config{
		Dir:        o.Dir,
		Target:     o.Target,
		Binary:     o.Binary,
		Controlled: o.Controlled,
		Locked:     o.Lock,
		Unlock:     o.Unlock,
		Build:      o.BuildSettings,
	}
}

func (o *Build) AddFlags(flags fangs.FlagSet) {
	flags.StringVarP(&o.Target, "target", "",
		"entrypoint package to build and analyze (default: the module's sole main package)")
	flags.StringVarP(&o.Binary, "binary", "b",
		"analyze a prebuilt binary instead of building from source")
	flags.StringArrayVarP(&o.Controlled, "controlled", "C",
		"1st-class module patterns whose imports are cuttable, beyond the main module "+
			"(repeatable; use \"github.com/anchore/...\" to control a whole org transitively)")
	flags.StringArrayVarP(&o.Lock, "lock", "l",
		"module patterns to lock so they are never suggested for pruning (repeatable; exact, glob, or \"path/...\")")
	flags.StringArrayVarP(&o.Unlock, "unlock", "",
		"re-open these locked modules as prune candidates (repeatable; overrides the default lock on controlled modules)")
}

func (o *Build) DescribeFields(d fangs.FieldDescriptionSet) {
	d.Add(&o.Target, "entrypoint package to build and analyze")
	d.Add(&o.Binary, "prebuilt binary to analyze instead of building from source")
	d.Add(&o.Controlled, "1st-class module patterns whose imports are cuttable, beyond the main module "+
		"(e.g. \"github.com/anchore/...\" to control a whole org transitively)")
	d.Add(&o.Lock, "module patterns to lock so they are never suggested for pruning (exact, glob, or \"path/...\")")
	d.Add(&o.Unlock, "locked modules to re-open as prune candidates (overrides the default lock on controlled modules)")
}

// Matrix controls the `bonsai matrix` subject: run the analysis across a set of build cells and
// report the worst-case go floor plus platform divergence. The cells come from the analysis.matrix
// config section (via the embedded Build) or the ad-hoc --platform flags.
type Matrix struct {
	Build     `yaml:",inline" json:",inline" mapstructure:",squash"`
	Platforms []string `yaml:"-" json:"-" mapstructure:"-"` // --platform: ad-hoc cells, replace the config matrix
	Tags      []string `yaml:"-" json:"-" mapstructure:"-"` // --tags: applied to --platform cells
	Size      bool     `yaml:"-" json:"-" mapstructure:"-"` // --size: build each cell, add size columns
	Jobs      int      `yaml:"-" json:"-" mapstructure:"-"` // --jobs: concurrency
	Wide      bool     `yaml:"-" json:"-" mapstructure:"-"` // --wide: full module-by-cell grid
}

func (o *Matrix) AddFlags(flags fangs.FlagSet) {
	flags.StringArrayVarP(&o.Platforms, "platform", "",
		"ad-hoc build cell \"os/arch\" or \"os/arch+tag,tag\" (repeatable; replaces the configured matrix)")
	flags.StringArrayVarP(&o.Tags, "tags", "",
		"build tags applied to every --platform cell (repeatable)")
	flags.BoolVarP(&o.Size, "size", "",
		"build each cell and report per-cell size (default: floor only, no builds)")
	flags.IntVarP(&o.Jobs, "jobs", "j",
		"max cells to analyze concurrently (default: min(cells, GOMAXPROCS))")
	flags.BoolVarP(&o.Wide, "wide", "",
		"print the full module-by-cell grid instead of just the divergence")
}

// Anatomy controls the default `bonsai` subject: the binary's size, attributed by content and
// owner, plus the largest modules. It carries no prune or go-version analysis.
type Anatomy struct {
	Build      `yaml:",inline" json:",inline" mapstructure:",squash"`
	Why        bool `yaml:"why" json:"why" mapstructure:"why"`
	HideLocked bool `yaml:"hide-locked" json:"hide-locked" mapstructure:"hide-locked"`
	Sections   bool `yaml:"sections" json:"sections" mapstructure:"sections"`
	Top        int  `yaml:"top" json:"top" mapstructure:"top"`
}

func DefaultAnatomy() Anatomy {
	return Anatomy{Top: defaultTop}
}

func (o *Anatomy) AddFlags(flags fangs.FlagSet) {
	flags.BoolVarP(&o.Why, "why", "",
		"show import-why trees: under each module, the \"← imported by\" trace back to your 1st-class code")
	flags.BoolVarP(&o.HideLocked, "hide-locked", "",
		"omit locked modules from output instead of de-emphasizing them")
	flags.BoolVarP(&o.Sections, "sections", "",
		"include the binary's section layout (file-backed Mach-O/ELF sections)")
	flags.IntVarP(&o.Top, "top", "t",
		"number of rows to show in the largest-modules table")
}

func (o *Anatomy) PostLoad() error {
	if o.Top <= 0 {
		o.Top = defaultTop
	}
	return nil
}

func (o *Anatomy) DescribeFields(d fangs.FieldDescriptionSet) {
	d.Add(&o.Why, "show import-why trees: the \"imported by\" trace from each module back to your 1st-class code")
	d.Add(&o.HideLocked, "omit locked modules from output instead of de-emphasizing them")
	d.Add(&o.Sections, "include the binary's section layout in the anatomy report")
	d.Add(&o.Top, "maximum number of rows to show in the largest-modules table")
}

// Prune controls the `bonsai prune` subject: which dependencies, if removed, free the most
// bytes, with coupling and a greedy plan. Shapley fair-blame is opt-in.
type Prune struct {
	Build      `yaml:",inline" json:",inline" mapstructure:",squash"`
	Why        bool `yaml:"why" json:"why" mapstructure:"why"`
	Blame      bool `yaml:"blame" json:"blame" mapstructure:"blame"`
	HideLocked bool `yaml:"hide-locked" json:"hide-locked" mapstructure:"hide-locked"`
	Top        int  `yaml:"top" json:"top" mapstructure:"top"`
}

func DefaultPrune() Prune {
	return Prune{Top: defaultTop}
}

func (o *Prune) AddFlags(flags fangs.FlagSet) {
	flags.BoolVarP(&o.Why, "why", "",
		"show import-why trees: under each candidate, the \"← imported by\" trace back to your 1st-class code")
	flags.BoolVarP(&o.Blame, "blame", "",
		"also compute Shapley fair-blame: each target's fair share of shared weight")
	flags.BoolVarP(&o.HideLocked, "hide-locked", "",
		"omit locked modules from output instead of de-emphasizing them")
	flags.IntVarP(&o.Top, "top", "t",
		"number of rows to show in ranked tables")
}

func (o *Prune) PostLoad() error {
	if o.Top <= 0 {
		o.Top = defaultTop
	}
	return nil
}

func (o *Prune) DescribeFields(d fangs.FieldDescriptionSet) {
	d.Add(&o.Why, "show import-why trees: the \"imported by\" trace from each candidate back to your 1st-class code")
	d.Add(&o.Blame, "also compute Shapley fair-blame attribution across prune targets")
	d.Add(&o.HideLocked, "omit locked modules from output instead of de-emphasizing them")
	d.Add(&o.Top, "maximum number of rows to show in each ranked table")
}
