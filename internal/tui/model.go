// Package tui is Rook's Bubble Tea shell (§11 of SPEC.md): spec view,
// task board, agent streams, iteration history, and a config/status bar.
package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/rymawby/rook/internal/config"
	"github.com/rymawby/rook/internal/loopengine"
)

// headerLines/footerLines are how much vertical space the tab bar and
// status bar (§11) reserve outside each tab's own viewport.
const (
	headerLines = 2 // tab bar + blank line
	footerLines = 2 // blank line + status bar
)

type tab int

const (
	tabSpec tab = iota
	tabTasks
	tabStreams
	tabHistory
	tabCount
)

func (t tab) String() string {
	switch t {
	case tabSpec:
		return "Spec"
	case tabTasks:
		return "Task board"
	case tabStreams:
		return "Agent streams"
	case tabHistory:
		return "Iteration history"
	default:
		return "?"
	}
}

// taskView is the live view of one task, keyed by ID, updated as events
// arrive from the loop engine.
type taskView struct {
	id          string
	status      string
	instruction string
	scopeFiles  []string
	blockedBy   []string
}

// iterationView summarizes one completed (or in-progress) iteration for
// the history tab.
type iterationView struct {
	n                  int
	completionEstimate float64
	satisfied          bool
	acceptancePassing  bool
	summary            string
	at                 time.Time
	done               bool
}

type model struct {
	cfg    *config.Config
	engine *loopengine.Engine
	cancel context.CancelFunc

	active tab

	specContent string
	specRevInfo string

	tasks   map[string]*taskView
	taskIDs []string // insertion order, for stable rendering

	iterations   map[int]*iterationView
	iterationIDs []int

	loopStatus string // "running" | "stopped: <reason>"
	iterN      int

	// viewports holds one independently-scrollable Bubbles viewport per tab
	// (§11.1), so each tab keeps its own scroll position across tab switches.
	viewports [tabCount]viewport.Model
	ready     bool // sized once the first tea.WindowSizeMsg arrives

	width, height int

	quitting bool
}

// engineEventsMsg wraps one loopengine.Event as a tea.Msg.
type engineEventsMsg loopengine.Event

// engineDoneMsg is sent when engine.Run returns.
type engineDoneMsg struct {
	reason loopengine.StopReason
	err    error
}

func waitForEvent(ch <-chan loopengine.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return engineEventsMsg(ev)
	}
}

func runEngine(ctx context.Context, e *loopengine.Engine) tea.Cmd {
	return func() tea.Msg {
		reason, err := e.Run(ctx)
		return engineDoneMsg{reason: reason, err: err}
	}
}

func initialModel(cfg *config.Config, engine *loopengine.Engine, cancel context.CancelFunc) model {
	specContent, _ := readSpecFile(cfg.AbsSpecPath())
	return model{
		cfg:         cfg,
		engine:      engine,
		cancel:      cancel,
		active:      tabSpec,
		specContent: specContent,
		tasks:       map[string]*taskView{},
		iterations:  map[int]*iterationView{},
		loopStatus:  "running",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		runEngine(engineCtx, m.engine),
		waitForEvent(m.engine.Events()),
	)
}

// engineCtx is set by Run below before the program starts; Bubble Tea's
// Init has no way to receive extra arguments, so we thread it through a
// package-level var set immediately before tea.NewProgram runs.
var engineCtx context.Context

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		vpWidth := msg.Width
		vpHeight := msg.Height - headerLines - footerLines
		if vpHeight < 0 {
			vpHeight = 0
		}
		if !m.ready {
			for t := tab(0); t < tabCount; t++ {
				m.viewports[t] = viewport.New(vpWidth, vpHeight)
			}
			m.ready = true
		} else {
			for t := tab(0); t < tabCount; t++ {
				m.viewports[t].Width = vpWidth
				m.viewports[t].Height = vpHeight
			}
		}
		m.refreshViewports()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewports[m.active], cmd = m.viewports[m.active].Update(msg)
		return m, cmd

	case engineEventsMsg:
		m.applyEvent(loopengine.Event(msg))
		m.refreshViewports()
		return m, waitForEvent(m.engine.Events())

	case engineDoneMsg:
		m.loopStatus = fmt.Sprintf("stopped: %s", msg.reason)
		if msg.err != nil {
			m.loopStatus += fmt.Sprintf(" (%v)", msg.err)
		}
		return m, nil
	}
	return m, nil
}

func (m *model) applyEvent(ev loopengine.Event) {
	m.iterN = ev.IterationN
	switch ev.Type {
	case loopengine.EventIterationStart:
		m.tasks = map[string]*taskView{}
		m.taskIDs = nil
		iv := &iterationView{n: ev.IterationN, at: ev.At}
		m.iterations[ev.IterationN] = iv
		m.iterationIDs = appendIfMissing(m.iterationIDs, ev.IterationN)

	case loopengine.EventAssessDone:
		if ev.Assessment != nil {
			if iv := m.iterations[ev.IterationN]; iv != nil {
				iv.completionEstimate = ev.Assessment.CompletionEstimate
			}
		}

	case loopengine.EventTaskUpdate:
		if ev.Task != nil {
			t := ev.Task
			tv, ok := m.tasks[t.ID]
			if !ok {
				tv = &taskView{id: t.ID}
				m.tasks[t.ID] = tv
				m.taskIDs = append(m.taskIDs, t.ID)
			}
			tv.status = string(t.Status)
			tv.instruction = t.Instruction
			tv.scopeFiles = t.ScopeFiles
			tv.blockedBy = t.BlockedBy
		}

	case loopengine.EventIterationDone:
		if ev.Verdict != nil {
			if iv := m.iterations[ev.IterationN]; iv != nil {
				iv.satisfied = ev.Verdict.Satisfied
				iv.acceptancePassing = ev.Verdict.AcceptancePassing
				iv.summary = ev.Verdict.Summary
				iv.done = true
			}
		}

	case loopengine.EventStopped:
		m.loopStatus = fmt.Sprintf("stopped: %s", ev.StopReason)

	case loopengine.EventError:
		m.loopStatus = fmt.Sprintf("error: %v", ev.Err)
	}
}

func appendIfMissing(s []int, v int) []int {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// handleKey handles the small set of app-reserved keys (quit, tab
// switching); everything else is forwarded to the active tab's viewport,
// which owns scrolling (arrows, j/k, pgup/pgdown, ctrl+u/d, g/G, home/end —
// §11.1's default Bubbles viewport bindings).
func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		if m.quitting {
			return m, tea.Quit
		}
		m.quitting = true
		m.engine.RequestStop()
		m.loopStatus = "stopping (finishing current iteration)..."
		return m, nil
	case "1":
		m.active = tabSpec
		return m, nil
	case "2":
		m.active = tabTasks
		return m, nil
	case "3":
		m.active = tabStreams
		return m, nil
	case "4":
		m.active = tabHistory
		return m, nil
	case "tab":
		m.active = (m.active + 1) % tabCount
		return m, nil
	case "shift+tab":
		m.active = (m.active - 1 + tabCount) % tabCount
		return m, nil
	}
	var cmd tea.Cmd
	m.viewports[m.active], cmd = m.viewports[m.active].Update(msg)
	return m, cmd
}

// Run starts the TUI, driving engine to completion (or until the user
// quits) inside it.
func Run(ctx context.Context, engine *loopengine.Engine, cfg *config.Config) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	engineCtx = runCtx

	m := initialModel(cfg, engine, cancel)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	return err
}
