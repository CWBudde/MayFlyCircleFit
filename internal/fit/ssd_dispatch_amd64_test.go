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
	ssdAVX2DisabledHelper = "MAYFLY_TEST_SSD_AVX2_DISABLED"
	ssdSIMDDisabledHelper = "MAYFLY_TEST_SSD_SIMD_DISABLED"
)

// TestSSDAVX2DisabledFallback starts a fresh test process because GODEBUG CPU
// overrides are consumed before package initialization and cannot be tested by
// changing the environment in the current process.
//
// Without AVX2 the amd64 tier below it is SSE2, not scalar.
func TestSSDAVX2DisabledFallback(t *testing.T) {
	if os.Getenv(ssdAVX2DisabledHelper) == "1" {
		if cpu.X86.HasAVX2 {
			t.Fatal("cpu.X86.HasAVX2 is true with GODEBUG=cpu.avx2=off")
		}
		if ActiveSSDBackend != SSDBackendSSE2 {
			t.Fatalf("SSD backend = %s, want SSE2", ActiveSSDBackend)
		}

		a := []uint8{0, 10, 20, 255}
		b := []uint8{30, 40, 50, 0}
		if got, want := fastSSD(a, b, 4, 1, 1), fastSSD_Scalar(a, b, 4, 1, 1); got != want {
			t.Fatalf("fallback SSD = %v, scalar = %v", got, want)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestSSDAVX2DisabledFallback$")
	cmd.Env = ssdAVX2DisabledEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("AVX2-disabled subprocess failed: %v\n%s", err, output)
	}
}

// TestSSDSIMDDisabledFallback verifies that MAYFLY_DISABLE_SIMD=1 reaches the
// scalar kernel. GODEBUG=cpu.all=off cannot express this on amd64 because SSE2
// is a required feature there. It re-execs for the same reason as the test
// above: dispatch reads the environment once during package initialization.
func TestSSDSIMDDisabledFallback(t *testing.T) {
	if os.Getenv(ssdSIMDDisabledHelper) == "1" {
		if !SIMDDisabledByEnv() {
			t.Fatalf("%s is not observed as set", simdDisableEnv)
		}
		if ActiveSSDBackend != SSDBackendScalar {
			t.Fatalf("SSD backend = %s, want scalar", ActiveSSDBackend)
		}

		a := []uint8{0, 10, 20, 255}
		b := []uint8{30, 40, 50, 0}
		if got, want := fastSSD(a, b, 4, 1, 1), fastSSD_Scalar(a, b, 4, 1, 1); got != want {
			t.Fatalf("fallback SSD = %v, scalar = %v", got, want)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestSSDSIMDDisabledFallback$")
	cmd.Env = ssdSIMDDisabledEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("SIMD-disabled subprocess failed: %v\n%s", err, output)
	}
}

func ssdAVX2DisabledEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+2)
	godebug := "cpu.avx2=off"
	for _, entry := range environ {
		if strings.HasPrefix(entry, "GODEBUG=") {
			if value := strings.TrimPrefix(entry, "GODEBUG="); value != "" {
				godebug = value + "," + godebug
			}
			continue
		}
		if strings.HasPrefix(entry, ssdAVX2DisabledHelper+"=") {
			continue
		}
		if strings.HasPrefix(entry, simdDisableEnv+"=") {
			continue
		}
		if strings.HasPrefix(entry, requiredSSDBackendEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GODEBUG="+godebug, ssdAVX2DisabledHelper+"=1")
}

func ssdSIMDDisabledEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+2)
	for _, entry := range environ {
		if strings.HasPrefix(entry, ssdSIMDDisabledHelper+"=") {
			continue
		}
		if strings.HasPrefix(entry, simdDisableEnv+"=") {
			continue
		}
		if strings.HasPrefix(entry, requiredSSDBackendEnv+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, simdDisableEnv+"=1", ssdSIMDDisabledHelper+"=1")
}

// TestFastSSD_SSE2MaxWidthDispatchBoundary exercises the width > ssdSSE2MaxWidth
// branch in fastSSD_SSE2. No other test reaches it, because the rest of the
// suite stops at width 512.
//
// This is a dispatch-boundary test, not an overflow test: it proves that the
// strict comparison routes ssdSSE2MaxWidth to the SSE2 kernel and
// ssdSSE2MaxWidth+1 to the scalar kernel, and that both sides agree. The
// constant is deliberately about 3x conservative, so neither width comes close
// to overflowing. A maximum-difference row totals width*3*255*255 =
// width*195075, which stays inside int32 up to width 11008, and no single lane
// even holds that aggregate: PMADDWD pairwise-adds into (R^2+G^2) and B^2
// dwords, so the busiest lane carries at most width*65025 and would not
// overflow until roughly width 33026.
func TestFastSSD_SSE2MaxWidthDispatchBoundary(t *testing.T) {
	if ActiveSSDBackend != SSDBackendSSE2 {
		t.Skipf("Skipping SSE2 dispatch boundary test: active backend is %s, not SSE2", ActiveSSDBackend)
	}

	// Black against white maximizes the per-pixel difference on every channel.
	black := color.NRGBA{A: 255}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}

	for _, width := range []int{ssdSSE2MaxWidth, ssdSSE2MaxWidth + 1} {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			const height = 1

			a := solidColorNRGBA(width, height, black)
			b := solidColorNRGBA(width, height, white)

			got := fastSSD(a.Pix, b.Pix, a.Stride, width, height)

			if want := fastSSD_Scalar(a.Pix, b.Pix, a.Stride, width, height); got != want {
				t.Fatalf("width %d: dispatched SSD = %.0f, scalar = %.0f", width, got, want)
			}
			if exact := float64(width) * float64(height) * 3 * 255 * 255; got != exact {
				t.Fatalf("width %d: dispatched SSD = %.0f, exact = %.0f", width, got, exact)
			}
		})
	}
}
