package renderer

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/opt"
)

type fixedPolishOptimizer struct {
	params  []float64
	calls   int
	options []opt.RunOptions
}

type incumbentPolishOptimizer struct {
	options []opt.RunOptions
}

func (o *incumbentPolishOptimizer) Run(func([]float64) float64, []float64, []float64, int) ([]float64, float64) {
	return nil, 0
}

func (o *incumbentPolishOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	o.options = append(o.options, options)
	params := append([]float64(nil), options.Initial.Params...)
	return opt.Result{BestParams: params, BestCost: problem.Eval(params), Iterations: 1, Evaluations: 1}, nil
}

func (o *fixedPolishOptimizer) Run(eval func([]float64) float64, _, _ []float64, _ int) ([]float64, float64) {
	params := append([]float64(nil), o.params...)
	return params, eval(params)
}

func (o *fixedPolishOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	o.calls++
	o.options = append(o.options, options)
	params := append([]float64(nil), o.params...)
	cost := problem.Eval(params)
	if options.Observer != nil {
		options.Observer(opt.Progress{Iterations: 3, Evaluations: 7, BestParams: params, BestCost: cost})
	}
	return opt.Result{
		BestParams:  params,
		BestCost:    cost,
		Iterations:  3,
		Evaluations: 7,
		Termination: opt.TerminationCompleted,
	}, nil
}

func TestPolishCircleBatchCommitsStrictImprovement(t *testing.T) {
	black := color.NRGBA{A: 255}
	ref := solidImage(5, 5, black)
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 128, G: 128, B: 128, A: 255}, 1)
	replacement := circleParams(2, 2, 5, black, 1)
	optimizer := &fixedPolishOptimizer{params: replacement}

	var observed opt.Progress
	var boundary BatchPolishProgress
	result, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     2,
		Observer:      func(progress opt.Progress) { observed = progress },
		OnSweep:       func(progress BatchPolishProgress) error { boundary = progress; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AcceptedSweeps != 1 || result.Sweeps != 2 || optimizer.calls != 2 {
		t.Fatalf("accepted/sweeps/calls = %d/%d/%d, want 1/2/2", result.AcceptedSweeps, result.Sweeps, optimizer.calls)
	}
	if result.BestCost != 0 || !reflect.DeepEqual(result.BestParams, replacement) {
		t.Fatalf("best result = cost %v params %v, want zero-cost replacement %v", result.BestCost, result.BestParams, replacement)
	}
	if observed.Iterations != 6 || len(observed.BestParams) != len(initial) {
		t.Fatalf("observer progress = %+v, want cumulative iterations and complete params", observed)
	}
	if boundary.Accepted || boundary.Sweep != 2 || !reflect.DeepEqual(boundary.BestParams, replacement) {
		t.Fatalf("rejected boundary did not preserve committed solution: %+v", boundary)
	}
	if got := result.BestImage.NRGBAAt(2, 2); got != black {
		t.Fatalf("best image center = %#v, want %#v", got, black)
	}
}

func TestPolishCircleBatchRollsBackRejectedSweepExactly(t *testing.T) {
	black := color.NRGBA{A: 255}
	ref := solidImage(5, 5, black)
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 96, G: 96, B: 96, A: 255}, 1)
	worse := circleParams(2, 2, 5, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, 1)
	initialCost := base.Cost(initial)

	result, err := PolishCircleBatchContext(context.Background(), base, &fixedPolishOptimizer{params: worse}, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Sweeps != 3 || result.AcceptedSweeps != 0 {
		t.Fatalf("sweeps/accepted = %d/%d, want 3/0", result.Sweeps, result.AcceptedSweeps)
	}
	if result.BestCost != initialCost || !reflect.DeepEqual(result.BestParams, initial) {
		t.Fatalf("rejected sweep changed result: cost %v params %v", result.BestCost, result.BestParams)
	}
	if got, want := result.BestImage.NRGBAAt(2, 2), base.Render(initial).NRGBAAt(2, 2); got != want {
		t.Fatalf("rollback image center = %#v, want %#v", got, want)
	}
}

