//go:build amd64

package renderer

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"testing"

	"golang.org/x/sys/cpu"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

// exactSpanFixture builds a span with two guard pixels on each side, so a
// kernel that writes outside its range is caught rather than tolerated.
func exactSpanFixture(pixels int, source *rand.Rand) []byte {
	pix := make([]byte, (pixels+4)*4)
	for i := range pix {
		pix[i] = byte(source.Intn(256))
	}
	for i := 3; i < len(pix); i += 4 {
		pix[i] = 255
	}
	return pix
}

// exactSpanSizes straddles the two-pixel batch and the dispatch cutoff.
var exactSpanSizes = []int{2, 4, 6, 8, 15, 16, 17, 23, 24, 25, 31, 32, 33, 64, 255, 256, 257, 1024}

// TestCompositeSpanExactSSE2MatchesScalar is the byte-parity test that decides
// whether this kernel is allowed to be the default. Not "within a tolerance":
// the kernel reproduces the scalar op sequence exactly, so any difference at
// all is a defect.
//
// It calls the kernel directly rather than going through compositeOpaqueSpan,
// so the dispatch cutoff cannot hide short spans from it. SSE2 is baseline on
// amd64, so this runs on every host including AVX2 ones, where dispatch would
// never select the kernel.
func TestCompositeSpanExactSSE2MatchesScalar(t *testing.T) {
	source := rand.New(rand.NewSource(20260816))
	colors := []struct{ r, g, b, alpha float64 }{
		{0, 0, 0, 0},
		{1, 1, 1, 1},
		{0.2, 0.6, 0.9, 0.37},
		{0.5, 0.5, 0.5, 0.5},
		{0.13, 0.87, 0.41, 0.02},
		{0.99, 0.01, 0.5, 0.98},
		{1, 0, 0, 1.0 / 255},
	}

	for _, pixels := range exactSpanSizes {
		for _, c := range colors {
			name := fmt.Sprintf("%d/%.2f_%.2f_%.2f_a%.4f", pixels, c.r, c.g, c.b, c.alpha)
			t.Run(name, func(t *testing.T) {
				got := exactSpanFixture(pixels, source)
				want := append([]byte(nil), got...)

				constants := exactSpanConstants(c.r, c.g, c.b, c.alpha)
				compositeSpanExactSSE2(&got[8], pixels/2, &constants[0])
				compositeOpaqueSpanScalar(want, 8, pixels/2*2, c.r, c.g, c.b, c.alpha)

				if !bytes.Equal(got, want) {
					for i := range got {
						if got[i] != want[i] {
							t.Fatalf("byte %d: sse2 = %d, scalar = %d", i, got[i], want[i])
						}
					}
				}
			})
		}
	}
}

// TestCompositeSpanExactSSE2RandomMatchesScalar is the randomized sweep behind
// the fixed colors above. Seven hand-picked colors are not an accuracy
// contract.
func TestCompositeSpanExactSSE2RandomMatchesScalar(t *testing.T) {
	source := rand.New(rand.NewSource(4242))
	for round := range 4000 {
		pixels := 2 * (1 + source.Intn(40))
		r, g, b := source.Float64(), source.Float64(), source.Float64()
		alpha := source.Float64()

		got := exactSpanFixture(pixels, source)
		want := append([]byte(nil), got...)

		constants := exactSpanConstants(r, g, b, alpha)
		compositeSpanExactSSE2(&got[8], pixels/2, &constants[0])
		compositeOpaqueSpanScalar(want, 8, pixels, r, g, b, alpha)

		if !bytes.Equal(got, want) {
			t.Fatalf("round %d (%d px, %v/%v/%v @ %v) differs from scalar", round, pixels, r, g, b, alpha)
		}
	}
}

// TestCompositeSpanExactSSE2PreservesArbitraryAlpha covers the one lane whose
// correctness is arithmetic rather than obvious. Alpha is carried through by
// giving lane 3 identity constants, so it must survive any byte value, not just
// the 255 an opaque canvas happens to hold.
func TestCompositeSpanExactSSE2PreservesArbitraryAlpha(t *testing.T) {
	const pixels = 256
	pix := make([]byte, pixels*4)
	for i := range pix {
		pix[i] = byte(i)
	}
	alphaBefore := make([]byte, 0, pixels)
	for i := 3; i < len(pix); i += 4 {
		alphaBefore = append(alphaBefore, pix[i])
	}

	constants := exactSpanConstants(0.13, 0.57, 0.91, 0.37)
	compositeSpanExactSSE2(&pix[0], pixels/2, &constants[0])

	for i, index := 0, 3; index < len(pix); i, index = i+1, index+4 {
		if pix[index] != alphaBefore[i] {
			t.Fatalf("pixel %d alpha = %d, want %d", i, pix[index], alphaBefore[i])
		}
	}
}

