package opt

import (
	"errors"
	"strings"
	"testing"
)

// TestGuardCheckpointVersion pins every branch of the resume guard. The running
// version is supplied rather than read, so the table can exercise a real
// mismatch even though a test binary carries no module information.
func TestGuardCheckpointVersion(t *testing.T) {
	cases := []struct {
		name          string
		library       string
		recorded      string
		running       string
		allowMismatch bool
		wantRefusal   bool
		wantWarning   bool
		mustName      []string
	}{
		{
			name:     "matching version is silent",
			recorded: "v0.5.1",
			running:  "v0.5.1",
		},
		{
			name:        "legacy checkpoint warns and proceeds",
			recorded:    "",
			running:     "v0.5.1",
			wantWarning: true,
			mustName:    []string{"v0.5.1"},
		},
		{
			name:        "blank recorded version is treated as absent",
			recorded:    "   ",
			running:     "v0.5.1",
			wantWarning: true,
			mustName:    []string{"v0.5.1"},
		},
		{
			name:        "unknown recorded version warns and proceeds",
			recorded:    unknownLibraryVersion,
			running:     "v0.5.1",
			wantWarning: true,
			mustName:    []string{"v0.5.1"},
		},
		{
			name:        "unknown running version warns and proceeds",
			recorded:    "v0.4.0",
			running:     unknownLibraryVersion,
			wantWarning: true,
			mustName:    []string{"v0.4.0"},
		},
		{
			name:        "missing running version is treated as unknown",
			recorded:    "v0.4.0",
			running:     "",
			wantWarning: true,
			mustName:    []string{"v0.4.0"},
		},
		{
			name:     "v0.7.1 build accepts a v0.7.0 checkpoint",
			recorded: "v0.7.0",
			running:  "v0.7.1",
		},
		{
			name:     "v0.7.0 build accepts a v0.7.1 checkpoint",
			recorded: "v0.7.1",
			running:  "v0.7.0",
		},
		{
			name:        "v0.6.0 checkpoint is still refused under v0.7.1",
			recorded:    "v0.6.0",
			running:     "v0.7.1",
			wantRefusal: true,
			mustName:    []string{"v0.6.0", "v0.7.1"},
		},
		{
			name:        "v0.6.0 checkpoint is still refused under v0.7.0",
			recorded:    "v0.6.0",
			running:     "v0.7.0",
			wantRefusal: true,
			mustName:    []string{"v0.6.0", "v0.7.0"},
		},
		{
			name:        "the MayFly allowlist does not carry over to another library",
			library:     "Dragonfly",
			recorded:    "v0.7.0",
			running:     "v0.7.1",
			wantRefusal: true,
			mustName:    []string{"Dragonfly", "v0.7.0", "v0.7.1"},
		},
		{
			name:        "mismatch is refused",
			recorded:    "v0.4.0",
			running:     "v0.5.1",
			wantRefusal: true,
			mustName:    []string{"v0.4.0", "v0.5.1"},
		},
		{
			name:          "mismatch proceeds under the override",
			recorded:      "v0.4.0",
			running:       "v0.5.1",
			allowMismatch: true,
			wantWarning:   true,
			mustName:      []string{"v0.4.0", "v0.5.1"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			library := testCase.library
			if library == "" {
				library = "MayFly"
			}

			warning, err := GuardCheckpointVersion(library, testCase.recorded, testCase.running, testCase.allowMismatch)

			if testCase.wantRefusal {
				if !errors.Is(err, ErrOptimizerVersionMismatch) {
					t.Fatalf("err = %v, want ErrOptimizerVersionMismatch", err)
				}

				assertNames(t, err.Error(), testCase.mustName)

				return
			}

			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}

			if testCase.wantWarning == (warning == "") {
				t.Fatalf("warning = %q, wantWarning = %v", warning, testCase.wantWarning)
			}

			assertNames(t, warning, testCase.mustName)
		})
	}
}

func assertNames(t *testing.T, message string, names []string) {
	t.Helper()

	for _, name := range names {
		if !strings.Contains(message, name) {
			t.Fatalf("%q does not name %q", message, name)
		}
	}
}
