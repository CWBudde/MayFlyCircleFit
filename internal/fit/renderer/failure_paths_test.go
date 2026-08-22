package renderer

import (
	"context"
	"errors"
	"image"
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

func failureTestReference() *image.NRGBA {
	ref := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := range ref.Pix {
		ref.Pix[i] = 255
	}

	return ref
}

// errOptimizerFailed stands in for whatever an optimizer reports when it cannot
// finish: a device fault, an internal invariant, a failed allocation.
var errOptimizerFailed = errors.New("optimizer exploded")

// failingOptimizer fails on the nth call to RunContext, counting from one, so a
// staged pipeline can be failed part-way rather than only at its first stage.
type failingOptimizer struct {
	failOnCall int
	calls      int
}

func (o *failingOptimizer) RunContext(_ context.Context, problem opt.Problem, _ opt.RunOptions) (opt.Result, error) {
	o.calls++
	if o.calls == o.failOnCall {
		return opt.Result{Iterations: 3, Termination: opt.TerminationCompleted}, errOptimizerFailed
	}

	params := transparentParams(problem.Dim / paramsPerCircle)

	return opt.Result{
		BestParams:  params,
		BestCost:    problem.Eval(params),
		Iterations:  3,
		Evaluations: 1,
		Termination: opt.TerminationCompleted,
	}, nil
}

func (o *failingOptimizer) Run(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	params := transparentParams(dim / paramsPerCircle)
	return params, eval(params)
}

// TestOptimizerFailurePropagates is the optimizer-failure scenario. Every
// pipeline must surface the failure instead of returning a partial result: a
// caller that gets a nil error takes the cost it is handed as real, and a
// checkpoint written from it would record a fit that never happened.
func TestOptimizerFailurePropagates(t *testing.T) {
	ref := failureTestReference()

	tests := []struct {
		name       string
		failOnCall int
		run        func(opt.Optimizer) error
	}{
		{
			name:       "joint",
			failOnCall: 1,
			run: func(optimizer opt.Optimizer) error {
				_, err := OptimizeJoint(NewCPURenderer(ref, 2), optimizer, 2, DisabledConvergenceConfig())
				return err
			},
		},
		{
			name:       "sequential first circle",
			failOnCall: 1,
			run: func(optimizer opt.Optimizer) error {
				_, err := OptimizeSequential(NewCPURenderer(ref, 3), optimizer, 3, DisabledConvergenceConfig(), nil)
				return err
			},
		},
		{
			name:       "sequential later circle",
			failOnCall: 3,
			run: func(optimizer opt.Optimizer) error {
				_, err := OptimizeSequential(NewCPURenderer(ref, 3), optimizer, 3, DisabledConvergenceConfig(), nil)
				return err
			},
		},
		{
			name:       "batch first stage",
			failOnCall: 1,
			run: func(optimizer opt.Optimizer) error {
				_, err := OptimizeBatch(NewCPURenderer(ref, 4), optimizer, 4, 2, DisabledConvergenceConfig())
				return err
			},
		},
		{
			name:       "batch later stage",
			failOnCall: 2,
			run: func(optimizer opt.Optimizer) error {
				_, err := OptimizeBatch(NewCPURenderer(ref, 4), optimizer, 4, 2, DisabledConvergenceConfig())
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			optimizer := &failingOptimizer{failOnCall: test.failOnCall}

			err := test.run(optimizer)
			if err == nil {
				t.Fatal("pipeline returned nil, want the optimizer failure")
			}

			if !errors.Is(err, errOptimizerFailed) {
				t.Fatalf("error = %v, want it to wrap the optimizer failure", err)
			}

			if optimizer.calls < test.failOnCall {
				t.Fatalf("optimizer ran %d times, want at least %d", optimizer.calls, test.failOnCall)
			}
		})
	}
}

// shortResultOptimizer returns a parameter vector of the wrong length, which is
// how a mismatched optimizer adapter fails rather than by returning an error.
type shortResultOptimizer struct{}

func (shortResultOptimizer) RunContext(_ context.Context, problem opt.Problem, _ opt.RunOptions) (opt.Result, error) {
	return opt.Result{
		BestParams:  make([]float64, problem.Dim-1),
		Iterations:  1,
		Termination: opt.TerminationCompleted,
	}, nil
}

func (shortResultOptimizer) Run(eval func([]float64) float64, _, _ []float64, dim int) ([]float64, float64) {
	params := transparentParams(dim / paramsPerCircle)
	return params, eval(params)
}

// TestInvalidOptimizerResultKeepsItsCause guards the error chain, not just the
// classification. Callers switch on ErrInvalidOptimizationInput to decide what
// to report, but anyone debugging needs the specific complaint — which length
// was returned and which was wanted — to stay reachable rather than being
// flattened into the message.
func TestInvalidOptimizerResultKeepsItsCause(t *testing.T) {
	ref := failureTestReference()

	_, err := OptimizeJoint(NewCPURenderer(ref, 2), shortResultOptimizer{}, 2, DisabledConvergenceConfig())
	if err == nil {
		t.Fatal("OptimizeJoint() = nil, want an invalid-result error")
	}

	if !errors.Is(err, ErrInvalidOptimizationInput) {
		t.Fatalf("error = %v, want ErrInvalidOptimizationInput", err)
	}

	if !strings.Contains(err.Error(), "parameter length") {
		t.Fatalf("error = %v, want the underlying length complaint to survive the wrap", err)
	}
	// Wrapping a sentinel and a cause in one Errorf produces a multi-error,
	// which exposes Unwrap() []error rather than Unwrap() error. Asserting on
	// the tree is what distinguishes a real wrap from a %v that only copies the
	// cause's text into the message.
	multi, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("error = %v (%T), want the cause wrapped rather than formatted into the message", err, err)
	}

	causes := multi.Unwrap()
	if len(causes) != 2 {
		t.Fatalf("unwrapped %d causes, want the sentinel and the underlying complaint", len(causes))
	}

	if !errors.Is(causes[0], ErrInvalidOptimizationInput) {
		t.Fatalf("first cause = %v, want ErrInvalidOptimizationInput", causes[0])
	}

	if !strings.Contains(causes[1].Error(), "parameter length") {
		t.Fatalf("second cause = %v, want the length complaint", causes[1])
	}
}
