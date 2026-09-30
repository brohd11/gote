package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestJoinRule(t *testing.T) {
	for _, tc := range []struct{ name, below, want string }{
		{"plain", "text", "────────"},
		{"separator", "ab│cd", "──┬─────"},
		{"box corners stay flat", "┌──┐", "────────"},
		{"joined borders", "├─┼─┘", "┬─┬─┬───"},
		{"wide rune keeps columns", "界│", "──┬─────"},
		{"styled row", "\x1b[31mab\x1b[0m│", "──┬─────"},
		{"clipped past width", "12345678│", "────────"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ansi.Strip(joinRule(8, tc.below)); got != tc.want {
				t.Fatalf("joinRule(%q) = %q, want %q", tc.below, got, tc.want)
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
