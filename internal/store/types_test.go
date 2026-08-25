package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestCheckpoint_JSONSerialization(t *testing.T) {
	original := &Checkpoint{
		JobID:       testJobID(1),
		BestParams:  []float64{100.5, 50.2, 25.0, 0.8, 0.2, 0.1, 0.9},
		BestCost:    0.0234,
		InitialCost: 0.5621,
		Iteration:   500,
		Timestamp:   time.Date(2025, 10, 23, 10, 30, 0, 0, time.UTC),
		Config: JobConfig{
			RefPath: "assets/test.png",
			Mode:    "joint",
			Circles: 1,
			Iters:   1000,
			PopSize: 30,
			Seed:    42,
		},
	}

	// Serialize to JSON
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Failed to marshal checkpoint: %v", err)
	}

	// Verify JSON is not empty
	if len(data) == 0 {
		t.Fatal("Marshaled JSON is empty")
	}

	// Deserialize from JSON
	var restored Checkpoint
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Failed to unmarshal checkpoint: %v", err)
	}

	// Verify all fields match
	if restored.JobID != original.JobID {
		t.Errorf("JobID mismatch: expected %s, got %s", original.JobID, restored.JobID)
	}

	if restored.BestCost != original.BestCost {
		t.Errorf("BestCost mismatch: expected %f, got %f", original.BestCost, restored.BestCost)
	}

	if restored.InitialCost != original.InitialCost {
		t.Errorf("InitialCost mismatch: expected %f, got %f", original.InitialCost, restored.InitialCost)
	}

	if restored.Iteration != original.Iteration {
		t.Errorf("Iteration mismatch: expected %d, got %d", original.Iteration, restored.Iteration)
	}

	if !restored.Timestamp.Equal(original.Timestamp) {
		t.Errorf("Timestamp mismatch: expected %v, got %v", original.Timestamp, restored.Timestamp)
	}

	if len(restored.BestParams) != len(original.BestParams) {
		t.Fatalf("BestParams length mismatch: expected %d, got %d", len(original.BestParams), len(restored.BestParams))
	}

	for i := range original.BestParams {
		if restored.BestParams[i] != original.BestParams[i] {
			t.Errorf("BestParams[%d] mismatch: expected %f, got %f", i, original.BestParams[i], restored.BestParams[i])
		}
	}

	if restored.Config.RefPath != original.Config.RefPath {
		t.Errorf("Config.RefPath mismatch: expected %s, got %s", original.Config.RefPath, restored.Config.RefPath)
	}

	if restored.Config.Mode != original.Config.Mode {
		t.Errorf("Config.Mode mismatch: expected %s, got %s", original.Config.Mode, restored.Config.Mode)
	}

	if restored.Config.Circles != original.Config.Circles {
		t.Errorf("Config.Circles mismatch: expected %d, got %d", original.Config.Circles, restored.Config.Circles)
	}
}

func TestCheckpoint_JSONIndented(t *testing.T) {
	checkpoint := &Checkpoint{
		JobID:       testJobID(1),
		BestParams:  []float64{1.0, 2.0, 3.0, 0.5, 0.5, 0.5, 1.0},
		BestCost:    0.1,
		InitialCost: 0.5,
		Iteration:   100,
		Timestamp:   time.Now(),
		Config: JobConfig{
			RefPath: "test.png",
			Mode:    "joint",
			Circles: 1,
			Iters:   100,
			PopSize: 10,
			Seed:    0,
		},
	}

	// Serialize with indentation (like FSStore does)
	data, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal with indent: %v", err)
	}

	// Verify it's valid JSON and can be unmarshaled
	var restored Checkpoint
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Failed to unmarshal indented JSON: %v", err)
	}

	if restored.JobID != checkpoint.JobID {
		t.Errorf("JobID mismatch after indented serialization")
	}
}

func TestCheckpoint_Validate_Valid(t *testing.T) {
	checkpoint := &Checkpoint{
		JobID:       testJobID(1),
		BestParams:  []float64{100, 50, 25, 0.8, 0.2, 0.1, 0.9},
		BestCost:    0.1,
		InitialCost: 0.5,
		Iteration:   100,
		Timestamp:   time.Now(),
		Config: JobConfig{
			RefPath: "test.png",
			Mode:    "joint",
			Circles: 1,
			Iters:   1000,
			PopSize: 30,
			Seed:    42,
		},
	}

	err := checkpoint.Validate()
	if err != nil {
		t.Errorf("Valid checkpoint should not have validation error: %v", err)
	}
}

