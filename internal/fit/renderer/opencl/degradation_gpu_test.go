//go:build gpu

// The tests below reach into the shared degradation record directly, which is
// the only way to exercise propagation without a device that fails on demand.
// They stay serial because the backend does not advertise safe concurrent
// evaluation.
//
//nolint:testpackage,paralleltest // sets the unexported degradation record on one serial device
package opencl

import (
	"image"
	"math"
	"os"
	"testing"
)

// Degradation is a fact about the device, not about one Renderer value. The
// staged pipelines evaluate every circle through a separate session, so a
// per-renderer flag left a sequential or batch run reporting a clean device
// while everything after the failure was costed on the CPU.
//
// The device event itself cannot be induced here -- that would need a
// fault-injection hook in the cgo path or a card that fails on demand -- so
// these tests set the shared record directly and assert that it is shared. That
// is the part the reporting depends on.
func TestOpenCLDegradationIsSharedBetweenARendererAndItsSessions(t *testing.T) {
	base, cleanup := newOpenCLTestRenderer(t, 4)
	defer cleanup()

	session, sessionCleanup, err := base.NewSession(2)
	if err != nil {
		t.Fatalf("NewSession() = %v", err)
	}
	defer sessionCleanup()

	if base.Degraded() || session.Degraded() {
		t.Fatal("a freshly built renderer and session report degraded")
	}

	// Upwards: this is the case the staged pipelines hit. The job reads the
	// base renderer, and every evaluation happens on a session.
	session.degraded.Store(true)

	if !base.Degraded() {
		t.Fatal("base.Degraded() = false after a session degraded; a staged run would report a clean device")
	}

	// Downwards, which is the same record seen from the other side: a session
	// created after the device is gone must not rediscover it. That is what
	// keeps a sequential run from paying one device timeout per stage.
	later, laterCleanup, err := base.NewSession(3)
	if err != nil {
		t.Fatalf("NewSession() after degradation = %v", err)
	}
	defer laterCleanup()

	if !later.Degraded() {
		t.Fatal("a session created after degradation reports a healthy device")
	}
}

// Not rediscovering the loss means doing no device work, not merely reporting
// it. A session built after the record is set routes every Cost and Render to
// its CPU fallback whatever it allocated, so allocating anyway is not just
// waste: on a real device loss the kernel and buffer creation is what fails,
// and NewSession returning an error aborts the staged pipeline instead of
// letting it finish on the CPU.
//
// The device here is healthy and the record is set by hand, which is the only
// way to exercise this without a card that fails on demand -- so the assertion
// is that the session holds nothing from the device, not that construction
// survived a failure.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLSessionAfterDegradationDoesNoDeviceWork(t *testing.T) {
	base, cleanup := newOpenCLTestRenderer(t, 4)
	defer cleanup()

	baseRuntime := base.Runtime()
	if baseRuntime == nil {
		t.Fatal("base renderer reports no runtime")
	}

	base.degraded.Store(true)

	session, sessionCleanup, err := base.NewSession(3)
	if err != nil {
		t.Fatalf("NewSession() after degradation = %v", err)
	}

	defer sessionCleanup()

	if session.Runtime() != nil {
		t.Error("a session built after degradation holds a runtime: it brought up device state it can never use")
	}

	if got := session.ProgramBuilds(); got != 0 {
		t.Errorf("ProgramBuilds() = %d, want 0: the session reached the engine", got)
	}

	if session.renderKernel != nil || session.reduceKernel != nil {
		t.Error("a session built after degradation created kernels")
	}

	if session.outputBuffer != nil || session.paramsBuffer != nil ||
		session.partialBufferA != nil || session.partialBufferB != nil {
		t.Error("a session built after degradation allocated device buffers")
	}

	// It still has to be a working renderer, answering from the fallback. The
	// stub fallback costs +Inf, which no device evaluation returns, so this
	// distinguishes the two sources rather than merely checking for a number.
	params := make([]float64, session.Dim())
	for i := range params {
		params[i] = float64(i%5) + 1
	}

	if cost := session.Cost(params); !math.IsInf(cost, 1) {
		t.Errorf("Cost() = %v, want the fallback's +Inf: the session answered from somewhere else", cost)
	}

	if img := session.Render(params); img == nil || img.Bounds() != base.Reference().Bounds() {
		t.Error("Render() did not return a fallback image over the reference bounds")
	}

	if !session.Degraded() {
		t.Error("a session created after degradation reports a healthy device")
	}
}

func newOpenCLTestRenderer(t *testing.T, circles int) (*Renderer, func()) {
	t.Helper()

	ref := patternedReference(image.Rect(0, 0, 8, 8))

	r, cleanup, err := New(ref, circles, newStubFallback)
	if err != nil {
		if os.Getenv("CIRCLEFIT_REQUIRE_OPENCL") == "1" {
			t.Fatalf("required OpenCL backend unavailable: %v", err)
		}

		t.Skipf("OpenCL backend unavailable: %v", err)
	}

	if r.Degraded() {
		cleanup()
		t.Skip("OpenCL backend degraded to the fallback renderer")
	}

	return r, cleanup
}
