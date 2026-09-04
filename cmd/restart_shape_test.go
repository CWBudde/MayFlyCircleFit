//nolint:testpackage // exercises unexported resume helpers, as the other cmd tests do
package cmd

import (
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
)

// The two shapes each table names. Spelling them once keeps the case labels
// identical across the tables, which is what makes a failure say which shape
// broke rather than which table it broke in.
const (
	caseFillingCap = "filling cap"
	caseFixedCount = "fixed count"
)

// A negative --restarts is the budget-filling shape: a cap of abs(N) times
// --iters, spent on as many whole cold attempts as fit inside it. The CLI has
// to carry the sign all the way to the configuration, because clamping it
// anywhere on the way turns the campaign into a single attempt without saying
// so.
//
//nolint:paralleltest // mutates the package-level command flags, which every test in this package shares.
func TestRestartsFlagAcceptsABudgetFillingCap(t *testing.T) {
	restore := optimizerRestarts

	t.Cleanup(func() {
		optimizerRestarts = restore

		err := runCmd.Flags().Set("restarts", "1")
		if err != nil {
			t.Fatalf("restore --restarts: %v", err)
		}
	})

	// Both spellings, because pflag consumes the next argument as the value of
	// an int flag even when it begins with a minus, and a caller writing the
	// separated form must not silently get something else.
	for _, args := range [][]string{{"--restarts=-8"}, {"--restarts", "-8"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			optimizerRestarts = 1

			err := runCmd.Flags().Parse(args)
			if err != nil {
				t.Fatalf("parse %v: %v", args, err)
			}

			if optimizerRestarts != -8 {
				t.Fatalf("optimizerRestarts = %d, want -8 so the filling cap reaches the configuration",
					optimizerRestarts)
			}
		})
	}
}

// The flag help is the only place an operator learns the shape exists.
//
//nolint:paralleltest // reads the package-level command flags, which every test in this package mutates.
func TestRestartsFlagHelpDocumentsTheFillingShape(t *testing.T) {
	flag := runCmd.Flags().Lookup("restarts")
	if flag == nil {
		t.Fatal("run command has no --restarts flag")
	}

	for _, want := range []string{"negative", "--optimizer-epochs"} {
		if !strings.Contains(flag.Usage, want) {
			t.Fatalf("--restarts help does not mention %q: %s", want, flag.Usage)
		}
	}
}

// What the CLI hands to Normalize has to come back unchanged. Only zero — an
// omitted value — is filled in.
func TestRestartsSurvivesConfigConstruction(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		restarts int
		want     int
	}{
		{caseFillingCap, -8, -8},
		{"smallest filling cap", -1, -1},
		{"largest filling cap", -app.MaxOptimizerRestarts, -app.MaxOptimizerRestarts},
		{caseFixedCount, 4, 4},
		{"omitted", 0, 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			config, err := app.Normalize(app.JobConfig{
				RefPath:           "reference.png",
				Mode:              app.ModeJoint,
				Circles:           10,
				Iters:             100,
				PopSize:           30,
				OptimizerEpochs:   1,
				OptimizerRestarts: testCase.restarts,
			})
			if err != nil {
				t.Fatalf("Normalize() = %v, want nil", err)
			}

			if config.OptimizerRestarts != testCase.want {
				t.Fatalf("OptimizerRestarts = %d, want %d", config.OptimizerRestarts, testCase.want)
			}
		})
	}
}

// Resume reads its restart shape from the checkpoint, so a clamp here would
// resume a filling campaign as a single cold attempt.
func TestCheckpointRestartsKeepsTheFillingShape(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		restarts int
		want     int
	}{
		{caseFillingCap, -8, -8},
		{"smallest filling cap", -1, -1},
		{"largest filling cap", -app.MaxOptimizerRestarts, -app.MaxOptimizerRestarts},
		{caseFixedCount, 4, 4},
		// Every checkpoint written before the field existed carries zero, and
		// those resume as the historical single attempt.
		{"written before the field existed", 0, 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := checkpointRestarts(testCase.restarts); got != testCase.want {
				t.Fatalf("checkpointRestarts(%d) = %d, want %d", testCase.restarts, got, testCase.want)
			}
		})
	}
}

// The dry-run row is where an author reads a document back. A filling cap must
// be named there, because the ITERATIONS column prints the same bound for a
// fixed count and for a cap of the same magnitude.
func TestStagePlanParametersNamesTheRestartShape(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		restarts int
		want     string
	}{
		{"single attempt says nothing", 1, ""},
		{caseFixedCount, 4, "4 restarts"},
		{caseFillingCap, -8, "restarts filling 8 × iters"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			stage := app.ScheduleStage{Config: app.JobConfig{
				Mode:              app.ModeJoint,
				Iters:             100,
				PopSize:           30,
				OptimizerEpochs:   1,
				OptimizerRestarts: testCase.restarts,
			}}

			got := stagePlanParameters(stage)

			if testCase.want == "" {
				if strings.Contains(got, "restart") {
					t.Fatalf("stagePlanParameters() = %q, want no restart clause", got)
				}

				return
			}

			if !strings.Contains(got, testCase.want) {
				t.Fatalf("stagePlanParameters() = %q, want it to contain %q", got, testCase.want)
			}
		})
	}
}
