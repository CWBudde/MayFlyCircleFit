package store_test

import (
	"encoding/json"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// TestNewCheckpointRecordsTheEngineAndItsVersion pins that a checkpoint says
// which optimizer produced it. Without the engine, a resumed Dragonfly run
// silently becomes a MayFly run; without the matching version, the resume guard
// compares a Dragonfly cost against a MayFly version and refuses a legitimate
// resume.
func TestNewCheckpointRecordsTheEngineAndItsVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		optimizer   app.Optimizer
		wantEngine  app.Optimizer
		wantVersion string
	}{
		{name: "mayfly", optimizer: app.OptimizerMayfly, wantEngine: app.OptimizerMayfly, wantVersion: opt.LibraryVersion()},
		{
			name:        "dragonfly",
			optimizer:   app.OptimizerDragonfly,
			wantEngine:  app.OptimizerDragonfly,
			wantVersion: opt.DragonflyLibraryVersion(),
		},
		{
			name:        "cmaes",
			optimizer:   app.OptimizerCMAES,
			wantEngine:  app.OptimizerCMAES,
			wantVersion: opt.CMAESLibraryVersion(),
		},
		// A configuration written before the field existed records no engine
		// and keeps MayFly's version, exactly as it always did.
		{name: "absent", optimizer: "", wantEngine: "", wantVersion: opt.LibraryVersion()},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			config := store.JobConfig{RefPath: "ref.png", Circles: 1, Optimizer: test.optimizer}
			checkpoint := store.NewCheckpoint("job", make([]float64, 7), 1, 2, 3, config)

			if checkpoint.Config.Optimizer != test.wantEngine {
				t.Errorf("Config.Optimizer = %q, want %q", checkpoint.Config.Optimizer, test.wantEngine)
			}

			if checkpoint.OptimizerVersion != test.wantVersion {
				t.Errorf("OptimizerVersion = %q, want %q", checkpoint.OptimizerVersion, test.wantVersion)
			}
		})
	}
}

// TestCheckpointRoundTripsTheEngine covers the persisted contract: the engine
// has to survive a save and load, and a document written before the field
// existed has to decode as MayFly rather than as an error.
func TestCheckpointRoundTripsTheEngine(t *testing.T) {
	t.Parallel()

	config := store.JobConfig{RefPath: "ref.png", Circles: 1, Optimizer: app.OptimizerDragonfly}

	encoded, err := json.Marshal(store.NewCheckpoint("job", make([]float64, 7), 1, 2, 3, config))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded store.Checkpoint

	err = json.Unmarshal(encoded, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.Config.ResolvedOptimizer() != app.OptimizerDragonfly {
		t.Errorf("ResolvedOptimizer() = %q, want %q", decoded.Config.ResolvedOptimizer(), app.OptimizerDragonfly)
	}

	var legacy store.Checkpoint

	err = json.Unmarshal([]byte(`{"jobId":"job","config":{"refPath":"ref.png","circles":1}}`), &legacy)
	if err != nil {
		t.Fatalf("Unmarshal(legacy) error = %v", err)
	}

	if legacy.Config.Optimizer != "" {
		t.Errorf("legacy Config.Optimizer = %q, want it absent", legacy.Config.Optimizer)
	}

	if legacy.Config.ResolvedOptimizer() != app.OptimizerMayfly {
		t.Errorf("legacy ResolvedOptimizer() = %q, want %q", legacy.Config.ResolvedOptimizer(), app.OptimizerMayfly)
	}
}

func TestCheckpointRoundTripsCMAESConfiguration(t *testing.T) {
	t.Parallel()

	sigma := 0.2
	active := false
	config := store.JobConfig{
		RefPath: "ref.png", Circles: 1, Optimizer: app.OptimizerCMAES,
		InitialSigma: &sigma, ActiveCMA: &active,
		CovarianceMode: app.CMAESCovarianceBlock, RestartStrategy: app.CMAESRestartBIPOP,
	}

	encoded, err := json.Marshal(store.NewCheckpoint("job", make([]float64, 7), 1, 2, 3, config))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	var decoded store.Checkpoint

	err = json.Unmarshal(encoded, &decoded)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.Config.ResolvedOptimizer() != app.OptimizerCMAES ||
		decoded.Config.ResolvedCMAESInitialSigma() != sigma ||
		decoded.Config.ResolvedCMAESActive() ||
		decoded.Config.ResolvedCMAESCovarianceMode() != app.CMAESCovarianceBlock ||
		decoded.Config.ResolvedCMAESRestartStrategy() != app.CMAESRestartBIPOP {
		t.Fatalf("decoded CMA-ES config = %+v", decoded.Config)
	}
}
