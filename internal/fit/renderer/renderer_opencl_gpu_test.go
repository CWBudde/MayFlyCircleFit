//go:build gpu

package renderer

import (
	"image"
	"image/color"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
	"github.com/cwbudde/circlefit/internal/fit/renderer/opencl"
)

const (
	openCLCostAbsoluteTolerance = 0.01
	// Float32 compositing and circle-edge coverage may differ slightly from the
	// float64 CPU path, while the rendered channels remain within two bytes.
	openCLCostRelativeTolerance = 0.01
)

func TestOpenCLRendererMatchesCPU(t *testing.T) {
	ref := patternedReference(image.Rect(0, 0, 32, 32))
	params := []float64{
		16, 16, 8, 0.2, 0.4, 0.8, 0.9,
		8, 24, 5, 0.9, 0.1, 0.3, 0.45,
	}

	assertOpenCLParity(t, ref, params)
}

func TestOpenCLReductionBoundaries(t *testing.T) {
	probeRef := patternedReference(image.Rect(0, 0, 1, 1))
	probe, releaseProbe := newOpenCLTestRenderer(t, probeRef, 0)
	localSize := probe.LocalSize()
	releaseProbe()

	pixelCounts := []int{
		1,
		max(1, localSize-1),
		localSize,
		localSize + 1,
		localSize*2 - 1,
		localSize*2 + 1,
	}
	for _, pixelCount := range pixelCounts {
		t.Run("pixels_"+strconv.Itoa(pixelCount), func(t *testing.T) {
			ref := patternedReference(image.Rect(0, 0, pixelCount, 1))
			params := []float64{
				float64(pixelCount) / 2, 0, max(1, float64(pixelCount)/4),
				0.3, 0.7, 0.2, 0.65,
			}
			assertOpenCLParity(t, ref, params)
		})
	}
}

func TestOpenCLReferenceOriginStrideAndAlpha(t *testing.T) {
	parent := image.NewNRGBA(image.Rect(-4, -3, 29, 23))
	refBounds := image.Rect(2, 1, 21, 14)
	ref := parent.SubImage(refBounds).(*image.NRGBA)
	for y := refBounds.Min.Y; y < refBounds.Max.Y; y++ {
		for x := refBounds.Min.X; x < refBounds.Max.X; x++ {
			ref.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*17 + y*3) & 0xff),
				G: uint8((x*5 + y*19) & 0xff),
				B: uint8((x*11 + y*7) & 0xff),
				A: uint8((x*13 + y*23) & 0xff),
			})
		}
	}

	params := []float64{
		9, 6, 7, 1, 0, 0.25, 0.8,
		-4, 2, 6, 0, 1, 0.5, 0.4,
		15, 11, 0, 0.2, 0.3, 1, 0.9,
	}
	assertOpenCLParity(t, ref, params)
}

func TestOpenCLZeroCirclesAndInvalidParams(t *testing.T) {
	ref := patternedReference(image.Rect(0, 0, 17, 9))
	assertOpenCLParity(t, ref, nil)
	empty, releaseEmpty := newOpenCLTestRenderer(t, image.NewNRGBA(image.Rect(0, 0, 0, 0)), 0)
	if cost := empty.Cost(nil); !math.IsInf(cost, 1) {
		releaseEmpty()
		t.Fatalf("Cost(empty image) = %v, want +Inf", cost)
	}
	if bounds := empty.Render(nil).Bounds(); !bounds.Empty() {
		releaseEmpty()
		t.Fatalf("Render(empty image) bounds = %v, want empty", bounds)
	}
	releaseEmpty()

	r, release := newOpenCLTestRenderer(t, ref, 1)
	defer release()
	short := make([]float64, paramsPerCircle-1)
	if cost := r.Cost(short); !math.IsInf(cost, 1) {
		t.Fatalf("Cost(short params) = %v, want +Inf", cost)
	}
	if r.Degraded() {
		t.Fatal("invalid parameters permanently degraded the OpenCL backend")
	}
	want := NewCPURenderer(ref, 1).Render(short)
	got := r.Render(short)
	assertNRGBAWithin(t, want, got, 0)
}

