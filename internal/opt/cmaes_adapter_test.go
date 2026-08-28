package opt_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cwbudde/circlefit/internal/opt"
)

func cmaesSphere(params []float64) float64 {
	var sum float64
	for _, value := range params {
		sum += value * value
	}

	return sum
}

func cmaesProblem() opt.Problem {
	return opt.Problem{
		Eval:  cmaesSphere,
		Lower: []float64{-5, -1, 0},
		Upper: []float64{5, 9, 2},
		Dim:   3,
	}
}

func cmaesLifecycle(t *testing.T, optimizer opt.Optimizer) opt.LifecycleOptimizer {
	t.Helper()

	lifecycle, ok := optimizer.(opt.LifecycleOptimizer)
	if !ok {
		t.Fatalf("optimizer %T is not a LifecycleOptimizer", optimizer)
	}

	return lifecycle
}

func TestCMAESAdapterSatisfiesTheOptimizerInterfaces(t *testing.T) {
	t.Parallel()

	optimizer := opt.NewCMAES(10, 8, 1)

	if _, ok := optimizer.(opt.LifecycleOptimizer); !ok {
		t.Error("NewCMAES does not return a LifecycleOptimizer")
	}

	if _, ok := optimizer.(opt.ResumableOptimizer); !ok {
		t.Error("NewCMAES does not return a ResumableOptimizer")
	}

	if budget := opt.StageIterationBudget(optimizer); budget != 10 {
		t.Errorf("StageIterationBudget() = %d, want 10", budget)
	}

	if width := opt.ParallelEvaluationWidth(optimizer); width != 1 {
		t.Errorf("ParallelEvaluationWidth() = %d, want 1", width)
	}

	parallel := opt.NewCMAES(10, 8, 1, opt.WithCMAESParallelEvaluation(4))
	if width := opt.ParallelEvaluationWidth(parallel); width != 4 {
		t.Errorf("ParallelEvaluationWidth() = %d, want 4", width)
	}
}

func TestCMAESAdapterRunStaysInBoundsAndReportsWork(t *testing.T) {
	t.Parallel()

	problem := cmaesProblem()

	result, err := cmaesLifecycle(t, opt.NewCMAES(20, 10, 42)).
		RunContext(context.Background(), problem, opt.RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Termination != opt.TerminationCompleted {
		t.Errorf("Termination = %q, want %q", result.Termination, opt.TerminationCompleted)
	}

	if result.Iterations == 0 || result.Iterations > 20 || result.Evaluations == 0 {
		t.Errorf("unexpected measured work: %+v", result)
	}

	if len(result.BestParams) != problem.Dim {
		t.Fatalf("BestParams length = %d, want %d", len(result.BestParams), problem.Dim)
	}

	for index, value := range result.BestParams {
		if value < problem.Lower[index] || value > problem.Upper[index] {
			t.Errorf("BestParams[%d] = %v, outside [%v,%v]",
				index, value, problem.Lower[index], problem.Upper[index])
		}
	}

	if result.BestCost != problem.Eval(result.BestParams) {
		t.Errorf("BestCost = %v, does not match objective at BestParams", result.BestCost)
	}
}

func TestCMAESAdapterIsDeterministicAndVariesContinuationSeeds(t *testing.T) {
	t.Parallel()

	run := func(options opt.RunOptions) opt.Result {
		result, err := cmaesLifecycle(t, opt.NewCMAES(15, 10, 7)).
			RunContext(context.Background(), cmaesProblem(), options)
		if err != nil {
			t.Fatalf("RunContext() error = %v", err)
		}

		return result
	}

	first := run(opt.RunOptions{})

	second := run(opt.RunOptions{})
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("fixed seed produced different results:\nfirst:  %#v\nsecond: %#v", first, second)
	}

	continued := run(opt.RunOptions{ResumeCount: 1})
	restarted := run(opt.RunOptions{SeedOffset: 1})

	if reflect.DeepEqual(first, continued) {
		t.Error("ResumeCount did not vary the CMA-ES run seed")
	}

	if reflect.DeepEqual(first, restarted) {
		t.Error("SeedOffset did not vary the CMA-ES run seed")
	}
}

func TestCMAESAdapterReportsProgressAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var updates []opt.Progress

	result, err := cmaesLifecycle(t, opt.NewCMAES(100, 10, 42)).RunContext(
		ctx,
		cmaesProblem(),
		opt.RunOptions{Observer: func(progress opt.Progress) {
			updates = append(updates, progress)
			progress.BestParams[0] = 999

			cancel()
		}},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RunContext() error = %v, want context.Canceled", err)
	}

	if result.Termination != opt.TerminationCancelled {
		t.Errorf("Termination = %q, want %q", result.Termination, opt.TerminationCancelled)
	}

	if len(updates) != 1 {
		t.Fatalf("observer updates = %d, want 1", len(updates))
	}

	if len(result.BestParams) == 0 || result.BestParams[0] == 999 {
		t.Error("observer received a mutable optimizer snapshot")
	}
}

func TestCMAESAdapterRepairsObjectiveConstraintsAndResults(t *testing.T) {
	t.Parallel()

	const repaired = 0.75
	problem := opt.Problem{
		Eval: func(params []float64) float64 {
			if params[0] != repaired {
				t.Errorf("objective saw unrepaired parameters: %v", params)
			}

			return cmaesSphere(params)
		},
		Repair: func(params []float64) { params[0] = repaired },
		Inequalities: []opt.InequalityConstraint{func(params []float64) float64 {
			if params[0] != repaired {
				t.Errorf("constraint saw unrepaired parameters: %v", params)
			}

			return 0.5 - params[0]
		}},
		Lower: []float64{0, -1},
		Upper: []float64{1, 1},
		Dim:   2,
	}

	result, err := cmaesLifecycle(t, opt.NewCMAES(5, 10, 42)).RunContext(
		context.Background(),
		problem,
		opt.RunOptions{
			Initial: &opt.Candidate{Params: []float64{0, 0}, Cost: -1},
			Observer: func(progress opt.Progress) {
				if progress.BestParams[0] != repaired {
					t.Errorf("progress reported unrepaired parameters: %v", progress.BestParams)
				}
			},
		},
	)
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.BestParams[0] != repaired {
		t.Errorf("BestParams = %v, want repaired first coordinate", result.BestParams)
	}

	if result.BestCost < 0 {
		t.Errorf("BestCost = %v, adapter retained stale pre-repair cost", result.BestCost)
	}
}

func TestCMAESAdapterRetainsInitialAndAdditionalSeeds(t *testing.T) {
	t.Parallel()

	problem := opt.Problem{
		Eval:  cmaesSphere,
		Lower: []float64{-1, -1},
		Upper: []float64{1, 1},
		Dim:   2,
	}
	initial := opt.Candidate{Params: []float64{0.5, 0.5}, Cost: 0.5}
	additional := opt.Candidate{Params: []float64{0, 0}, Cost: 0}

	result, err := cmaesLifecycle(t, opt.NewCMAES(3, 8, 9)).RunContext(
		context.Background(),
		problem,
		opt.RunOptions{
			Initial:         &initial,
			AdditionalSeeds: []opt.Candidate{additional},
			ResumeCount:     2,
		},
	)
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.BestCost != 0 || !slices.Equal(result.BestParams, additional.Params) {
		t.Errorf("result = %+v, want exact additional optimum retained", result)
	}
}

