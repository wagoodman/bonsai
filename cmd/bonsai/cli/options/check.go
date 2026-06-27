package options

import (
	"github.com/anchore/fangs"
)

var _ fangs.FieldDescriber = (*Check)(nil)

// Check is the bonsai check budget: the CI gate thresholds, read from the config's `check:` block.
// Sizes are human strings ("25MB"); an empty field means that rule is not enforced. An absent
// block means there is nothing to check.
type Check struct {
	MaxBinarySize string            `yaml:"max-binary-size" json:"max-binary-size" mapstructure:"max-binary-size"`
	MaxGoVersion  string            `yaml:"max-go-version" json:"max-go-version" mapstructure:"max-go-version"`
	Deny          []string          `yaml:"deny" json:"deny" mapstructure:"deny"`
	MaxModuleSize map[string]string `yaml:"max-module-size" json:"max-module-size" mapstructure:"max-module-size"`
	Action        string            `yaml:"action" json:"action" mapstructure:"action"` // what a violation does: fail | warn (default "fail")
}

func (o *Check) DescribeFields(d fangs.FieldDescriptionSet) {
	d.Add(&o.MaxBinarySize, "fail if the binary exceeds this size (human string, e.g. \"25MB\"); "+
		"gates the accounted (~ stripped / release) size by default, or the literal on-disk size when --binary is given")
	d.Add(&o.MaxGoVersion, "fail if the dep-imposed go-version floor exceeds this directive (e.g. \"1.23\")")
	d.Add(&o.Deny, "module patterns that must never appear in the build (exact, glob, or \"path/...\")")
	d.Add(&o.MaxModuleSize, "per-module self-size caps, module pattern -> human size (e.g. \"2MB\")")
	d.Add(&o.Action, "what a budget violation does: fail (non-zero exit) or warn (print only); default: fail")
}
