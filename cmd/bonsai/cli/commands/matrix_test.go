package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

func TestParsePlatform(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		extraTags []string
		want      bonsai.Platform
		wantErr   bool
	}{
		{name: "os/arch", in: "linux/amd64", want: bonsai.Platform{GOOS: "linux", GOARCH: "amd64"}},
		{name: "os/arch+tags", in: "linux/amd64+netgo,cgo", want: bonsai.Platform{GOOS: "linux", GOARCH: "amd64", Tags: []string{"netgo", "cgo"}}},
		{name: "extra tags appended", in: "darwin/arm64", extraTags: []string{"netgo"}, want: bonsai.Platform{GOOS: "darwin", GOARCH: "arm64", Tags: []string{"netgo"}}},
		{name: "missing arch", in: "linux", wantErr: true},
		{name: "empty arch", in: "linux/", wantErr: true},
		{name: "empty", in: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parsePlatform(tt.in, tt.extraTags)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