func TestCMAESAdapterMapsEarlyStopProgressAndEpochs(t *testing.T) {
	t.Parallel()

	var epoch opt.EpochBoundary

	result, err := cmaesLifecycle(t, opt.NewCMAES(
		100,
		10,
		42,
		opt.WithCMAESEarlyStop(opt.Stop{TargetCost: 1e9}),
	)).RunContext(context.Background(), cmaesProblem(), opt.RunOptions{
		ProgressMapper: func(progress opt.Progress) opt.Progress {
			progress.BestParams = append([]float64{99}, progress.BestParams...)

			return progress
		},
		EpochObserver: func(boundary opt.EpochBoundary) error {
			epoch = boundary

			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Termination != opt.TerminationTargetCost || result.Iterations >= 100 {
		t.Errorf("early-stop result = %+v", result)
	}

	if epoch.Epoch != 1 || epoch.Termination != opt.TerminationTargetCost {
		t.Errorf("epoch boundary = %+v", epoch)
	}

	if len(epoch.Progress.BestParams) != len(result.BestParams)+1 ||
		epoch.Progress.BestParams[0] != 99 {
		t.Errorf("epoch mapper was not applied: %+v", epoch.Progress)
	}
}

func TestCMAESAdapterForwardsStructuredLogging(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))

	_, err := cmaesLifecycle(t, opt.NewCMAES(
		2, 8, 42, opt.WithCMAESLogger(logger),
	)).RunContext(context.Background(), cmaesProblem(), opt.RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if !strings.Contains(output.String(), "event=optimization_completed") {
		t.Errorf("CMA-ES completion event was not forwarded: %s", output.String())
	}
}

func TestCMAESAdapterPropagatesEpochObserverErrors(t *testing.T) {
	t.Parallel()

	want := errors.New("save checkpoint")

	result, err := cmaesLifecycle(t, opt.NewCMAES(2, 8, 42)).RunContext(
		context.Background(),
		cmaesProblem(),
		opt.RunOptions{EpochObserver: func(opt.EpochBoundary) error { return want }},
	)
	if !errors.Is(err, want) {
		t.Fatalf("RunContext() error = %v, want %v", err, want)
	}

	if result.Evaluations == 0 || len(result.BestParams) == 0 {
		t.Errorf("observer error discarded the completed result: %+v", result)
	}
}

func TestCMAESAdapterRejectsInvalidRunInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		optimizer opt.Optimizer
		problem   opt.Problem
		options   opt.RunOptions
	}{
		{name: "invalid problem", optimizer: opt.NewCMAES(2, 8, 1)},
		{
			name: "negative resume count", optimizer: opt.NewCMAES(2, 8, 1),
			problem: cmaesProblem(), options: opt.RunOptions{ResumeCount: -1},
		},
		{
			name: "invalid continuation", optimizer: opt.NewCMAES(2, 8, 1),
			problem: cmaesProblem(),
			options: opt.RunOptions{Continuation: &opt.ContinuationProfile{}},
		},
		{
			name: "invalid population", optimizer: opt.NewCMAES(2, 1, 1),
			problem: cmaesProblem(),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := cmaesLifecycle(t, testCase.optimizer).RunContext(
				context.Background(), testCase.problem, testCase.options,
			)
			if err == nil {
				t.Fatal("RunContext() accepted invalid input")
			}
		})
	}
}

func TestCMAESUsesTheConsumerRestartWrapper(t *testing.T) {
	t.Parallel()

	base := opt.NewCMAES(4, 8, 7)
	restarted := opt.WithRestarts(base, 3)

	if budget := opt.StageIterationBudget(restarted); budget != 12 {
		t.Errorf("StageIterationBudget(WithRestarts(CMAES)) = %d, want 12", budget)
	}

	result, err := cmaesLifecycle(t, restarted).RunContext(
		context.Background(), cmaesProblem(), opt.RunOptions{},
	)
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Evaluations == 0 || len(result.BestParams) == 0 {
		t.Errorf("restarted CMA-ES returned no result: %+v", result)
	}
}

func TestCMAESParallelEvaluationMatchesSerial(t *testing.T) {
	t.Parallel()

	run := func(workers int) opt.Result {
		options := make([]opt.CMAESOption, 0, 1)
		if workers > 1 {
			options = append(options, opt.WithCMAESParallelEvaluation(workers))
		}

		result, err := cmaesLifecycle(t, opt.NewCMAES(25, 20, 4242, options...)).
			RunContext(context.Background(), cmaesProblem(), opt.RunOptions{})
		if err != nil {
			t.Fatalf("RunContext() error = %v", err)
		}

		return result
	}

	serial := run(1)
	parallel := run(4)

	wider := run(7)
	if !reflect.DeepEqual(serial, parallel) || !reflect.DeepEqual(serial, wider) {
		t.Fatalf("CMA-ES parallel evaluation diverged:\nserial:   %#v\nparallel: %#v\nwider:    %#v",
			serial, parallel, wider)
	}
}

