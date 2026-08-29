package app

import "testing"

func TestOptimizerRestartsDefaultsToOne(t *testing.T) {
	t.Parallel()

	config, err := Normalize(JobConfig{RefPath: "reference.png"})
	if err != nil {
		t.Fatal(err)
	}

	if config.OptimizerRestarts != 1 {
		t.Fatalf("OptimizerRestarts = %d, want 1 so an unset config keeps the historical single attempt",
			config.OptimizerRestarts)
	}
}

func TestOptimizerRestartsBounds(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		restarts int
		valid    bool
	}{
		{"zero is filled in by defaults", 0, true},
		{"one", 1, true},
		{"limit", MaxOptimizerRestarts, true},
		{"negative", -1, false},
		{"above limit", MaxOptimizerRestarts + 1, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := Normalize(JobConfig{
				RefPath:           "reference.png",
				OptimizerRestarts: testCase.restarts,
			})

			if testCase.valid && err != nil {
				t.Fatalf("Normalize() = %v, want nil", err)
			}

			if !testCase.valid && err == nil {
				t.Fatal("Normalize() = nil, want an error")
			}
		})
	}
}

// Restarts multiply the planned work exactly as epochs do. A plan that ignored
// them would under-report a restarted campaign's cost by the restart factor.
func TestPlannedIterationsCountsRestarts(t *testing.T) {
	t.Parallel()

	base, err := Normalize(JobConfig{RefPath: "reference.png", Mode: ModeJoint, Iters: 100})
	if err != nil {
		t.Fatal(err)
	}

	restarted := base
	restarted.OptimizerRestarts = 4

	single := ScheduleStage{Config: base}.PlannedIterations()
	multiple := ScheduleStage{Config: restarted}.PlannedIterations()

	if multiple != single*4 {
		t.Fatalf("planned iterations with 4 restarts = %d, want %d (4x the single-attempt %d)",
			multiple, single*4, single)
	}
}