func TestOpenCLCostDefersImageReadback(t *testing.T) {
	ref := patternedReference(image.Rect(0, 0, 31, 17))
	r, release := newOpenCLTestRenderer(t, ref, 1)
	defer release()

	params := []float64{15, 8, 6, 0.2, 0.6, 0.9, 0.75}
	_ = r.Cost(params)
	if !r.DeviceValid() {
		t.Fatal("Cost did not leave a valid device result")
	}
	if r.ImageValid() {
		t.Fatal("Cost materialized the output image on the host")
	}

	deviceHash := r.DeviceHash()
	evaluations := r.Evaluations()
	_ = r.Render(params)
	if !r.ImageValid() {
		t.Fatal("Render did not materialize the resident device image")
	}
	if r.DeviceHash() != deviceHash {
		t.Fatal("Render changed the cached device evaluation for identical parameters")
	}
	if r.Evaluations() != evaluations {
		t.Fatal("Render reran the kernels for identical parameters")
	}

	changed := append([]float64(nil), params...)
	changed[0]++
	_ = r.Cost(changed)
	if !r.DeviceValid() || r.DeviceHash() == deviceHash {
		t.Fatal("changed parameters did not replace the cached device evaluation")
	}
	if r.Evaluations() != evaluations+1 {
		t.Fatal("changed parameters did not run exactly one new device evaluation")
	}
	if r.ImageValid() {
		t.Fatal("changed parameters did not invalidate the host image cache")
	}
}

func TestOpenCLOptimizationPipelines(t *testing.T) {
	ref := solidImage(3, 3, color.NRGBA{A: 255})

	// Every opaqueBlackOptimizer circle covers this 3x3 reference completely, so
	// the first one drives the cost to zero and the rest add nothing at all.
	// wantCircles therefore tracks what each pipeline retains, not the requested
	// count, and batch exhausts its refill budget.
	//
	// The three pipelines differ in what they do with a stage that is only
	// partly useful, and batch is the one that has to weigh the cost of asking
	// again. Sequential offers one circle per stage, so a useless circle is
	// simply rejected. Batch offers batchSize of them at once: dropping the
	// redundant one leaves the stage short, and the only way to fill that hole
	// is a refill stage -- a whole further optimizer run at the full configured
	// iteration budget. So a batch that improves the image is retained as the
	// optimizer produced it, and batch keeps both circles of its first stage
	// where sequential keeps one. Later stages add nothing whatsoever, are
	// retained by neither, and are refilled until the bounded attempts run out.
	//
	// The CPU equivalents are TestOptimizeBatchKeepsAWholeBatchOnTheReplayPath,
	// which drives this same case through a renderer double with the same
	// no-accumulated-canvas shape and is runnable without a device,
	// TestOptimizeBatchRejectsIneffectiveCirclesAfterBoundedRefill, and
	// TestStagedOptimizationRollsBackWorseningStage.
	//
	// Session counts follow the replay path: the OpenCL renderer offers
	// newSession but no accumulated canvas, so each stage replays retained
	// circles into a fresh session.
	tests := []struct {
		name        string
		circles     int
		wantStages  int
		wantCircles int
		wantSession int
		run         func(Renderer) (*OptimizationResult, error)
	}{
		{
			name:        "joint",
			circles:     3,
			wantStages:  1,
			wantCircles: 3,
			wantSession: 0,
			run: func(r Renderer) (*OptimizationResult, error) {
				return OptimizeJoint(r, opaqueBlackOptimizer(), 3, DisabledConvergenceConfig())
			},
		},
		{
			name:        "sequential",
			circles:     3,
			wantStages:  3,
			wantCircles: 1, // stages two and three add nothing and are pruned
			// Baseline plus three stages. There used to be a fifth, a final
			// replay session that re-rendered the whole draw order to produce
			// BestImage; with an accumulated canvas finishStagedResult returns
			// the retained canvas directly and never opens it.
			wantSession: 4,
			run: func(r Renderer) (*OptimizationResult, error) {
				return OptimizeSequential(r, opaqueBlackOptimizer(), 3, DisabledConvergenceConfig(), nil)
			},
		},
		{
			name:        "batch",
			circles:     5,
			wantStages:  6, // 2+2+1 stages plus MaxExtraBatchStages refill attempts
			wantCircles: 2, // the whole first batch, redundant second circle included
			wantSession: 8, // one fewer than before: the final replay session, as above
			run: func(r Renderer) (*OptimizationResult, error) {
				return OptimizeBatch(r, opaqueBlackOptimizer(), 5, 2, DisabledConvergenceConfig())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, release := newOpenCLTestRenderer(t, ref, tt.circles)
			defer release()
			tracking := &trackingOpenCLFactory{openCLAdapter: base}

			result, err := tt.run(tracking)
			if err != nil {
				t.Fatalf("optimize with OpenCL: %v", err)
			}
			if result.Stages != tt.wantStages {
				t.Fatalf("stages = %d, want %d", result.Stages, tt.wantStages)
			}
			if got, want := len(result.BestParams), tt.wantCircles*paramsPerCircle; got != want {
				t.Fatalf("parameter count = %d, want %d", got, want)
			}
			if result.BestCost != 0 {
				t.Fatalf("best cost = %v, want 0", result.BestCost)
			}
			if got := result.BestImage.NRGBAAt(1, 1); got != (color.NRGBA{A: 255}) {
				t.Fatalf("best image pixel = %#v, want opaque black", got)
			}
			if base.Degraded() {
				t.Fatal("base OpenCL renderer degraded to CPU")
			}
			if len(tracking.sessions) != tt.wantSession {
				t.Fatalf("OpenCL sessions = %d, want %d", len(tracking.sessions), tt.wantSession)
			}
			for i, session := range tracking.sessions {
				if session.Degraded() {
					t.Fatalf("OpenCL session %d degraded to CPU", i)
				}
				if session.Evaluations() == 0 {
					t.Fatalf("OpenCL session %d performed no device evaluations", i)
				}
				// Every stage above ran on the base renderer's engine rather
				// than on one of its own. TestOpenCLEngineIsSharedAcrossSessions
				// asserts the same thing about NewSession directly; this is the
				// end-to-end version, because it is the pipelines that create
				// the sessions and the pipelines whose per-stage rebuild the
				// sharing was meant to remove.
				if !session.sharedBaseRuntime {
					t.Fatalf("OpenCL session %d did not run on the base renderer's runtime: "+
						"the stage brought up a device of its own", i)
				}

				if session.programBuilds != 1 {
					t.Fatalf("OpenCL session %d saw %d program builds, want 1: the stage recompiled the kernel source",
						i, session.programBuilds)
				}
			}
		})
	}
}

