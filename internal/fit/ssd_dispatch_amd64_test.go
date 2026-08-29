//go:build amd64

package fit

import (
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/cpu"
)

const (
	ssdDetectedTierHelper = "CIRCLEFIT_TEST_SSD_DETECTED_TIER"
	ssdForcedTierHelper   = "CIRCLEFIT_TEST_SSD_FORCED_TIER"
)

// TestSSDKernelPerForcedTier is the in-process replacement for what used to
// need one subprocess per configuration. Tier forcing re-runs every registered
// dispatch site, so a single test can walk the whole amd64 ladder and check
// both which kernel was installed and that it agrees with scalar.
//
//nolint:paralleltest // forces the process-global SIMD tier, which no two tests may do at once
func TestSSDKernelPerForcedTier(t *testing.T) {
	tiers := []SIMDTier{TierScalar, TierSSE2}
	if cpu.X86.HasAVX2 {
		tiers = append(tiers, TierAVX2)
	}

	a := []uint8{0, 10, 20, 255}
	b := []uint8{30, 40, 50, 0}

	//nolint:paralleltest // each subtest runs under a forced tier the loop owns
	for _, tier := range tiers {
		t.Run(tier.String(), func(t *testing.T) {
			SetForcedTier(tier)

			defer ResetTierDetection()

			if got := ActiveSSDKernel(); got != tier {
				t.Fatalf("forced tier %s installed the %s kernel", tier, got)
			}

			if got, want := fastSSD(a, b, 4, 1, 1), fastSSD_Scalar(a, b, 4, 1, 1); got != want {
				t.Fatalf("%s SSD = %v, scalar = %v", tier, got, want)
			}
		})
	}
}

// TestSSDForcedTierRestored proves ResetTierDetection puts the process back,
// which is what makes the forcing above safe for the rest of the suite.
//
//nolint:paralleltest // forces the process-global SIMD tier, which no two tests may do at once
func TestSSDForcedTierRestored(t *testing.T) {
	before := ActiveSSDKernel()

	SetForcedTier(TierScalar)
	ResetTierDetection()

	if after := ActiveSSDKernel(); after != before {
		t.Fatalf("kernel after reset = %s, want %s", after, before)
	}
}

// TestSSDDetectedTierWithoutAVX2 starts a fresh process because GODEBUG CPU
// overrides are consumed before package initialization. Unlike the forcing test
// above, this one checks *detection*: that masking the feature bit really moves
// the detected tier down, rather than that dispatch honors a value we handed it.
//
// Without AVX2 the amd64 tier below it is SSE2, not scalar.
//
//nolint:paralleltest // spawns a subprocess that re-detects the process-global SIMD tier
func TestSSDDetectedTierWithoutAVX2(t *testing.T) {
	if os.Getenv(ssdDetectedTierHelper) == "1" {
		if cpu.X86.HasAVX2 {
			t.Fatal("cpu.X86.HasAVX2 is true with GODEBUG=cpu.avx2=off")
		}

		if Tier() != TierSSE2 {
			t.Fatalf("detected tier = %s, want sse2", Tier())
		}

		if ActiveSSDKernel() != TierSSE2 {
			t.Fatalf("SSD kernel = %s, want sse2", ActiveSSDKernel())
		}

		return
	}

	runTierSubprocess(t, "^TestSSDDetectedTierWithoutAVX2$", ssdDetectedTierHelper, map[string]string{
		"GODEBUG": "cpu.avx2=off",
	})
}

// TestSSDTierEnvForcesScalar verifies the operator lever end to end, in the one
// place where it cannot be checked in-process: CIRCLEFIT_SIMD_TIER is read during
// package initialization.
//
// GODEBUG=cpu.all=off cannot express this on amd64, because x/sys/cpu marks
// sse2 Required there and ORs the requirement back in after processing GODEBUG.
//
//nolint:paralleltest // spawns a subprocess that re-detects the process-global SIMD tier
func TestSSDTierEnvForcesScalar(t *testing.T) {
	if os.Getenv(ssdForcedTierHelper) == "1" {
		if Tier() != TierScalar {
			t.Fatalf("detected tier = %s, want scalar", Tier())
		}

		if ActiveSSDKernel() != TierScalar {
			t.Fatalf("SSD kernel = %s, want scalar", ActiveSSDKernel())
		}

		if ActiveSADKernel() != TierScalar {
			t.Fatalf("SAD kernel = %s, want scalar", ActiveSADKernel())
		}

		return
	}

	runTierSubprocess(t, "^TestSSDTierEnvForcesScalar$", ssdForcedTierHelper, map[string]string{
		simdTierEnv: TierScalar.String(),
	})
}