func TestPolishCircleBatchRejectedSweepsRotateAcrossCircleGroups(t *testing.T) {
	ref := solidImage(12, 4, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	base := NewCPURenderer(ref, 3)
	initial := append(circleParams(1, 1, 1, color.NRGBA{R: 32, A: 255}, 0.2), circleParams(5, 1, 1, color.NRGBA{R: 64, A: 255}, 0.3)...)
	initial = append(initial, circleParams(9, 1, 1, color.NRGBA{R: 96, A: 255}, 0.4)...)
	optimizer := &incumbentPolishOptimizer{}

	result, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     3,
		Strategy:      BatchPolishHybridOverlap,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Sweeps != 3 || result.AcceptedSweeps != 0 || len(optimizer.options) != 3 {
		t.Fatalf("rotating rejected sweeps = %d/%d options=%d, want 3/0/3", result.Sweeps, result.AcceptedSweeps, len(optimizer.options))
	}
	seenX := make(map[float64]bool)
	for _, options := range optimizer.options {
		seenX[options.Initial.Params[0]] = true
	}
	if len(seenX) != 3 {
		t.Fatalf("rejected active groups used x coordinates %v, want all three circles", seenX)
	}
}

func TestPolishCircleBatchMapsEpochBoundariesToCompleteVector(t *testing.T) {
	black := color.NRGBA{A: 255}
	ref := solidImage(5, 5, black)
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 128, G: 128, B: 128, A: 255}, 1)
	replacement := circleParams(2, 2, 5, black, 1)
	optimizer := opt.WithEpochs(&fixedPolishOptimizer{params: replacement}, 2)
	var boundaries []BatchPolishEpoch

	_, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
		OnEpoch: func(boundary BatchPolishEpoch) error {
			boundaries = append(boundaries, boundary)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(boundaries) != 2 {
		t.Fatalf("epoch boundaries = %d, want 2", len(boundaries))
	}
	for epoch, boundary := range boundaries {
		if boundary.Epoch != epoch+1 || len(boundary.BestParams) != len(initial) {
			t.Fatalf("boundary %d = %+v, want complete parameter vector", epoch+1, boundary)
		}
	}
	if boundaries[1].Iterations != 6 || boundaries[1].Evaluations <= boundaries[0].Evaluations {
		t.Fatalf("epoch work is not cumulative: %+v", boundaries)
	}
}

func TestMergeActiveCircleParamsPreservesOriginalDrawSlots(t *testing.T) {
	full := make([]float64, 4*paramsPerCircle)
	for circle := range 4 {
		for parameter := range paramsPerCircle {
			full[circle*paramsPerCircle+parameter] = float64(circle*10 + parameter)
		}
	}
	before := append([]float64(nil), full...)
	active := append(circleParams(1, 1, 1, color.NRGBA{R: 10, A: 255}, 0.5), circleParams(2, 2, 2, color.NRGBA{B: 20, A: 255}, 0.75)...)

	mergeActiveCircleParams(full, []int{1, 3}, active)

	if !reflect.DeepEqual(full[:paramsPerCircle], before[:paramsPerCircle]) ||
		!reflect.DeepEqual(full[2*paramsPerCircle:3*paramsPerCircle], before[2*paramsPerCircle:3*paramsPerCircle]) {
		t.Fatal("inactive circle draw slots changed")
	}
	if !reflect.DeepEqual(full[paramsPerCircle:2*paramsPerCircle], active[:paramsPerCircle]) ||
		!reflect.DeepEqual(full[3*paramsPerCircle:], active[paramsPerCircle:]) {
		t.Fatal("active circles were not restored to their original draw slots")
	}
}

func TestSelectHybridOverlapCirclesCombinesWeakAnchorsAndPartners(t *testing.T) {
	params := append(circleParams(2, 2, 2, color.NRGBA{A: 255}, 1), circleParams(17, 17, 2, color.NRGBA{A: 255}, 1)...)
	params = append(params, circleParams(2, 2, 4, color.NRGBA{A: 255}, 1)...)
	params = append(params, circleParams(17, 17, 4, color.NRGBA{A: 255}, 1)...)
	params = append(params, circleParams(10, 10, 1, color.NRGBA{A: 255}, 1)...)
	audit := BatchAudit{Circles: []CircleAudit{
		{Circle: 1, OriginalCircle: 1, MSEContribution: 1, FinalChangedPixels: 10},
		{Circle: 2, OriginalCircle: 2, MSEContribution: 2, FinalChangedPixels: 10},
		{Circle: 3, OriginalCircle: 3, MSEContribution: 20, FinalChangedPixels: 10},
		{Circle: 4, OriginalCircle: 4, MSEContribution: 30, FinalChangedPixels: 10},
		{Circle: 5, OriginalCircle: 5, MSEContribution: 3, FinalChangedPixels: 10},
	}}

	got := selectHybridOverlapCircles(params, audit, 4, 20, 20, nil)
	want := []int{0, 1, 2, 3}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hybrid active set = %v, want weak anchors plus overlap partners %v", got, want)
	}
	retained := removeActiveCircleParams(params, got)
	if !reflect.DeepEqual(retained, params[4*paramsPerCircle:]) {
		t.Fatal("hybrid removal did not preserve the retained draw order")
	}
}