func TestCheckpoint_Validate_EmptyJobID(t *testing.T) {
	checkpoint := &Checkpoint{
		JobID:       "",
		BestParams:  []float64{1, 2, 3, 4, 5, 6, 7},
		BestCost:    0.1,
		InitialCost: 0.5,
		Iteration:   100,
		Timestamp:   time.Now(),
		Config: JobConfig{
			RefPath: "test.png",
			Mode:    "joint",
			Circles: 1,
			Iters:   100,
			PopSize: 10,
		},
	}

	err := checkpoint.Validate()
	if err == nil {
		t.Fatal("Expected validation error for empty JobID")
	}

	validationError := &ValidationError{}
	if !errors.As(err, &validationError) {
		t.Errorf("Expected ValidationError, got %T", err)
	}
}

func TestCheckpoint_Validate_NilBestParams(t *testing.T) {
	checkpoint := &Checkpoint{
		JobID:       testJobID(1),
		BestParams:  nil,
		BestCost:    0.1,
		InitialCost: 0.5,
		Iteration:   100,
		Timestamp:   time.Now(),
		Config: JobConfig{
			RefPath: "test.png",
			Mode:    "joint",
			Circles: 1,
			Iters:   100,
			PopSize: 10,
		},
	}

	err := checkpoint.Validate()
	if err == nil {
		t.Fatal("Expected validation error for nil BestParams")
	}
}

func TestCheckpoint_Validate_EmptyBestParams(t *testing.T) {
	checkpoint := &Checkpoint{
		JobID:       testJobID(1),
		BestParams:  []float64{},
		BestCost:    0.1,
		InitialCost: 0.5,
		Iteration:   100,
		Timestamp:   time.Now(),
		Config: JobConfig{
			RefPath: "test.png",
			Mode:    "joint",
			Circles: 1,
			Iters:   100,
			PopSize: 10,
		},
	}

	err := checkpoint.Validate()
	if err == nil {
		t.Fatal("Expected validation error for empty BestParams")
	}
}

func TestCheckpoint_Validate_InvalidParamsLength(t *testing.T) {
	testCases := []struct {
		name       string
		bestParams []float64
	}{
		{"not multiple of 7", []float64{1, 2, 3, 4, 5}},
		{"wrong count for circles", []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}}, // 14 params = 2 circles, but config says 1
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			checkpoint := &Checkpoint{
				JobID:       testJobID(1),
				BestParams:  tc.bestParams,
				BestCost:    0.1,
				InitialCost: 0.5,
				Iteration:   100,
				Timestamp:   time.Now(),
				Config: JobConfig{
					RefPath: "test.png",
					Mode:    "joint",
					Circles: 1, // Expects 7 params
					Iters:   100,
					PopSize: 10,
				},
			}

			err := checkpoint.Validate()
			if err == nil {
				t.Fatalf("Expected validation error for %s", tc.name)
			}
		})
	}
}

func TestCheckpoint_Validate_NegativeValues(t *testing.T) {
	testCases := []struct {
		name        string
		bestCost    float64
		initialCost float64
		iteration   int
	}{
		{"negative cost", -0.1, 0.5, 100},
		{"negative initial cost", 0.1, -0.5, 100},
		{"negative iteration", 0.1, 0.5, -10},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			checkpoint := &Checkpoint{
				JobID:       testJobID(1),
				BestParams:  []float64{1, 2, 3, 4, 5, 6, 7},
				BestCost:    tc.bestCost,
				InitialCost: tc.initialCost,
				Iteration:   tc.iteration,
				Timestamp:   time.Now(),
				Config: JobConfig{
					RefPath: "test.png",
					Mode:    "joint",
					Circles: 1,
					Iters:   100,
					PopSize: 10,
				},
			}

			err := checkpoint.Validate()
			if err == nil {
				t.Fatalf("Expected validation error for %s", tc.name)
			}
		})
	}
}

func TestCheckpoint_Validate_ZeroTimestamp(t *testing.T) {
	checkpoint := &Checkpoint{
		JobID:       testJobID(1),
		BestParams:  []float64{1, 2, 3, 4, 5, 6, 7},
		BestCost:    0.1,
		InitialCost: 0.5,
		Iteration:   100,
		Timestamp:   time.Time{}, // Zero value
		Config: JobConfig{
			RefPath: "test.png",
			Mode:    "joint",
			Circles: 1,
			Iters:   100,
			PopSize: 10,
		},
	}

	err := checkpoint.Validate()
	if err == nil {
		t.Fatal("Expected validation error for zero timestamp")
	}
}

