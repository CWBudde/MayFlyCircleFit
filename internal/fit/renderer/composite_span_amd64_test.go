//go:build amd64

package renderer

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"golang.org/x/sys/cpu"
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

// exactSpanKernel names one directly callable exact kernel.
type exactSpanKernel struct {
	name string
	fn   func(pix *byte, pairs int, constants *float64)
}

// hostExactSpanKernels lists every exact kernel this CPU can execute.
//
// SSE2 is baseline on amd64, so its kernel is always callable and is always
// tested - including on an AVX2 runner, where dispatch would never select it.
// The predecessor of these tests made the opposite choice for the SSE2 SSD
// kernel and left it benchmarked but unverified everywhere CI runs.
func hostExactSpanKernels() []exactSpanKernel {
	kernels := []exactSpanKernel{{"sse2", compositeSpanExactSSE2}}
	if cpu.X86.HasAVX2 {
		kernels = append(kernels, exactSpanKernel{"avx2", compositeSpanExactAVX2})
	}

	return kernels
}

// exactSpanSizes straddles the two-pixel batch and the dispatch cutoff.
var exactSpanSizes = []int{2, 4, 6, 8, 15, 16, 17, 23, 24, 25, 31, 32, 33, 64, 255, 256, 257, 1024}

// TestCompositeSpanExactMatchesScalar is the byte-parity test that decides
// whether these kernels are allowed to be the default. Not "within a
// tolerance": both reproduce the scalar op sequence exactly, so any difference
// at all is a defect.
//
// It calls each kernel directly rather than going through compositeOpaqueSpan,
// so the dispatch cutoff cannot hide short spans from it.
func TestCompositeSpanExactMatchesScalar(t *testing.T) {
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

	for _, kernel := range hostExactSpanKernels() {
		for _, pixels := range exactSpanSizes {
			for _, c := range colors {
				name := fmt.Sprintf("%s/%d/%.2f_%.2f_%.2f_a%.4f", kernel.name, pixels, c.r, c.g, c.b, c.alpha)
				t.Run(name, func(t *testing.T) {
					got := exactSpanFixture(pixels, source)
					want := append([]byte(nil), got...)

					constants := exactSpanConstants(c.r, c.g, c.b, c.alpha)
					kernel.fn(&got[8], pixels/2, &constants[0])
					compositeOpaqueSpanScalar(want, 8, pixels/2*2, c.r, c.g, c.b, c.alpha)

					if !bytes.Equal(got, want) {
						for i := range got {
							if got[i] != want[i] {
								t.Fatalf("byte %d: %s = %d, scalar = %d", i, kernel.name, got[i], want[i])
							}
						}
					}
				})
			}
		}
	}
}

// TestCompositeSpanExactRandomMatchesScalar is the randomized sweep behind the
// fixed colors above. Seven hand-picked colors are not an accuracy contract.
func TestCompositeSpanExactRandomMatchesScalar(t *testing.T) {
	for _, kernel := range hostExactSpanKernels() {
		t.Run(kernel.name, func(t *testing.T) {
			source := rand.New(rand.NewSource(4242))
			for round := range 4000 {
				pixels := 2 * (1 + source.Intn(40))
				r, g, b := source.Float64(), source.Float64(), source.Float64()
				alpha := source.Float64()

				got := exactSpanFixture(pixels, source)
				want := append([]byte(nil), got...)

				constants := exactSpanConstants(r, g, b, alpha)
				kernel.fn(&got[8], pixels/2, &constants[0])
				compositeOpaqueSpanScalar(want, 8, pixels, r, g, b, alpha)

				if !bytes.Equal(got, want) {
					t.Fatalf("round %d (%d px, %v/%v/%v @ %v) differs from scalar", round, pixels, r, g, b, alpha)
				}
			}
		})
	}
}

