package report

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

func TestHumize(t *testing.T) {
	// decimal units (1000-based), one fractional digit above the byte threshold.
	tests := []struct {
		name string
		in   uint64
		want string
	}{
		{name: "zero", in: 0, want: "0 B"},
		{name: "below the unit boundary", in: 999, want: "999 B"},
		{name: "exactly one kB", in: 1000, want: "1.0 kB"},
		{name: "fractional kB", in: 1500, want: "1.5 kB"},
		{name: "one MB", in: 1_000_000, want: "1.0 MB"},
		{name: "fractional GB", in: 1_500_000_000, want: "1.5 GB"},
		{name: "TB", in: 2_000_000_000_000, want: "2.0 TB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, humize(tt.in))
		})
	}
}

func TestPctStr(t *testing.T) {
	tests := []struct {
		name        string
		part, whole uint64
		want        string
	}{
		{name: "zero whole avoids divide-by-zero", part: 5, whole: 0, want: "0.0%"},
		{name: "rounds to one decimal", part: 320, whole: 400, want: "80.0%"},
		{name: "full", part: 10, whole: 10, want: "100.0%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, pctStr(tt.part, tt.whole))
		})
	}
}

func TestGetPct(t *testing.T) {
	tests := []struct {
		name string
		p    *bonsai.PruneResult
		want string
	}{
		{name: "no potential renders a dash", p: &bonsai.PruneResult{FreedBytes: 0, PotentialBytes: 0}, want: "-"},
		{name: "share of subtree freed", p: &bonsai.PruneResult{FreedBytes: 320, PotentialBytes: 400}, want: "80.0%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, getPct(tt.p))
		})
	}
}

func TestJoinModules(t *testing.T) {
	tests := []struct {
		name    string
		modules []string
		n       int
		want    string
	}{
		{name: "empty", modules: nil, n: 3, want: ""},
		{name: "under the limit", modules: []string{"a", "b"}, n: 3, want: "a, b"},
		{name: "at the limit", modules: []string{"a", "b", "c"}, n: 3, want: "a, b, c"},
		{name: "over the limit collapses the overflow", modules: []string{"a", "b", "c", "d", "e"}, n: 3, want: "a, b, c +2 more"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, joinModules(tt.modules, tt.n))
		})
	}
}

func TestShortModule(t *testing.T) {
	tests := []struct {
		name   string
		module string
		want   string
	}{
		{name: "empty is the main module", module: "", want: "main"},
		{name: "last path element", module: "github.com/example/foo", want: "foo"},
		{name: "no slash returns the whole thing", module: "single", want: "single"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shortModule(tt.module))
		})
	}
}

func TestImporterNote(t *testing.T) {
	tests := []struct {
		name      string
		importers int
		want      string
	}{
		{name: "none says nothing", importers: 0, want: ""},
		{name: "negative says nothing", importers: -1, want: ""},
		{name: "singular", importers: 1, want: "  (imported by 1 module)"},
		{name: "plural", importers: 5, want: "  (imported by 5 modules)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, importerNote(tt.importers))
		})
	}
}

func TestKindLabel(t *testing.T) {
	tests := []struct {
		name string
		m    bonsai.ModuleSize
		want string
	}{
		{name: "locked wins over direct", m: bonsai.ModuleSize{Ignored: true, Direct: true}, want: "locked"},
		{name: "direct dependency", m: bonsai.ModuleSize{Direct: true}, want: "direct"},
		{name: "indirect dependency", m: bonsai.ModuleSize{}, want: "indirect"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, kindLabel(tt.m))
		})
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain text untouched", in: "hello", want: "hello"},
		{name: "single sequence stripped", in: "\x1b[31mhello\x1b[0m", want: "hello"},
		{name: "multiple sequences stripped", in: "\x1b[1m\x1b[32mok\x1b[0m done", want: "ok done"},
		{name: "lone sequence", in: "\x1b[31m", want: ""},
		{name: "unterminated escape drops the tail", in: "keep\x1b[1", want: "keep"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, stripANSI(tt.in))
		})
	}
}
