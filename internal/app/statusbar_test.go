package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/brohd11/bubblestack/components"
	"github.com/brohd11/bubblestack/core"
)

// statusSpanAt is the x of the span id in the last rendered bar, or -1.
func statusSpanAt(s *homeScreen, id string) int {
	for _, span := range s.statusSpans {
		if span.id == id {
			return span.x0
		}
	}
	return -1
}

func TestStatusBarLayout(t *testing.T) {
	model, s, sh := tabTestHome(t)
	resize := func(w int) { model, _ = model.Update(tea.WindowSizeMsg{Width: w, Height: 24}) }
	for _, w := range []int{100, 60, 20, 8} {
		resize(w)
		if got := lipgloss.Width(s.statusBar(sh)); got != w {
			t.Fatalf("width %d: bar is %d cells", w, got)
		}
	}
	resize(60)
	bar := stripANSI(s.statusBar(sh))
	if !strings.HasPrefix(bar, " ≡ ▁ │") || !strings.HasSuffix(bar, "Ln 1, Col 1 ") {
		t.Fatalf("bar = %q", bar)
	}
	if strings.Contains(bar, "Diag") {
		t.Fatalf("dock tabs should show only while the dock is open: %q", bar)
	}

	if sh.Chrome == nil {
		sh.Chrome = &core.Chrome{}
	}
	sh.Chrome.Status = components.NewStatusLine()
	sh.Chrome.Status.Set(strings.Repeat("long message ", 20))
	long := stripANSI(s.statusBar(sh))
	if lipgloss.Width(long) != 60 || !strings.HasSuffix(long, "Ln 1, Col 1 ") || !strings.Contains(long, "…") {
		t.Fatalf("a long message should be clipped before the cursor segment: %q", long)
	}

	s.toggleBottom(sh)
	if bar := stripANSI(s.statusBar(sh)); !strings.Contains(bar, "Diag · Search") {
		t.Fatalf("an open dock should put its tabs in the control spot: %q", bar)
	}
}

func TestStatusBarClicks(t *testing.T) {
	model, s, sh := tabTestHome(t)
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	click := func(id string) {
		t.Helper()
		s.statusBar(sh)
		x := statusSpanAt(s, id)
		if x < 0 {
			t.Fatalf("no %s span in the bar", id)
		}
		_, act := s.Update(sh, tea.MouseClickMsg{X: x, Y: sh.BodyY() + s.h, Button: tea.MouseLeft})
		if act.Cmd != nil {
			act.Cmd()
		}
	}

	sidebar := s.sidebar
	click(statusSidebar)
	if s.sidebar == sidebar {
		t.Fatal("the ≡ toggle should flip the sidebar")
	}
	click(statusDock)
	if !s.bottomVisible {
		t.Fatal("the ▁ toggle should open the dock")
	}
	click(bottomSearch)
	if s.bottom.active != bottomSearch || s.focusedPane() != s.bottom {
		t.Fatal("a dock tab should select that panel and focus the dock")
	}
	click(statusDock)
	if s.bottomVisible {
		t.Fatal("the ▁ toggle should close the dock again")
	}

	// A click on the row that misses every item is still the bar's.
	var _ core.Screen = s
	next, act := s.Update(sh, tea.MouseClickMsg{X: 79, Y: sh.BodyY() + s.h, Button: tea.MouseLeft})
	if next != s || act.Msg != nil {
		t.Fatal("a click on the bar's empty space should do nothing")
	}
}
