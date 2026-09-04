package app

import (
	"strconv"
	"testing"
)

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
		{"above limit", MaxOptimizerRestarts + 1, false},
		// A negative count is the budget-filling shape: a cap of abs(N) times
		// iters, spent on as many whole cold attempts as fit inside it. The
		// magnitude is what the bound applies to, on either sign.
		{"minus one fills a single-attempt cap", -1, true},
		{"a filling cap of several attempts", -8, true},
		{"negative limit", -MaxOptimizerRestarts, true},
		{"below negative limit", -MaxOptimizerRestarts - 1, false},
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

// A negative count is the budget-filling shape and Normalize must leave it
// alone. Only zero is filled in; a sign is a request, not an omission.
func TestOptimizerRestartsPreservesTheFillingSign(t *testing.T) {
	t.Parallel()

	for _, restarts := range []int{-1, -4, -MaxOptimizerRestarts} {
		t.Run(strconv.Itoa(restarts), func(t *testing.T) {
			t.Parallel()

			config, err := Normalize(JobConfig{RefPath: "reference.png", OptimizerRestarts: restarts})
			if err != nil {
				t.Fatal(err)
			}

			if config.OptimizerRestarts != restarts {
				t.Fatalf("OptimizerRestarts = %d, want %d left untouched so the filling shape survives",
					config.OptimizerRestarts, restarts)
			}
		})
	}
}

// The cap a filling run may spend is exactly abs(N) times what one attempt
// costs, so the plan multiplies by the magnitude and stays an exact upper
// bound. A plan that read the value would report a negative budget.
func TestPlannedIterationsUsesTheRestartMagnitude(t *testing.T) {
	t.Parallel()

	base, err := Normalize(JobConfig{RefPath: "reference.png", Mode: ModeJoint, Iters: 100})
	if err != nil {
		t.Fatal(err)
	}

	single := ScheduleStage{Config: base}.PlannedIterations()

	for _, restarts := range []int{-1, -4, -MaxOptimizerRestarts} {
		t.Run(strconv.Itoa(restarts), func(t *testing.T) {
			t.Parallel()

			filling := base
			filling.OptimizerRestarts = restarts

			fixed := base
			fixed.OptimizerRestarts = -restarts

			got := ScheduleStage{Config: filling}.PlannedIterations()
			want := single * -restarts

			if got != want {
				t.Fatalf("planned iterations with %d restarts = %d, want %d (%dx the single-attempt %d)",
					restarts, got, want, -restarts, single)
			}

			if fixedTotal := (ScheduleStage{Config: fixed}).PlannedIterations(); got != fixedTotal {
				t.Fatalf("planned iterations with %d restarts = %d, want the %d of the matching fixed count %d",
					restarts, got, fixedTotal, -restarts)
			}
		})
	}
}
