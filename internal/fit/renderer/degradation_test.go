//nolint:testpackage // exercises the unexported degradable interface alongside the exported accessor
package renderer

import "testing"

// degradableStub reports a fixed degradation state. It embeds the interface
// rather than implementing it, because Degraded is the only method under test
// and a stub that answered the rest would invite calls this test does not make.
type degradableStub struct {
	Renderer

	degraded bool
}

func (s degradableStub) Degraded() bool { return s.degraded }

func TestDegradedIsFalseForABackendThatCannotDegrade(t *testing.T) {
	t.Parallel()

	// The CPU renderer has nothing to fall back to, so the question is not
	// merely unanswered for it -- the answer is no.
	if Degraded(NewCPURenderer(failureTestReference(), 2)) {
		t.Fatal("Degraded(CPU renderer) = true, want false: the CPU path has no fallback to degrade to")
	}
}

func TestDegradedReportsWhatTheBackendSays(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		want bool
	}{
		{name: "running on its own path", want: false},
		{name: "fallen back to the CPU", want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if got := Degraded(degradableStub{degraded: testCase.want}); got != testCase.want {
				t.Fatalf("Degraded() = %t, want %t", got, testCase.want)
			}
		})
	}
}
