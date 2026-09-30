package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestJoinRule(t *testing.T) {
	for _, tc := range []struct{ name, above, below, want string }{
		{"plain", "", "text", "────────"},
		{"separator", "", "ab│cd", "──┬─────"},
		{"box corners stay flat", "", "┌──┐", "────────"},
		{"joined borders", "", "├─┼─┘", "┌─┬─┬───"},
		{"corners at the ends", "", "│      │", "┌──────┐"},
		{"wide rune keeps columns", "", "界│", "──┬─────"},
		{"styled row", "", "\x1b[31mab\x1b[0m│", "──┬─────"},
		{"clipped past width", "", "12345678│", "────────"},
		{"shared edge", "│  │   │", "│ │    │", "├─┬┴───┤"},
		{"crossing", "│  │   │", "│  │   │", "├──┼───┤"},
		{"closing a column", "│  │", "", "└──┴────"},
		{"box bottom above stays flat", "└──┘", "", "────────"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ansi.Strip(joinRule(8, tc.above, tc.below)); got != tc.want {
				t.Fatalf("joinRule(%q, %q) = %q, want %q", tc.above, tc.below, got, tc.want)
			}
		})
	}
}

// TestHeaderRuleJoinsGroupDivider: the header is a fixed leaf above the panes, and its rule
// takes a ┬ where a split's divider meets it.
func TestHeaderRuleJoinsGroupDivider(t *testing.T) {
	s, sh := groupHome(t)
	seedDoc(t, s, sh, "a.md", "# A")
	b, _ := seedDoc(t, s, sh, "b.md", "# B")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	right := s.groups()[1]
	lines := strings.Split(ansi.Strip(s.View(sh)), "\n")
	if !strings.Contains(lines[0], s.CrumbLabel(false)) {
		t.Fatalf("header row = %q", lines[0])
	}
	col := right.openTabs.x // the separator's column, in terminal cells
	if rule := []rune(lines[headerRows-1]); col >= len(rule) || rule[col] != '┬' {
		t.Fatalf("rule has no junction at column %d: %q", col, lines[headerRows-1])
	}
	if right.openTabs.y != sh.BodyY()+headerRows {
		t.Fatalf("panes start at %d, want below the %d header rows", right.openTabs.y, headerRows)
	}
}

// TestDockRuleJoinsColumns: with the dock open the sidebar drops its bottom edge and every
// column above closes onto the dock's top rule; closing the dock gives the edge back.
func TestDockRuleJoinsColumns(t *testing.T) {
	s, sh := groupHome(t)
	seedDoc(t, s, sh, "a.md", "# A")
	b, _ := seedDoc(t, s, sh, "b.md", "# B")
	s.openDoc(sh, b)
	s.moveTab(sh, 1)
	s.toggleBottom(sh)
	lines := strings.Split(ansi.Strip(s.View(sh)), "\n")
	r := s.bottom.y - sh.BodyY()
	rule := []rune(lines[r])
	sidebarEdge := s.sidebarPaneWidth() - 1
	sep := s.groups()[1].openTabs.x
	if rule[0] != '├' || rule[sidebarEdge] != '┴' || rule[sep] != '┴' || rule[len(rule)-1] != '┤' && rule[len(rule)-1] != '┐' {
		t.Fatalf("dock rule = %q (sidebar edge %d, separator %d)", lines[r], sidebarEdge, sep)
	}
	if strings.Contains(lines[r-1], "└") {
		t.Fatalf("the sidebar should leave its bottom to the dock rule: %q", lines[r-1])
	}

	s.toggleBottom(sh)
	if view := ansi.Strip(s.View(sh)); !strings.Contains(view, "└") {
		t.Fatalf("closing the dock should give the sidebar its bottom edge back:\n%s", view)
	}
}
