//go:build arm64

package fit

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/cpu"
)

const ssdNEONDisabledHelper = "CIRCLEFIT_TEST_SSD_NEON_DISABLED"

func TestARM64SIMDDispatchMatchesCPUFeatures(t *testing.T) {
	// The environment overrides outrank the feature check: ASIMD is mandatory
	// on ARM64 and stays reported even when the scalar fallback was requested.
	wantSSD := TierScalar
	if cpu.ARM64.HasASIMD && Tier() == TierNEON {
		wantSSD = TierNEON
	}
	if ActiveSSDKernel() != wantSSD {
		t.Fatalf("SSD kernel = %s, want %s", ActiveSSDKernel(), wantSSD)
	}

	// SAD does not have an ARM64 assembly kernel.
	if ActiveSADKernel() != TierScalar {
		t.Fatalf("SAD kernel = %s, want scalar", ActiveSADKernel())
	}

	a := []uint8{10, 20, 30, 255}
	b := []uint8{12, 18, 33, 0}
	if got, want := fastSSD(a, b, 4, 1, 1), fastSSD_Scalar(a, b, 4, 1, 1); got != want {
		t.Fatalf("SSD dispatch = %v, want %v", got, want)
	}
	if got, want := fastSAD(a, b, 4, 1, 1), fastSAD_Scalar(a, b, 4, 1, 1); got != want {
		t.Fatalf("scalar SAD dispatch = %v, want %v", got, want)
	}
}

// TestARM64SSDKernelPerForcedTier is the in-process ladder walk, the arm64 twin
// of the amd64 test. It needs no subprocess because tier forcing re-runs every
// registered dispatch site.
func TestARM64SSDKernelPerForcedTier(t *testing.T) {
	tiers := []SIMDTier{TierScalar}
	if cpu.ARM64.HasASIMD {
		tiers = append(tiers, TierNEON)
	}

	a := []uint8{0, 10, 20, 255}
	b := []uint8{30, 40, 50, 0}

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

// TestSSDNEONDisabledFallback starts a fresh process because GODEBUG CPU
// overrides are consumed before package initialization. Unlike the forcing test
// above, this one checks detection: that masking the feature bit really moves
// the detected tier down.
//
// cpu.all=off is used because ASIMD is mandatory on ARM64 and is not exposed as
// an individual Go runtime feature override on every supported toolchain.
func TestSSDNEONDisabledFallback(t *testing.T) {
	if os.Getenv(ssdNEONDisabledHelper) == "1" {
		if cpu.ARM64.HasASIMD {
			t.Fatal("cpu.ARM64.HasASIMD is true with GODEBUG=cpu.all=off")
		}
		if Tier() != TierScalar {
			t.Fatalf("detected tier = %s, want scalar", Tier())
		}
		if ActiveSSDKernel() != TierScalar {
			t.Fatalf("SSD kernel = %s, want scalar", ActiveSSDKernel())
		}

		a := []uint8{0, 10, 20, 255}
		b := []uint8{30, 40, 50, 0}
		if got, want := fastSSD(a, b, 4, 1, 1), fastSSD_Scalar(a, b, 4, 1, 1); got != want {
			t.Fatalf("fallback SSD = %v, scalar = %v", got, want)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestSSDNEONDisabledFallback$")
	cmd.Env = ssdNEONDisabledEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("NEON-disabled subprocess failed: %v\n%s", err, output)
	}
}

func ssdNEONDisabledEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+2)
	godebug := "cpu.all=off"
	for _, entry := range environ {
		if strings.HasPrefix(entry, "GODEBUG=") {
			if value := strings.TrimPrefix(entry, "GODEBUG="); value != "" {
				godebug = value + "," + godebug
			}
			continue
		}
		if strings.HasPrefix(entry, ssdNEONDisabledHelper+"=") ||
			strings.HasPrefix(entry, simdTierEnv+"=") ||
			strings.HasPrefix(entry, simdDisableEnv+"=") ||
			strings.HasPrefix(entry, requiredTierEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GODEBUG="+godebug, ssdNEONDisabledHelper+"=1")
}
