package tui

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func readSpecFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

var (
	styleTabActive   = lipgloss.NewStyle().Bold(true).Padding(0, 1).Background(lipgloss.Color("62")).Foreground(lipgloss.Color("230"))
	styleTabInactive = lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("245"))
	styleStatusBar   = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("235")).Padding(0, 1)
	styleHeading     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	styleDim         = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleDone        = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleFailed      = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleRunning     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	stylePending     = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
)

func (m model) View() string {
	if m.quitting && m.loopStatus == "" {
		return "stopping...\n"
	}
	if !m.ready {
		return "starting rook...\n"
	}

	var b strings.Builder
	b.WriteString(m.renderTabs())
	b.WriteString("\n")
	b.WriteString(m.viewports[m.active].View())
	b.WriteString("\n")
	b.WriteString(m.renderStatusBar())
	return b.String()
}

// refreshViewports re-renders every tab's body from current model state and
// feeds it (word-wrapped to that viewport's width, §11) into its viewport,
// preserving each tab's existing scroll offset. Called whenever content or
// terminal size changes, never from View, which stays a pure readout.
func (m *model) refreshViewports() {
	if !m.ready {
		return
	}
	bodies := [tabCount]string{
		tabSpec:    m.renderSpec(),
		tabTasks:   m.renderTasks(),
		tabStreams: m.renderStreams(),
		tabHistory: m.renderHistory(),
	}
	for t, body := range bodies {
		if w := m.viewports[t].Width; w > 0 {
			body = lipgloss.NewStyle().Width(w).Render(body)
		}
		m.viewports[t].SetContent(body)
	}
}

func (m model) renderTabs() string {
	var parts []string
	for t := tab(0); t < tabCount; t++ {
		label := fmt.Sprintf("%d %s", t+1, t)
		if t == m.active {
			parts = append(parts, styleTabActive.Render(label))
		} else {
			parts = append(parts, styleTabInactive.Render(label))
		}
	}
	return strings.Join(parts, " ")
}

func (m model) renderSpec() string {
	var b strings.Builder
	b.WriteString(styleHeading.Render("Spec"))
	b.WriteString(styleDim.Render(fmt.Sprintf("  (%s)", m.cfg.AbsSpecPath())))
	b.WriteString("\n\n")
	b.WriteString(m.specContent)
	return b.String()
}

func (m model) renderTasks() string {
	var b strings.Builder
	b.WriteString(styleHeading.Render(fmt.Sprintf("Task board — iteration %d", m.iterN)))
	b.WriteString("\n\n")

	if len(m.taskIDs) == 0 {
		b.WriteString(styleDim.Render("no tasks yet this iteration"))
		return b.String()
	}

	cols := []string{"pending", "blocked", "running", "done", "failed"}
	for _, col := range cols {
		var ids []string
		for _, id := range m.taskIDs {
			if m.tasks[id].status == col {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			continue
		}
		b.WriteString(statusStyle(col).Render(fmt.Sprintf("%s (%d)", strings.ToUpper(col), len(ids))))
		b.WriteString("\n")
		for _, id := range ids {
			t := m.tasks[id]
			line := fmt.Sprintf("  %s — %s", t.id, t.instruction)
			b.WriteString(line)
			b.WriteString("\n")
			if len(t.scopeFiles) > 0 {
				b.WriteString(styleDim.Render(fmt.Sprintf("    scope: %s", strings.Join(t.scopeFiles, ", "))))
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func statusStyle(status string) lipgloss.Style {
	switch status {
	case "done":
		return styleDone
	case "failed":
		return styleFailed
	case "running":
		return styleRunning
	default:
		return stylePending
	}
}

func (m model) renderStreams() string {
	var b strings.Builder
	b.WriteString(styleHeading.Render("Agent streams"))
	b.WriteString("\n\n")
	if len(m.taskIDs) == 0 {
		b.WriteString(styleDim.Render("no active subagents"))
		return b.String()
	}
	for _, id := range m.taskIDs {
		t := m.tasks[id]
		b.WriteString(fmt.Sprintf("[%s] %s: %s", t.status, t.id, t.instruction))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(styleDim.Render("full transcripts: .rook/logs/subagent-<task-id>-attempt<N>.log"))
	return b.String()
}

func (m model) renderHistory() string {
	var b strings.Builder
	b.WriteString(styleHeading.Render("Iteration history"))
	b.WriteString("\n\n")

	ids := append([]int(nil), m.iterationIDs...)
	sort.Sort(sort.Reverse(sort.IntSlice(ids)))
	for _, n := range ids {
		iv := m.iterations[n]
		status := "in progress"
		if iv.done {
			status = fmt.Sprintf("satisfied=%v acceptance=%v", iv.satisfied, iv.acceptancePassing)
		}
		b.WriteString(fmt.Sprintf("iter %d — %.0f%% — %s\n", n, iv.completionEstimate*100, status))
		if iv.summary != "" {
			b.WriteString(styleDim.Render("  " + iv.summary))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m model) renderStatusBar() string {
	scroll := ""
	if vp := m.viewports[m.active]; vp.TotalLineCount() > vp.Height {
		scroll = fmt.Sprintf("  scroll=%.0f%%", vp.ScrollPercent()*100)
	}
	text := fmt.Sprintf(
		" orchestrator=%s/%s  subagent=%s/%s (x%d)  loop=%s  iter=%d%s  [1-4 tabs, q quit] ",
		m.cfg.Roles.Orchestrator.Backend, m.cfg.Roles.Orchestrator.Model,
		m.cfg.Roles.Subagent.Backend, m.cfg.Roles.Subagent.Model, m.cfg.Roles.Subagent.Concurrency,
		m.loopStatus, m.iterN, scroll,
	)
	return styleStatusBar.Render(text)
}
