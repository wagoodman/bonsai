package commands

import (
	"github.com/spf13/cobra"
)

const pathArg = "DIR"

// pathArgHelp describes the DIR positional argument, shared across commands.
const pathArgHelp = "the module directory to build and analyze (default: current directory)"

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
