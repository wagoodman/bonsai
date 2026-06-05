package commands

import (
	"github.com/spf13/cobra"
)

const pathArg = "DIR"

// chainArgs composes several cobra positional-argument processors into one, running them
// in order and stopping at the first error.
func chainArgs(processors ...func(cmd *cobra.Command, args []string) error) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		for _, p := range processors {
			if err := p(cmd, args); err != nil {
				return err
			}
		}
		return nil
	}
}
