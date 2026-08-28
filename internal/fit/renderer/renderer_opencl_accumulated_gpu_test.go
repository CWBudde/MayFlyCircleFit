//go:build gpu

package renderer //nolint:testpackage // uses the unexported newSessionWithCanvas hook

import (
	"context"
	"fmt"
	"image"
	"math"
	"math/rand"
	"testing"
)

// accumulatedDepths are retained-circle counts a stage can carry. The point of
// the accumulated canvas is that the work stops depending on them, so the sweep
// spans a range wide enough for a linear term to show.
var accumulatedDepths = []int{1, 2, 8, 32}

// TestOpenCLAccumulatedCanvasMatchesReplay holds the accumulated path to
// byte-exact equality with replaying the whole draw order, on the same device.
//
// Tolerance zero is the right bar here, and it is a property of the kernel
// rather than an optimistic choice. The circle loop quantizes to eight bits
// after every layer to mirror the CPU's NRGBA round-trip, so the colour state
// after D circles is already an exact eight-bit value; reading it back as
// byte/255 recovers the identical float32. There is no accumulation of float32
// error across the stage boundary to absorb, so any difference at all is a real
// defect -- a wrong argument index, a stale buffer, an alpha that was not
// carried -- and not device noise.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLAccumulatedCanvasMatchesReplay(t *testing.T) {
	sizes := []image.Point{{X: 1, Y: 1}, {X: 7, Y: 5}, {X: 32, Y: 32}}

	for _, size := range sizes {
		ref := patternedReference(image.Rect(0, 0, size.X, size.Y))

		for _, depth := range accumulatedDepths {
			t.Run(sizeAndDepthName(size, depth), func(t *testing.T) {
				//nolint:gosec // a reproducible fixture, not a security context
				rng := rand.New(rand.NewSource(int64(depth*1000 + size.X*31 + size.Y)))
				params := randomCircles(t, rng, depth+1, size)
				prefix := params[:depth*paramsPerCircle]
				appended := params[depth*paramsPerCircle:]

				base, releaseBase := newOpenCLTestRenderer(t, ref, depth)
				defer releaseBase()

				canvas := cloneNRGBA(base.Render(prefix))

				session, releaseSession, err := base.newSessionWithCanvas(canvas, 1)
				if err != nil {
					t.Fatalf("newSessionWithCanvas() = %v", err)
				}
				defer releaseSession()

				replay, releaseReplay := newOpenCLTestRenderer(t, ref, depth+1)
				defer releaseReplay()

				wantCost := replay.Cost(params)

				gotCost := session.Cost(appended)
				if wantCost != gotCost {
					t.Fatalf("accumulated cost %v, replay cost %v: the two disagree at depth %d",
						gotCost, wantCost, depth)
				}

				assertNRGBAWithin(t, replay.Render(params), session.Render(appended), 0)
			})
		}
	}
}

// TestOpenCLAccumulatedCanvasMatchesCPU is the cross-backend half, and it is a
// budget rather than an equality: each backend builds its retained canvas with
// its own renderer, which is exactly what the pipelines do.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLAccumulatedCanvasMatchesCPU(t *testing.T) {
	size := image.Point{X: 33, Y: 17}
	ref := patternedReference(image.Rect(0, 0, size.X, size.Y))

	for _, depth := range accumulatedDepths {
		t.Run(sizeAndDepthName(size, depth), func(t *testing.T) {
			//nolint:gosec // a reproducible fixture, not a security context
			rng := rand.New(rand.NewSource(int64(depth) + 4242))
			params := randomCircles(t, rng, depth+1, size)
			prefix := params[:depth*paramsPerCircle]
			appended := params[depth*paramsPerCircle:]

			cpu := NewCPURenderer(ref, depth)

			cpuSession, cpuCleanup, err := cpu.newSessionWithCanvas(cloneNRGBA(cpu.Render(prefix)), 1)
			if err != nil {
				t.Fatalf("CPU newSessionWithCanvas() = %v", err)
			}
			defer cpuCleanup()

			base, releaseBase := newOpenCLTestRenderer(t, ref, depth)
			defer releaseBase()

			session, releaseSession, err := base.newSessionWithCanvas(cloneNRGBA(base.Render(prefix)), 1)
			if err != nil {
				t.Fatalf("newSessionWithCanvas() = %v", err)
			}
			defer releaseSession()

			assertCostWithin(t, cpuSession.Cost(appended), session.Cost(appended))
			assertNRGBAWithin(t, cpuSession.Render(appended), session.Render(appended), openCLMaxChannelDeviation)
		})
	}
}

