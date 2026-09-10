package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rymawby/rook/internal/config"
)

// newTestModel builds a model with no engine wiring, just enough state for
// the four render* functions (§11) to produce content taller than any
// viewport used in these tests.
func newTestModel() model {
	return model{
		cfg:         config.Default(),
		active:      tabSpec,
		specContent: strings.Repeat("spec line\n", 200),
		tasks:       map[string]*taskView{},
		iterations:  map[int]*iterationView{},
		loopStatus:  "running",
	}
}

func mustModel(t *testing.T, tm tea.Model) model {
	t.Helper()
	mm, ok := tm.(model)
	if !ok {
		t.Fatalf("Update returned %T, want model", tm)
	}
	return mm
}

func TestWindowSizeInitializesAndSizesEveryTabsViewport(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	mm := mustModel(t, updated)

	if !mm.ready {
		t.Fatal("expected model to be ready after the first WindowSizeMsg")
	}
	wantHeight := 24 - headerLines - footerLines
	for tb := tab(0); tb < tabCount; tb++ {
		vp := mm.viewports[tb]
		if vp.Width != 80 {
			t.Errorf("tab %v: Width = %d, want 80", tb, vp.Width)
		}
		if vp.Height != wantHeight {
			t.Errorf("tab %v: Height = %d, want %d", tb, vp.Height, wantHeight)
		}
	}
}

// TestSpecTabIsScrollable is a direct regression test for the reported bug:
// the spec tab rendered the full markdown with no way to scroll it.
func TestSpecTabIsScrollable(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	mm := mustModel(t, updated)

	if mm.viewports[tabSpec].TotalLineCount() <= mm.viewports[tabSpec].Height {
		t.Fatal("test setup bug: spec content should be taller than the viewport")
	}

	before := mm.viewports[tabSpec].YOffset
	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
	mm = mustModel(t, updated)

	if mm.viewports[tabSpec].YOffset <= before {
		t.Fatalf("expected the spec tab to scroll down on 'down', YOffset stayed at %d", mm.viewports[tabSpec].YOffset)
	}
}

func TestPerTabScrollPositionPreservedAcrossTabSwitch(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	mm := mustModel(t, updated)

	for i := 0; i < 5; i++ {
		updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyDown})
		mm = mustModel(t, updated)
	}
	specOffset := mm.viewports[tabSpec].YOffset
	if specOffset == 0 {
		t.Fatal("test setup bug: expected the spec tab to have scrolled")
	}

	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	mm = mustModel(t, updated)
	if mm.active != tabTasks {
		t.Fatalf("expected '2' to switch to the task board tab, active = %v", mm.active)
	}

	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	mm = mustModel(t, updated)
	if mm.active != tabSpec {
		t.Fatalf("expected '1' to switch back to the spec tab, active = %v", mm.active)
	}
	if mm.viewports[tabSpec].YOffset != specOffset {
		t.Errorf("expected spec tab's scroll position preserved at %d, got %d", specOffset, mm.viewports[tabSpec].YOffset)
	}
}

func TestReservedKeysAreNotForwardedToTheViewport(t *testing.T) {
	m := newTestModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
	mm := mustModel(t, updated)

	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	mm = mustModel(t, updated)
	if mm.active != tabTasks {
		t.Fatalf("expected '2' to be handled as a reserved tab-switch key, active = %v", mm.active)
	}

	updated, _ = mm.Update(tea.KeyMsg{Type: tea.KeyTab})
	mm = mustModel(t, updated)
	if mm.active != tabStreams {
		t.Fatalf("expected tab to cycle from Task board to Agent streams, active = %v", mm.active)
	}
}
