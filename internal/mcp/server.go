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

// NewServer assembles the bonsai MCP server with its analysis tools. version is stamped into the
// server implementation info reported to clients.
func NewServer(version string) *Server { //nolint:funlen // one cohesive registration of the tool set with its descriptions
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
		Description: "Rank dependencies by prize — the bytes at stake if the module left the binary — on two axes: " +
			"the prize (full-graph retained size, across the controlled boundary) and the effort to realize it. A " +
			"big prize with freedBytes 0 is NOT a non-candidate: it is weight pinned by an uncontrolled dep, named in " +
			"pinnedBy (replace/patch/upstream it), with prizeByEntryPackage showing where the weight concentrates. " +
			"effort is quickWin, coordinated (co-prune other targets), pinnedByDep, or core. Weigh prize against " +
			"effort rather than grabbing the easiest cut; the biggest win is often pinnedByDep, not the top freedBytes.",
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

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "bonsai_diff",
		Description: "Report the size and go-version-floor delta this branch makes against a git ref: the net binary " +
			"size change, which modules were added/removed/changed (direct vs transitive), and any floor movement. " +
			"Builds both the working tree and the merge-base baseline from source. Use to answer \"what did my change " +
			"do to the binary?\" against a committed ref.",
	}, s.diff)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "bonsai_check",
		Description: "Evaluate the project's committed size / go-version / deny-list budget (the config's check: block) " +
			"against the built target and report pass/fail with the specific violations. Use to confirm a change still " +
			"passes the CI gate. An absent budget reports configured=false, not a failure.",
	}, s.check)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "bonsai_matrix",
		Description: "Run the analysis across a build matrix (GOOS/GOARCH/tags) and report the worst-case go floor — the " +
			"MAX over every platform shipped, the number that actually constrains go.mod — plus which modules are " +
			"universal versus platform-specific. Cells come from the platforms argument, else analysis.matrix, else " +
			".goreleaser.yaml, else a default set. Floor-only by default (no builds, cross-compiles without a cgo " +
			"toolchain); set size to build each cell for the exact post-DCE floor and per-cell size.",
		OutputSchema: openObjectSchema, // CellResult.Size embeds ModuleSize.Why (recursive ImportNode)
	}, s.matrix)

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
