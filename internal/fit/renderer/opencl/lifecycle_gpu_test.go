//go:build gpu

package opencl //nolint:testpackage // exercises unexported teardown on one serial device

import "testing"

// A Renderer waits on the engine's command queue before it releases its own
// kernels and buffers, and that queue is borrowed: the engine frees it as soon
// as the last holder gives its reference back. Before the borrowed handles were
// cleared, a second teardown therefore called clFinish on a released queue.
//
// That is a driver-side use-after-free rather than a Go panic, so it would not
// have surfaced as a test failure anywhere else -- and the teardown it breaks is
// exactly the one a shared engine makes reachable, because releasing a session
// no longer implies releasing the queue. The property the old, per-renderer
// release had for free is asserted here instead.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLRendererCleanupIsIdempotent(t *testing.T) {
	r, cleanup := newOpenCLTestRenderer(t, 3)

	// A real evaluation first, so the queue has work in flight for the wait in
	// releaseOwn to actually wait on rather than a queue that was never used.
	params := make([]float64, r.Dim())
	for i := range params {
		params[i] = float64(i%5) + 1
	}

	if cost := r.Cost(params); cost <= 0 {
		t.Fatalf("cost = %v, want a positive value from a live device", cost)
	}

	if r.Degraded() {
		t.Fatal("renderer degraded before teardown: this exercised no device")
	}

	cleanup()
	cleanup()

	if r.engine != nil {
		t.Error("engine reference still held after teardown")
	}

	if r.queue != nil || r.context != nil || r.device != nil || r.runtime != nil {
		t.Error("borrowed device handles still set after teardown")
	}
}

// A session and its parent hold separate references to one engine, so the
// engine has to outlive whichever is torn down first. Releasing the parent
// before the session is the order nothing in the pipeline produces today and
// nothing in the type system prevents.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLEngineOutlivesTeardownInEitherOrder(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		parentFirst bool
	}{
		{name: "session_first", parentFirst: false},
		{name: "parent_first", parentFirst: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			base, baseCleanup := newOpenCLTestRenderer(t, 2)

			session, sessionCleanup, err := base.NewSession(3)
			if err != nil {
				t.Fatalf("NewSession: %v", err)
			}

			params := make([]float64, session.Dim())
			for i := range params {
				params[i] = float64(i%5) + 1
			}

			if cost := session.Cost(params); cost <= 0 {
				t.Fatalf("session cost = %v, want a positive value", cost)
			}

			if testCase.parentFirst {
				baseCleanup()
				sessionCleanup()

				return
			}

			sessionCleanup()

			// The parent must still be able to reach the device after the
			// session has gone.
			baseParams := make([]float64, base.Dim())
			for i := range baseParams {
				baseParams[i] = float64(i%5) + 1
			}

			if cost := base.Cost(baseParams); cost <= 0 {
				t.Fatalf("base cost after session teardown = %v, want positive", cost)
			}

			if base.Degraded() {
				t.Error("base degraded after a session was torn down")
			}

			baseCleanup()
		})
	}
}
