package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

func TestIsEmptyInputs(t *testing.T) {
	tests := []struct {
		name string
		in   bonsai.ClassInputs
		want bool
	}{
		{name: "all empty", in: bonsai.ClassInputs{}, want: true},
		{name: "controlled set", in: bonsai.ClassInputs{Controlled: []string{"github.com/x/a"}}, want: false},
		{name: "locked set", in: bonsai.ClassInputs{Locked: []string{"github.com/x/a"}}, want: false},
		{name: "unlock set", in: bonsai.ClassInputs{Unlock: []string{"github.com/x/a"}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isEmptyInputs(tt.in))
		})
	}
}