type trackingOpenCLFactory struct {
	openCLAdapter
	sessions []trackedOpenCLSession
}

// trackedOpenCLSession is a session plus the two engine facts that have to be
// read while it is alive. A pipeline releases each session as its stage ends,
// and teardown clears the borrowed runtime handle and gives the engine
// reference back, so Runtime() and ProgramBuilds() both answer zero afterwards.
// Degraded() and Evaluations() survive teardown and are still read from the
// session itself.
type trackedOpenCLSession struct {
	*opencl.Renderer

	sharedBaseRuntime bool
	programBuilds     uint64
}

func (r *trackingOpenCLFactory) newSession(circleCount int) (Renderer, func(), error) {
	session, cleanup, err := r.openCLAdapter.Renderer.NewSession(circleCount)
	if err != nil {
		return nil, cleanup, err
	}

	return r.track(session), cleanup, nil
}

// newSessionWithCanvas has to be tracked too. The embedded adapter supplies one
// already, so leaving this out would compile and silently count zero sessions
// for the accumulated staged path -- which is the only path the pipelines take
// now.
func (r *trackingOpenCLFactory) newSessionWithCanvas(canvas *image.NRGBA, circleCount int) (Renderer, func(), error) {
	session, cleanup, err := r.NewSessionWithCanvas(canvas, circleCount)
	if err != nil {
		return nil, cleanup, err
	}

	return r.track(session), cleanup, nil
}

