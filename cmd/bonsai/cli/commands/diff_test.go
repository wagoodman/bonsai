package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDiffArgs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		binary  bool
		wantDir string
		wantRef string
	}{
		{name: "ref only", args: []string{"main"}, wantDir: "", wantRef: "main"},
		{name: "dir and ref", args: []string{"./cmd/app", "main"}, wantDir: "./cmd/app", wantRef: "main"},
		{name: "binary baseline no args", args: []string{}, binary: true, wantDir: "", wantRef: ""},
		{name: "binary baseline with dir", args: []string{"./cmd/app"}, binary: true, wantDir: "./cmd/app", wantRef: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, ref := parseDiffArgs(tt.args, tt.binary)
			assert.Equal(t, tt.wantDir, dir)
			assert.Equal(t, tt.wantRef, ref)
		})
	}
}
