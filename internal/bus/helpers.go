package bus

import (
	"github.com/wagoodman/go-partybus"
	"github.com/wagoodman/go-progress"

	"github.com/wagoodman/bonsai/bonsai/event"
	"github.com/wagoodman/bonsai/internal/redact"
)

// PublishTask publishes a long-running task onto the bus and returns a progress handle the
// caller drives (SetCompleted / SetError). A total of -1 indicates indeterminate progress.
func PublishTask(titles event.Title, context string, total int) *event.ManualStagedProgress {
	prog := event.ManualStagedProgress{
		Manual: *progress.NewManual(int64(total)),
	}

	publish(partybus.Event{
		Type: event.TaskType,
		Source: event.Task{
			Title:   titles,
			Context: context,
		},
		Value: progress.StagedProgressable(&struct {
			progress.Stager
			progress.Progressable
		}{
			Stager:       &prog.Stage,
			Progressable: &prog.Manual,
		}),
	})

	return &prog
}

// Exit signals the application's event loop to tear down and exit.
func Exit() {
	publish(partybus.Event{
		Type: event.CLIExitType,
	})
}

// Report publishes a final report to be shown to the user on stdout.
func Report(report string) {
	if publisher == nil {
		// prevent any further actions taken on the report (such as redaction) since it won't be published anyway
		return
	}
	publish(partybus.Event{
		Type:  event.CLIReportType,
		Value: redact.Apply(report),
	})
}

// Notify publishes a notification to be shown to the user on stderr.
func Notify(message string) {
	if publisher == nil {
		// prevent any further actions taken on the report (such as redaction) since it won't be published anyway
		return
	}
	publish(partybus.Event{
		Type:  event.CLINotificationType,
		Value: redact.Apply(message),
	})
}
