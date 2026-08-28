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
	"os"
	"testing"
)

// Degradation is a fact about the device, not about one Renderer value. The
// staged pipelines evaluate every circle through an independent session, so a
// per-renderer flag left a sequential or batch run reporting a clean device
// while everything after the failure was costed on the CPU.
//
// The device event itself cannot be induced here -- that would need a
// fault-injection hook in the cgo path or a card that fails on demand -- so
// these tests set the shared record directly and assert that it is shared. That
// is the part the reporting depends on.
func TestDegradationIsSharedBetweenARendererAndItsSessions(t *testing.T) {
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