func TestSelectHybridOverlapCirclesRotatesAwayFromVisitedGroup(t *testing.T) {
	params := make([]float64, 6*paramsPerCircle)
	audit := BatchAudit{Circles: make([]CircleAudit, 6)}
	for circle := range 6 {
		copy(params[circle*paramsPerCircle:], circleParams(float64(circle*3), 1, 1, color.NRGBA{A: 255}, 1))
		audit.Circles[circle] = CircleAudit{
			Circle: circle + 1, OriginalCircle: circle + 1,
			MSEContribution: float64(circle + 1), FinalChangedPixels: 1,
		}
	}

	got := selectHybridOverlapCircles(params, audit, 3, 20, 5, map[int]int{0: 1, 1: 1, 2: 1})
	for _, circle := range got {
		if circle < 3 {
			t.Fatalf("rotated active set = %v, want only previously unvisited circles", got)
		}
	}
}

func TestPolishCircleBatchHybridSeedsIncumbentAndResidualAlternative(t *testing.T) {
	ref := solidImage(7, 7, color.NRGBA{A: 255})
	base := NewCPURenderer(ref, 1)
	initial := circleParams(3, 3, 3, color.NRGBA{R: 96, G: 96, B: 96, A: 255}, 1)
	optimizer := &fixedPolishOptimizer{params: initial}

	result, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
		Strategy:      BatchPolishHybridOverlap,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AcceptedSweeps != 0 || len(optimizer.options) != 1 {
		t.Fatalf("hybrid result/options = %+v/%d", result, len(optimizer.options))
	}
	options := optimizer.options[0]
	if options.Initial == nil || !reflect.DeepEqual(options.Initial.Params, initial) {
		t.Fatalf("hybrid incumbent seed = %+v, want current active parameters", options.Initial)
	}
	if len(options.AdditionalSeeds) != 1 || reflect.DeepEqual(options.AdditionalSeeds[0].Params, initial) {
		t.Fatalf("hybrid residual alternatives = %+v, want distinct seed", options.AdditionalSeeds)
	}
	if options.Continuation == nil || options.Continuation.LocalFraction != 1 ||
		options.Continuation.Sigma != 0.02 || options.Continuation.CoordinateRate != 0.2 ||
		options.Continuation.MaxVelocity != 0.02 {
		t.Fatalf("hybrid continuation profile = %+v, want local polishing profile", options.Continuation)
	}
}

func TestHighestResidualRegionSkipsVisitedTiles(t *testing.T) {
	reference := solidImage(8, 8, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	canvas := cloneNRGBA(reference)
	canvas.SetNRGBA(1, 1, color.NRGBA{A: 255})
	canvas.SetNRGBA(6, 6, color.NRGBA{R: 128, G: 128, B: 128, A: 255})

	first, firstIndex, err := highestResidualRegion(canvas, reference, nil)
	if err != nil {
		t.Fatal(err)
	}
	if firstIndex != 0 || first != image.Rect(0, 0, 2, 2) {
		t.Fatalf("first residual region = %v index %d, want top-left tile", first, firstIndex)
	}
	second, secondIndex, err := highestResidualRegion(canvas, reference, map[int]bool{firstIndex: true})
	if err != nil {
		t.Fatal(err)
	}
	if secondIndex != 15 || second != image.Rect(6, 6, 8, 8) {
		t.Fatalf("second residual region = %v index %d, want bottom-right tile", second, secondIndex)
	}
}

func TestSelectResidualRegionActiveSetCombinesWeakSlotAndInfluencer(t *testing.T) {
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	black := color.NRGBA{A: 255}
	canvasRef := solidImage(8, 8, white)
	canvasRenderer := NewCPURenderer(canvasRef, 3)
	params := append(circleParams(7, 7, 1, black, 0.1), circleParams(1, 1, 3, black, 0.5)...)
	params = append(params, circleParams(6, 6, 3, black, 0.8)...)
	reference := cloneNRGBA(canvasRenderer.Render(params))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			reference.SetNRGBA(x, y, black)
		}
	}
	base := NewCPURenderer(reference, 3)

	selection, err := selectResidualRegionActiveSet(base, params, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selection.RegionIndex != 0 || !reflect.DeepEqual(selection.ReplacementCircles, []int{0}) {
		t.Fatalf("regional replacement selection = %+v, want weak circle 1 in top-left region", selection)
	}
	if !reflect.DeepEqual(selection.Circles, []int{0, 1}) {
		t.Fatalf("regional active circles = %v, want weak slot and top-left influencer [0 1]", selection.Circles)
	}
}

