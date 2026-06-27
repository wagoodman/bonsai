package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDiffArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantDir string
		wantRef string
	}{
		{name: "ref only", args: []string{"main"}, wantDir: "", wantRef: "main"},
		{name: "dir and ref", args: []string{"./cmd/app", "main"}, wantDir: "./cmd/app", wantRef: "main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, ref := parseDiffArgs(tt.args)
			assert.Equal(t, tt.wantDir, dir)
			assert.Equal(t, tt.wantRef, ref)
		})
	}
}