func TestCheckpoint_Validate_InvalidConfig(t *testing.T) {
	testCases := []struct {
		name   string
		config JobConfig
	}{
		{"empty refPath", JobConfig{RefPath: "", Mode: "joint", Circles: 1, Iters: 100, PopSize: 10}},
		{"empty mode", JobConfig{RefPath: "test.png", Mode: "", Circles: 1, Iters: 100, PopSize: 10}},
		{"zero circles", JobConfig{RefPath: "test.png", Mode: "joint", Circles: 0, Iters: 100, PopSize: 10}},
		{"negative circles", JobConfig{RefPath: "test.png", Mode: "joint", Circles: -1, Iters: 100, PopSize: 10}},
		{"zero iters", JobConfig{RefPath: "test.png", Mode: "joint", Circles: 1, Iters: 0, PopSize: 10}},
		{"zero popSize", JobConfig{RefPath: "test.png", Mode: "joint", Circles: 1, Iters: 100, PopSize: 0}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			checkpoint := &Checkpoint{
				JobID:       testJobID(1),
				BestParams:  []float64{1, 2, 3, 4, 5, 6, 7},
				BestCost:    0.1,
				InitialCost: 0.5,
				Iteration:   100,
				Timestamp:   time.Now(),
				Config:      tc.config,
			}

			err := checkpoint.Validate()
			if err == nil {
				t.Fatalf("Expected validation error for %s", tc.name)
			}
		})
	}
}

func TestCheckpoint_IsCompatible_Compatible(t *testing.T) {
	checkpoint := &Checkpoint{
		Config: JobConfig{
			RefPath: "test.png",
			Mode:    "joint",
			Circles: 10,
		},
	}

	config := JobConfig{
		RefPath: "test.png",
		Mode:    "joint",
		Circles: 10,
	}

	err := checkpoint.IsCompatible(config)
	if err != nil {
		t.Errorf("Compatible configs should not return error: %v", err)
	}
}

func TestCheckpoint_IsCompatible_DifferentRefPath(t *testing.T) {
	checkpoint := &Checkpoint{
		Config: JobConfig{
			RefPath: "test1.png",
			Mode:    "joint",
			Circles: 10,
		},
	}

	config := JobConfig{
		RefPath: "test2.png",
		Mode:    "joint",
		Circles: 10,
	}

	err := checkpoint.IsCompatible(config)
	if err == nil {
		t.Fatal("Expected compatibility error for different RefPath")
	}

	compatibilityError := &CompatibilityError{}
	if !errors.As(err, &compatibilityError) {
		t.Errorf("Expected CompatibilityError, got %T", err)
	}
}

func TestCheckpoint_IsCompatible_DifferentMode(t *testing.T) {
	checkpoint := &Checkpoint{
		Config: JobConfig{
			RefPath: "test.png",
			Mode:    "joint",
			Circles: 10,
		},
	}

	config := JobConfig{
		RefPath: "test.png",
		Mode:    "sequential",
		Circles: 10,
	}

	err := checkpoint.IsCompatible(config)
	if err == nil {
		t.Fatal("Expected compatibility error for different Mode")
	}
}

func TestCheckpoint_IsCompatible_DifferentCircles(t *testing.T) {
	checkpoint := &Checkpoint{
		Config: JobConfig{
			RefPath: "test.png",
			Mode:    "joint",
			Circles: 10,
		},
	}

	config := JobConfig{
		RefPath: "test.png",
		Mode:    "joint",
		Circles: 20,
	}

	err := checkpoint.IsCompatible(config)
	if err == nil {
		t.Fatal("Expected compatibility error for different Circles")
	}
}

func TestCheckpointInfo_FromCheckpoint(t *testing.T) {
	checkpoint := &Checkpoint{
		JobID:     testJobID(1),
		BestCost:  0.123,
		Iteration: 500,
		Timestamp: time.Now(),
		Config: JobConfig{
			RefPath: "test.png",
			Mode:    "joint",
			Circles: 10,
		},
	}

	info := checkpoint.ToInfo()

	if info.JobID != checkpoint.JobID {
		t.Errorf("JobID mismatch: expected %s, got %s", checkpoint.JobID, info.JobID)
	}

	if info.BestCost != checkpoint.BestCost {
		t.Errorf("BestCost mismatch: expected %f, got %f", checkpoint.BestCost, info.BestCost)
	}

	if info.Iteration != checkpoint.Iteration {
		t.Errorf("Iteration mismatch: expected %d, got %d", checkpoint.Iteration, info.Iteration)
	}

	if !info.Timestamp.Equal(checkpoint.Timestamp) {
		t.Errorf("Timestamp mismatch")
	}

	if info.Mode != checkpoint.Config.Mode {
		t.Errorf("Mode mismatch: expected %s, got %s", checkpoint.Config.Mode, info.Mode)
	}

	if info.Circles != checkpoint.Config.Circles {
		t.Errorf("Circles mismatch: expected %d, got %d", checkpoint.Config.Circles, info.Circles)
	}

	if info.RefPath != checkpoint.Config.RefPath {
		t.Errorf("RefPath mismatch: expected %s, got %s", checkpoint.Config.RefPath, info.RefPath)
	}
}