// TestOpenCLSessionWithCanvasRejectsBadCanvas pins the validation, translucency
// included. A translucent base cannot reach this path from the pipelines --
// every canvas they supply comes from Render, which writes alpha 255 -- and the
// kernel and the CPU renderer agree only on opaque canvases, so refusing one is
// the honest answer rather than rendering something neither backend describes.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLSessionWithCanvasRejectsBadCanvas(t *testing.T) {
	ref := patternedReference(image.Rect(0, 0, 16, 12))

	base, release := newOpenCLTestRenderer(t, ref, 2)
	defer release()

	translucent := image.NewNRGBA(image.Rect(0, 0, 16, 12))
	for i := range translucent.Pix {
		translucent.Pix[i] = 0xFE
	}

	tests := []struct {
		name    string
		canvas  *image.NRGBA
		circles int
	}{
		{name: "nil", canvas: nil, circles: 1},
		{name: "wrong dimensions", canvas: image.NewNRGBA(image.Rect(0, 0, 8, 12)), circles: 1},
		{name: "negative circle count", canvas: base.initialCanvas(), circles: -1},
		{name: "translucent canvas", canvas: translucent, circles: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session, cleanup, err := base.newSessionWithCanvas(tt.canvas, tt.circles)
			if err == nil {
				cleanup()
				t.Fatalf("newSessionWithCanvas(%s) succeeded, want an error (session %T)", tt.name, session)
			}
		})
	}
}

// TestOpenCLInitialCanvasIsOpaqueWhite covers the other half of the interface.
// newStagedAccumulator refuses to accumulate on a nil canvas, so a wrong answer
// here would silently drop the whole backend back to replaying.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLInitialCanvasIsOpaqueWhite(t *testing.T) {
	ref := patternedReference(image.Rect(0, 0, 9, 4))

	base, release := newOpenCLTestRenderer(t, ref, 3)
	defer release()

	canvas := base.initialCanvas()
	if canvas == nil {
		t.Fatal("initialCanvas() = nil: the staged pipelines would fall back to replaying")
	}

	if canvas.Bounds().Dx() != 9 || canvas.Bounds().Dy() != 4 {
		t.Fatalf("initialCanvas() bounds = %v, want 9x4", canvas.Bounds())
	}

	for i, got := range canvas.Pix {
		if got != 0xFF {
			t.Fatalf("initialCanvas() byte %d = %d, want 255", i, got)
		}
	}

	// A snapshot, not the renderer's own state: the accumulator mutates what it
	// is handed, and the pipelines create several sessions over one canvas.
	if second := base.initialCanvas(); &second.Pix[0] == &canvas.Pix[0] {
		t.Fatal("initialCanvas() handed out the same buffer twice")
	}
}

// TestOpenCLBatchAppendFromRetainedCanvas exercises the entry point this
// tranche unlocked. Before the accumulated canvas it returned
// ErrStagedOptimizationUnsupported for OpenCL, because it is the one caller
// that cannot fall back to replaying: it has a canvas and no parameters to
// replay from. It is how a completed checkpoint extends by one circle without
// redrawing thousands of immutable ones.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLBatchAppendFromRetainedCanvas(t *testing.T) {
	ref := patternedReference(image.Rect(0, 0, 24, 24))

	base, release := newOpenCLTestRenderer(t, ref, 4)
	defer release()

	// In-bounds by hand: this entry point validates the prefix against the
	// renderer's bounds, and randomCircles deliberately produces off-canvas
	// circles for the rasterizer tests.
	prefixParams := []float64{
		6, 6, 4, 0.2, 0.4, 0.6, 0.8,
		16, 15, 5, 0.9, 0.1, 0.3, 0.7,
	}

	prefixSession, prefixCleanup, err := base.newSession(2)
	if err != nil {
		t.Fatalf("newSession() = %v", err)
	}

	prefixCanvas := cloneNRGBA(prefixSession.Render(prefixParams))
	prefixCost := prefixSession.Cost(prefixParams)

	prefixCleanup()

	result, err := OptimizeBatchAppendFromCanvasContext(
		context.Background(), base, opaqueBlackOptimizer(),
		prefixParams, prefixCanvas, prefixCost, 4, 2, DisabledConvergenceConfig(),
	)
	if err != nil {
		t.Fatalf("OptimizeBatchAppendFromCanvasContext() = %v", err)
	}

	if result.InitialCost != prefixCost {
		t.Fatalf("initial cost = %v, want the supplied prefix cost %v", result.InitialCost, prefixCost)
	}

	if result.BestImage == nil {
		t.Fatal("result carries no image")
	}

	if base.Degraded() {
		t.Fatal("the retained-prefix batch run degraded to the CPU")
	}
}

