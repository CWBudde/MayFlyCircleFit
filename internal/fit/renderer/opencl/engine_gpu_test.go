//go:build gpu

package opencl //nolint:testpackage // reads the unexported engine on one serial device

import "testing"

// A renderer and the sessions derived from it must run on one engine. The
// property is invisible from the outside -- a session that rebuilt everything
// would still evaluate correctly, only far slower -- so it is asserted here on
// the two pieces of state that can only be shared: the compile count and the
// runtime pointer.
//
// The sessions are given different circle counts because that is the axis that
// used to force a session to start over: it re-enumerated the platforms and
// devices, created a context and queue, and compiled the kernel source again,
// none of which depends on the count that changed.
//
//nolint:paralleltest // one serial device; the backend withholds concurrent evaluation
func TestOpenCLEngineIsSharedAcrossSessions(t *testing.T) {
	sessionCircles := []int{1, 2, 3, 4, 5}

	base, cleanup := newOpenCLTestRenderer(t, 6)
	t.Cleanup(cleanup)

	if got := base.ProgramBuilds(); got != 1 {
		t.Fatalf("base ProgramBuilds() = %d, want 1", got)
	}

	for _, circles := range sessionCircles {
		session, sessionCleanup, err := base.NewSession(circles)
		if err != nil {
			t.Fatalf("NewSession(%d) = %v", circles, err)
		}

		// Registered rather than deferred so every session outlives the loop:
		// an engine that is only shared until the first teardown is not shared.
		t.Cleanup(sessionCleanup)

		if got := session.ProgramBuilds(); got != 1 {
			t.Errorf("ProgramBuilds() = %d after a session with %d circles, want 1: "+
				"the session compiled the kernel source again instead of sharing the engine", got, circles)
		}

		if session.Runtime() != base.Runtime() {
			t.Errorf("session with %d circles runs on runtime %p, base runs on %p: "+
				"the session brought up a device of its own", circles, session.Runtime(), base.Runtime())
		}

		assertSessionEvaluatesOnDevice(t, session, circles)
	}

	// Read once more at the end, because the assertions above ran before the
	// later sessions existed. This is the statement about all six renderers.
	if got := base.ProgramBuilds(); got != 1 {
		t.Fatalf("ProgramBuilds() = %d after %d sessions, want 1", got, len(sessionCircles))
	}
}

// assertSessionEvaluatesOnDevice checks that a session sharing the engine is
// still a working renderer: sharing must not leave it costing on the CPU
// fallback or reading a sibling's buffers.
func assertSessionEvaluatesOnDevice(t *testing.T, session *Renderer, circles int) {
	t.Helper()

	params := make([]float64, session.Dim())
	for i := range params {
		params[i] = float64(i%5) + 1
	}

	if cost := session.Cost(params); cost <= 0 {
		t.Errorf("session with %d circles: cost = %v, want a positive value from a live device", circles, cost)
	}

	if session.Degraded() {
		t.Errorf("session with %d circles degraded to the CPU fallback", circles)
	}
}
