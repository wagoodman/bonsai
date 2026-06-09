package bonsai

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name       string
		shared     bool
		controlled []string
		locked     []string
		unlock     []string
		wantClass  map[string]moduleClass
		wantTarget map[string]bool
	}{
		{
			name:       "main only controlled reduces to direct deps",
			controlled: nil, // only the main module is controlled
			wantClass: map[string]moduleClass{
				"app": classMain, "stereo": classSecond, "syft": classSecond,
				"gcr": classThird, "docker": classThird, "oci": classThird,
			},
			// only modules directly imported by main (controlled) are targets.
			wantTarget: map[string]bool{"stereo": true, "syft": true},
		},
		{
			name:       "controlled org widens the frontier",
			controlled: []string{"stereo", "syft"},
			wantClass: map[string]moduleClass{
				"app": classMain, "stereo": classFirst, "syft": classFirst,
				"gcr": classSecond, "docker": classThird, "oci": classThird,
			},
			// stereo/syft are controlled+locked (not targets); gcr is the 2nd-class target.
			wantTarget: map[string]bool{"gcr": true},
		},
		{
			name:       "locking a 2nd-class dep removes it as a target",
			controlled: []string{"stereo", "syft"},
			locked:     []string{"gcr"},
			wantTarget: map[string]bool{}, // gcr locked, nothing else droppable
		},
		{
			name:       "unlocking a controlled module makes it a target",
			controlled: []string{"stereo", "syft"},
			unlock:     []string{"stereo"},
			// stereo is imported by main (controlled) and now unlocked → a target.
			wantTarget: map[string]bool{"gcr": true, "stereo": true},
		},
		{
			name:       "shared: oci directly imported by syft becomes a 2nd-class target",
			shared:     true,
			controlled: []string{"stereo", "syft"},
			wantClass: map[string]moduleClass{
				"gcr": classSecond, "oci": classSecond, "docker": classThird,
			},
			wantTarget: map[string]bool{"gcr": true, "oci": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := userScenario(tt.shared).build()
			c := classify(g, newPatternMatcher(tt.controlled), newPatternMatcher(tt.locked), newPatternMatcher(tt.unlock))

			for mod, want := range tt.wantClass {
				assert.Equalf(t, want, c.classOf(mod), "class of %s", mod)
			}
			if tt.wantTarget != nil {
				got := map[string]bool{}
				for _, target := range c.targets() {
					got[target] = true
				}
				assert.Equal(t, tt.wantTarget, got, "prune targets")
			}
		})
	}
}
