//go:build gpu

package opencl

import (
	"image"
	"image/color"
	"os"
	"testing"
)

// TestOpenCLAccumulatedFallbackStartsFromTheRetainedCanvas is the test for the
// sharpest trap in the accumulated path, and it is deliberately about the
// fallback rather than the device.
//
// Cost and Render have no error return, so a device lost inside a staged
// session degrades permanently and silently. If the fallback for such a session
// were built from white while the session's retained canvas already held
// several hundred circles, every remaining evaluation would answer with the
// cost of a completely different image, the run would finish, and nothing in
// it would say so. The canvas therefore has to reach the fallback factory --
// both on the healthy path, where the fallback is built ahead of a failure that
// may never come, and on the already-degraded path, where it is the only
// renderer there will be.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLAccumulatedFallbackStartsFromTheRetainedCanvas(t *testing.T) {
	ref := patternedReference(image.Rect(0, 0, 8, 8))
	recorder := &recordingFallbackFactory{}

	base, cleanup, err := New(ref, 4, recorder.build)
	if err != nil {
		if os.Getenv("CIRCLEFIT_REQUIRE_OPENCL") == "1" {
			t.Fatalf("required OpenCL backend unavailable: %v", err)
		}

		t.Skipf("OpenCL backend unavailable: %v", err)
	}
	defer cleanup()

	if base.Degraded() {
		t.Skip("OpenCL backend degraded to the fallback renderer")
	}

	if got := recorder.last(); got != nil {
		t.Fatalf("base renderer's fallback was built over a canvas %v, want nil (white)", got.Bounds())
	}

	canvas := opaqueCanvas(image.Rect(0, 0, 8, 8))

	session, sessionCleanup, err := base.NewSessionWithCanvas(canvas, 1)
	if err != nil {
		t.Fatalf("NewSessionWithCanvas() = %v", err)
	}
	defer sessionCleanup()

	if got := recorder.last(); got != canvas {
		t.Fatal("an accumulated session's fallback was not built over its retained canvas: " +
			"a device lost mid-stage would answer with the cost of a different image")
	}

	if plain, plainCleanup, plainErr := base.NewSession(1); plainErr == nil {
		defer plainCleanup()

		if got := recorder.last(); got != nil {
			t.Fatalf("a plain session's fallback was built over a canvas %v, want nil (white)", got.Bounds())
		}

		_ = plain
	}

	// The already-degraded path builds no device state at all, so it is a
	// separate construction path and needs the canvas separately.
	session.degraded.Store(true)

	degraded, degradedCleanup, err := base.NewSessionWithCanvas(canvas, 1)
	if err != nil {
		t.Fatalf("NewSessionWithCanvas() after degradation = %v", err)
	}
	defer degradedCleanup()

	if !degraded.Degraded() {
		t.Fatal("a session created after degradation reports a healthy device")
	}

	if got := recorder.last(); got != canvas {
		t.Fatal("a session created after degradation was given a white fallback " +
			"while its retained canvas held the whole prefix")
	}
}

// TestOpenCLInitialCanvasReflectsTheSessionsBase pins the other direction: an
// accumulated session reports its own retained canvas rather than white, which
// is what lets the pipelines chain one stage onto the next.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLInitialCanvasReflectsTheSessionsBase(t *testing.T) {
	base, cleanup := newOpenCLTestRenderer(t, 4)
	defer cleanup()

	canvas := opaqueCanvas(image.Rect(0, 0, 8, 8))
	canvas.SetNRGBA(3, 4, color.NRGBA{R: 1, G: 2, B: 3, A: 255})

	session, sessionCleanup, err := base.NewSessionWithCanvas(canvas, 1)
	if err != nil {
		t.Fatalf("NewSessionWithCanvas() = %v", err)
	}
	defer sessionCleanup()

	got := session.InitialCanvas()
	if got == nil {
		t.Fatal("InitialCanvas() = nil for an accumulated session")
	}

	if got.NRGBAAt(3, 4) != canvas.NRGBAAt(3, 4) {
		t.Fatalf("InitialCanvas() pixel = %#v, want %#v", got.NRGBAAt(3, 4), canvas.NRGBAAt(3, 4))
	}

	if &got.Pix[0] == &canvas.Pix[0] {
		t.Fatal("InitialCanvas() handed back the session's own canvas rather than a snapshot")
	}
}

// recordingFallbackFactory captures the canvas each fallback was built over.
type recordingFallbackFactory struct {
	canvases []*image.NRGBA
}

func (f *recordingFallbackFactory) build(reference, canvas *image.NRGBA, _ int) Fallback {
	f.canvases = append(f.canvases, canvas)

	return stubFallback{reference: reference}
}

func (f *recordingFallbackFactory) last() *image.NRGBA {
	if len(f.canvases) == 0 {
		return nil
	}

	return f.canvases[len(f.canvases)-1]
}

func opaqueCanvas(bounds image.Rectangle) *image.NRGBA {
	canvas := image.NewNRGBA(bounds)
	for i := range canvas.Pix {
		canvas.Pix[i] = 0xFF
	}

	return canvas
}