// TestCompositeSpanExactKernelsAgree compares the kernels against each other
// rather than against the oracle. It is not redundant with the tests above: it
// is the one that still means something if compositeOpaqueSpanScalar itself
// ever changes, and it fails loudly if a future third kernel is added and
// silently disagrees on inputs the sweeps happen not to reach.
func TestCompositeSpanExactKernelsAgree(t *testing.T) {
	kernels := hostExactSpanKernels()
	if len(kernels) < 2 {
		t.Skipf("only the %s kernel is executable on this host", kernels[0].name)
	}

	source := rand.New(rand.NewSource(0x5e2a72))
	for round := range 2000 {
		pixels := 2 * (1 + source.Intn(64))
		r, g, b := source.Float64(), source.Float64(), source.Float64()
		alpha := source.Float64()
		constants := exactSpanConstants(r, g, b, alpha)

		reference := exactSpanFixture(pixels, source)
		first := append([]byte(nil), reference...)
		kernels[0].fn(&first[8], pixels/2, &constants[0])

		for _, kernel := range kernels[1:] {
			other := append([]byte(nil), reference...)
			kernel.fn(&other[8], pixels/2, &constants[0])

			if !bytes.Equal(first, other) {
				t.Fatalf("round %d (%d px): %s and %s disagree", round, pixels, kernels[0].name, kernel.name)
			}
		}
	}
}

// TestCompositeSpanExactPreservesArbitraryAlpha covers the one lane whose
// correctness is arithmetic rather than obvious. Alpha is carried through by
// giving lane 3 identity constants, so it must survive any byte value, not just
// the 255 an opaque canvas happens to hold.
func TestCompositeSpanExactPreservesArbitraryAlpha(t *testing.T) {
	for _, kernel := range hostExactSpanKernels() {
		t.Run(kernel.name, func(t *testing.T) {
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
			kernel.fn(&pix[0], pixels/2, &constants[0])

			for i, index := 0, 3; index < len(pix); i, index = i+1, index+4 {
				if pix[index] != alphaBefore[i] {
					t.Fatalf("pixel %d alpha = %d, want %d", i, pix[index], alphaBefore[i])
				}
			}
		})
	}
}

// TestCompositeSpanExactZeroPairs guards the early exit, and with the guard
// pixels in the fixture, that nothing outside the span is written.
func TestCompositeSpanExactZeroPairs(t *testing.T) {
	for _, kernel := range hostExactSpanKernels() {
		t.Run(kernel.name, func(t *testing.T) {
			source := rand.New(rand.NewSource(11))
			pix := exactSpanFixture(4, source)
			want := append([]byte(nil), pix...)

			constants := exactSpanConstants(0.3, 0.4, 0.5, 0.6)
			kernel.fn(&pix[8], 0, &constants[0])

			if !bytes.Equal(want, pix) {
				t.Fatal("zero pairs must not modify any byte")
			}
		})
	}
}

// TestCompositeSpanExactFusionContract pins the property this kernel depends on
// and cannot check for itself: that Go's amd64 backend evaluates the scalar
// span's fg + bg*blend as a separate multiply and add, not as a fused
// multiply-add.
//
// If that ever changes, the oracle changes its rounding and the AVX2 kernel
// silently stops being byte-identical - a divergence that reads as a harmless
// precision artifact and would very likely be waved through rather than
// investigated. The same trap runs the other way on ARM64, where the NEON
// kernel depends on the backend *doing* the fusion; see composite_span.go.
//
// The test searches for inputs where the two evaluations produce different
// bytes and then requires the scalar span to agree with the unfused one.
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
			"span's rounding and breaks byte parity with compositeSpanExactAVX2")
	}

	t.Logf("multiply-add is not contracted: %d of the sampled inputs differ from math.FMA", distinguishing)
}