func TestCMAESParallelEvaluationCallsObjectiveConcurrently(t *testing.T) {
	t.Parallel()

	const dim = 4
	lower := make([]float64, dim)

	upper := make([]float64, dim)
	for index := range lower {
		lower[index], upper[index] = -1, 1
	}

	var inFlight atomic.Int64
	var peak atomic.Int64
	eval := func(params []float64) float64 {
		current := inFlight.Add(1)

		for {
			observed := peak.Load()
			if current <= observed || peak.CompareAndSwap(observed, current) {
				break
			}
		}

		total := 0.0
		for range 200_000 {
			total += cmaesSphere(params)
		}

		inFlight.Add(-1)

		return total
	}

	_, err := cmaesLifecycle(t, opt.NewCMAES(
		1, 16, 99, opt.WithCMAESParallelEvaluation(4),
	)).RunContext(context.Background(), opt.Problem{
		Eval: eval, Lower: lower, Upper: upper, Dim: dim,
	}, opt.RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if peak.Load() < 2 {
		t.Fatalf("peak concurrent evaluations = %d, want at least 2", peak.Load())
	}
}

// TestCMAESAdapterReportsConvergenceBelowTheBudget pins the mapping of CMA-ES's
// distribution-aware criteria (TolX, TolFun, TolXUp, condition number, and the
// no-effect tests). They stop the run before the iteration cap, so reporting
// TerminationCompleted would contradict that value's promise that the budget
// was consumed, and would hide the stage from StagesStoppedEarly.
func TestCMAESAdapterReportsConvergenceBelowTheBudget(t *testing.T) {
	t.Parallel()

	const maxIters = 4000

	result, err := cmaesLifecycle(t, opt.NewCMAES(maxIters, 12, 7)).
		RunContext(context.Background(), cmaesProblem(), opt.RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Iterations >= maxIters {
		t.Fatalf("Iterations = %d, want a run that stops below the %d cap",
			result.Iterations, maxIters)
	}

	if result.Termination != opt.TerminationConvergence {
		t.Fatalf("Termination = %q, want %q", result.Termination, opt.TerminationConvergence)
	}
}

// TestCMAESAdapterReportsCompletedAtTheBudget keeps the counterpart honest: a
// run that does consume its cap still reports completion.
func TestCMAESAdapterReportsCompletedAtTheBudget(t *testing.T) {
	t.Parallel()

	const maxIters = 3

	result, err := cmaesLifecycle(t, opt.NewCMAES(maxIters, 12, 7)).
		RunContext(context.Background(), cmaesProblem(), opt.RunOptions{})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if result.Termination != opt.TerminationCompleted {
		t.Fatalf("Termination = %q, want %q", result.Termination, opt.TerminationCompleted)
	}
}

func TestCMAESConfigurationModesRun(t *testing.T) {
	t.Parallel()

	const dim = 14
	lower := make([]float64, dim)
	upper := make([]float64, dim)

	for index := range upper {
		upper[index] = 1
	}

	for _, mode := range []string{"full", "separable", "block"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			result, err := cmaesLifecycle(t, opt.NewCMAES(
				4, 10, 42,
				opt.WithCMAESInitialSigma(0.2),
				opt.WithCMAESCovarianceMode(mode, 7),
				opt.WithCMAESActiveCMA(false),
			)).RunContext(context.Background(), opt.Problem{
				Eval: cmaesSphere, Lower: lower, Upper: upper, Dim: dim,
			}, opt.RunOptions{})
			if err != nil {
				t.Fatalf("RunContext() error = %v", err)
			}

			if result.Iterations < 1 || result.Iterations > 4 || result.Evaluations != result.Iterations*10 {
				t.Errorf("work = %d iterations/%d evaluations", result.Iterations, result.Evaluations)
			}
		})
	}
}

func TestCMAESSearchDiagnosticsMatchProgress(t *testing.T) {
	t.Parallel()

	var updates []opt.Progress

	_, err := cmaesLifecycle(t, opt.NewCMAES(
		4, 10, 42, opt.WithCMAESSearchDiagnostics(),
	)).RunContext(context.Background(), cmaesProblem(), opt.RunOptions{
		Observer: func(progress opt.Progress) { updates = append(updates, progress) },
	})
	if err != nil {
		t.Fatalf("RunContext() error = %v", err)
	}

	if len(updates) != 4 {
		t.Fatalf("diagnostic updates = %d, want 4", len(updates))
	}

	for _, update := range updates {
		if update.Diagnostics == nil || update.Diagnostics.Sigma <= 0 ||
			update.Diagnostics.ConditionNumber < 1 {
			t.Fatalf("diagnostic update = %+v, want positive sigma and condition >= 1", update)
		}

		if update.Diagnostics.DistributionExtent <= 0 {
			t.Fatalf("diagnostic update = %+v, want a positive distribution extent", update)
		}
	}
}