// TestSSDDisableEnvIsTierScalarAlias keeps the older lever working, because CI
// steps and operator notes already use it.
//
//nolint:paralleltest // spawns a subprocess that re-detects the process-global SIMD tier
func TestSSDDisableEnvIsTierScalarAlias(t *testing.T) {
	if os.Getenv(ssdForcedTierHelper) == "2" {
		if Tier() != TierScalar {
			t.Fatalf("detected tier = %s, want scalar", Tier())
		}

		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestSSDDisableEnvIsTierScalarAlias$")

	cmd.Env = append(tierSubprocessEnv(os.Environ()), simdDisableEnv+"=1", ssdForcedTierHelper+"=2")

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, output)
	}
}

// TestSIMDTierEnvRejectsUnknownValue proves the env lever fails loudly. Quietly
// substituting a detected tier for an unparseable request would let a CI gate
// that asks for SSE2 pass while measuring AVX2.
//
//nolint:paralleltest // spawns a subprocess that re-detects the process-global SIMD tier
func TestSIMDTierEnvRejectsUnknownValue(t *testing.T) {
	if os.Getenv(ssdForcedTierHelper) == "3" {
		// Reaching any assertion here means the panic did not happen.
		t.Fatalf("package initialized with %s=nonsense, tier = %s", simdTierEnv, Tier())
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestSIMDTierEnvRejectsUnknownValue$")

	cmd.Env = append(tierSubprocessEnv(os.Environ()), simdTierEnv+"=nonsense", ssdForcedTierHelper+"=3")

	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("subprocess succeeded with %s=nonsense\n%s", simdTierEnv, output)
	}

	if !strings.Contains(string(output), "is not a tier name") {
		t.Fatalf("subprocess failed without the expected diagnostic:\n%s", output)
	}
}

// runTierSubprocess re-execs this test binary with extra environment. GODEBUG
// is merged rather than replaced so an outer setting is not silently dropped.
func runTierSubprocess(t *testing.T, pattern, helper string, extra map[string]string) {
	t.Helper()

	env := tierSubprocessEnv(os.Environ())

	for key, value := range extra {
		if key == "GODEBUG" {
			if existing := os.Getenv("GODEBUG"); existing != "" {
				value = existing + "," + value
			}
		}

		env = append(env, key+"="+value)
	}

	env = append(env, helper+"=1")

	cmd := exec.Command(os.Args[0], "-test.run="+pattern)

	cmd.Env = env

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess failed: %v\n%s", err, output)
	}
}

// tierSubprocessEnv strips every variable that would otherwise leak the outer
// configuration into the child.
func tierSubprocessEnv(environ []string) []string {
	stripped := []string{
		"GODEBUG=", simdTierEnv + "=", simdDisableEnv + "=",
		ssdDetectedTierHelper + "=", ssdForcedTierHelper + "=",
	}

	env := make([]string, 0, len(environ)+2)
	for _, entry := range environ {
		skip := false

		for _, prefix := range stripped {
			if strings.HasPrefix(entry, prefix) {
				skip = true
				break
			}
		}

		if !skip {
			env = append(env, entry)
		}
	}

	return env
}

// TestFastSSD_SSE2MaxWidthDispatchBoundary exercises the width > ssdSSE2MaxWidth
// branch in fastSSD_SSE2. No other test reaches it, because the rest of the
// suite stops at width 512.
//
// It calls fastSSD_SSE2 directly rather than going through the installed
// kernel, so it runs on an AVX2 development machine too. The SSE2 kernel is
// safe to call on any amd64 CPU; gating this on the active tier only meant the
// boundary went unchecked everywhere except one CI step.
//
// This is a dispatch-boundary test, not an overflow test: it proves that the
// strict comparison routes ssdSSE2MaxWidth to the SSE2 kernel and
// ssdSSE2MaxWidth+1 to the scalar kernel, and that both sides agree. Neither
// width comes close to overflowing; see the derivation on ssdSSE2MaxWidth.
func TestFastSSD_SSE2MaxWidthDispatchBoundary(t *testing.T) {
	t.Parallel()

	// Black against white maximizes the per-pixel difference on every channel.
	black := color.NRGBA{A: 255}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	for _, width := range []int{ssdSSE2MaxWidth, ssdSSE2MaxWidth + 1} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			t.Parallel()

			const height = 1

			a := solidColorNRGBA(width, height, black)
			b := solidColorNRGBA(width, height, white)

			got := fastSSD_SSE2(a.Pix, b.Pix, a.Stride, width, height)

			if want := fastSSD_Scalar(a.Pix, b.Pix, a.Stride, width, height); got != want {
				t.Fatalf("width %d: SSE2 SSD = %.0f, scalar = %.0f", width, got, want)
			}

			if exact := float64(width) * float64(height) * 3 * 255 * 255; got != exact {
				t.Fatalf("width %d: SSE2 SSD = %.0f, exact = %.0f", width, got, exact)
			}
		})
	}
}
