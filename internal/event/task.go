package event

import "github.com/wagoodman/go-progress"

// ManualStagedProgress is a task handle the analysis drives: Manual for the count/completion, and
// an AtomicStage for the live grey "what happened" aux text shown after the title. The stage is
// atomic because the analysis goroutine writes it (Set) while the UI goroutine reads it (Stage).
type ManualStagedProgress struct {
	*progress.AtomicStage
	progress.Manual
}

// SetStage sets the grey aux text shown after the task title. Named (rather than the embedded
// Set) because AtomicStage.Set(string) and Manual.Set(int64) both promote and collide.
func (m *ManualStagedProgress) SetStage(s string) {
	m.AtomicStage.Set(s)
}

type Title struct {
	Default      string
	WhileRunning string
	OnSuccess    string
	OnFail       string
}

type Task struct {
	Title   Title
	Context string
}