// TestCMAESDistributionExtentTracksTheSamplingEllipse pins the property that
// makes the extent worth recording alongside sigma: it is sigma scaled by the
// distribution's largest axis, so it answers "how wide is the search" where
// sigma alone cannot. CMA-ES identifies only sigma^2 * C and the library does
// not renormalize C, so the two numbers are free to move in opposite
// directions.
//
// All three covariance modes are exercised because each represents the ellipse
// differently: full and separable report a dense Eigenvectors matrix, while
// block reports the sparse per-block form instead. D itself is documented to
// reach the observer through Eigenvalues in every mode, and the block subtest
// is what holds the library to that. A block size of two against the
// three-dimensional problem partitions the coordinates into two unequal blocks.
func TestCMAESDistributionExtentTracksTheSamplingEllipse(t *testing.T) {
	t.Parallel()

	modes := []struct {
		mode      string
		blockSize int
	}{
		{mode: "full"},
		{mode: "separable"},
		{mode: "block", blockSize: 2},
	}

	for _, covariance := range modes {
		t.Run(covariance.mode, func(t *testing.T) {
			t.Parallel()

			var updates []opt.Progress

			_, err := cmaesLifecycle(t, opt.NewCMAES(
				12, 16, 4242,
				opt.WithCMAESSearchDiagnostics(),
				opt.WithCMAESCovarianceMode(covariance.mode, covariance.blockSize),
			)).RunContext(context.Background(), cmaesProblem(), opt.RunOptions{
				Observer: func(progress opt.Progress) { updates = append(updates, progress) },
			})
			if err != nil {
				t.Fatalf("RunContext() error = %v", err)
			}

			if len(updates) == 0 {
				t.Fatal("no diagnostic updates observed")
			}

			// An isotropic start has every axis at 1, so the first extent is
			// the initial sigma itself. Anything else means max(D) was read
			// from the wrong representation — the separable and block modes
			// carry their axes differently from full covariance.
			first := updates[0].Diagnostics
			if ratio := first.DistributionExtent / first.Sigma; ratio < 0.5 || ratio > 2 {
				t.Fatalf("first extent/sigma = %g, want the ellipse near isotropic at the start", ratio)
			}

			for _, update := range updates {
				diagnostics := update.Diagnostics
				if diagnostics.DistributionExtent <= 0 {
					t.Fatalf("extent = %g, want positive", diagnostics.DistributionExtent)
				}

				// max(D) is a standard deviation, so the extent is bounded by
				// sigma times the square root of the condition number relative
				// to the smallest axis. The weaker invariant that always holds
				// is that a finite sigma yields a finite extent.
				if math.IsInf(diagnostics.DistributionExtent, 0) || math.IsNaN(diagnostics.DistributionExtent) {
					t.Fatalf("extent = %g, want finite", diagnostics.DistributionExtent)
				}
			}
		})
	}
}

func TestCMAESRestartStrategiesShareOneEvaluationBudget(t *testing.T) {
	t.Parallel()

	for _, strategy := range []string{"ipop", "bipop"} {
		t.Run(strategy, func(t *testing.T) {
			t.Parallel()

			var observed []opt.Progress

			result, err := cmaesLifecycle(t, opt.NewCMAES(
				20, 8, 123,
				opt.WithCMAESRestartStrategy(strategy),
			)).RunContext(context.Background(), opt.Problem{
				Eval:  func([]float64) float64 { return 1 },
				Lower: []float64{0, 0, 0}, Upper: []float64{1, 1, 1}, Dim: 3,
			}, opt.RunOptions{Observer: func(progress opt.Progress) {
				observed = append(observed, progress)
			}})
			if err != nil {
				t.Fatalf("RunContext() error = %v", err)
			}

			if result.Evaluations != 160 {
				t.Errorf("Evaluations = %d, want shared budget 160", result.Evaluations)
			}

			if result.Iterations < 2 || result.Iterations > 20 {
				t.Errorf("Iterations = %d, want several runs within cap 20", result.Iterations)
			}

			for index := 1; index < len(observed); index++ {
				if observed[index].Iterations <= observed[index-1].Iterations ||
					observed[index].Evaluations <= observed[index-1].Evaluations ||
					observed[index].BestCost > observed[index-1].BestCost {
					t.Fatalf("progress regressed at %d: before=%+v after=%+v",
						index, observed[index-1], observed[index])
				}
			}
		})
	}
}

func TestCMAESRejectsInvalidConfigurationOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		optimizer opt.Optimizer
	}{
		{name: "sigma", optimizer: opt.NewCMAES(2, 8, 1, opt.WithCMAESInitialSigma(0))},
		{name: "covariance", optimizer: opt.NewCMAES(2, 8, 1, opt.WithCMAESCovarianceMode("sparse", 0))},
		{name: "restart", optimizer: opt.NewCMAES(2, 8, 1, opt.WithCMAESRestartStrategy("random"))},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := cmaesLifecycle(t, testCase.optimizer).RunContext(
				context.Background(), cmaesProblem(), opt.RunOptions{},
			)
			if err == nil {
				t.Fatal("RunContext() accepted an invalid CMA-ES option")
			}
		})
	}
}
