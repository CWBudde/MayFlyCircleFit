//go:build arm64

package renderer

import (
	"bytes"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"testing"
	"unsafe"

	"github.com/cwbudde/circlefit/internal/fit"
	"golang.org/x/sys/cpu"
)

const compositeNEONDisabledHelper = "CIRCLEFIT_TEST_COMPOSITE_NEON_DISABLED"

func TestCompositeSpanARM64DispatchMatchesCPUFeatures(t *testing.T) {
	// The environment overrides outrank the feature check: ASIMD is mandatory
	// on ARM64 and stays reported even when the scalar fallback was requested.
	want := fit.TierScalar
	if cpu.ARM64.HasASIMD && fit.Tier() == fit.TierNEON {
		want = fit.TierNEON
	}
	if compositeSpanKernel != want {
		t.Fatalf("composite span kernel = %s, want %s", compositeSpanKernel, want)
	}
}

// TestCompositeSpanFollowsForcedTier proves the span compositor is wired to the
// same tier switch as every other kernel, and that the scalar fallback it
// reaches is byte-identical. The subprocess test below still earns its keep: it
// covers detection under GODEBUG, which forcing cannot.
func TestCompositeSpanFollowsForcedTier(t *testing.T) {
	fit.SetForcedTier(fit.TierScalar)
	defer fit.ResetTierDetection()

	if compositeSpanKernel != fit.TierScalar {
		t.Fatalf("composite span kernel = %s after forcing scalar", compositeSpanKernel)
	}

	// 512 pixels is above compositeSpanNEONMinPixels, so an unforced host would
	// have taken the NEON path here.
	got := makeOpaqueSpanFixture(512)
	want := append([]byte(nil), got...)
	blend := newSpanBlend(0.13, 0.57, 0.91, 0.37)
	compositeOpaqueSpan(&blend, got, 0, 512, 0.13, 0.57, 0.91, 0.37)
	compositeOpaqueSpanScalar(want, 0, 512, 0.13, 0.57, 0.91, 0.37)
	if !bytes.Equal(got, want) {
		t.Fatal("forced-scalar span dispatch differs from the scalar compositor")
	}
}

// TestCompositeSpanNEONMatchesScalar is the byte-exactness contract for the
// NEON kernel itself. Until this test existed nothing compared the kernel's
// output to compositeOpaqueSpanScalar: the two tests above both force the
// scalar tier, so they compare the scalar path against itself and would stay
// green if the kernel drifted arbitrarily.
//
// The colour distribution is the whole test. The kernel and the scalar
// reference differ only when a product lands on a half-integer boundary, and
// uniformly random float colours essentially never do - an earlier version of
// this sweep used them and found zero mismatches in 51.2 million evaluations
// against a reference that was genuinely wrong. Colours the renderer actually
// receives are k/255, which hit those boundaries often enough that the same
// defect shows up in a few thousand bytes per run. Keep the k/255 sampling.
func TestCompositeSpanNEONMatchesScalar(t *testing.T) {
	if fit.Tier() != fit.TierNEON {
		t.Skipf("detected tier is %s, so the NEON kernel is not the one under test", fit.Tier())
	}

	source := rand.New(rand.NewPCG(0x9e37, 0x1234))

	// Above compositeSpanNEONMinPixels, so the kernel handles the whole span
	// apart from the tail the scalar fallback finishes.
	const pixels = 512

	for trial := range 512 {
		got := makeOpaqueSpanFixture(pixels)
		want := append([]byte(nil), got...)

		r := float64(source.IntN(256)) / 255
		g := float64(source.IntN(256)) / 255
		b := float64(source.IntN(256)) / 255
		alpha := float64(source.IntN(101)) / 100

		blend := newSpanBlend(r, g, b, alpha)
		compositeOpaqueSpan(&blend, got, 0, pixels, r, g, b, alpha)
		compositeOpaqueSpanScalar(want, 0, pixels, r, g, b, alpha)

		if !bytes.Equal(got, want) {
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("trial %d: NEON byte %d = %d, scalar = %d (rgba %g,%g,%g,%g)",
						trial, i, got[i], want[i], r, g, b, alpha)
				}
			}
		}
	}
}

func TestCompositeSpanNEONDisabledFallback(t *testing.T) {
	if os.Getenv(compositeNEONDisabledHelper) == "1" {
		if cpu.ARM64.HasASIMD {
			t.Fatal("cpu.ARM64.HasASIMD is true with GODEBUG=cpu.all=off")
		}
		if compositeSpanKernel != fit.TierScalar {
			t.Fatalf("composite span kernel = %s, want scalar", compositeSpanKernel)
		}
		got := makeOpaqueSpanFixture(256)
		want := append([]byte(nil), got...)
		blend := newSpanBlend(0.13, 0.57, 0.91, 0.37)
		compositeOpaqueSpan(&blend, got, 0, 256, 0.13, 0.57, 0.91, 0.37)
		compositeOpaqueSpanScalar(want, 0, 256, 0.13, 0.57, 0.91, 0.37)
		if !bytes.Equal(got, want) {
			t.Fatal("disabled NEON dispatch differs from scalar")
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestCompositeSpanNEONDisabledFallback$")
	cmd.Env = compositeNEONDisabledEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("NEON-disabled subprocess failed: %v\n%s", err, output)
	}
}

func BenchmarkCompositeOpaqueSpanNEONKernel(b *testing.B) {
	if !cpu.ARM64.HasASIMD {
		b.Skip("NEON unavailable")
	}
	for _, pixels := range []int{8, 16, 64, 256} {
		b.Run(benchmarkSpanSizeName(pixels), func(b *testing.B) {
			pix := makeOpaqueSpanFixture(pixels)
			const r, g, blue, alpha = 0.13, 0.57, 0.91, 0.37
			fgR, fgG, fgB, bgBlend := r*alpha, g*alpha, blue*alpha, 1-alpha
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				compositeOpaqueSpanNEON(unsafe.Pointer(&pix[0]), pixels, fgR, fgG, fgB, bgBlend)
			}
		})
	}
}

func benchmarkSpanSizeName(pixels int) string {
	switch pixels {
	case 8:
		return "8"
	case 16:
		return "16"
	case 64:
		return "64"
	case 256:
		return "256"
	default:
		return "other"
	}
}

func compositeNEONDisabledEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+2)
	godebug := "cpu.all=off"
	for _, entry := range environ {
		if strings.HasPrefix(entry, "GODEBUG=") {
			if value := strings.TrimPrefix(entry, "GODEBUG="); value != "" {
				godebug = value + "," + godebug
			}
			continue
		}
		if strings.HasPrefix(entry, compositeNEONDisabledHelper+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GODEBUG="+godebug, compositeNEONDisabledHelper+"=1")
}
