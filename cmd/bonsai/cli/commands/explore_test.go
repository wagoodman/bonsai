package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/wagoodman/bonsai/internal/bonsai"
)

func TestSortedUnique(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "empty", in: nil, want: nil},
		{name: "only empties", in: []string{"", ""}, want: nil},
		{name: "sorts", in: []string{"c", "a", "b"}, want: []string{"a", "b", "c"}},
		{name: "dedupes and drops empty", in: []string{"b", "a", "b", "", "a"}, want: []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sortedUnique(tt.in))
		})
	}
}

func TestSameInputs(t *testing.T) {
	tests := []struct {
		name string
		a    bonsai.ClassInputs
		b    bonsai.ClassInputs
		want bool
	}{
		{
			name: "both empty",
			want: true,
		},
		{
			name: "equal across all lists",
			a:    bonsai.ClassInputs{Controlled: []string{"a"}, Locked: []string{"b"}, Unlock: []string{"c"}},
			b:    bonsai.ClassInputs{Controlled: []string{"a"}, Locked: []string{"b"}, Unlock: []string{"c"}},
			want: true,
		},
		{
			name: "differ in locked",
			a:    bonsai.ClassInputs{Locked: []string{"a"}},
			b:    bonsai.ClassInputs{Locked: []string{"a", "b"}},
			want: false,
		},
		{
			name: "differ in controlled",
			a:    bonsai.ClassInputs{Controlled: []string{"a"}},
			b:    bonsai.ClassInputs{Controlled: []string{"z"}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, sameInputs(tt.a, tt.b))
		})
	}
}