func TestPolishCircleBatchResidualRegionContinuesAfterRejectedTile(t *testing.T) {
	black := color.NRGBA{A: 255}
	ref := solidImage(5, 5, black)
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 96, G: 96, B: 96, A: 255}, 1)
	optimizer := &fixedPolishOptimizer{params: initial}

	result, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     2,
		Strategy:      BatchPolishResidualRegion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Sweeps != 2 || result.AcceptedSweeps != 0 || optimizer.calls != 2 {
		t.Fatalf("regional sweeps/accepted/calls = %d/%d/%d, want 2/0/2", result.Sweeps, result.AcceptedSweeps, optimizer.calls)
	}
}

func TestPolishCircleBatchValidatesOptions(t *testing.T) {
	ref := solidImage(3, 3, color.NRGBA{A: 255})
	base := NewCPURenderer(ref, 1)
	params := circleParams(1, 1, 1, color.NRGBA{A: 255}, 1)
	tests := []BatchPolishOptions{
		{ActiveSetSize: 0, MaxSweeps: 1},
		{ActiveSetSize: 2, MaxSweeps: 1},
		{ActiveSetSize: 1, MaxSweeps: -1},
		{ActiveSetSize: 1, MaxSweeps: 1, Strategy: "unsupported"},
	}
	for _, options := range tests {
		if _, err := PolishCircleBatchContext(context.Background(), base, &fixedPolishOptimizer{params: params}, params, options); err == nil {
			t.Fatalf("PolishCircleBatchContext options %+v returned nil error", options)
		}
	}
}

// stagedOnlyRenderer forwards staged session creation and in-place compositing
// but hides canvas-seeded sessions, so polishing evaluates the complete parameter
// vector for every candidate the way it did before the fixed prefix was baked.
// Everything else keeps working, which isolates baking in comparisons.
type stagedOnlyRenderer struct {
	Renderer
	rendererSessionFactory
	compositor inPlaceCompositor
}

func newStagedOnlyRenderer(cpu *CPURenderer) stagedOnlyRenderer {
	return stagedOnlyRenderer{Renderer: cpu, rendererSessionFactory: cpu, compositor: cpu}
}

func (s stagedOnlyRenderer) compositeParams(img *image.NRGBA, params []float64, count int) {
	s.compositor.compositeParams(img, params, count)
}

func (s stagedOnlyRenderer) initialCanvas() *image.NRGBA {
	return s.compositor.initialCanvas()
}

// recordingPolishOptimizer evaluates a deterministic spread of candidates around
// the incumbent and keeps every cost it saw, so two runs can be compared
// evaluation by evaluation.
type recordingPolishOptimizer struct {
	steps int
	costs []float64
}

func (o *recordingPolishOptimizer) Run(func([]float64) float64, []float64, []float64, int) ([]float64, float64) {
	return nil, 0
}

func (o *recordingPolishOptimizer) RunContext(_ context.Context, problem opt.Problem, options opt.RunOptions) (opt.Result, error) {
	steps := o.steps
	if steps == 0 {
		steps = 4
	}
	best := append([]float64(nil), options.Initial.Params...)
	bestCost := problem.Eval(best)
	o.costs = append(o.costs, bestCost)
	for step := 1; step <= steps; step++ {
		candidate := append([]float64(nil), options.Initial.Params...)
		for i := range candidate {
			candidate[i] += float64(step) * 0.75 * float64(i%3-1)
		}
		cost := problem.Eval(candidate)
		o.costs = append(o.costs, cost)
		if cost < bestCost {
			best, bestCost = candidate, cost
		}
	}
	return opt.Result{
		BestParams:  best,
		BestCost:    bestCost,
		Iterations:  1,
		Evaluations: len(o.costs),
		Termination: opt.TerminationCompleted,
	}, nil
}

