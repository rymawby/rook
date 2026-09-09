package loopengine

import (
	"time"

	"github.com/rymawby/rook/internal/task"
)

type EventType string

const (
	EventIterationStart EventType = "iteration_start"
	EventAssessDone     EventType = "assess_done"
	EventDispatchStart  EventType = "dispatch_start"
	EventTaskUpdate     EventType = "task_update"
	EventAcceptanceDone EventType = "acceptance_done"
	EventIterationDone  EventType = "iteration_done"
	EventStopped        EventType = "stopped"
	EventError          EventType = "error"
)

// Event is emitted on Engine.Events() as the loop progresses, for the TUI
// and `rook run --headless` to consume.
type Event struct {
	Type       EventType
	IterationN int
	Task       *task.Task
	Assessment *Assessment
	Verdict    *Verdict
	StopReason StopReason
	Message    string
	Err        error
	At         time.Time
}

func (e *Engine) emit(ev Event) {
	ev.At = time.Now()
	select {
	case e.events <- ev:
	default:
		// Channel full: drop rather than block the loop. The TUI/headless
		// consumer is expected to drain promptly; a slow consumer
		// shouldn't stall the build loop itself.
	}
}

// Events returns the channel of loop-progress events. Must be read from
// concurrently with Run to avoid the drop-when-full behavior above.
func (e *Engine) Events() <-chan Event { return e.events }
