package store

import (
	"encoding/json"
	"testing"
	"time"

)

func TestCheckpoint_JSONSerialization(t *testing.T) {
	original := &Checkpoint{
		JobID:       "test-job-123",
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
		JobID:       "test-job",
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
		JobID:       "valid-job",
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

	if _, ok := err.(*ValidationError); !ok {
		t.Errorf("Expected ValidationError, got %T", err)
	}
}

func TestCheckpoint_Validate_NilBestParams(t *testing.T) {
	checkpoint := &Checkpoint{
		JobID:       "test",
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
		JobID:       "test",
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
				JobID:       "test",
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
				JobID:       "test",
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
		JobID:       "test",
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
				JobID:       "test",
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

	if _, ok := err.(*CompatibilityError); !ok {
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
		JobID:       "test-job",
		BestCost:    0.123,
		Iteration:   500,
		Timestamp:   time.Now(),
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
	jobID := "test-job"
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
