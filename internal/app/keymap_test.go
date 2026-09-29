package app

import (
	"os"
	"testing"
)

// TestMain installs gote's keymap (ctrl+q force-quit) so router-driven tests see the
// chords the app runs with.
func TestMain(m *testing.M) {
	applyKeymap()
	os.Exit(m.Run())
}
