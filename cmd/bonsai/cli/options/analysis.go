package options

import (
	"github.com/anchore/fangs"
)

var _ interface {
	fangs.FlagAdder
	fangs.PostLoader
	fangs.FieldDescriber
} = (*Analysis)(nil)

// Analysis holds the inputs that control how a module is built and analyzed and how much
// of the ranked output is shown. The module directory is taken from the positional
// argument; Dir here mirrors it for the loaded config.
type Analysis struct {
	// bound options (appear in --help)
	Target      string   `yaml:"target" json:"target" mapstructure:"target"`
	Binary      string   `yaml:"binary" json:"binary" mapstructure:"binary"`
	Controlled  []string `yaml:"controlled" json:"controlled" mapstructure:"controlled"`
	Ignore      []string `yaml:"ignore" json:"ignore" mapstructure:"ignore"`
	Unlock      []string `yaml:"unlock" json:"unlock" mapstructure:"unlock"`
	Blame       bool     `yaml:"blame" json:"blame" mapstructure:"blame"`
	NoWhy       bool     `yaml:"no-why" json:"no-why" mapstructure:"no-why"`
	HideIgnored bool     `yaml:"hide-ignored" json:"hide-ignored" mapstructure:"hide-ignored"`
	Top         int      `yaml:"top" json:"top" mapstructure:"top"`

	Dir string `yaml:"-" json:"-" mapstructure:"-"` // set from the positional directory argument
}

const defaultTop = 40

func DefaultAnalysis() Analysis {
	return Analysis{
		Top: defaultTop,
	}
}

func (o *Analysis) AddFlags(flags fangs.FlagSet) {
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
	flags.BoolVarP(&o.Blame, "blame", "",
		"also compute Shapley fair-blame: each target's fair share of shared weight")
	flags.BoolVarP(&o.NoWhy, "no-why", "",
		"hide the import-why trees (the \"← imported by\" traces shown under modules)")
	flags.BoolVarP(&o.HideIgnored, "hide-ignored", "",
		"omit locked modules from output instead of de-emphasizing them")
	flags.IntVarP(&o.Top, "top", "t",
		"number of rows to show in ranked tables")
}

func (o *Analysis) PostLoad() error {
	// a non-positive limit is meaningless; fall back to the default rather than render
	// empty tables.
	if o.Top <= 0 {
		o.Top = defaultTop
	}
	return nil
}

func (o *Analysis) DescribeFields(d fangs.FieldDescriptionSet) {
	d.Add(&o.Target, "entrypoint package to build and analyze")
	d.Add(&o.Binary, "prebuilt binary to analyze instead of building from source")
	d.Add(&o.Controlled, "1st-class module patterns whose imports are cuttable, beyond the main module "+
		"(e.g. \"github.com/anchore/...\" to control a whole org transitively)")
	d.Add(&o.Ignore, "module patterns never suggested for pruning, i.e. locked (exact, glob, or \"path/...\")")
	d.Add(&o.Unlock, "locked modules to re-open as prune candidates (overrides the default lock on controlled modules)")
	d.Add(&o.Blame, "also compute Shapley fair-blame attribution across prune targets")
	d.Add(&o.NoWhy, "hide the import-why trees (the \"imported by\" traces shown under modules)")
	d.Add(&o.HideIgnored, "omit locked modules from output instead of de-emphasizing them")
	d.Add(&o.Top, "maximum number of rows to show in each ranked table")
}
