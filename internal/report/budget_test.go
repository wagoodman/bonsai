package report

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

func TestParseBudget(t *testing.T) {
	t.Run("absent block is empty/unconfigured", func(t *testing.T) {
		b, err := ParseBudget(BudgetSpec{})
		require.NoError(t, err)
		assert.False(t, b.configured())
		assert.Equal(t, ActionFail, b.action) // defaults to fail
	})

	t.Run("parses sizes and defaults action", func(t *testing.T) {
		b, err := ParseBudget(BudgetSpec{
			MaxBinarySize: "25MB",
			MaxModuleSize: map[string]string{"github.com/foo/bar": "2MB"},
		})
		require.NoError(t, err)
		assert.True(t, b.configured())
		assert.Equal(t, uint64(25_000_000), b.maxBinarySize)
		require.Len(t, b.moduleCaps, 1)
		assert.Equal(t, uint64(2_000_000), b.moduleCaps[0].limit)
	})

	t.Run("bad size string errors", func(t *testing.T) {
		_, err := ParseBudget(BudgetSpec{MaxBinarySize: "twenty megs"})
		assert.Error(t, err)
		_, err = ParseBudget(BudgetSpec{MaxModuleSize: map[string]string{"x": "huge"}})
		assert.Error(t, err)
	})

	t.Run("unknown action errors", func(t *testing.T) {
		_, err := ParseBudget(BudgetSpec{Action: "explode"})
		assert.Error(t, err)
	})
}

func TestEvaluateBudget(t *testing.T) {
	size := func(accounted, binary uint64, mods ...bonsai.ModuleSize) bonsai.SizeReport {
		return bonsai.SizeReport{AccountedSize: accounted, BinarySize: binary, Modules: mods}
	}
	inBuild := func(path string, sz uint64) bonsai.ModuleSize {
		return bonsai.ModuleSize{Module: path, Size: sz, InBuild: true}
	}

	tests := []struct {
		name      string
		size      bonsai.SizeReport
		floor     bonsai.GoFloor
		check     BudgetSpec
		binaryArt bool
		wantPass  bool
		wantRules []string // rules expected to appear in violations
	}{
		{
			name:     "binary under cap passes",
			size:     size(20_000_000, 22_000_000),
			check:    BudgetSpec{MaxBinarySize: "25MB"},
			wantPass: true,
		},
		{
			name:     "binary exactly at cap passes",
			size:     size(25_000_000, 25_000_000), // == limit, the gate is strictly >
			check:    BudgetSpec{MaxBinarySize: "25MB"},
			wantPass: true,
		},
		{
			name:      "binary one byte over cap fails",
			size:      size(25_000_001, 25_000_001),
			check:     BudgetSpec{MaxBinarySize: "25MB"},
			wantPass:  false,
			wantRules: []string{"max-binary-size"},
		},
		{
			name:      "binary over cap fails on accounted size",
			size:      size(27_000_000, 30_000_000),
			check:     BudgetSpec{MaxBinarySize: "25MB"},
			wantPass:  false,
			wantRules: []string{"max-binary-size"},
		},
		{
			name:      "binary artifact gates on-disk size",
			size:      size(20_000_000, 30_000_000), // accounted under, on-disk over
			check:     BudgetSpec{MaxBinarySize: "25MB"},
			binaryArt: true,
			wantPass:  false,
			wantRules: []string{"max-binary-size"},
		},
		{
			name:      "go floor above budget fails",
			size:      size(1, 1),
			floor:     bonsai.GoFloor{Version: "1.24"},
			check:     BudgetSpec{MaxGoVersion: "1.23"},
			wantPass:  false,
			wantRules: []string{"max-go-version"},
		},
		{
			name:     "go floor at budget passes",
			size:     size(1, 1),
			floor:    bonsai.GoFloor{Version: "1.23"},
			check:    BudgetSpec{MaxGoVersion: "1.23"},
			wantPass: true,
		},
		{
			name:     "empty floor never violates",
			size:     size(1, 1),
			floor:    bonsai.GoFloor{}, // no dep imposes a floor
			check:    BudgetSpec{MaxGoVersion: "1.21"},
			wantPass: true,
		},
		{
			name:      "denied module in build fails",
			size:      size(1, 1, inBuild("github.com/aws/aws-sdk-go", 5_000_000)),
			check:     BudgetSpec{Deny: []string{"github.com/aws/aws-sdk-go"}},
			wantPass:  false,
			wantRules: []string{"deny"},
		},
		{
			name:     "denied module not in build passes",
			size:     size(1, 1, inBuild("github.com/other/thing", 1000)),
			check:    BudgetSpec{Deny: []string{"github.com/aws/aws-sdk-go"}},
			wantPass: true,
		},
		{
			name:      "module over cap by exact path fails",
			size:      size(1, 1, inBuild("github.com/klauspost/compress", 3_000_000)),
			check:     BudgetSpec{MaxModuleSize: map[string]string{"github.com/klauspost/compress": "2MB"}},
			wantPass:  false,
			wantRules: []string{"max-module-size"},
		},
		{
			name:      "module over cap by pattern fails",
			size:      size(1, 1, inBuild("github.com/klauspost/compress", 3_000_000)),
			check:     BudgetSpec{MaxModuleSize: map[string]string{"github.com/klauspost/...": "2MB"}},
			wantPass:  false,
			wantRules: []string{"max-module-size"},
		},
		{
			name:     "module exactly at cap passes",
			size:     size(1, 1, inBuild("github.com/klauspost/compress", 2_000_000)), // == cap, strictly >
			check:    BudgetSpec{MaxModuleSize: map[string]string{"github.com/klauspost/compress": "2MB"}},
			wantPass: true,
		},
		{
			name: "module not in the build is skipped for deny and caps",
			size: size(1, 1, bonsai.ModuleSize{Module: "github.com/aws/aws-sdk-go", Size: 9_000_000, InBuild: false}),
			check: BudgetSpec{
				Deny:          []string{"github.com/aws/aws-sdk-go"},
				MaxModuleSize: map[string]string{"github.com/aws/...": "2MB"},
			},
			wantPass: true, // a module present in go.mod but not linked into the build can't violate
		},
		{
			name:     "warn action keeps pass true",
			size:     size(27_000_000, 30_000_000),
			check:    BudgetSpec{MaxBinarySize: "25MB", Action: "warn"},
			wantPass: true,
		},
		{
			name:  "multiple violations counted",
			size:  size(27_000_000, 30_000_000, inBuild("github.com/aws/aws-sdk-go", 5_000_000)),
			floor: bonsai.GoFloor{Version: "1.24"},
			check: BudgetSpec{
				MaxBinarySize: "25MB",
				MaxGoVersion:  "1.23",
				Deny:          []string{"github.com/aws/aws-sdk-go"},
			},
			wantPass:  false,
			wantRules: []string{"max-binary-size", "max-go-version", "deny"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := ParseBudget(tt.check)
			require.NoError(t, err)
			rep := EvaluateBudget(tt.size, tt.floor, b, tt.binaryArt)
			assert.Equal(t, tt.wantPass, rep.Pass)

			got := map[string]bool{}
			for _, v := range rep.Violations {
				got[v.Rule] = true
			}
			for _, r := range tt.wantRules {
				assert.Truef(t, got[r], "expected a %q violation, got %+v", r, rep.Violations)
			}
		})
	}
}
