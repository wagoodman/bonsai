package ui

import (
	"sync"

	"github.com/anchore/bubbly"
	"github.com/anchore/bubbly/bubbles/taskprogress"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/wagoodman/bonsai/internal/event"
	"github.com/wagoodman/bonsai/internal/log"
	"github.com/wagoodman/go-partybus"
	"github.com/wagoodman/go-progress"
)

var _ bubbly.EventHandler = (*Handler)(nil)

// Handler responds to bonsai bus events by producing bubbletea models for the frame to
// render (e.g. a task-progress line per analysis phase).
type Handler struct {
	state *State
	bubbly.EventHandler
}

type State struct {
	WindowSize tea.WindowSizeMsg
	Running    *sync.WaitGroup
}

func New() *Handler {
	d := bubbly.NewEventDispatcher()

	h := &Handler{
		EventHandler: d,
		state: &State{
			Running: &sync.WaitGroup{},
		},
	}

	d.AddHandlers(map[partybus.EventType]bubbly.EventHandlerFn{
		event.TaskType: h.handleTask,
	})

	return h
}

func (m *Handler) State() *State {
	return m.state
}

func (m *Handler) handleTask(e partybus.Event) ([]tea.Model, tea.Cmd) {
	cmd, prog, err := event.ParseTaskType(e)
	if err != nil {
		log.Warnf("unable to parse event: %+v", err)
		return nil, nil
	}

	return m.handleStagedProgressable(prog, taskprogress.Title{
		Default: cmd.Title.Default,
		Running: cmd.Title.WhileRunning,
		Success: cmd.Title.OnSuccess,
		Failed:  cmd.Title.OnFail,
	}, cmd.Context), nil
}

func (m *Handler) handleStagedProgressable(prog progress.StagedProgressable, title taskprogress.Title, context ...string) []tea.Model {
	tsk := taskprogress.New(
		m.state.Running,
		taskprogress.WithStagedProgressable(prog),
	)
	tsk.HideProgressOnSuccess = true
	tsk.TitleOptions = title
	tsk.Context = context
	tsk.WindowSize = m.state.WindowSize

	return []tea.Model{tsk}
}
