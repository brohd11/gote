package app

import (
	"strings"

	"charm.land/bubbles/v2/key"
)

// altShiftKey accepts legacy terminals' Alt+uppercase encoding as well as
// terminals that report Shift explicitly. Help always names the physical chord.
func altShiftKey(letter, description string) key.Binding {
	upper := strings.ToUpper(letter)
	return key.NewBinding(
		key.WithKeys("alt+"+upper, "alt+shift+"+letter, "alt+shift+"+upper),
		key.WithHelp("alt+shift+"+letter, description),
	)
}