func (r *trackingOpenCLFactory) track(session *opencl.Renderer) Renderer {
	r.sessions = append(r.sessions, trackedOpenCLSession{
		Renderer:          session,
		sharedBaseRuntime: session.Runtime() == r.Runtime(),
		programBuilds:     session.ProgramBuilds(),
	})

	return openCLAdapter{session}
}

func assertOpenCLParity(t *testing.T, ref *image.NRGBA, params []float64) {
	t.Helper()
	circles := len(params) / paramsPerCircle
	cpu := NewCPURenderer(ref, circles)
	gpu, release := newOpenCLTestRenderer(t, ref, circles)
	defer release()

	wantCost := cpu.Cost(params)
	gotCost := gpu.Cost(params)
	gpuImage := gpu.Render(params)
	if imageCost := fit.MSECost(gpuImage, ref); math.Abs(imageCost-gotCost) > openCLCostAbsoluteTolerance {
		t.Fatalf("device cost %f does not describe rendered image cost %f", gotCost, imageCost)
	}
	assertCostWithin(t, wantCost, gotCost)
	assertNRGBAWithin(t, cpu.Render(params), gpuImage, 2)
}

func newOpenCLTestRenderer(t *testing.T, ref *image.NRGBA, circles int) (openCLAdapter, func()) {
	t.Helper()
	renderer, cleanup, err := NewOpenCLRenderer(ref, circles)
	if err != nil {
		if os.Getenv("CIRCLEFIT_REQUIRE_OPENCL") == "1" {
			t.Fatalf("required GPU backend unavailable: %v", err)
		}
		t.Skipf("GPU backend unavailable: %v", err)
	}
	r, ok := renderer.(openCLAdapter)
	if !ok {
		cleanup()
		t.Fatalf("NewOpenCLRenderer returned %T, want openCLAdapter", renderer)
	}

	// A device error after initialization degrades the renderer permanently and
	// silently -- Cost and Render have no error return -- and every later answer
	// comes from the CPU fallback. A parity assertion would then compare the CPU
	// oracle against the CPU fallback and pass while exercising no device at
	// all, which is the one outcome a run under CIRCLEFIT_REQUIRE_OPENCL must
	// never produce. Checking at teardown guards every test built through this
	// helper, rather than the ones that remembered to ask.
	return r, func() {
		if r.Degraded() {
			t.Errorf("OpenCL renderer degraded to its CPU fallback: this result exercised no device")
		}

		cleanup()
	}
}

func assertCostWithin(t *testing.T, want, got float64) {
	t.Helper()
	tolerance := openCLCostAbsoluteTolerance + math.Abs(want)*openCLCostRelativeTolerance
	if diff := math.Abs(want - got); diff > tolerance {
		t.Fatalf("cost mismatch (cpu=%f gpu=%f diff=%f tolerance=%f)", want, got, diff, tolerance)
	}
}

func patternedReference(bounds image.Rectangle) *image.NRGBA {
	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ref.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*31 + y*7) & 0xff),
				G: uint8((x*13 + y*29) & 0xff),
				B: uint8((x*3 + y*37) & 0xff),
				A: uint8((x*17 + y*11) & 0xff),
			})
		}
	}
	return ref
}

func assertNRGBAWithin(t *testing.T, a, b *image.NRGBA, tolerance uint8) {
	t.Helper()

	if a.Bounds().Dx() != b.Bounds().Dx() || a.Bounds().Dy() != b.Bounds().Dy() {
		t.Fatalf("dimensions mismatch: %v vs %v", a.Bounds(), b.Bounds())
	}

	for y := 0; y < a.Bounds().Dy(); y++ {
		for x := 0; x < a.Bounds().Dx(); x++ {
			ai := a.PixOffset(a.Bounds().Min.X+x, a.Bounds().Min.Y+y)
			bi := b.PixOffset(b.Bounds().Min.X+x, b.Bounds().Min.Y+y)
			for c := 0; c < 4; c++ {
				va := a.Pix[ai+c]
				vb := b.Pix[bi+c]
				if diff := absUint8Diff(va, vb); diff > tolerance {
					t.Fatalf("pixel mismatch at (%d,%d) channel %d: %d vs %d", x, y, c, va, vb)
				}
			}
		}
	}
}

func absUint8Diff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}
