package mcp

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// openObjectSchema is a permissive output schema used for the tools whose result embeds the
// recursive ImportNode (the import-why tree), which the SDK's schema inference can't express as
// a cycle. The full structured data still flows in the result; only the advertised schema is
// relaxed to a plain object. Tradeoff: relaxing the schema also drops the per-field jsonschema
// descriptions on those tools' DTOs from what the client sees — the data is unchanged, but the
// field-level docs don't reach the agent. It is applied only where ImportNode actually appears
// (anatomy, size_targets via PrunePlanStep.Why, locate_cuts); go_floor and measure keep their
// inferred, fully-described schemas.
var openObjectSchema = json.RawMessage(`{"type":"object"}`)

// Server wraps the MCP server and the warm build cache shared across its tools.
type Server struct {
	mcp   *mcp.Server
	cache *resolveCache
}

// NewServer assembles the bonsai MCP server with its five tools. version is stamped into the
// server implementation info reported to clients.
func NewServer(version string) *Server {
	s := &Server{
		mcp:   mcp.NewServer(&mcp.Implementation{Name: "bonsai", Title: "bonsai binary-size guide", Version: version}, nil),
		cache: newResolveCache(),
	}

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "bonsai_anatomy",
		Description: "Report the binary's size shape: total size and what occupies it, attributed by content " +
			"(code / data / pclntab) and by owner (which module). Use to orient before pruning and to read the " +
			"size after edits.",
		OutputSchema: openObjectSchema,
	}, s.anatomy)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "bonsai_size_targets",
		Description: "Rank which dependencies, if removed, free the most binary bytes — and in what order. Each " +
			"candidate carries the benefit (bytes freed alone, and the larger freeable subtree) and the cost " +
			"(how many first-party packages and import statements reference it), plus a coarse effort verdict. " +
			"Use to find high-value, least-work prune or replace candidates.",
		OutputSchema: openObjectSchema,
	}, s.sizeTargets)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "bonsai_go_floor",
		Description: "Report the lowest go directive your owned modules can declare, which dependencies pin that " +
			"floor, and an actionable verdict: whether you can lower the directive right now (you over-declare) " +
			"or which deps must be pruned to go lower. Use to lower a project's minimum Go version.",
	}, s.goFloor)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "bonsai_locate_cuts",
		Description: "Drill into one module: the exact first-party import sites to edit (file:line), the " +
			"per-entry-package retained bytes (how much each imported package is worth — the scope of a partial " +
			"rewrite), what other modules leave vs survive if it is pruned (and who holds the survivors), and " +
			"whether pruning it lowers your go-version floor. Use to act on a chosen prune/replace/rewrite target.",
		OutputSchema: openObjectSchema,
	}, s.locateCuts)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "bonsai_measure",
		Description: "Cheaply report just the binary size and the go-version floor, skipping the prune ranking. " +
			"Use to confirm an edit had the intended effect in a tight loop.",
	}, s.measure)

	return s
}

// Run serves the MCP protocol over the given transport until the client disconnects or ctx is
// cancelled, then releases cached build artifacts.
func (s *Server) Run(ctx context.Context, t mcp.Transport) error {
	defer s.cache.close()
	return s.mcp.Run(ctx, t)
}

// Serve runs a bonsai MCP server over stdio — the entrypoint for `bonsai mcp`.
func Serve(ctx context.Context, version string) error {
	return NewServer(version).Run(ctx, &mcp.StdioTransport{})
}