// TestCompositeSpanExactSSE2ZeroPairs guards the early exit and, with the guard
// pixels in the fixture, that nothing outside the span is written.
func TestCompositeSpanExactSSE2ZeroPairs(t *testing.T) {
	pix := exactSpanFixture(4, rand.New(rand.NewSource(11)))
	want := append([]byte(nil), pix...)

	constants := exactSpanConstants(0.3, 0.4, 0.5, 0.6)
	compositeSpanExactSSE2(&pix[8], 0, &constants[0])

	if !bytes.Equal(want, pix) {
		t.Fatal("zero pairs must not modify any byte")
	}
}

// TestCompositeSpanExactFusionContract pins the property this kernel depends on
// and cannot check for itself: that Go's amd64 backend evaluates the scalar
// span's fg + bg*blend as a separate multiply and add, not as a fused
// multiply-add.
//
// If that ever changes, the oracle changes its rounding and the kernel silently
// stops being byte-identical - a divergence that reads as a harmless precision
// artifact and would very likely be waved through rather than investigated. The
// same trap runs the other way on ARM64, where the NEON kernel depends on the
// backend *doing* the fusion; see composite_span.go.
func TestCompositeSpanExactFusionContract(t *testing.T) {
	source := rand.New(rand.NewSource(31337))

	// The property is about the compiled expression, so the check has to be on
	// the doubles. Comparing the finished bytes hides it: the two evaluations
	// differ by an ulp, and *255 plus truncation almost never turns that into a
	// different byte - a search over two million random inputs found none.
	// "Rarely observable" is not "safe": whether a given render trips it depends
	// on the pixel values, so a fused build would produce a mismatch that
	// appears and disappears with the image.
	distinguishing := 0
	for round := 0; round < 100_000 && distinguishing < 50; round++ {
		bg := float64(source.Intn(256)) * inv255
		colour := source.Float64()
		alpha := source.Float64()
		fg := colour * alpha
		blend := 1 - alpha

		if fg+bg*blend != math.FMA(bg, blend, fg) {
			distinguishing++
		}
	}

	if distinguishing == 0 {
		t.Fatal("fg + bg*blend is indistinguishable from math.FMA: the Go amd64 " +
			"backend appears to contract multiply-add, which changes the scalar " +
			"span's rounding and breaks byte parity with compositeSpanExactSSE2")
	}
	t.Logf("multiply-add is not contracted: %d of the sampled inputs differ from math.FMA", distinguishing)
}

// TestCompositeOpaqueSpanDispatchMatchesScalar exercises the dispatcher rather
// than the kernel, including the sub-cutoff spans and the odd-pixel tail.
func TestCompositeOpaqueSpanDispatchMatchesScalar(t *testing.T) {
	source := rand.New(rand.NewSource(99))
	for _, pixels := range []int{1, 2, 3, 5, 7, 23, 24, 25, 33, 129, 1023} {
		t.Run(fmt.Sprintf("%d", pixels), func(t *testing.T) {
			got := exactSpanFixture(pixels, source)
			want := append([]byte(nil), got...)

			compositeOpaqueSpan(got, 8, pixels, 0.2, 0.6, 0.9, 0.37)
			compositeOpaqueSpanScalar(want, 8, pixels, 0.2, 0.6, 0.9, 0.37)

			if !bytes.Equal(got, want) {
				t.Fatalf("dispatched span differs from scalar at %d pixels (kernel %s)", pixels, compositeSpanKernel)
			}
		})
	}
}

// TestCompositeOpaqueSpanPairDispatchMatchesScalar covers the paired path,
// which has its own cutoff branch and its own tail handling.
func TestCompositeOpaqueSpanPairDispatchMatchesScalar(t *testing.T) {
	source := rand.New(rand.NewSource(1234))
	for _, pixels := range []int{1, 2, 23, 24, 25, 65} {
		t.Run(fmt.Sprintf("%d", pixels), func(t *testing.T) {
			stride := (pixels + 8) * 4
			got := make([]byte, stride*2)
			for i := range got {
				got[i] = byte(source.Intn(256))
			}
			for i := 3; i < len(got); i += 4 {
				got[i] = 255
			}
			want := append([]byte(nil), got...)

			compositeOpaqueSpanPair(got, 8, stride+8, pixels, 0.2, 0.6, 0.9, 0.37)
			compositeOpaqueSpanPairScalar(want, 8, stride+8, pixels, 0.2, 0.6, 0.9, 0.37)

			if !bytes.Equal(got, want) {
				t.Fatalf("dispatched pair differs from scalar at %d pixels", pixels)
			}
		})
	}
}

