package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEndToEndGoFloor(t *testing.T) {
	s := newSession(t)

	floor := s.GoFloor(nil)
	assert.Equal(t, "1.23", floor.Version, "libc's directive pins the dep floor")
	assert.Contains(t, floor.Critical, libc, "libc is the module holding the floor up")
	assert.Equal(t, "1.25", floor.OwnedMax, "app declares go 1.25")
	assert.Equal(t, "1.21", floor.NextVersion, "dropping libc would leave the 1.21 deps as the floor")

	// pruning everything that pulls libc in removes the floor entirely.
	dropped := s.GoFloor(map[string]bool{liba: true, libs: true})
	assert.NotEqual(t, "1.23", dropped.Version, "floor must drop once libc leaves the build")
}

// TestEndToEndGoFloorDynamics uses the deep fixture's directives (a and b tied at 1.24, s at 1.23)
// to cover the parts the basic floor test can't: multiple modules tied at the floor, and the floor
// dropping as the pinning targets are pruned.
func TestEndToEndGoFloorDynamics(t *testing.T) {
	s := newSessionAt(t, deepDir(t))

	floor := s.GoFloor(nil)
	assert.Equal(t, "1.24", floor.Version)
	assert.ElementsMatch(t, []string{deepA, deepB}, floor.Critical, "a and b are tied at the floor")
	assert.Equal(t, "1.23", floor.NextVersion, "s (1.23) is the next floor once a and b go")

	// pruning one of the tied pair leaves the other still pinning 1.24.
	one := s.GoFloor(map[string]bool{deepA: true})
	assert.Equal(t, "1.24", one.Version)
	assert.ElementsMatch(t, []string{deepB}, one.Critical)

	// pruning both drops the floor to s's 1.23.
	both := s.GoFloor(map[string]bool{deepA: true, deepB: true})
	assert.Equal(t, "1.23", both.Version)
	assert.ElementsMatch(t, []string{deepS}, both.Critical)
}
