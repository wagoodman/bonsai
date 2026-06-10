/*
Package event provides event types for all events that the library publishes onto the event bus. By convention, for each
event defined here there should be a corresponding event parser defined in this package.
*/
package event

import (
	"github.com/wagoodman/bonsai/internal"
	"github.com/wagoodman/go-partybus"
)

const (
	typePrefix    = internal.ApplicationName
	cliTypePrefix = typePrefix + "-cli"

	// TaskType is a partybus event that occurs when a long-running task is started.
	TaskType partybus.EventType = typePrefix + "-task"

	// CLIExitType is a partybus event indicating the main process is to exit
	CLIExitType         partybus.EventType = cliTypePrefix + "-exit-event"
	CLIReportType       partybus.EventType = cliTypePrefix + "-report"
	CLINotificationType partybus.EventType = cliTypePrefix + "-notification"
)
