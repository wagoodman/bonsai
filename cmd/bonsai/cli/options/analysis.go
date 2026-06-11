package options

import (
	"github.com/anchore/fangs"
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
)

const defaultTop = 40

// Build holds the inputs shared by every report subject: how to build (or load) the binary and
// which modules count as 1st-class / locked. The module directory comes from the positional
// argument; Dir mirrors it for the loaded config.
type Build struct {
	Target     string   `yaml:"target" json:"target" mapstructure:"target"`
	Binary     string   `yaml:"binary" json:"binary" mapstructure:"binary"`
	Controlled []string `yaml:"controlled" json:"controlled" mapstructure:"controlled"`
	Ignore     []string `yaml:"ignore" json:"ignore" mapstructure:"ignore"`
	Unlock     []string `yaml:"unlock" json:"unlock" mapstructure:"unlock"`

	Dir string `yaml:"-" json:"-" mapstructure:"-"` // set from the positional directory argument
}

func (o *Build) AddFlags(flags fangs.FlagSet) {
	flags.StringVarP(&o.Target, "target", "",
		"entrypoint package to build and analyze (default: the module's sole main package)")
	flags.StringVarP(&o.Binary, "binary", "b",
		"analyze a prebuilt binary instead of building from source")
	flags.StringArrayVarP(&o.Controlled, "controlled", "C",
		"1st-class module patterns whose imports are cuttable, beyond the main module "+
			"(repeatable; use \"github.com/anchore/...\" to control a whole org transitively)")
	flags.StringArrayVarP(&o.Ignore, "ignore", "i",
		"module patterns never suggested for pruning, i.e. locked (repeatable; exact, glob, or \"path/...\")")
	flags.StringArrayVarP(&o.Unlock, "unlock", "",
		"re-open these locked modules as prune candidates (repeatable; overrides the default lock on controlled modules)")
}

func (o *Build) DescribeFields(d fangs.FieldDescriptionSet) {
	d.Add(&o.Target, "entrypoint package to build and analyze")
	d.Add(&o.Binary, "prebuilt binary to analyze instead of building from source")
	d.Add(&o.Controlled, "1st-class module patterns whose imports are cuttable, beyond the main module "+
		"(e.g. \"github.com/anchore/...\" to control a whole org transitively)")
	d.Add(&o.Ignore, "module patterns never suggested for pruning, i.e. locked (exact, glob, or \"path/...\")")
	d.Add(&o.Unlock, "locked modules to re-open as prune candidates (overrides the default lock on controlled modules)")
}

// Anatomy controls the default `bonsai` subject: the binary's size, attributed by content and
// owner, plus the largest modules. It carries no prune or go-version analysis.
type Anatomy struct {
	Build       `yaml:",inline" json:",inline" mapstructure:",squash"`
	Why         bool `yaml:"why" json:"why" mapstructure:"why"`
	HideIgnored bool `yaml:"hide-ignored" json:"hide-ignored" mapstructure:"hide-ignored"`
	Sections    bool `yaml:"sections" json:"sections" mapstructure:"sections"`
	Top         int  `yaml:"top" json:"top" mapstructure:"top"`
}

func DefaultAnatomy() Anatomy {
	return Anatomy{Top: defaultTop}
}

func (o *Anatomy) AddFlags(flags fangs.FlagSet) {
	flags.BoolVarP(&o.Why, "why", "",
		"show import-why trees: under each module, the \"← imported by\" trace back to your 1st-class code")
	flags.BoolVarP(&o.HideIgnored, "hide-ignored", "",
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
	d.Add(&o.HideIgnored, "omit locked modules from output instead of de-emphasizing them")
	d.Add(&o.Sections, "include the binary's section layout in the anatomy report")
	d.Add(&o.Top, "maximum number of rows to show in the largest-modules table")
}

// Prune controls the `bonsai prune` subject: which dependencies, if removed, free the most
// bytes, with coupling and a greedy plan. Shapley fair-blame is opt-in.
type Prune struct {
	Build       `yaml:",inline" json:",inline" mapstructure:",squash"`
	Why         bool `yaml:"why" json:"why" mapstructure:"why"`
	Blame       bool `yaml:"blame" json:"blame" mapstructure:"blame"`
	HideIgnored bool `yaml:"hide-ignored" json:"hide-ignored" mapstructure:"hide-ignored"`
	Top         int  `yaml:"top" json:"top" mapstructure:"top"`
}

func DefaultPrune() Prune {
	return Prune{Top: defaultTop}
}

func (o *Prune) AddFlags(flags fangs.FlagSet) {
	flags.BoolVarP(&o.Why, "why", "",
		"show import-why trees: under each candidate, the \"← imported by\" trace back to your 1st-class code")
	flags.BoolVarP(&o.Blame, "blame", "",
		"also compute Shapley fair-blame: each target's fair share of shared weight")
	flags.BoolVarP(&o.HideIgnored, "hide-ignored", "",
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
	d.Add(&o.HideIgnored, "omit locked modules from output instead of de-emphasizing them")
	d.Add(&o.Top, "maximum number of rows to show in each ranked table")
}
