package renderer

import (
	"bytes"
	"image"
	"math"
	"slices"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/opt"
)

func TestIncrementalCostBoundaryParity(t *testing.T) {
	t.Parallel()

	const width, height = 41, 37
	reference := randomNRGBA(width, height, 10_016)

	opaque := randomNRGBA(width, height, 10_017)
	for offset := 3; offset < len(opaque.Pix); offset += 4 {
		opaque.Pix[offset] = 255
	}

	translucent := randomNRGBA(width, height, 10_018)
	transparent := image.NewNRGBA(image.Rect(0, 0, width, height))

	tests := []struct {
		name    string
		canvas  *image.NRGBA
		circles []fit.Circle
	}{
		{name: "integer_tangencies", canvas: opaque, circles: []fit.Circle{{X: 20, Y: 18, R: 8, CR: 1, Opacity: 1}}},
		{name: "fractional_tangencies", canvas: translucent, circles: []fit.Circle{{X: 20.5, Y: 18.5, R: 8.5, CG: 1, Opacity: 0.375}}},
		{name: "clipped_left", canvas: opaque, circles: []fit.Circle{{X: 0, Y: 18, R: 17, CB: 1, Opacity: 0.75}}},
		{name: "clipped_right", canvas: translucent, circles: []fit.Circle{{X: width, Y: 18, R: 17, CR: 1, Opacity: 0.75}}},
		{name: "clipped_top", canvas: opaque, circles: []fit.Circle{{X: 20, Y: 0, R: 17, CG: 1, Opacity: 0.75}}},
		{name: "clipped_bottom", canvas: translucent, circles: []fit.Circle{{X: 20, Y: height, R: 17, CB: 1, Opacity: 0.75}}},
		{name: "clipped_corner", canvas: opaque, circles: []fit.Circle{{X: 0, Y: 0, R: 29, CR: 1, CG: 1, Opacity: 0.5}}},
		{name: "overlap_and_repeated_writes", canvas: translucent, circles: []fit.Circle{
			{X: 18, Y: 18, R: 15, CR: 1, Opacity: 0.25},
			{X: 23, Y: 18, R: 15, CG: 1, Opacity: 0.5},
			{X: 20, Y: 22, R: 15, CB: 1, Opacity: 0.75},
			{X: 18, Y: 18, R: 15, CR: 1, CG: 1, CB: 1, Opacity: 0.125},
		}},
		{name: "transparent_color", canvas: opaque, circles: []fit.Circle{{X: 20, Y: 18, R: 100, CR: 1, CG: 1, CB: 1}}},
		{name: "transparent_base", canvas: transparent, circles: []fit.Circle{{X: 20, Y: 18, R: 13, CR: 1, CG: 0.25, CB: 0.5, Opacity: 0.5}}},
	}

	for _, test := range tests {
		params := encodeCircles(test.circles)
		for _, threads := range []int{1, 4} {
			t.Run(test.name+"/threads="+fmtInt(threads), func(t *testing.T) {
				t.Parallel()

				assertIncrementalCostParity(t, reference, test.canvas, params, threads)
			})
		}
	}
}

func TestIncrementalCostSIMDSpanBoundaries(t *testing.T) {
	t.Parallel()

	for _, width := range []int{1, 2, 3, 4, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65} {
		t.Run(fmtInt(width), func(t *testing.T) {
			t.Parallel()

			const height = 5
			reference := randomNRGBA(width, height, int64(20_000+width))
			canvas := randomNRGBA(width, height, int64(30_000+width))
			params := encodeCircles([]fit.Circle{{
				X: float64(width) / 2, Y: 2, R: float64(width + height),
				CR: 1, CG: 0.25, CB: 0.75, Opacity: 0.625,
			}})
			assertIncrementalCostParity(t, reference, canvas, params, 1)
		})
	}
}

