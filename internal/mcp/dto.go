package mcp

import "github.com/wagoodman/bonsai/internal/bonsai"

// SizeTargetsOutput is the size_targets tool result: prune candidates ranked by prize (bytes at
// stake, the two-axis prize-vs-effort frame) and the greedy prune plan.
type SizeTargetsOutput struct {
	AccountedSize uint64                 `json:"accountedSize" jsonschema:"the binary size the savings project down from"`
	MainModule    string                 `json:"mainModule"`
	Candidates    []Candidate            `json:"candidates" jsonschema:"prune candidates, ranked by prize (bytes at stake, descending)"`
	Plan          []bonsai.PrunePlanStep `json:"prunePlan,omitempty" jsonschema:"a greedy order to cut the candidates, by marginal savings"`
}

// Candidate is one prune candidate on two axes: the prize (bytes at stake if the module left the
// binary, across the controlled boundary) and the effort to realize it (freed-alone bytes,
// coupling, and what blocks a clean cut). A big prize with a 0 freedBytes is not a non-candidate:
// it is weight pinned by an uncontrolled dep (see pinnedBy). Finer rewrite scope comes from
// locate_cuts.
type Candidate struct {
	Module              string                `json:"module"`
	Class               string                `json:"class"`
	PrizeBytes          uint64                `json:"prizeBytes" jsonschema:"bytes at stake if this module vanished (full-graph retained size, across the controlled boundary); the ranking key, always >= freedBytes"`
	FreedBytes          uint64                `json:"freedBytes" jsonschema:"the unilateral slice: bytes freed by pruning this module from your own code alone (exclusive)"`
	PotentialBytes      uint64                `json:"potentialBytes" jsonschema:"freeable bytes in its subtree if co-holder targets are pruned too"`
	SharedWith          []bonsai.SharedHolder `json:"sharedWith,omitempty" jsonschema:"weight not freed alone, and the other targets that hold it"`
	PinnedBy            []string              `json:"pinnedBy,omitempty" jsonschema:"locked, uncontrolled deps importing this that hold the prize against a controlled cut; the modules to replace, patch, or upstream to claim it"`
	PrizeByEntryPackage []bonsai.EntryPackage `json:"prizeByEntryPackage,omitempty" jsonschema:"where the prize concentrates across entry packages; turns the ceiling into the achievable slice (e.g. weight behind one getter you could strip)"`
	ImportingPackages   int                   `json:"importingPackages" jsonschema:"distinct first-party packages that import it (cost signal)"`
	ImportSites         int                   `json:"importSites" jsonschema:"total first-party import statements referencing it (cost signal)"`
	Effort              string                `json:"effort" jsonschema:"how to realize the prize: quickWin (cut your imports), coordinated (co-prune other targets), pinnedByDep (replace/patch a locked dep in pinnedBy), or core (too wired in to cut)"`
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