func TestBakedSuffixSessionMatchesFullVector(t *testing.T) {
	const width, height = 20, 16
	ref := solidImage(width, height, color.NRGBA{R: 200, G: 40, B: 90, A: 255})
	canvas := solidImage(width, height, color.NRGBA{R: 20, G: 220, B: 40, A: 255})
	params := polishParityParams()
	circleCount := len(params) / paramsPerCircle

	for _, custom := range []bool{false, true} {
		for prefixCircles := 1; prefixCircles < circleCount; prefixCircles++ {
			var base *CPURenderer
			if custom {
				base = NewCPURendererWithCanvas(ref, canvas, circleCount)
			} else {
				base = NewCPURenderer(ref, circleCount)
			}
			wantImage := cloneNRGBA(base.Render(params))
			wantCost := base.Cost(params)

			suffix, cleanup, ok := bakedSuffixSession(base, params, prefixCircles, circleCount)
			if !ok {
				t.Fatalf("bakedSuffixSession(prefix %d) not applied", prefixCircles)
			}
			gotCost := suffix.Cost(params[prefixCircles*paramsPerCircle:])
			gotImage := suffix.Render(params[prefixCircles*paramsPerCircle:])
			if gotCost != wantCost {
				t.Errorf("custom canvas %v prefix %d: cost = %v, want %v", custom, prefixCircles, gotCost, wantCost)
			}
			if !bytes.Equal(gotImage.Pix, wantImage.Pix) {
				t.Errorf("custom canvas %v prefix %d: baked image differs from full render", custom, prefixCircles)
			}
			cleanup()
		}
	}
}

func TestBakedSuffixSessionSkipsUnsupportedInput(t *testing.T) {
	ref := solidImage(6, 6, color.NRGBA{A: 255})
	params := polishParityParams()
	circleCount := len(params) / paramsPerCircle
	base := NewCPURenderer(ref, circleCount)

	if _, _, ok := bakedSuffixSession(base, params, 0, circleCount); ok {
		t.Error("bakedSuffixSession(prefix 0) applied, want fallback")
	}
	if _, _, ok := bakedSuffixSession(base, params, circleCount, circleCount); ok {
		t.Error("bakedSuffixSession(full prefix) applied, want fallback")
	}
	hidden := newStagedOnlyRenderer(base)
	if _, _, ok := bakedSuffixSession(hidden, params, 1, circleCount); ok {
		t.Error("bakedSuffixSession(unsupported backend) applied, want fallback")
	}
}

