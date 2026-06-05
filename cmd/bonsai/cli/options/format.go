package options

import (
	"fmt"
	"strings"

	"github.com/anchore/fangs"
)

var _ interface {
	fangs.FlagAdder
	fangs.PostLoader
} = (*Format)(nil)

type Format struct {
	Output           string   `yaml:"output" json:"output" mapstructure:"output"`
	AllowableFormats []string `yaml:"-" json:"-" mapstructure:"-"`
}

func (o *Format) AddFlags(flags fangs.FlagSet) {
	flags.StringVarP(
		&o.Output,
		"output", "o",
		fmt.Sprintf("output format to report results in (allowable values: %s)", strings.Join(o.AllowableFormats, ", ")),
	)
}

func (o *Format) PostLoad() error {
	if len(o.AllowableFormats) == 0 {
		return nil
	}
	for _, f := range o.AllowableFormats {
		if strings.EqualFold(f, o.Output) {
			return nil
		}
	}
	return fmt.Errorf("invalid output format %q (allowable values: %s)", o.Output, strings.Join(o.AllowableFormats, ", "))
}