// TestCompositeSpanFollowsForcedTier walks the ladder in one process, which is
// the point of the central tier: it proves this dispatch site reads Tier()
// rather than deciding once at init.
//
// Forcing AVX2 must land on scalar, not on a kernel: there is no exact AVX2
// assembly here, and a switch arm that claims a tier it has no code for is
// exactly the defect this revision replaces.
func TestCompositeSpanFollowsForcedTier(t *testing.T) {
	defer fit.ResetTierDetection()

	source := rand.New(rand.NewSource(7))
	for _, tc := range []struct {
		tier      fit.SIMDTier
		want      fit.SIMDTier
		reachable bool
	}{
		{fit.TierScalar, fit.TierScalar, true},
		{fit.TierSSE2, fit.TierSSE2, true},
		// SetForcedTier panics on a tier the host cannot execute, so this arm
		// only runs where AVX2 exists. It is still worth keeping: it is the
		// assertion that an AVX2 host falls back to scalar rather than to a
		// kernel that does not exist here.
		{fit.TierAVX2, fit.TierScalar, cpu.X86.HasAVX2},
	} {
		t.Run(tc.tier.String(), func(t *testing.T) {
			if !tc.reachable {
				t.Skipf("host CPU cannot execute the %s tier", tc.tier)
			}
			fit.SetForcedTier(tc.tier)

			if compositeSpanKernel != tc.want {
				t.Fatalf("composite span kernel = %s after forcing %s, want %s",
					compositeSpanKernel, tc.tier, tc.want)
			}

			// Every length, so the sub-cutoff branch, the vector body and the
			// odd tail are all exercised at every tier.
			for _, pixels := range []int{1, 2, 3, 23, 24, 25, 33, 512} {
				got := exactSpanFixture(pixels, source)
				want := append([]byte(nil), got...)
				compositeOpaqueSpan(got, 8, pixels, 0.13, 0.57, 0.91, 0.37)
				compositeOpaqueSpanScalar(want, 8, pixels, 0.13, 0.57, 0.91, 0.37)
				if !bytes.Equal(got, want) {
					t.Fatalf("%s dispatch differs from the scalar compositor at %d pixels", tc.tier, pixels)
				}
			}
		})
	}
}

// TestCompositeOpaqueSpanDoesNotAllocate pins what makes the constant block
// affordable at all: it lives on the caller's stack.
//
// exactSpanConstants returns a [20]float64, and the dispatcher hands its address
// to an assembly kernel. That stays free only while the call is direct and the
// declaration carries //go:noescape - route it through a function pointer and
// every composited span mallocs 160 bytes, which costs more than the kernel
// saves.
//
// The tier is forced rather than detected: an AVX2 host installs the scalar
// kernel here, which builds no constant block at all, so an unforced run would
// pass on every development machine without ever touching the thing under test.
func TestCompositeOpaqueSpanDoesNotAllocate(t *testing.T) {
	defer fit.ResetTierDetection()
	fit.SetForcedTier(fit.TierSSE2)
	if compositeSpanKernel != fit.TierSSE2 {
		t.Fatalf("composite span kernel = %s after forcing sse2, want sse2", compositeSpanKernel)
	}

	pix := exactSpanFixture(512, rand.New(rand.NewSource(3)))

	allocs := testing.AllocsPerRun(200, func() {
		compositeOpaqueSpan(pix, 8, 512, 0.13, 0.57, 0.91, 0.37)
		compositeOpaqueSpanPair(pix, 8, 8, 200, 0.13, 0.57, 0.91, 0.37)
	})
	if allocs != 0 {
		t.Fatalf("span dispatch allocated %.1f times per call, want 0", allocs)
	}
}

// BenchmarkCompositeSpanExactCutoff measures the quantity the cutoff constant is
// actually about: the kernel plus the per-span constant setup the dispatcher
// pays for it, against the scalar span at the same length.
//
// It bypasses compositeOpaqueSpan's own cutoff on purpose - benchmarking through
// the dispatcher would only confirm that the current constant does what it says,
// never whether it is in the right place. b.ReportAllocs is not decoration:
// measured through a func value instead of a direct call the kernel looks 5x to
// 9x slower than scalar, and that is the malloc, not the code.
func BenchmarkCompositeSpanExactCutoff(b *testing.B) {
	source := rand.New(rand.NewSource(17))
	for _, pixels := range []int{2, 4, 6, 8, 10, 12, 14, 16, 20, 24, 32, 48, 64} {
		pix := exactSpanFixture(pixels, source)

		b.Run(fmt.Sprintf("scalar/%d", pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))
			for range b.N {
				compositeOpaqueSpanScalar(pix, 8, pixels, 0.13, 0.57, 0.91, 0.37)
			}
		})
		b.Run(fmt.Sprintf("sse2/%d", pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))
			for range b.N {
				constants := exactSpanConstants(0.13, 0.57, 0.91, 0.37)
				compositeSpanExactSSE2(&pix[8], pixels/2, &constants[0])
			}
		})
	}
}

func BenchmarkCompositeSpanExactDirect(b *testing.B) {
	source := rand.New(rand.NewSource(5))
	for _, pixels := range []int{4, 8, 16, 32, 64, 128, 256, 512, 1024} {
		pix := exactSpanFixture(pixels, source)
		constants := exactSpanConstants(0.13, 0.57, 0.91, 0.37)

		b.Run(fmt.Sprintf("scalar/%d", pixels), func(b *testing.B) {
			b.SetBytes(int64(pixels * 4))
			for range b.N {
				compositeOpaqueSpanScalar(pix, 8, pixels, 0.13, 0.57, 0.91, 0.37)
			}
		})
		b.Run(fmt.Sprintf("sse2/%d", pixels), func(b *testing.B) {
			b.SetBytes(int64(pixels * 4))
			for range b.N {
				compositeSpanExactSSE2(&pix[8], pixels/2, &constants[0])
			}
		})
	}
}
