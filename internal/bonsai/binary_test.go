package bonsai

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPackageOfSymbol(t *testing.T) {
	tests := []struct {
		name string
		sym  string
		want string
	}{
		{name: "main package", sym: "main.main", want: "main"},
		{name: "stdlib function", sym: "fmt.Println", want: "fmt"},
		{name: "module function", sym: "github.com/foo/bar.Baz", want: "github.com/foo/bar"},
		{name: "method on pointer receiver", sym: "github.com/foo/bar.(*T).M", want: "github.com/foo/bar"},
		{name: "nested stdlib", sym: "internal/poll.(*FD).Read", want: "internal/poll"},
		{name: "empty is generated", sym: "", want: "<generated>"},
		{name: "no dot is generated", sym: "runtimeonly", want: "<generated>"},
		{name: "type metadata is generated", sym: "type:.eq.runtime._type", want: "<generated>"},
		{name: "go-prefixed is generated", sym: "go:itab.foo", want: "<generated>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, packageOfSymbol(tt.sym))
		})
	}
}

func TestHasPrefixAt(t *testing.T) {
	b := []byte{1, 2, 3, 4}
	tests := []struct {
		name   string
		off    int
		prefix []byte
		want   bool
	}{
		{name: "match at start", off: 0, prefix: []byte{1, 2}, want: true},
		{name: "match mid", off: 1, prefix: []byte{2, 3}, want: true},
		{name: "mismatch", off: 2, prefix: []byte{3, 5}, want: false},
		{name: "runs past the end", off: 3, prefix: []byte{4, 5}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, hasPrefixAt(b, tt.off, tt.prefix))
		})
	}
}

func TestIsDebugSection(t *testing.T) {
	tests := []struct {
		name string
		sec  string
		want bool
	}{
		{name: "elf debug", sec: ".debug_info", want: true},
		{name: "macho dwarf", sec: "__DWARF __debug_line", want: true},
		{name: "compressed debug", sec: ".zdebug_info", want: true},
		{name: "text is not debug", sec: ".text", want: false},
		{name: "rodata is not debug", sec: ".rodata", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isDebugSection(tt.sec))
		})
	}
}

func TestCountAttributable(t *testing.T) {
	syms := []binSymbol{{sect: 0}, {sect: -1}, {sect: 5}, {sect: -1}}
	assert.Equal(t, 2, countAttributable(syms))
	assert.Equal(t, 0, countAttributable(nil))
}

// TestAttributeFromSymbols exercises the format-agnostic core: per-symbol delta-fill within a
// section, code-vs-data classification, skipping of pclntab/debug/out-of-range symbols, and the
// proportional distribution of the gopclntab section across packages by code footprint.
func TestAttributeFromSymbols(t *testing.T) {
	secs := []binSection{
		{name: ".text", addr: 0x1000, size: 300, fileBacked: true, isText: true},
		{name: ".rodata", addr: 0x2000, size: 200, fileBacked: true},
		{name: ".gopclntab", addr: 0x3000, size: 100, fileBacked: true, isPclntb: true},
		{name: ".debug_info", addr: 0x4000, size: 999, fileBacked: true},
	}
	syms := []binSymbol{
		// text (section 0): three packages' code, sizes by delta to the next symbol / section end.
		{name: "github.com/foo/bar.A", addr: 0, sect: 0},   // 100
		{name: "github.com/foo/bar.B", addr: 100, sect: 0}, // 100
		{name: "github.com/baz/qux.C", addr: 200, sect: 0}, // 100 (to section end 300)
		// data (section 1): named data attributed but not counted as code.
		{name: "github.com/foo/bar.d", addr: 0, sect: 1},  // 50
		{name: "github.com/baz/qux.d", addr: 50, sect: 1}, // 150 (to section end 200)
		// symbols that must be ignored: pclntab, debug, out-of-range, and unassigned sections.
		{name: "github.com/foo/bar.pcln", addr: 0, sect: 2},
		{name: "github.com/foo/bar.dbg", addr: 0, sect: 3},
		{name: "github.com/foo/bar.oob", addr: 0, sect: 9},
		{name: "github.com/foo/bar.neg", addr: 0, sect: -1},
	}

	// PclntabSize is set by the caller (loadBinary) before attribution; CodeSize is 300, so the
	// 60-byte pclntab splits 40/20 between bar (200 code) and qux (100 code).
	info := &binaryInfo{SelfSize: map[string]uint64{}, CodeSelfSize: map[string]uint64{}, PclntabSize: 60}
	attributeFromSymbols(secs, syms, info)

	want := &binaryInfo{
		CodeSize:    300,
		DataSize:    200,
		PclntabSize: 60,
		CodeSelfSize: map[string]uint64{
			"github.com/foo/bar": 200,
			"github.com/baz/qux": 100,
		},
		SelfSize: map[string]uint64{
			"github.com/foo/bar": 290, // 200 code + 50 data + 40 pclntab
			"github.com/baz/qux": 270, // 100 code + 150 data + 20 pclntab
		},
	}
	if diff := cmp.Diff(want, info); diff != "" {
		t.Errorf("attribution mismatch (-want +got):\n%s", diff)
	}
}

const testBinarySource = `package main

import (
	"fmt"
	"strings"
)

func main() {
	fmt.Println(strings.ToUpper("hello bonsai"))
}
`

// buildTestBinary compiles a tiny module to a real executable so the platform-specific binary
// reader (Mach-O/ELF/PE) and full attribution path can be exercised end-to-end. Skips when the
// toolchain is unavailable or in -short mode, since it shells out to `go build`.
func buildTestBinary(t *testing.T, stripped bool) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping real-binary build in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/testbin\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(testBinarySource), 0o644))

	out := filepath.Join(dir, "testbin")
	args := []string{"build", "-o", out}
	if stripped {
		args = append(args, "-ldflags=-s -w")
	}
	args = append(args, ".")
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, b)
	}
	return out
}

func TestLoadBinaryUnstripped(t *testing.T) {
	info, err := loadBinary(buildTestBinary(t, false))
	require.NoError(t, err)

	assert.False(t, info.Stripped, "a default build carries a symbol table")
	assert.Positive(t, info.FileSize)
	assert.Positive(t, info.SectionsSize)
	assert.Positive(t, info.CodeSize)
	assert.Positive(t, info.DataSize, "unstripped builds attribute named data too")
	assert.NotEmpty(t, info.Sections)
	// buildinfo reports the module; symbol attribution buckets the entrypoint code under "main".
	assert.Equal(t, "example.com/testbin", info.MainModule)
	assert.Contains(t, info.SelfSize, "main")
	assert.Contains(t, info.SelfSize, "runtime")
}

func TestLoadBinaryStripped(t *testing.T) {
	info, err := loadBinary(buildTestBinary(t, true))
	require.NoError(t, err)

	assert.True(t, info.Stripped, "`-s -w` removes the symbol table")
	assert.Positive(t, info.CodeSize, "code is recovered from gopclntab even when stripped")
	assert.Zero(t, info.DataSize, "the stripped path attributes executable code only")
	assert.NotEmpty(t, info.SelfSize)
}
