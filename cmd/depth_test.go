package cmd

import (
	"os"
	"strings"
	"testing"
)

// TestResolveDepth pins the rung the environment sits on: below anything typed, above
// the config's scan_depth. The two easy to get backwards: a typed flag must beat a set
// variable, and a blank variable must read as unset rather than as depth 0, which is
// itself a real depth.
func TestResolveDepth(t *testing.T) {
	tests := []struct {
		name        string
		env         string // "" means leave the variable unset entirely
		flagDepth   int
		flagChanged bool
		want        int
		wantSet     bool
		wantErr     bool
	}{
		{name: "unset falls through", flagDepth: 0, want: 0},
		{name: "blank reads as unset", env: " ", flagDepth: 0, want: 0},
		{name: "set supplies the depth", env: "2", flagDepth: 0, want: 2, wantSet: true},
		{name: "surrounding space is trimmed", env: " 2 ", flagDepth: 0, want: 2, wantSet: true},
		{name: "zero is a depth", env: "0", flagDepth: 0, want: 0, wantSet: true},
		{name: "a typed flag wins", env: "2", flagDepth: 4, flagChanged: true, want: 4, wantSet: true},
		{name: "a typed flag wins even unparseable", env: "abc", flagDepth: 4, flagChanged: true, want: 4, wantSet: true},
		{name: "not a number", env: "abc", flagDepth: 0, want: 0, wantErr: true},
		{name: "negative", env: "-1", flagDepth: 0, want: 0, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setenv also unsets for the subtest when the case wants it gone, and
			// restores whatever the developer's own shell had afterwards.
			if tt.env == "" {
				t.Setenv(depthEnv, "")
				os.Unsetenv(depthEnv)
			} else {
				t.Setenv(depthEnv, tt.env)
			}

			got, set, err := resolveDepth(tt.flagDepth, tt.flagChanged)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), depthEnv) {
				t.Errorf("error %q does not name %s, so the user cannot tell which knob is wrong", err, depthEnv)
			}
			if got != tt.want {
				t.Errorf("depth = %d, want %d", got, tt.want)
			}
			if set != tt.wantSet {
				t.Errorf("set = %v, want %v — an unset variable must leave the config's scan_depth deciding", set, tt.wantSet)
			}
		})
	}
}