func TestNewCheckpoint(t *testing.T) {
	jobID := testJobID(1)
	bestParams := []float64{1, 2, 3, 4, 5, 6, 7}
	bestCost := 0.123
	initialCost := 0.5
	iteration := 500
	config := JobConfig{
		RefPath: "test.png",
		Mode:    "joint",
		Circles: 1,
		Iters:   1000,
		PopSize: 30,
		Seed:    42,
	}

	checkpoint := NewCheckpoint(jobID, bestParams, bestCost, initialCost, iteration, config)

	if checkpoint.JobID != jobID {
		t.Errorf("JobID mismatch: expected %s, got %s", jobID, checkpoint.JobID)
	}

	if checkpoint.BestCost != bestCost {
		t.Errorf("BestCost mismatch: expected %f, got %f", bestCost, checkpoint.BestCost)
	}

	if checkpoint.Iteration != iteration {
		t.Errorf("Iteration mismatch: expected %d, got %d", iteration, checkpoint.Iteration)
	}

	if checkpoint.Timestamp.IsZero() {
		t.Error("Timestamp should not be zero")
	}

	if len(checkpoint.BestParams) != len(bestParams) {
		t.Errorf("BestParams length mismatch")
	}
}

// TestCheckpointAcceptsNewTerminationValues covers the reasons that became
// reachable once optimizer termination was propagated end to end. The wire
// field is free-form, so these must survive a round trip and validation without
// a schema-version bump.
func TestCheckpointAcceptsNewTerminationValues(t *testing.T) {
	for _, termination := range []string{"target_cost", "stagnation", "convergence", "stage_convergence", "refill_limit", "completed"} {
		t.Run(termination, func(t *testing.T) {
			original := &Checkpoint{
				JobID:            "3f2504e0-4f89-41d3-9a0c-0305e82c3301",
				BestParams:       make([]float64, 7),
				BestCost:         1.5,
				RequestedCircles: 1,
				ActualCircles:    1,
				EffectiveSeed:    3,
				Iterations:       10,
				Evaluations:      100,
				Termination:      termination,
				Timestamp:        time.Now(),
				Config:           JobConfig{RefPath: "reference.png", Mode: "joint", Circles: 1, Iters: 10, PopSize: 20},
			}

			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}

			var restored Checkpoint
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}

			if restored.Termination != termination {
				t.Fatalf("Termination = %q, want %q", restored.Termination, termination)
			}

			if restored.SchemaVersion != CheckpointSchemaVersion {
				t.Fatalf("SchemaVersion = %d, want %d", restored.SchemaVersion, CheckpointSchemaVersion)
			}

			if err := restored.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
}

// TestOldCheckpointLoadsWithoutEarlyStopFields is the backward-compatibility
// gate for the optimizer-level stopping configuration. Checkpoints written
// before those fields existed must still load, validate, and resume with early
// stopping disabled.
func TestOldCheckpointLoadsWithoutEarlyStopFields(t *testing.T) {
	tests := []struct {
		name            string
		schema          string
		wantTermination string
	}{
		{name: "schema v0", schema: "", wantTermination: TerminationLegacy},
		{name: "schema v1", schema: `"schemaVersion": 1,`, wantTermination: TerminationLegacy},
		{name: "schema v2", schema: `"schemaVersion": 2,`, wantTermination: TerminationUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := `{
				` + test.schema + `
				"jobId": "8d5f1c2a-1f2e-4c3b-8a9d-2b6c7e0f1a34",
				"bestParams": [0, 0, 1, 0, 0, 0, 0],
				"bestCost": 2.5,
				"initialCost": 5,
				"requestedCircles": 1,
				"actualCircles": 1,
				"effectiveSeed": 11,
				"iterations": 40,
				"timestamp": "2026-01-02T03:04:05Z",
				"config": {
					"refPath": "reference.png",
					"mode": "joint",
					"circles": 1,
					"iters": 100,
					"popSize": 30,
					"seed": 11
				}
			}`

			var checkpoint Checkpoint

			err := json.Unmarshal([]byte(raw), &checkpoint)
			if err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}

			err = checkpoint.Validate()
			if err != nil {
				t.Fatalf("Validate() error = %v", err)
			}

			if checkpoint.SchemaVersion != CheckpointSchemaVersion {
				t.Fatalf("SchemaVersion = %d, want %d", checkpoint.SchemaVersion, CheckpointSchemaVersion)
			}

			if checkpoint.Termination != test.wantTermination {
				t.Fatalf("Termination = %q, want %q", checkpoint.Termination, test.wantTermination)
			}

			config := checkpoint.Config
			if config.StopTargetCost != 0 || config.StopMinImprovement != 0 ||
				config.StopStagnationIters != 0 || config.StopMinIters != 0 {
				t.Fatalf("old checkpoint gained early-stop settings: %+v", config)
			}

			if config.EarlyStopEnabled() {
				t.Fatal("old checkpoint resumes with early stopping enabled")
			}
		})
	}
}
