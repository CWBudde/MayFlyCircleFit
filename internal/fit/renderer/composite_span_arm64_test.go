//go:build arm64

package renderer

import (
	"bytes"
	"fmt"
	"image"
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

// TestRenderCircleRowsDoesNotAllocate pins the compositor's blend scalars to
// the render goroutine's stack, one call level above where
// TestCompositeOpaqueSpanDoesNotAllocate looks.
//
// It is the ARM64 half of the guard the amd64 hoist added. That test only
// enters compositeOpaqueSpan directly, so it sees the frames in
// composite_span_arm64.go and nothing else. Now that the blend is built by the
// per-circle caller in renderer_cpu.go, the frame that owns it is not on that
// test's call path at all, and a blend that escaped there would cost one heap
// allocation per circle per shard while every existing guard still passed.
//
// Not parallel: it forces the process-global SIMD tier, so concurrent subtests
// would race fit.SetForcedTier against every other dispatch site.
//
//nolint:paralleltest // forces the process-global SIMD tier
func TestRenderCircleRowsDoesNotAllocate(t *testing.T) {
	defer fit.ResetTierDetection()

	const (
		width  = 129
		height = 97
	)

	for _, forced := range []struct {
		tier      fit.SIMDTier
		reachable bool
	}{
		{fit.TierScalar, true},
		{fit.TierNEON, cpu.ARM64.HasASIMD},
	} {
		t.Run(forced.tier.String(), func(t *testing.T) {
			if !forced.reachable {
				t.Skipf("host CPU cannot execute the %s tier", forced.tier)
			}

			fit.SetForcedTier(forced.tier)

			reference := image.NewNRGBA(image.Rect(0, 0, width, height))
			renderer := NewCPURenderer(reference, 1)

			if !renderer.opaqueCanvas {
				t.Fatal("default canvas is not opaque, so the exact span path is unreachable")
			}

			canvas := image.NewNRGBA(image.Rect(0, 0, width, height))
			circle := fit.Circle{X: 64.25, Y: 48.75, R: 40.5, CR: 0.13, CG: 0.57, CB: 0.91, Opacity: 0.37}

			allocs := testing.AllocsPerRun(200, func() {
				renderer.renderCircleScanlineRowsTracked(canvas, circle, 0, height, nil)
			})
			if allocs != 0 {
				t.Fatalf("circle row walk allocated %.1f times per call at the %s tier, want 0", allocs, forced.tier)
			}
		})
	}
}

// TestSpanBlendSurvivesTierChange pins spanBlend as tier-independent state, the
// ARM64 half of the amd64 guard of the same name.
//
// The blend is four float64s the NEON kernel takes as arguments, and production
// reuses one blend for a whole circle. The failure this guards is the adjacent
// change nothing else would catch: caching compositeSpanKernel or the cutoff
// into the blend when it is built. TestCompositeSpanFollowsForcedTier and
// TestRendererKernelsFollowForcedTier both inspect the package variables, so a
// stale copy inside a blend would pass them while silently pinning one circle to
// the tier that was current when it began.
//
//nolint:paralleltest // forces the process-global SIMD tier
func TestSpanBlendSurvivesTierChange(t *testing.T) {
	defer fit.ResetTierDetection()

	const (
		r     = 0.13
		g     = 0.57
		blue  = 0.91
		alpha = 0.37
	)

	// Byte parity alone cannot catch a cached tier, because the NEON kernel is
	// byte-identical to the scalar span by construction - that is the whole
	// premise of docs/exact-span-compositors.md. So assert the structural
	// property first: two blends for the same colour, built under different
	// forced tiers, must be identical. A tier or a cutoff smuggled into the
	// struct makes them differ, and spanBlend is comparable precisely so this
	// can be checked.
	fit.SetForcedTier(fit.TierScalar)

	blend := newSpanBlend(r, g, blue, alpha)

	if cpu.ARM64.HasASIMD {
		fit.SetForcedTier(fit.TierNEON)

		if neonBlend := newSpanBlend(r, g, blue, alpha); neonBlend != blend {
			t.Fatal("spanBlend depends on the tier that was current when it was built")
		}
	}

	// 512 is above compositeSpanNEONMinPixels and 300 leaves a tail the scalar
	// fallback finishes, so both halves of the dispatch read the reused blend.
	for _, forced := range []struct {
		tier      fit.SIMDTier
		reachable bool
	}{
		{fit.TierScalar, true},
		{fit.TierNEON, cpu.ARM64.HasASIMD},
		{fit.TierScalar, true},
	} {
		if !forced.reachable {
			continue
		}

		fit.SetForcedTier(forced.tier)

		if compositeSpanKernel != forced.tier {
			t.Fatalf("composite span kernel = %s after forcing %s", compositeSpanKernel, forced.tier)
		}

		for _, pixels := range []int{8, 255, 256, 300, 512} {
			got := makeOpaqueSpanFixture(pixels)
			want := append([]byte(nil), got...)

			compositeOpaqueSpan(&blend, got, 0, pixels, r, g, blue, alpha)
			compositeOpaqueSpanScalar(want, 0, pixels, r, g, blue, alpha)

			if !bytes.Equal(got, want) {
				t.Fatalf("reused blend differs from scalar at the %s tier, %d pixels", forced.tier, pixels)
			}
		}
	}
}

// BenchmarkCompositeOpaqueSpanNEONCutoff is the command that re-derives
// compositeSpanNEONMinPixels on ARM64 benchmarking hardware.
//
// The `hoisted` arm is what the dispatcher pays for now - the four blend
// scalars are built once per circle and every span of every row reads them - and
// `rebuilt` is what 256 was measured against, so the difference between them is
// the per-span setup the hoist removed. `scalar` is the reference each has to
// beat. The kernel is called by name rather than through a func value, because
// an indirect call defeats //go:noescape; docs/exact-span-compositors.md records
// that mistake making a kernel measure five to nine times slower than scalar.
//
// Emulated timings do not count. Run it on real ARM64 silicon, pinned, at
// GOMAXPROCS=1, and take the median of several runs.
func BenchmarkCompositeOpaqueSpanNEONCutoff(b *testing.B) {
	if !cpu.ARM64.HasASIMD {
		b.Skip("NEON unavailable")
	}

	const (
		r     = 0.13
		g     = 0.57
		blue  = 0.91
		alpha = 0.37
	)

	for _, pixels := range []int{8, 16, 24, 32, 64, 128, 192, 256, 512} {
		pix := makeOpaqueSpanFixture(pixels)
		blend := newSpanBlend(r, g, blue, alpha)

		b.Run(fmt.Sprintf("scalar/%d", pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))

			for range b.N {
				compositeOpaqueSpanScalar(pix, 0, pixels, r, g, blue, alpha)
			}
		})
		b.Run(fmt.Sprintf("neon_hoisted/%d", pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))

			for range b.N {
				compositeOpaqueSpanNEON(
					unsafe.Pointer(&pix[0]), pixels, blend.fgR, blend.fgG, blend.fgB, blend.bgBlend)
			}
		})
		b.Run(fmt.Sprintf("neon_rebuilt/%d", pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))

			for range b.N {
				perSpan := newSpanBlend(r, g, blue, alpha)
				compositeOpaqueSpanNEON(
					unsafe.Pointer(&pix[0]), pixels,
					perSpan.fgR, perSpan.fgG, perSpan.fgB, perSpan.bgBlend)
			}
		})
	}
}
