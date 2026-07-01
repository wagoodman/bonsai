package integration

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/bonsai/internal/bonsai"
	"github.com/wagoodman/bonsai/internal/report"
)

// checkFixture resolves the fixture once and returns the size + floor reports the budget evaluator
// consumes, mirroring what the `check` command feeds into report.EvaluateBudget.
func checkFixture(t *testing.T) (bonsai.SizeReport, bonsai.GoFloor) {
	t.Helper()
	requireLong(t)
	r, err := bonsai.Resolve(bonsai.Config{Dir: fixtureDir(t)})
	require.NoError(t, err)
	t.Cleanup(r.Close)
	return r.Size(), r.GoFloor()
}

func evalBudget(t *testing.T, size bonsai.SizeReport, floor bonsai.GoFloor, spec report.BudgetSpec) report.CheckReport {
	t.Helper()
	b, err := report.ParseBudget(spec)
	require.NoError(t, err)
	return report.EvaluateBudget(size, floor, b, false)
}

func hasRule(rep report.CheckReport, rule string) bool {
	for _, v := range rep.Violations {
		if v.Rule == rule {
			return true
		}
	}
	return false
}

// TestEndToEndCheckBudget drives the CI-gate contract end to end: a real build's size + go-floor
// evaluated against budgets, asserting each rule fires (or doesn't) and the overall pass/fail.
func TestEndToEndCheckBudget(t *testing.T) {
	size, floor := checkFixture(t)

	// a comfortably-generous budget passes with no violations.
	clean := evalBudget(t, size, floor, report.BudgetSpec{MaxBinarySize: "1GB", MaxGoVersion: "1.30"})
	assert.True(t, clean.Pass)
	assert.Empty(t, clean.Violations)

	// size over budget -> max-binary-size violation, overall fail.
	overSize := evalBudget(t, size, floor, report.BudgetSpec{MaxBinarySize: "1MB"})
	assert.False(t, overSize.Pass)
	assert.True(t, hasRule(overSize, "max-binary-size"))

	// go floor (1.23) above the allowed max -> max-go-version violation.
	overGo := evalBudget(t, size, floor, report.BudgetSpec{MaxGoVersion: "1.22"})
	assert.False(t, overGo.Pass)
	assert.True(t, hasRule(overGo, "max-go-version"))
	// ...but a max at/above the floor is fine.
	assert.True(t, evalBudget(t, size, floor, report.BudgetSpec{MaxGoVersion: "1.23"}).Pass)

	// a denied module that's in the build -> deny violation naming it.
	denied := evalBudget(t, size, floor, report.BudgetSpec{Deny: []string{libc}})
	assert.False(t, denied.Pass)
	assert.True(t, hasRule(denied, "deny"))
}