func TestPolishCircleBatchBakedPrefixMatchesFullVector(t *testing.T) {
	const width, height = 20, 16
	ref := solidImage(width, height, color.NRGBA{R: 200, G: 40, B: 90, A: 255})
	params := polishParityParams()
	circleCount := len(params) / paramsPerCircle

	run := func(bake bool) (*BatchPolishResult, []float64, [][]int) {
		t.Helper()
		cpu := NewCPURenderer(ref, circleCount)
		cpu.SetThreads(1)
		var base Renderer = cpu
		if !bake {
			base = newStagedOnlyRenderer(cpu)
		}
		optimizer := &recordingPolishOptimizer{}
		var activeSets [][]int
		result, err := PolishCircleBatchContext(context.Background(), base, optimizer, params, BatchPolishOptions{
			ActiveSetSize: 2,
			MaxSweeps:     3,
			Strategy:      BatchPolishHybridOverlap,
			OnSweep: func(progress BatchPolishProgress) error {
				activeSets = append(activeSets, progress.ActiveSet)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("PolishCircleBatchContext(bake %v) error = %v", bake, err)
		}
		return result, optimizer.costs, activeSets
	}

	baked, bakedCosts, bakedSets := run(true)
	full, fullCosts, fullSets := run(false)

	if !reflect.DeepEqual(bakedSets, fullSets) {
		t.Fatalf("active sets = %v, want %v", bakedSets, fullSets)
	}
	// Active sets are one-based, so a minimum above one means circles ahead of the
	// first active slot were baked instead of replayed.
	if !slices.ContainsFunc(bakedSets, func(set []int) bool { return slices.Min(set) > 1 }) {
		t.Fatalf("active sets %v never left a bakeable prefix", bakedSets)
	}
	// A sweep that touches the first circle must still exercise the fallback.
	if !slices.ContainsFunc(bakedSets, func(set []int) bool { return slices.Min(set) == 1 }) {
		t.Fatalf("active sets %v never exercised the unbaked path", bakedSets)
	}
	if !reflect.DeepEqual(bakedCosts, fullCosts) {
		t.Fatalf("evaluated costs = %v, want %v", bakedCosts, fullCosts)
	}
	if baked.BestCost != full.BestCost || !reflect.DeepEqual(baked.BestParams, full.BestParams) {
		t.Fatalf("baked result = cost %v params %v, want cost %v params %v",
			baked.BestCost, baked.BestParams, full.BestCost, full.BestParams)
	}
	if !bytes.Equal(baked.BestImage.Pix, full.BestImage.Pix) {
		t.Fatal("baked polishing image differs from full-vector polishing image")
	}
}

func TestSelectContiguousWindowCirclesPrefersLatestUnvisitedWindow(t *testing.T) {
	visits := make(map[int]int)
	active := selectContiguousWindowCircles(10, 3, visits)
	if !reflect.DeepEqual(active, []int{7, 8, 9}) {
		t.Fatalf("first window = %v, want the last three draw slots", active)
	}
	// Baking depends on the window being consecutive and starting as late as
	// possible: a start of 7 leaves seven circles bakeable instead of none.
	for _, circle := range active {
		visits[circle]++
	}
	if next := selectContiguousWindowCircles(10, 3, visits); !reflect.DeepEqual(next, []int{4, 5, 6}) {
		t.Fatalf("second window = %v, want the next unvisited run below it", next)
	}
}

func TestSelectContiguousWindowCirclesCoversEveryCircle(t *testing.T) {
	const circleCount, activeSetSize = 10, 3
	visits := make(map[int]int)
	seen := make(map[int]bool, circleCount)
	// ceil(10/3) sweeps is the point at which every draw slot must have been
	// offered to the optimizer at least once.
	for sweep := 0; sweep < (circleCount+activeSetSize-1)/activeSetSize; sweep++ {
		active := selectContiguousWindowCircles(circleCount, activeSetSize, visits)
		for i, circle := range active {
			if i > 0 && circle != active[i-1]+1 {
				t.Fatalf("sweep %d selected non-contiguous window %v", sweep, active)
			}
			seen[circle] = true
			visits[circle]++
		}
	}
	for circle := range circleCount {
		if !seen[circle] {
			t.Fatalf("circle %d was never polished, seen = %v", circle, seen)
		}
	}
}

func TestSelectContiguousWindowCirclesClampsOversizedActiveSet(t *testing.T) {
	active := selectContiguousWindowCircles(3, 5, make(map[int]int))
	if !reflect.DeepEqual(active, []int{0, 1, 2}) {
		t.Fatalf("oversized active set = %v, want every circle", active)
	}
}

func TestPolishCircleBatchContiguousWindowBakesPrefixAndMatchesFullVector(t *testing.T) {
	const width, height = 20, 16
	ref := solidImage(width, height, color.NRGBA{R: 200, G: 40, B: 90, A: 255})
	params := polishParityParams()
	circleCount := len(params) / paramsPerCircle

	run := func(bake bool) (*BatchPolishResult, []float64, [][]int) {
		t.Helper()
		cpu := NewCPURenderer(ref, circleCount)
		cpu.SetThreads(1)
		var base Renderer = cpu
		if !bake {
			base = newStagedOnlyRenderer(cpu)
		}
		optimizer := &recordingPolishOptimizer{}
		var activeSets [][]int
		result, err := PolishCircleBatchContext(context.Background(), base, optimizer, params, BatchPolishOptions{
			ActiveSetSize: 2,
			MaxSweeps:     3,
			Strategy:      BatchPolishContiguousWindow,
			OnSweep: func(progress BatchPolishProgress) error {
				activeSets = append(activeSets, progress.ActiveSet)
				return nil
			},
		})
		if err != nil {
			t.Fatalf("PolishCircleBatchContext(bake %v) error = %v", bake, err)
		}
		return result, optimizer.costs, activeSets
	}

	baked, bakedCosts, bakedSets := run(true)
	full, fullCosts, fullSets := run(false)

	if !reflect.DeepEqual(bakedSets, fullSets) {
		t.Fatalf("active sets = %v, want %v", bakedSets, fullSets)
	}
	// Active sets are one-based. The first sweep must sit at the end of the draw
	// order, which is the whole point of the strategy.
	if !reflect.DeepEqual(bakedSets[0], []int{circleCount - 1, circleCount}) {
		t.Fatalf("first active set = %v, want the last two draw slots", bakedSets[0])
	}
	for _, set := range bakedSets {
		for i := 1; i < len(set); i++ {
			if set[i] != set[i-1]+1 {
				t.Fatalf("active set %v is not contiguous", set)
			}
		}
	}
	// Sliding the window eventually reaches circle one, which has no bakeable
	// prefix, so both the baked and the fallback path are exercised here.
	if !slices.ContainsFunc(bakedSets, func(set []int) bool { return slices.Min(set) > 1 }) {
		t.Fatalf("active sets %v never left a bakeable prefix", bakedSets)
	}
	if !slices.ContainsFunc(bakedSets, func(set []int) bool { return slices.Min(set) == 1 }) {
		t.Fatalf("active sets %v never exercised the unbaked path", bakedSets)
	}
	// Baking must be a pure speed optimization: every evaluated cost, the final
	// parameters, and the rendered image have to match the unbaked replay.
	if !reflect.DeepEqual(bakedCosts, fullCosts) {
		t.Fatalf("evaluated costs = %v, want %v", bakedCosts, fullCosts)
	}
	if baked.BestCost != full.BestCost || !reflect.DeepEqual(baked.BestParams, full.BestParams) {
		t.Fatalf("baked result = cost %v params %v, want cost %v params %v",
			baked.BestCost, baked.BestParams, full.BestCost, full.BestParams)
	}
	if !bytes.Equal(baked.BestImage.Pix, full.BestImage.Pix) {
		t.Fatal("baked and unbaked polishing produced different images")
	}
}

// polishParityParams places a large, strong circle first so weak-circle selection
// leaves a non-empty fixed prefix to bake.
func polishParityParams() []float64 {
	params := circleParams(10, 8, 9, color.NRGBA{R: 190, G: 45, B: 85, A: 255}, 1)
	params = append(params, circleParams(4, 4, 4, color.NRGBA{R: 210, G: 60, B: 100, A: 255}, 0.9)...)
	params = append(params, circleParams(15, 5, 3, color.NRGBA{R: 120, G: 30, B: 60, A: 255}, 0.6)...)
	params = append(params, circleParams(6, 12, 3, color.NRGBA{R: 80, G: 200, B: 120, A: 255}, 0.4)...)
	params = append(params, circleParams(16, 12, 2, color.NRGBA{R: 30, G: 90, B: 220, A: 255}, 0.3)...)
	return params
}

// BenchmarkPolishCircleBatchStrategy contrasts a scattered active set against a
// contiguous one on an otherwise identical sweep. Both bake, so the only
// difference is how many circles the selected active set leaves bakeable.
func BenchmarkPolishCircleBatchStrategy(b *testing.B) {
	const width, height = 128, 128
	for _, circleCount := range []int{64, 256} {
		ref := solidImage(width, height, color.NRGBA{R: 60, G: 120, B: 180, A: 255})
		params := benchmarkPolishParams(circleCount, width, height)
		for _, strategy := range []BatchPolishStrategy{BatchPolishHybridOverlap, BatchPolishContiguousWindow} {
			b.Run(string(strategy)+"-"+strconv.Itoa(circleCount), func(b *testing.B) {
				cpu := NewCPURenderer(ref, circleCount)
				cpu.SetThreads(1)
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_, err := PolishCircleBatchContext(context.Background(), cpu, &recordingPolishOptimizer{steps: 200}, params, BatchPolishOptions{
						ActiveSetSize: 3,
						MaxSweeps:     1,
						Strategy:      strategy,
					})
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func benchmarkPolishParams(circleCount, width, height int) []float64 {
	params := make([]float64, 0, circleCount*paramsPerCircle)
	for i := 0; i < circleCount; i++ {
		params = append(params, circleParams(
			float64((i*37)%width)+0.5,
			float64((i*53)%height)+0.5,
			4+float64(i%7),
			color.NRGBA{R: uint8(i * 3), G: uint8(i * 5), B: uint8(i * 7), A: 255},
			0.4+float64(i%4)/10,
		)...)
	}
	return params
}

func BenchmarkPolishCircleBatch(b *testing.B) {
	const width, height = 128, 128
	for _, circleCount := range []int{64, 256} {
		ref := solidImage(width, height, color.NRGBA{R: 60, G: 120, B: 180, A: 255})
		params := make([]float64, 0, circleCount*paramsPerCircle)
		for i := 0; i < circleCount; i++ {
			params = append(params, circleParams(
				float64((i*37)%width)+0.5,
				float64((i*53)%height)+0.5,
				4+float64(i%7),
				color.NRGBA{R: uint8(i * 3), G: uint8(i * 5), B: uint8(i * 7), A: 255},
				0.4+float64(i%4)/10,
			)...)
		}

		newBase := func(bake bool) Renderer {
			cpu := NewCPURenderer(ref, circleCount)
			cpu.SetThreads(1)
			if bake {
				return cpu
			}
			return newStagedOnlyRenderer(cpu)
		}
		for _, bake := range []bool{true, false} {
			name := "baked-"
			if !bake {
				name = "full-vector-"
			}
			b.Run(name+strconv.Itoa(circleCount), func(b *testing.B) {
				base := newBase(bake)
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_, err := PolishCircleBatchContext(context.Background(), base, &recordingPolishOptimizer{steps: 200}, params, BatchPolishOptions{
						ActiveSetSize: 3,
						MaxSweeps:     1,
						Strategy:      BatchPolishHybridOverlap,
					})
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func circleParams(x, y, radius float64, c color.NRGBA, opacity float64) []float64 {
	return []float64{
		x,
		y,
		radius,
		float64(c.R) / 255,
		float64(c.G) / 255,
		float64(c.B) / 255,
		opacity,
	}
}

// parallelPolishOptimizer reports a concurrent evaluation width the way a
// MayflyAdapter configured with opt.WithParallelEvaluation does.
type parallelPolishOptimizer struct {
	fixedPolishOptimizer
	workers int
}

func (o *parallelPolishOptimizer) ParallelEvaluationWorkers() int { return o.workers }

// TestPolishCircleBatchRejectsParallelOptimizer guards the one optimizer-driven
// objective in the repository that is not re-entrant. Polishing's sweep
// evaluator merges every candidate into one shared parameter vector and
// evaluates it on one shared session, which is what makes a sweep
// transactional; the staged pipelines avoid that by leasing a session per
// evaluation, and polishing has no such pool.
//
// Until now nothing enforced the separation: safety rested entirely on the two
// polisher construction sites happening not to pass the parallel option, and
// wiring it in would have raced the shared vector, the session canvas, and the
// evaluation counter. Every one of those failures is silent -- a plausible
// wrong cost and a corrupt image, with no error -- so the run must be refused.
func TestPolishCircleBatchRejectsParallelOptimizer(t *testing.T) {
	ref := solidImage(5, 5, color.NRGBA{A: 255})
	base := NewCPURenderer(ref, 1)
	initial := circleParams(2, 2, 5, color.NRGBA{R: 128, G: 128, B: 128, A: 255}, 1)
	optimizer := &parallelPolishOptimizer{workers: 4}

	_, err := PolishCircleBatchContext(context.Background(), base, optimizer, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
	})
	if err == nil {
		t.Fatal("polishing accepted an optimizer configured for concurrent evaluation")
	}
	if !errors.Is(err, ErrInvalidOptimizationInput) {
		t.Fatalf("error = %v, want ErrInvalidOptimizationInput", err)
	}
	if optimizer.calls != 0 {
		t.Fatalf("optimizer ran %d times, want 0; the guard must refuse before evaluating", optimizer.calls)
	}

	// A serially configured optimizer of the same shape must still run.
	serial := &parallelPolishOptimizer{workers: 1}
	serial.params = circleParams(2, 2, 5, color.NRGBA{A: 255}, 1)
	if _, err := PolishCircleBatchContext(context.Background(), base, serial, initial, BatchPolishOptions{
		ActiveSetSize: 1,
		MaxSweeps:     1,
	}); err != nil {
		t.Fatalf("polishing rejected a serial optimizer: %v", err)
	}
}
