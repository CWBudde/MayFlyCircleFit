package opt

import (
	"errors"
	"strings"
	"testing"
)

// Versions used across the guard's table cases. MayFly v0.7.0 and v0.7.1 are the
// one allowlisted interchangeable pair; v0.6.0 is the release before it, which
// must stay refused.
const (
	testVersionMayfly070 = "v0.7.0"
	testVersionMayfly071 = "v0.7.1"
	testVersionMayfly060 = "v0.6.0"
	testVersionMayfly051 = "v0.5.1"
	testVersionMayfly040 = "v0.4.0"
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
			recorded: testVersionMayfly051,
			running:  testVersionMayfly051,
		},
		{
			name:        "legacy checkpoint warns and proceeds",
			recorded:    "",
			running:     testVersionMayfly051,
			wantWarning: true,
			mustName:    []string{testVersionMayfly051},
		},
		{
			name:        "blank recorded version is treated as absent",
			recorded:    "   ",
			running:     testVersionMayfly051,
			wantWarning: true,
			mustName:    []string{testVersionMayfly051},
		},
		{
			name:        "unknown recorded version warns and proceeds",
			recorded:    unknownLibraryVersion,
			running:     testVersionMayfly051,
			wantWarning: true,
			mustName:    []string{testVersionMayfly051},
		},
		{
			name:        "unknown running version warns and proceeds",
			recorded:    testVersionMayfly040,
			running:     unknownLibraryVersion,
			wantWarning: true,
			mustName:    []string{testVersionMayfly040},
		},
		{
			name:        "missing running version is treated as unknown",
			recorded:    testVersionMayfly040,
			running:     "",
			wantWarning: true,
			mustName:    []string{testVersionMayfly040},
		},
		{
			name:     "v0.7.1 build accepts a v0.7.0 checkpoint",
			recorded: testVersionMayfly070,
			running:  testVersionMayfly071,
		},
		{
			name:     "v0.7.0 build accepts a v0.7.1 checkpoint",
			recorded: testVersionMayfly071,
			running:  testVersionMayfly070,
		},
		{
			name:        "v0.6.0 checkpoint is still refused under v0.7.1",
			recorded:    testVersionMayfly060,
			running:     testVersionMayfly071,
			wantRefusal: true,
			mustName:    []string{testVersionMayfly060, testVersionMayfly071},
		},
		{
			name:        "v0.6.0 checkpoint is still refused under v0.7.0",
			recorded:    testVersionMayfly060,
			running:     testVersionMayfly070,
			wantRefusal: true,
			mustName:    []string{testVersionMayfly060, testVersionMayfly070},
		},
		{
			name:        "the MayFly allowlist does not carry over to another library",
			library:     "Dragonfly",
			recorded:    testVersionMayfly070,
			running:     testVersionMayfly071,
			wantRefusal: true,
			mustName:    []string{"Dragonfly", testVersionMayfly070, testVersionMayfly071},
		},
		{
			name:        "CMA-ES checkpoints refuse a different optimizer revision",
			library:     "CMA-ES",
			recorded:    "v0.0.0-20260825113115-96b7c9adff3a",
			running:     "v0.0.0-20260826120000-aaaaaaaaaaaa",
			wantRefusal: true,
			mustName: []string{
				"CMA-ES",
				"v0.0.0-20260825113115-96b7c9adff3a",
				"v0.0.0-20260826120000-aaaaaaaaaaaa",
			},
		},
		{
			name:        "mismatch is refused",
			recorded:    testVersionMayfly040,
			running:     testVersionMayfly051,
			wantRefusal: true,
			mustName:    []string{testVersionMayfly040, testVersionMayfly051},
		},
		{
			name:          "mismatch proceeds under the override",
			recorded:      testVersionMayfly040,
			running:       testVersionMayfly051,
			allowMismatch: true,
			wantWarning:   true,
			mustName:      []string{testVersionMayfly040, testVersionMayfly051},
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
