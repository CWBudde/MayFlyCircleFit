package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/circlefit/internal/opt"
)

func optimizerVersionTestConfig() JobConfig {
	return JobConfig{RefPath: "reference.png", Mode: "joint", Circles: 1, Iters: 100, PopSize: 30, Seed: 9}
}

// TestNewCheckpointRecordsOptimizerVersion pins the write side of the resume
// guard: a checkpoint has to say which optimizer produced it, or a later resume
// cannot tell that it is crossing a comparability boundary.
func TestNewCheckpointRecordsOptimizerVersion(t *testing.T) {
	checkpoint := NewCheckpoint(testJobID(1), []float64{1, 2, 3, 0.1, 0.2, 0.3, 0.4}, 0.1, 1, 42, optimizerVersionTestConfig())

	if want := opt.LibraryVersion(); checkpoint.OptimizerVersion != want {
		t.Fatalf("OptimizerVersion = %q, want %q", checkpoint.OptimizerVersion, want)
	}
}

// TestCheckpointOptimizerVersionRoundTrips pins the field through the wire
// format, including the listing projection a status view reads.
func TestCheckpointOptimizerVersionRoundTrips(t *testing.T) {
	checkpoint := NewCheckpoint(testJobID(2), []float64{1, 2, 3, 0.1, 0.2, 0.3, 0.4}, 0.1, 1, 42, optimizerVersionTestConfig())
	checkpoint.OptimizerVersion = "v0.4.0"

	data, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(data), `"optimizerVersion":"v0.4.0"`) {
		t.Fatalf("encoded checkpoint omits the optimizer version: %s", data)
	}

	var restored Checkpoint

	err = json.Unmarshal(data, &restored)
	if err != nil {
		t.Fatal(err)
	}

	if restored.OptimizerVersion != "v0.4.0" {
		t.Fatalf("OptimizerVersion = %q, want %q", restored.OptimizerVersion, "v0.4.0")
	}

	if info := restored.ToInfo(); info.OptimizerVersion != "v0.4.0" {
		t.Fatalf("info OptimizerVersion = %q, want %q", info.OptimizerVersion, "v0.4.0")
	}
}

// TestLegacyCheckpointHasNoOptimizerVersion pins the read side of the
// compatibility promise: a checkpoint written before the field existed decodes
// with it empty rather than being rejected.
func TestLegacyCheckpointHasNoOptimizerVersion(t *testing.T) {
	legacy := fmt.Sprintf(`{
		"jobId": %q,
		"bestParams": [1,2,3,0.1,0.2,0.3,0.4],
		"bestCost": 0.1,
		"initialCost": 1,
		"iteration": 5,
		"timestamp": %q,
		"config": {"refPath":"reference.png","mode":"joint","circles":1,"iters":100,"popSize":30,"seed":7}
	}`, testJobID(3), time.Now().UTC().Format(time.RFC3339Nano))

	var migrated Checkpoint

	err := json.Unmarshal([]byte(legacy), &migrated)
	if err != nil {
		t.Fatal(err)
	}

	if migrated.OptimizerVersion != "" {
		t.Fatalf("OptimizerVersion = %q, want empty for a legacy checkpoint", migrated.OptimizerVersion)
	}

	err = migrated.Validate()
	if err != nil {
		t.Fatalf("legacy checkpoint is invalid: %v", err)
	}
}
