package ui_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
)

// This file is the Go half of the state-badge parity check. ui.StateBadge and
// the stateClass/stateLabel/stateBadgeStyle trio in web/src/format.ts render
// the same badge in two languages: the server paints it, and then an island
// mounts and paints it again. Neither is the source of truth —
// web/src/state-badge-parity.json is, and web/src/format.test.ts checks the
// TypeScript against the same file, so a mapping changed on one side alone
// fails on both.
//
// It follows the seed and endpoint parity tests in internal/server: pin the
// contract as data, then check each implementation against the data rather
// than against the other implementation, which no single test process can
// reach.

// stateBadgeFixture is web/src/state-badge-parity.json.
type stateBadgeFixture struct {
	Note   string             `json:"note"`
	Badges []stateBadgeExpect `json:"badges"`
}

// stateBadgeExpect is one badge: the state that goes in, and the class, label
// and inline style that must come out. An empty Style means the badge is
// written without a style attribute at all.
type stateBadgeExpect struct {
	State string `json:"state"`
	Class string `json:"class"`
	Label string `json:"label"`
	Style string `json:"style"`
}

// stateBadgeParityPath is the shared fixture, reached from the package
// directory. It lives beside the TypeScript because that is the side that
// imports it directly; Go can read a path, TypeScript cannot read one outside
// its own tree without leaving the bundler's world.
const stateBadgeParityPath = "../../web/src/state-badge-parity.json"

func loadStateBadgeFixture(t *testing.T) stateBadgeFixture {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(stateBadgeParityPath))
	if err != nil {
		t.Fatalf("read the shared state-badge contract: %v", err)
	}

	var fixture stateBadgeFixture

	err = json.Unmarshal(raw, &fixture)
	if err != nil {
		t.Fatalf("decode %s: %v", stateBadgeParityPath, err)
	}

	if len(fixture.Badges) == 0 {
		t.Fatalf("%s enumerates no badges", stateBadgeParityPath)
	}

	return fixture
}

// TestStateBadgeMatchesSharedContract renders every state the contract names
// and compares the markup character for character. The expected markup is
// assembled from the same three fields the TypeScript side reads, so the two
// tests cannot agree with the fixture and disagree with each other.
func TestStateBadgeMatchesSharedContract(t *testing.T) {
	t.Parallel()

	fixture := loadStateBadgeFixture(t)

	for _, badge := range fixture.Badges {
		t.Run(fmt.Sprintf("state=%q", badge.State), func(t *testing.T) {
			t.Parallel()

			var output bytes.Buffer

			err := ui.StateBadge(badge.State).Render(context.Background(), &output)
			if err != nil {
				t.Fatalf("render the badge for %q: %v", badge.State, err)
			}

			want := fmt.Sprintf("<span class=%q>%s</span>", badge.Class, badge.Label)
			if badge.Style != "" {
				want = fmt.Sprintf("<span class=%q style=%q>%s</span>", badge.Class, badge.Style, badge.Label)
			}

			if got := output.String(); got != want {
				t.Errorf("StateBadge(%q) rendered\n\t%s\nwant\n\t%s", badge.State, got, want)
			}
		})
	}
}

// TestStateBadgeCoversEveryState pins the enumeration itself. The contract is a
// hand-written file, so nothing but this test stops a state from being added to
// the store and never reaching either badge; the fallthrough would paint it as
// a neutral chip and no one would notice until a stage read "Skipped" in the
// wrong color.
//
// store.ScheduleState is the whole vocabulary: its own doc comment says the
// values match the job states internal/server uses, and it adds the two the
// schedule needs on top of them. internal/server is not imported here because
// it imports this package, and the six JobState constants it declares are the
// same six strings.
func TestStateBadgeCoversEveryState(t *testing.T) {
	t.Parallel()

	fixture := loadStateBadgeFixture(t)

	covered := make(map[string]bool, len(fixture.Badges))
	for _, badge := range fixture.Badges {
		covered[badge.State] = true
	}

	states := []store.ScheduleState{
		store.ScheduleStatePending,
		store.ScheduleStateRunning,
		store.ScheduleStateCompleted,
		store.ScheduleStateFailed,
		store.ScheduleStateCancelled,
		store.ScheduleStatePaused,
		store.ScheduleStateSkipped,
	}

	for _, state := range states {
		if !covered[string(state)] {
			t.Errorf("%s does not cover the %q state", stateBadgeParityPath, state)
		}
	}

	// The unknown cases are the third axis the consolidation decided, so they
	// are part of the contract rather than an afterthought: an empty state and
	// a state neither side enumerates both have to be pinned.
	for _, unknown := range []string{"", "nonsense"} {
		if !covered[unknown] {
			t.Errorf("%s does not cover the unknown state %q", stateBadgeParityPath, unknown)
		}
	}
}
