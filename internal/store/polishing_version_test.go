package store_test

import (
	"encoding/json"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/opt"
	"github.com/cwbudde/circlefit/internal/store"
)

// testRefPath is the reference every checkpoint below names. None of these
// tests loads it; the field only has to be non-empty for the configuration to
// look like a real one.
const testRefPath = "ref.png"

// TestNewCheckpointRecordsTheSecondLibrary pins the version a job's polishing
// sweep ran, for the case where that is a library the base version does not
// speak for.
//
// A CMA-ES stage finished by the default MayFly sweep runs two libraries. With
// only the base version recorded, a behaviour-changing MayFly upgrade would
// resume such a checkpoint unremarked, which is the silent continuation the
// guard exists to refuse.
func TestNewCheckpointRecordsTheSecondLibrary(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		base     app.Optimizer
		polisher app.Optimizer
		polish   bool
		want     string
	}{
		"mayfly sweep under a cmaes base": {
			base: app.OptimizerCMAES, polisher: app.OptimizerMayfly, polish: true,
			want: opt.LibraryVersion(),
		},
		"cmaes sweep under a mayfly base": {
			base: app.OptimizerMayfly, polisher: app.OptimizerCMAES, polish: true,
			want: opt.CMAESLibraryVersion(),
		},
		// One library, so the base version already speaks for the sweep and a
		// second reading could only ever disagree with it.
		"one library": {base: app.OptimizerCMAES, polisher: app.OptimizerCMAES, polish: true},
		"no polishing": {
			base: app.OptimizerCMAES, polisher: app.OptimizerMayfly, polish: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			config := store.JobConfig{
				RefPath:            testRefPath,
				Circles:            1,
				Optimizer:          test.base,
				PolishingOptimizer: test.polisher,
				PolishingEnabled:   test.polish,
			}

			checkpoint := store.NewCheckpoint("job", make([]float64, 7), 1, 2, 3, config)
			if checkpoint.PolishingOptimizerVersion != test.want {
				t.Errorf("PolishingOptimizerVersion = %q, want %q",
					checkpoint.PolishingOptimizerVersion, test.want)
			}
		})
	}
}

// TestPolishingOptimizerVersionRoundTrips keeps the field on the wire. It is
// written by one process and read by another, so an encoder or decoder that
// drops it would restore the very gap the field closes -- and quietly, because
// an empty value reads as a legacy checkpoint rather than as an error.
func TestPolishingOptimizerVersionRoundTrips(t *testing.T) {
	t.Parallel()

	config := store.JobConfig{
		RefPath:            testRefPath,
		Circles:            1,
		Optimizer:          app.OptimizerCMAES,
		PolishingOptimizer: app.OptimizerMayfly,
		PolishingEnabled:   true,
	}

	checkpoint := store.NewCheckpoint("job", make([]float64, 7), 1, 2, 3, config)

	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded store.Checkpoint

	err = json.Unmarshal(encoded, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.PolishingOptimizerVersion != checkpoint.PolishingOptimizerVersion {
		t.Errorf("PolishingOptimizerVersion = %q, want %q",
			decoded.PolishingOptimizerVersion, checkpoint.PolishingOptimizerVersion)
	}
}

// TestLegacyCheckpointCarriesNoPolishingVersion pins the additive contract: a
// document written before the field existed decodes with it empty, which the
// guard reads as unknown rather than as a mismatch, so the schema version does
// not have to move.
func TestLegacyCheckpointCarriesNoPolishingVersion(t *testing.T) {
	t.Parallel()

	const legacy = `{"schemaVersion":2,"jobId":"job","bestParams":[0,0,0,0,0,0,0],` +
		`"bestCost":1,"initialCost":2,"iterations":3,"optimizerVersion":"v0.7.1",` +
		`"config":{"refPath":"ref.png","circles":1}}`

	var decoded store.Checkpoint

	err := json.Unmarshal([]byte(legacy), &decoded)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.PolishingOptimizerVersion != "" {
		t.Errorf("PolishingOptimizerVersion = %q, want empty", decoded.PolishingOptimizerVersion)
	}

	if decoded.OptimizerVersion != "v0.7.1" {
		t.Errorf("OptimizerVersion = %q, want v0.7.1", decoded.OptimizerVersion)
	}
}