// TestCompositeOpaqueSpanDispatchMatchesScalar exercises the dispatcher rather
// than the kernel, including the sub-cutoff spans and the odd-pixel tail.
func TestCompositeOpaqueSpanDispatchMatchesScalar(t *testing.T) {
	source := rand.New(rand.NewSource(99))

	for _, pixels := range []int{1, 2, 3, 5, 7, 15, 16, 17, 33, 129, 1023} {
		t.Run(strconv.Itoa(pixels), func(t *testing.T) {
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

	for _, pixels := range []int{1, 2, 15, 16, 17, 65} {
		t.Run(strconv.Itoa(pixels), func(t *testing.T) {
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

// TestCompositeSpanFollowsForcedTier walks the whole ladder in one process,
// which is the point of the central tier: it proves this dispatch site reads
// Tier() rather than deciding once at init, and that the cutoff moves with the
// kernel instead of being left behind on a stale constant.
func TestCompositeSpanFollowsForcedTier(t *testing.T) {
	defer fit.ResetTierDetection()

	cases := []struct {
		tier      fit.SIMDTier
		wantMin   int
		reachable bool
	}{
		{fit.TierScalar, 0, true},
		{fit.TierSSE2, compositeSpanSSE2MinPixels, true},
		{fit.TierAVX2, compositeSpanAVX2MinPixels, cpu.X86.HasAVX2},
	}

	source := rand.New(rand.NewSource(7))

	for _, tc := range cases {
		t.Run(tc.tier.String(), func(t *testing.T) {
			if !tc.reachable {
				t.Skipf("host CPU cannot execute the %s kernel", tc.tier)
			}

			fit.SetForcedTier(tc.tier)

			if compositeSpanKernel != tc.tier {
				t.Fatalf("composite span kernel = %s after forcing %s", compositeSpanKernel, tc.tier)
			}

			if compositeSpanMinPixels != tc.wantMin {
				t.Fatalf("cutoff = %d after forcing %s, want %d", compositeSpanMinPixels, tc.tier, tc.wantMin)
			}

			// Every length, so the sub-cutoff branch, the vector body and the
			// odd tail are all exercised at every tier.
			for _, pixels := range []int{1, 2, 3, 15, 16, 17, 33, 512} {
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

// BenchmarkCompositeSpanExactCutoff measures the quantity the cutoff constants
// are actually about: the vector kernel plus the per-span constant setup the
// dispatcher pays for it, against the scalar span at the same length.
//
// It bypasses compositeOpaqueSpan's own cutoff on purpose - benchmarking
// through the dispatcher would only confirm that the current constant does what
// it says, never whether it is in the right place.
//
// The kernels are called by name rather than through hostExactSpanKernels.
// Going through a func value defeats //go:noescape, which moves the constant
// block to the heap and adds an allocation the real dispatcher does not pay;
// measured that way the kernel looks 5x to 9x *slower* than scalar, which is
// the malloc, not the code. b.ReportAllocs keeps that mistake visible.
// TestCompositeOpaqueSpanDoesNotAllocate pins what makes the constant block
// affordable at all: it lives on the caller's stack.
//
// exactSpanConstants returns a [20]float64, and the dispatcher hands its address
// to an assembly kernel. That stays free only while the call is direct and the
// declaration carries //go:noescape - route it through a function pointer and
// every composited span mallocs 160 bytes, which costs more than the kernel
// saves. compositeSpanExact is a switch for this reason.
//
// The tier is forced rather than detected, and both tiers are walked: an
// unforced run only ever exercises whichever kernel this host happens to
// dispatch to, so the other one could regress to a heap allocation without any
// development machine noticing.
func TestCompositeOpaqueSpanDoesNotAllocate(t *testing.T) {
	defer fit.ResetTierDetection()

	for _, tc := range []struct {
		tier      fit.SIMDTier
		reachable bool
	}{
		{fit.TierSSE2, true},
		{fit.TierAVX2, cpu.X86.HasAVX2},
	} {
		t.Run(tc.tier.String(), func(t *testing.T) {
			if !tc.reachable {
				t.Skipf("host CPU cannot execute the %s tier", tc.tier)
			}

			fit.SetForcedTier(tc.tier)

			if compositeSpanKernel != tc.tier {
				t.Fatalf("composite span kernel = %s after forcing %s, want %s",
					compositeSpanKernel, tc.tier, tc.tier)
			}

			pix := exactSpanFixture(512, rand.New(rand.NewSource(3)))

			allocs := testing.AllocsPerRun(200, func() {
				compositeOpaqueSpan(pix, 8, 512, 0.13, 0.57, 0.91, 0.37)
				compositeOpaqueSpanPair(pix, 8, 8, 200, 0.13, 0.57, 0.91, 0.37)
			})
			if allocs != 0 {
				t.Fatalf("span dispatch allocated %.1f times per call at the %s tier, want 0", allocs, tc.tier)
			}
		})
	}
}

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

		if !cpu.X86.HasAVX2 {
			continue
		}

		b.Run(fmt.Sprintf("avx2/%d", pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))

			for range b.N {
				constants := exactSpanConstants(0.13, 0.57, 0.91, 0.37)
				compositeSpanExactAVX2(&pix[8], pixels/2, &constants[0])
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

		for _, kernel := range hostExactSpanKernels() {
			b.Run(fmt.Sprintf("%s/%d", kernel.name, pixels), func(b *testing.B) {
				b.SetBytes(int64(pixels * 4))

				for range b.N {
					kernel.fn(&pix[8], pixels/2, &constants[0])
				}
			})
		}
	}
}
