package renderer

import (
	"os"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
)

// requiredTierEnv duplicates the constant in internal/fit rather than exporting
// it, because it is a CI contract rather than an API. Both packages must honor
// it: a gate that sets it and runs only one of them proves nothing about the
// other, which is precisely how the SSE2 renderer dispatch went unasserted.
const requiredTierEnv = "CIRCLEFIT_REQUIRE_SIMD_TIER"

// TestRequiredSIMDTier is the renderer-side half of the CI assertion.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestRequiredSIMDTier(t *testing.T) {
	required := os.Getenv(requiredTierEnv)
	if required == "" {
		t.Skipf("%s is not set", requiredTierEnv)
	}

	want, ok := fit.ParseSIMDTier(required)
	if !ok {
		t.Fatalf("%s=%q is not a tier name", requiredTierEnv, required)
	}

	if got := fit.Tier(); got != want {
		t.Fatalf("detected tier = %s, required %s", got, want)
	}
}

// rendererKernels names every tier-dispatched kernel in this package. Keeping
// them in one list is the point: before fit.Tier() existed each of these made
// its own decision from the CPU feature bits and recorded it in its own way, so
// nothing could state -- let alone check -- that they agreed.
func rendererKernels() []struct {
	name string
	tier fit.SIMDTier
} {
	return []struct {
		name string
		tier fit.SIMDTier
	}{
		{"delta-SSD", deltaSSDKernel},
		{"circle-span float32", circleSpanFloat32Kernel},
		{"composite span", compositeSpanKernel},
	}
}

// TestRendererKernelsMatchTier is the invariant a CI step can rely on. The
// MAYFLY_REQUIRE_SSD_BACKEND variable it replaces was read only by
// internal/fit, so the CI step that ran this package under forced SSE2 asserted
// nothing about it at all: every kernel here could have fallen back to scalar
// and the gate would still have gone green.
//
// Each kernel is allowed to be narrower than the tier -- not every kernel
// exists at every tier, and the reasons are documented at each dispatch site --
// but none may be wider, and none may be a tier this architecture cannot run.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestRendererKernelsMatchTier(t *testing.T) {
	tier := fit.Tier()
	for _, kernel := range rendererKernels() {
		t.Logf("tier %s: %s kernel %s", tier, kernel.name, kernel.tier)

		if kernel.tier != tier && kernel.tier != fit.TierScalar {
			t.Errorf("%s kernel = %s, which is neither the tier (%s) nor scalar", kernel.name, kernel.tier, tier)
		}
	}
}

// TestRendererKernelsFollowForcedTier proves every dispatch site in this
// package is actually registered with the tier switch. A site that kept its own
// init-time decision would ignore the forced tier and fail here, which is the
// regression the old copy-paste dispatch made undetectable.
//
//nolint:paralleltest // forces the process-global SIMD tier, which no two tests may do at once
func TestRendererKernelsFollowForcedTier(t *testing.T) {
	fit.SetForcedTier(fit.TierScalar)

	defer fit.ResetTierDetection()

	for _, kernel := range rendererKernels() {
		if kernel.tier != fit.TierScalar {
			t.Errorf("%s kernel = %s after forcing the scalar tier", kernel.name, kernel.tier)
		}
	}
}
