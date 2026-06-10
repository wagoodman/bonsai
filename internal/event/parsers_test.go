package event

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wagoodman/go-partybus"
	"github.com/wagoodman/go-progress"
)

// stubStagedProgress is a minimal progress.StagedProgressable for parser tests.
type stubStagedProgress struct{}

func (stubStagedProgress) Stage() string  { return "" }
func (stubStagedProgress) Current() int64 { return 0 }
func (stubStagedProgress) Size() int64    { return 0 }
func (stubStagedProgress) Error() error   { return nil }

var _ progress.StagedProgressable = stubStagedProgress{}

func TestParseCLIReportType(t *testing.T) {
	tests := []struct {
		name        string
		event       partybus.Event
		wantContext string
		wantReport  string
		wantErr     require.ErrorAssertionFunc
	}{
		{
			name:        "report with context",
			event:       partybus.Event{Type: CLIReportType, Source: "analyze", Value: "the report"},
			wantContext: "analyze",
			wantReport:  "the report",
		},
		{
			// source is optional and defaults to empty.
			name:       "report without context",
			event:      partybus.Event{Type: CLIReportType, Value: "the report"},
			wantReport: "the report",
		},
		{
			name:    "wrong event type",
			event:   partybus.Event{Type: CLINotificationType, Value: "x"},
			wantErr: require.Error,
		},
		{
			name:    "non-string value",
			event:   partybus.Event{Type: CLIReportType, Value: 42},
			wantErr: require.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr == nil {
				tt.wantErr = require.NoError
			}
			gotContext, gotReport, err := ParseCLIReportType(tt.event)
			tt.wantErr(t, err)
			if err != nil {
				return
			}
			assert.Equal(t, tt.wantContext, gotContext)
			assert.Equal(t, tt.wantReport, gotReport)
		})
	}
}

func TestParseCLINotificationType(t *testing.T) {
	tests := []struct {
		name             string
		event            partybus.Event
		wantContext      string
		wantNotification string
		wantErr          require.ErrorAssertionFunc
	}{
		{
			name:             "notification with context",
			event:            partybus.Event{Type: CLINotificationType, Source: "build", Value: "heads up"},
			wantContext:      "build",
			wantNotification: "heads up",
		},
		{
			name:             "notification without context",
			event:            partybus.Event{Type: CLINotificationType, Value: "heads up"},
			wantNotification: "heads up",
		},
		{
			name:    "wrong event type",
			event:   partybus.Event{Type: CLIReportType, Value: "x"},
			wantErr: require.Error,
		},
		{
			name:    "non-string value",
			event:   partybus.Event{Type: CLINotificationType, Value: struct{}{}},
			wantErr: require.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr == nil {
				tt.wantErr = require.NoError
			}
			gotContext, gotNotification, err := ParseCLINotificationType(tt.event)
			tt.wantErr(t, err)
			if err != nil {
				return
			}
			assert.Equal(t, tt.wantContext, gotContext)
			assert.Equal(t, tt.wantNotification, gotNotification)
		})
	}
}

func TestParseTaskType(t *testing.T) {
	task := Task{Title: Title{Default: "scanning"}}
	prog := stubStagedProgress{}

	tests := []struct {
		name     string
		event    partybus.Event
		wantTask *Task
		wantErr  require.ErrorAssertionFunc
	}{
		{
			name:     "well-formed task event",
			event:    partybus.Event{Type: TaskType, Source: task, Value: prog},
			wantTask: &task,
		},
		{
			name:    "wrong event type",
			event:   partybus.Event{Type: CLIReportType, Source: task, Value: prog},
			wantErr: require.Error,
		},
		{
			name:    "source is not a Task",
			event:   partybus.Event{Type: TaskType, Source: "not a task", Value: prog},
			wantErr: require.Error,
		},
		{
			name:    "value is not progressable",
			event:   partybus.Event{Type: TaskType, Source: task, Value: "not progressable"},
			wantErr: require.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr == nil {
				tt.wantErr = require.NoError
			}
			gotTask, gotProg, err := ParseTaskType(tt.event)
			tt.wantErr(t, err)
			if err != nil {
				return
			}
			assert.Equal(t, tt.wantTask, gotTask)
			assert.NotNil(t, gotProg)
		})
	}
}
