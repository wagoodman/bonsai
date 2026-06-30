package report

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

func TestMaxSize(t *testing.T) {
	assert.Equal(t, uint64(0), maxSize(nil), "empty map is zero")
	assert.Equal(t, uint64(1500), maxSize(map[string]uint64{"a": 1000, "b": 1500, "c": 500}))
}

func TestFirstLine(t *testing.T) {
	assert.Equal(t, "boom", firstLine("boom"))
	assert.Equal(t, "first", firstLine("\n  \nfirst\nsecond"), "skips blank/whitespace lines")
	assert.Equal(t, "   \n\t\n", firstLine("   \n\t\n"), "all-whitespace falls through to the original")
}

// TestMatrixHeadlineAllFailed: when no cell succeeds, the headline says so instead of printing
// the misleading "no dependency imposes a go floor" success line.
func TestMatrixHeadlineAllFailed(t *testing.T) {
	rep := &bonsai.MatrixReport{
		Cells: []bonsai.CellResult{
			{Label: "linux/amd64", Err: "no cross toolchain"},
			{Label: "windows/amd64", Err: "no cross toolchain"},
		},
	}

	buf := &strings.Builder{}
	err := WriteMatrixTable(buf, rep, false, false)
	assert.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "all 2 cells failed to build")
	assert.NotContains(t, out, "no dependency imposes a go floor")
}
