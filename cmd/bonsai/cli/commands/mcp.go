package commands

import (
	"github.com/anchore/clio"
	"github.com/spf13/cobra"

	"github.com/wagoodman/bonsai/internal/mcp"
)

// MCP is the `bonsai mcp` command: a Model Context Protocol server exposing bonsai's analysis to
// an AI agent. It is a plain cobra command (not wired through clio's UI) because the stdio
// transport owns stdout for JSON-RPC — no progress UI may write there. Build progress and logs
// go to stderr; the server only ever writes protocol frames to stdout.
func MCP(id clio.Identification) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "run a Model Context Protocol server exposing bonsai's analysis to an AI agent",
		Long: "mcp starts a Model Context Protocol server over stdio, exposing bonsai as a yardstick for an AI " +
			"agent operating in a codebase: bonsai_size_targets ranks prune candidates, bonsai_go_floor reports " +
			"the minimum go directive and what pins it, bonsai_locate_cuts gives the exact edit sites and " +
			"consequences for one module, bonsai_anatomy shows the size shape, bonsai_measure cheaply " +
			"re-checks size and go-version after an edit, bonsai_diff reports the size/floor delta against a git " +
			"ref, bonsai_check evaluates the committed budget, and bonsai_matrix reports the worst-case floor " +
			"across a build matrix. Builds are cached and re-run automatically when source changes, so the edit " +
			"loop just works.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return mcp.Serve(cmd.Context(), id.Version)
		},
	}
}
