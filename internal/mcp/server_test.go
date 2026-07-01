package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connect wires an in-memory client to a freshly built bonsai MCP server, isolating the tool
// surface from the stdio transport and the clio CLI. The server is run in a goroutine until the
// test ends.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	srv := NewServer("test")
	serverT, clientT := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.Run(ctx, serverT) }()
	t.Cleanup(cancel)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0"}, nil)
	cs, err := client.Connect(context.Background(), clientT, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// the server must advertise exactly the intent-named tools.
func TestServerListsTools(t *testing.T) {
	cs := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	require.NoError(t, err)

	got := map[string]bool{}
	for _, tool := range res.Tools {
		got[tool.Name] = true
		assert.NotEmpty(t, tool.Description, "tool %s missing description", tool.Name)
	}
	want := []string{
		"bonsai_anatomy", "bonsai_size_targets", "bonsai_go_floor", "bonsai_locate_cuts", "bonsai_measure",
		"bonsai_diff", "bonsai_check", "bonsai_matrix",
	}
	for _, name := range want {
		assert.Truef(t, got[name], "missing tool %s", name)
	}
	assert.Len(t, res.Tools, len(want))
}

// calling bonsai_size_targets against the bonsai module itself returns ranked candidates with
// the cost/benefit fields and a verdict — the end-to-end path through Resolve, the engine, and
// the DTO mapping.
func TestSizeTargetsEndToEnd(t *testing.T) {
	cs := connect(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "bonsai_size_targets",
		Arguments: map[string]any{"dir": "../.."},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool returned error: %+v", res.Content)

	var out SizeTargetsOutput
	require.NoError(t, json.Unmarshal(structured(t, res), &out))
	require.NotEmpty(t, out.Candidates, "expected prune candidates for the bonsai module")
	assert.NotZero(t, out.AccountedSize)

	// candidates are ranked by prize descending, and each has an effort label.
	for i, c := range out.Candidates {
		assert.NotEmpty(t, c.Effort, "candidate %s missing effort", c.Module)
		assert.GreaterOrEqual(t, c.PrizeBytes, c.FreedBytes, "prize must be >= freed for %s", c.Module)
		if i > 0 {
			assert.GreaterOrEqual(t, out.Candidates[i-1].PrizeBytes, c.PrizeBytes, "candidates not sorted by prize")
		}
	}
}

// locate_cuts returns the import sites and entry-package scope for a real dependency.
func TestLocateCutsEndToEnd(t *testing.T) {
	cs := connect(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "bonsai_locate_cuts",
		Arguments: map[string]any{"dir": "../..", "module": "github.com/charmbracelet/lipgloss"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool returned error: %+v", res.Content)

	var out struct {
		Module string `json:"module"`
		Sites  []struct {
			File       string `json:"file"`
			Line       int    `json:"line"`
			ImportPath string `json:"importPath"`
		} `json:"sites"`
		EntryPackages []struct {
			ImportPath    string `json:"importPath"`
			RetainedBytes uint64 `json:"retainedBytes"`
		} `json:"entryPackages"`
	}
	require.NoError(t, json.Unmarshal(structured(t, res), &out))
	assert.Equal(t, "github.com/charmbracelet/lipgloss", out.Module)
	require.NotEmpty(t, out.Sites, "expected import sites")
	for _, s := range out.Sites {
		assert.NotEmpty(t, s.File)
		assert.Positive(t, s.Line)
	}
}

// structured pulls the tool's structured result out as JSON for typed decoding.
func structured(t *testing.T, res *mcp.CallToolResult) []byte {
	t.Helper()
	b, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	return b
}
