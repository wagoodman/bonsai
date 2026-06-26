package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInputConfigMergesBonsaiYAML(t *testing.T) {
	tests := []struct {
		name           string
		file           string // .bonsai.yaml content; empty means no file
		in             Input
		wantControlled []string
		wantLocked     []string
		wantUnlock     []string
		wantErr        require.ErrorAssertionFunc
	}{
		{
			name: "config lock honored when agent omits it",
			file: "analysis:\n  lock:\n    - github.com/a/b\n",
			in:   Input{},
			// the whole point: an agent that passes no lock still respects the curated lock.
			wantLocked: []string{"github.com/a/b"},
		},
		{
			name: "agent and config lists are unioned, sorted, deduped",
			file: "analysis:\n" +
				"  lock:\n    - github.com/a/b\n    - github.com/c/d\n" +
				"  controlled:\n    - github.com/me/...\n" +
				"  unlock:\n    - github.com/x/y\n",
			in: Input{
				Lock:       []string{"github.com/c/d", "github.com/e/f"},
				Controlled: []string{"github.com/you/..."},
			},
			wantControlled: []string{"github.com/me/...", "github.com/you/..."},
			wantLocked:     []string{"github.com/a/b", "github.com/c/d", "github.com/e/f"},
			wantUnlock:     []string{"github.com/x/y"},
		},
		{
			name:       "no config file leaves agent input intact",
			file:       "",
			in:         Input{Lock: []string{"github.com/a/b"}},
			wantLocked: []string{"github.com/a/b"},
		},
		{
			name:    "malformed config surfaces an error",
			file:    "analysis: [unterminated\n",
			in:      Input{},
			wantErr: require.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr == nil {
				tt.wantErr = require.NoError
			}
			dir := t.TempDir()
			if tt.file != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, ".bonsai.yaml"), []byte(tt.file), 0o644))
			}
			in := tt.in
			in.Dir = dir

			cfg, err := in.config()
			tt.wantErr(t, err)
			if err != nil {
				return
			}
			assert.Equal(t, dir, cfg.Dir)
			assert.Equal(t, tt.wantControlled, cfg.Controlled)
			assert.Equal(t, tt.wantLocked, cfg.Locked)
			assert.Equal(t, tt.wantUnlock, cfg.Unlock)
		})
	}
}