func TestIncrementalCostPreservesCandidateOrdering(t *testing.T) {
	t.Parallel()

	const width, height = 96, 80
	reference := randomNRGBA(width, height, 40_000)
	canvas := randomNRGBA(width, height, 40_001)

	for _, circles := range []int{1, 5} {
		t.Run(fmtInt(circles)+"_circles", func(t *testing.T) {
			t.Parallel()

			full := NewCPURendererWithCanvas(reference, canvas, circles)
			automatic := NewCPURendererWithCanvas(reference, canvas, circles)

			full.SetThreads(1)
			automatic.SetThreads(1)
			automatic.incrementalCostMode = incrementalCostAuto

			fullCosts := make([]float64, 96)
			automaticCosts := make([]float64, len(fullCosts))

			lower, upper := full.Bounds()
			for candidate := range fullCosts {
				params := incrementalValidationCandidate(lower, upper, candidate)
				fullCosts[candidate] = full.Cost(params)

				automaticCosts[candidate] = automatic.Cost(params)
				if automaticCosts[candidate] != fullCosts[candidate] {
					t.Fatalf("candidate %d: automatic cost %.17g, full cost %.17g", candidate, automaticCosts[candidate], fullCosts[candidate])
				}
			}

			fullOrder := rankedCostIndices(fullCosts)

			automaticOrder := rankedCostIndices(automaticCosts)
			if !slices.Equal(automaticOrder, fullOrder) {
				t.Fatalf("candidate ordering changed:\nautomatic %v\nfull      %v", automaticOrder, fullOrder)
			}
		})
	}
}

func TestIncrementalPipelineOutcomesMatchFullImage(t *testing.T) {
	t.Parallel()

	const totalCircles = 8
	reference := benchmarkPipelineReference(64, 64)
	convergence := ConvergenceConfig{Enabled: true, Patience: 2, Threshold: 1}

	tests := []struct {
		name string
		run  func(Renderer, opt.Optimizer) (*OptimizationResult, error)
	}{
		{name: "joint", run: func(r Renderer, optimizer opt.Optimizer) (*OptimizationResult, error) {
			return OptimizeJoint(r, optimizer, totalCircles, convergence)
		}},
		{name: "sequential", run: func(r Renderer, optimizer opt.Optimizer) (*OptimizationResult, error) {
			return OptimizeSequential(r, optimizer, totalCircles, convergence, nil)
		}},
		{name: "batch", run: func(r Renderer, optimizer opt.Optimizer) (*OptimizationResult, error) {
			return OptimizeBatch(r, optimizer, totalCircles, 2, convergence)
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			full := NewCPURenderer(reference, totalCircles)
			full.SetThreads(4)
			full.stagedIncremental = false
			incremental := NewCPURenderer(reference, totalCircles)
			incremental.SetThreads(4)
			incremental.stagedIncremental = true

			want, err := test.run(full, incrementalValidationOptimizer{evaluations: 12})
			if err != nil {
				t.Fatal(err)
			}

			got, err := test.run(incremental, incrementalValidationOptimizer{evaluations: 12})
			if err != nil {
				t.Fatal(err)
			}

			assertOptimizationResultsEqual(t, got, want)
		})
	}
}

func TestIncrementalMayflyOutcomesMatchFullImage(t *testing.T) {
	t.Parallel()

	const totalCircles = 4
	reference := benchmarkPipelineReference(48, 48)
	tests := []struct {
		name string
		run  func(Renderer, opt.Optimizer) (*OptimizationResult, error)
	}{
		{name: "joint", run: func(r Renderer, optimizer opt.Optimizer) (*OptimizationResult, error) {
			return OptimizeJoint(r, optimizer, totalCircles, DisabledConvergenceConfig())
		}},
		{name: "sequential", run: func(r Renderer, optimizer opt.Optimizer) (*OptimizationResult, error) {
			return OptimizeSequential(r, optimizer, totalCircles, DisabledConvergenceConfig(), nil)
		}},
		{name: "batch", run: func(r Renderer, optimizer opt.Optimizer) (*OptimizationResult, error) {
			return OptimizeBatch(r, optimizer, totalCircles, 2, DisabledConvergenceConfig())
		}},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			full := NewCPURenderer(reference, totalCircles)
			full.SetThreads(1)
			full.stagedIncremental = false
			incremental := NewCPURenderer(reference, totalCircles)
			incremental.SetThreads(1)
			incremental.stagedIncremental = true

			seed := int64(10_160 + index)

			want, err := test.run(full, opt.NewMayfly(2, 10, seed))
			if err != nil {
				t.Fatal(err)
			}

			got, err := test.run(incremental, opt.NewMayfly(2, 10, seed))
			if err != nil {
				t.Fatal(err)
			}

			assertOptimizationResultsEqual(t, got, want)
		})
	}
}