// TestOpenCLAccumulatedDeviationBudget is TestOpenCLDeviationBudget for the
// accumulated path, and it is a separate sweep rather than an extension of that
// one on purpose: the figures in docs/renderer-correctness.md describe the
// white-canvas path, and an accumulated session is a different measurement.
//
// The retained canvas is each backend's own render of the prefix, which is what
// the pipelines do, so any disagreement in the prefix is carried into the stage
// -- this is the sweep that would show it compounding.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLAccumulatedDeviationBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("randomized deviation sweep is not a short test")
	}

	sizes := []image.Point{{X: 32, Y: 32}, {X: 129, Y: 77}}

	var (
		worstChannel  int
		worstRelative float64
		worstScene    string
	)

	for _, size := range sizes {
		for _, depth := range []int{4, 32} {
			ref := patternedReference(image.Rect(0, 0, size.X, size.Y))

			//nolint:gosec // a reproducible fixture, not a security context
			rng := rand.New(rand.NewSource(int64(size.X*7919 + size.Y*104729 + depth)))
			prefix := randomCircles(t, rng, depth, size)

			cpu := NewCPURenderer(ref, depth)

			cpuSession, cpuCleanup, err := cpu.newSessionWithCanvas(cloneNRGBA(cpu.Render(prefix)), 1)
			if err != nil {
				t.Fatalf("CPU newSessionWithCanvas() = %v", err)
			}

			base, release := newOpenCLTestRenderer(t, ref, depth)

			session, sessionCleanup, err := base.newSessionWithCanvas(cloneNRGBA(base.Render(prefix)), 1)
			if err != nil {
				t.Fatalf("newSessionWithCanvas() = %v", err)
			}

			for trial := range 12 {
				appended := randomCircles(t, rng, 1, size)

				wantCost, gotCost := cpuSession.Cost(appended), session.Cost(appended)

				relative := 0.0
				if wantCost >= openCLRelativeCostFloor {
					relative = math.Abs(wantCost-gotCost) / wantCost
				}

				channel := maxChannelDeviation(cpuSession.Render(appended), session.Render(appended))
				if channel > worstChannel || relative > worstRelative {
					worstScene = fmt.Sprintf("%dx%d D=%d trial %d", size.X, size.Y, depth, trial)
				}

				worstChannel = max(worstChannel, channel)
				worstRelative = math.Max(worstRelative, relative)
			}

			sessionCleanup()
			release()
			cpuCleanup()
		}
	}

	t.Logf("accumulated: worst channel deviation %d, worst relative cost error %.6f%% (%s)",
		worstChannel, 100*worstRelative, worstScene)

	if worstChannel > openCLMaxChannelDeviation {
		t.Errorf("worst channel deviation %d exceeds the documented budget of %d",
			worstChannel, openCLMaxChannelDeviation)
	}

	if worstRelative > openCLMaxRelativeCostError {
		t.Errorf("worst relative cost error %.6f exceeds the documented budget of %.6f",
			worstRelative, openCLMaxRelativeCostError)
	}
}

func sizeAndDepthName(size image.Point, depth int) string {
	return fmt.Sprintf("%dx%d/D=%d", size.X, size.Y, depth)
}
