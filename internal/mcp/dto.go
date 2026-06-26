package mcp

import "github.com/wagoodman/bonsai/internal/bonsai"

// SizeTargetsOutput is the size_targets tool result: ranked prune candidates (each with the
// benefit bytes and the cost signals, plus an effort verdict) and the greedy prune plan.
type SizeTargetsOutput struct {
	AccountedSize uint64                 `json:"accountedSize" jsonschema:"the binary size the savings project down from"`
	MainModule    string                 `json:"mainModule"`
	Candidates    []Candidate            `json:"candidates" jsonschema:"prune candidates, ranked by bytes freed (descending)"`
	Plan          []bonsai.PrunePlanStep `json:"prunePlan,omitempty" jsonschema:"a greedy order to cut the candidates, by marginal savings"`
}

// Candidate is one prune candidate with both axes an agent needs to rank "least work, biggest
// win": the benefit (freed/potential bytes) and the cost (coupling counts), plus a coarse
// effort verdict. Finer rewrite scope (per-entry-package bytes, edit sites) comes from
// locate_cuts.
type Candidate struct {
	Module            string                `json:"module"`
	Class             string                `json:"class"`
	FreedBytes        uint64                `json:"freedBytes" jsonschema:"bytes freed by pruning this module alone (exclusive, retained size)"`
	PotentialBytes    uint64                `json:"potentialBytes" jsonschema:"freeable bytes in its subtree if co-holders are pruned too"`
	SharedWith        []bonsai.SharedHolder `json:"sharedWith,omitempty" jsonschema:"weight not freed alone, and the other targets that hold it"`
	ImportingPackages int                   `json:"importingPackages" jsonschema:"distinct first-party packages that import it (cost signal)"`
	ImportSites       int                   `json:"importSites" jsonschema:"total first-party import statements referencing it (cost signal)"`
	Verdict           string                `json:"verdict" jsonschema:"coarse effort label: quickWin, moderate, highEffort, or sharedOnly"`
}

// GoFloorOutput is the go_floor tool result: the dep-imposed minimum Go version, what pins it,
// and a machine-actionable verdict so the agent can act without re-deriving the decision tree.
type GoFloorOutput struct {
	Version     string   `json:"version,omitempty" jsonschema:"dep-imposed floor: the lowest go directive your owned modules can declare"`
	OwnedMax    string   `json:"ownedMax,omitempty" jsonschema:"highest go directive your own modules already declare"`
	Critical    []string `json:"critical,omitempty" jsonschema:"non-owned modules pinned exactly at the floor"`
	NextVersion string   `json:"nextVersion,omitempty" jsonschema:"floor reached after pruning every critical module"`
	Action      GoAction `json:"action"`
}

// GoAction is the actionable verdict for go_floor: whether the directive can be lowered right
// now (you over-declare) or which deps block going lower, so the agent reads the verdict and
// acts rather than re-deriving it from version strings.
type GoAction struct {
	CanLowerNow bool     `json:"canLowerNow" jsonschema:"true when your owned modules declare a higher go than deps require"`
	LowerTo     string   `json:"lowerTo,omitempty" jsonschema:"the go directive to write right now, if canLowerNow"`
	BlockedBy   []string `json:"blockedBy,omitempty" jsonschema:"deps to prune or replace to go below the current floor"`
	ThenReaches string   `json:"thenReaches,omitempty" jsonschema:"the floor reached once blockedBy modules are gone"`
}

// MeasureOutput is the measure tool result: the cheap before/after numbers for the edit loop —
// binary size and the go-version floor, with none of the prune ranking machinery.
type MeasureOutput struct {
	BinarySize    uint64 `json:"binarySize" jsonschema:"analyzed file size on disk"`
	AccountedSize uint64 `json:"accountedSize" jsonschema:"attributable size (approximately a stripped release binary)"`
	GoVersion     string `json:"goVersion,omitempty" jsonschema:"dep-imposed go-version floor"`
	OwnedMax      string `json:"ownedMax,omitempty" jsonschema:"highest go directive your own modules declare"`
}