//nolint:paralleltest // testing.AllocsPerRun pins GOMAXPROCS process-wide for the duration of the measurement
func TestIncrementalCostSteadyStateAllocations(t *testing.T) {
	const width, height = 256, 256
	reference := randomNRGBA(width, height, 50_000)
	canvas := randomNRGBA(width, height, 50_001)
	renderer := NewCPURendererWithCanvas(reference, canvas, 1)
	renderer.SetThreads(1)
	renderer.incrementalCostMode = incrementalCostAuto
	params := encodeCircles([]fit.Circle{{X: 128, Y: 128, R: 48, CR: 0.2, CG: 0.6, CB: 0.9, Opacity: 0.5}})
	rendererCostSink = renderer.Cost(params) // Grow reusable span storage.

	if allocations := testing.AllocsPerRun(100, func() {
		rendererCostSink = renderer.Cost(params)
	}); allocations != 0 {
		t.Fatalf("steady-state allocations = %v, want 0", allocations)
	}
}

func assertIncrementalCostParity(t *testing.T, reference, canvas *image.NRGBA, params []float64, threads int) {
	t.Helper()

	circles := len(params) / paramsPerCircle
	full := NewCPURendererWithCanvas(reference, canvas, circles)
	incremental := NewCPURendererWithCanvas(reference, canvas, circles)

	full.SetThreads(threads)
	incremental.SetThreads(threads)

	incremental.incrementalCostMode = incrementalCostForce
	if got, want := incremental.Cost(params), full.Cost(params); got != want {
		t.Fatalf("incremental cost = %.17g, full cost = %.17g", got, want)
	}
}

func rankedCostIndices(costs []float64) []int {
	indices := make([]int, len(costs))
	for i := range indices {
		indices[i] = i
	}

	slices.SortStableFunc(indices, func(a, b int) int {
		switch {
		case costs[a] < costs[b]:
			return -1
		case costs[a] > costs[b]:
			return 1
		default:
			return 0
		}
	})

	return indices
}

type incrementalValidationOptimizer struct {
	evaluations int
}

func (o incrementalValidationOptimizer) Run(eval func([]float64) float64, lower, upper []float64, _ int) ([]float64, float64) {
	bestCost := math.Inf(1)
	var best []float64

	for candidate := range o.evaluations {
		params := incrementalValidationCandidate(lower, upper, candidate)

		cost := eval(params)
		if cost < bestCost {
			bestCost = cost

			best = append(best[:0], params...)
		}
	}

	return best, bestCost
}

func incrementalValidationCandidate(lower, upper []float64, candidate int) []float64 {
	params := make([]float64, len(lower))
	radiusFractions := [...]float64{0.04, 0.08, 0.12, 0.18, 0.26, 0.34}

	for offset := 0; offset < len(params); offset += paramsPerCircle {
		circle := offset / paramsPerCircle
		xFraction := float64(1+(candidate*7+circle*3)%17) / 18
		yFraction := float64(1+(candidate*11+circle*5)%19) / 20
		params[offset+0] = lower[offset+0] + (upper[offset+0]-lower[offset+0])*xFraction
		params[offset+1] = lower[offset+1] + (upper[offset+1]-lower[offset+1])*yFraction
		radius := upper[offset+2] * radiusFractions[(candidate+circle)%len(radiusFractions)]
		params[offset+2] = max(lower[offset+2], min(radius, upper[offset+2]))
		params[offset+3] = float64((candidate+circle*3)%11) / 10
		params[offset+4] = float64((candidate*3+circle*5)%11) / 10
		params[offset+5] = float64((candidate*7+circle*2)%11) / 10
		params[offset+6] = float64((candidate+circle)%5) / 4
	}

	return params
}

func assertOptimizationResultsEqual(t *testing.T, got, want *OptimizationResult) {
	t.Helper()

	if got.BestCost != want.BestCost || got.InitialCost != want.InitialCost ||
		got.Iterations != want.Iterations || got.Evaluations != want.Evaluations ||
		got.Stages != want.Stages || got.OptimizedCircles != want.OptimizedCircles ||
		got.Termination != want.Termination || got.StagesStoppedEarly != want.StagesStoppedEarly {
		t.Fatalf("optimization metadata changed:\nincremental %+v\nfull        %+v", got, want)
	}

	if !slices.Equal(got.BestParams, want.BestParams) {
		t.Fatal("best candidate parameters changed")
	}

	if !bytes.Equal(got.BestImage.Pix, want.BestImage.Pix) {
		t.Fatal("best rendered image changed")
	}
}

func fmtInt(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte

	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}

	return string(digits[i:])
}
