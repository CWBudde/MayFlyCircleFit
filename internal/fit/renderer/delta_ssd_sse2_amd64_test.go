//go:build amd64

package renderer

import (
	"fmt"
	"math/rand"
	"strconv"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
	"golang.org/x/sys/cpu"
)

var deltaSSDSSE2PixelCounts = []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 63, 64, 65, 255, 256, 257}

// callDeltaSSDSSE2 invokes the kernel directly, tolerating empty spans the way
// the dispatcher does.
func callDeltaSSDSSE2(candidate, base, reference []byte, pixels int) int64 {
	if pixels == 0 {
		return 0
	}

	return deltaSSDSpanSSE2(&candidate[0], &base[0], &reference[0], pixels)
}

// TestDeltaSSDSpanSSE2MatchesScalar checks byte-exact parity on randomized data.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestDeltaSSDSpanSSE2MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(20_251))

	//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
	for _, pixels := range deltaSSDSSE2PixelCounts {
		t.Run(strconv.Itoa(pixels), func(t *testing.T) {
			for round := range 8 {
				candidate := make([]byte, pixels*4)
				base := make([]byte, pixels*4)
				reference := make([]byte, pixels*4)
				_, _ = rng.Read(candidate)
				_, _ = rng.Read(base)
				_, _ = rng.Read(reference)
				got := callDeltaSSDSSE2(candidate, base, reference, pixels)

				want := deltaSSDSpanScalar(candidate, base, reference, pixels)
				if got != want {
					t.Fatalf("round %d: sse2 delta = %d, scalar = %d", round, got, want)
				}
			}
		})
	}
}

// TestDeltaSSDSpanSSE2IdenticalSpansAreZero checks that an unchanged span
// produces an exactly zero delta, including a nonzero alpha channel.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestDeltaSSDSpanSSE2IdenticalSpansAreZero(t *testing.T) {
	rng := rand.New(rand.NewSource(20_252))

	for _, pixels := range deltaSSDSSE2PixelCounts {
		candidate := make([]byte, pixels*4)
		reference := make([]byte, pixels*4)
		_, _ = rng.Read(candidate)
		_, _ = rng.Read(reference)
		base := make([]byte, pixels*4)
		copy(base, candidate)

		if got := callDeltaSSDSSE2(candidate, base, reference, pixels); got != 0 {
			t.Fatalf("pixels=%d: sse2 delta = %d, want 0", pixels, got)
		}
	}
}

// TestDeltaSSDSpanSSE2SignedExtremes checks maximum-magnitude deltas in both
// directions, where the two accumulators must widen identically.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestDeltaSSDSpanSSE2SignedExtremes(t *testing.T) {
	for _, pixels := range deltaSSDSSE2PixelCounts {
		black := make([]byte, pixels*4)

		white := make([]byte, pixels*4)
		for offset := 0; offset < len(white); offset += 4 {
			white[offset+0] = 255
			white[offset+1] = 255
			white[offset+2] = 255
			white[offset+3] = 128 // Alpha must not contribute.
		}

		want := int64(pixels) * 3 * 255 * 255
		if got := callDeltaSSDSSE2(white, black, black, pixels); got != want {
			t.Fatalf("pixels=%d: positive delta = %d, want %d", pixels, got, want)
		}

		if got := callDeltaSSDSSE2(black, white, black, pixels); got != -want {
			t.Fatalf("pixels=%d: negative delta = %d, want %d", pixels, got, -want)
		}

		if got := deltaSSDSpanScalar(white, black, black, pixels); got != want {
			t.Fatalf("pixels=%d: scalar reference delta = %d, want %d", pixels, got, want)
		}
	}
}

// BenchmarkDeltaSSDSpanSSE2Direct measures the SSE2 kernel against scalar
// independently of which tier the host selected. BenchmarkDeltaSSDSpan only
// times the installed kernel, so on an AVX2 machine the SSE2 kernel was
// unmeasurable - which is how it kept an accumulator strategy the SSD kernel
// next to it had already established was the slower of the two.
func BenchmarkDeltaSSDSpanSSE2Direct(b *testing.B) {
	for _, pixels := range []int{4, 8, 16, 32, 64, 128, 256, 1024} {
		candidate := make([]byte, pixels*4)
		base := make([]byte, pixels*4)
		reference := make([]byte, pixels*4)
		rng := rand.New(rand.NewSource(int64(pixels)))
		_, _ = rng.Read(candidate)
		_, _ = rng.Read(base)
		_, _ = rng.Read(reference)

		b.Run(fmt.Sprintf("sse2/%d", pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 12))

			for range b.N {
				rendererDeltaSink = deltaSSDSpanSSE2(&candidate[0], &base[0], &reference[0], pixels)
			}
		})
	}
}

// TestDeltaSSDSpanSSE2ChunkBoundary covers the wrapper's chunking, which no
// other test reaches: every other span here is far below
// deltaSSDSSE2MaxPixels, so the kernel's int32 accumulators are never asked to
// hand off to the int64 total.
//
// The maximum-difference case is the one that matters. A single lane of a
// 16385-pixel span would carry 16385*130050 = 2130862500, past 2^31, so a
// kernel called without chunking would wrap and this test would catch it.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestDeltaSSDSpanSSE2ChunkBoundary(t *testing.T) {
	//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
	for _, pixels := range []int{
		deltaSSDSSE2MaxPixels - 1,
		deltaSSDSSE2MaxPixels,
		deltaSSDSSE2MaxPixels + 1,
		2*deltaSSDSSE2MaxPixels + 3,
		16385,
	} {
		t.Run(strconv.Itoa(pixels), func(t *testing.T) {
			black := make([]byte, pixels*4)

			white := make([]byte, pixels*4)
			for offset := 0; offset < len(white); offset += 4 {
				white[offset+0] = 255
				white[offset+1] = 255
				white[offset+2] = 255
				white[offset+3] = 128 // Alpha must not contribute.
			}

			want := int64(pixels) * 3 * 255 * 255
			if got := callDeltaSSDSSE2(white, black, black, pixels); got != want {
				t.Fatalf("positive delta = %d, want %d", got, want)
			}

			if got := callDeltaSSDSSE2(black, white, black, pixels); got != -want {
				t.Fatalf("negative delta = %d, want %d", got, -want)
			}

			// And the same span with random content, against scalar.
			rng := rand.New(rand.NewSource(int64(pixels)))
			candidate := make([]byte, pixels*4)
			base := make([]byte, pixels*4)
			reference := make([]byte, pixels*4)
			_, _ = rng.Read(candidate)
			_, _ = rng.Read(base)
			_, _ = rng.Read(reference)

			got := callDeltaSSDSSE2(candidate, base, reference, pixels)
			if scalar := deltaSSDSpanScalar(candidate, base, reference, pixels); got != scalar {
				t.Fatalf("sse2 delta = %d, scalar = %d", got, scalar)
			}
		})
	}
}

// TestDeltaSSDSpanDispatchPerForcedTier walks the whole amd64 ladder through
// the dispatcher in one process. Its predecessor skipped unless the host had
// already selected SSE2, so the SSE2 branch of deltaSSDSpan was never taken on a
// development machine and the ladder's tier ordering went unchecked.
//
//nolint:paralleltest // forces the process-global SIMD tier, which no two tests may do at once
func TestDeltaSSDSpanDispatchPerForcedTier(t *testing.T) {
	tiers := []fit.SIMDTier{fit.TierScalar, fit.TierSSE2}
	if cpu.X86.HasAVX2 {
		tiers = append(tiers, fit.TierAVX2)
	}

	rng := rand.New(rand.NewSource(20_253))
	candidate := make([]byte, 257*4)
	base := make([]byte, 257*4)
	reference := make([]byte, 257*4)
	_, _ = rng.Read(candidate)
	_, _ = rng.Read(base)
	_, _ = rng.Read(reference)

	//nolint:paralleltest // each subtest renders under a forced tier the loop owns
	for _, tier := range tiers {
		t.Run(tier.String(), func(t *testing.T) {
			fit.SetForcedTier(tier)

			defer fit.ResetTierDetection()

			if deltaSSDKernel != tier {
				t.Fatalf("delta-SSD kernel = %s at forced tier %s", deltaSSDKernel, tier)
			}

			for _, pixels := range deltaSSDSSE2PixelCounts {
				got := deltaSSDSpan(candidate, base, reference, pixels)

				want := deltaSSDSpanScalar(candidate, base, reference, pixels)
				if got != want {
					t.Fatalf("pixels=%d: dispatched delta = %d, scalar = %d", pixels, got, want)
				}
			}
		})
	}
}

// TestDeltaSSDSpanUsesSSE2ForShortSpansOnAVX2 pins the ladder's one non-obvious
// property: an AVX2 host still reaches the SSE2 kernel for 4-to-7-pixel spans,
// because the AVX2 kernel needs eight. Falling straight to scalar there was
// leaving the shortest vectorizable spans unvectorized.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestDeltaSSDSpanUsesSSE2ForShortSpansOnAVX2(t *testing.T) {
	if !cpu.X86.HasAVX2 || deltaSSDKernel != fit.TierAVX2 {
		t.Skipf("host tier is %s, not avx2", deltaSSDKernel)
	}

	rng := rand.New(rand.NewSource(20_254))

	for _, pixels := range []int{4, 5, 6, 7} {
		candidate := make([]byte, pixels*4)
		base := make([]byte, pixels*4)
		reference := make([]byte, pixels*4)
		_, _ = rng.Read(candidate)
		_, _ = rng.Read(base)
		_, _ = rng.Read(reference)

		got := deltaSSDSpan(candidate, base, reference, pixels)

		want := callDeltaSSDSSE2(candidate, base, reference, pixels)
		if got != want {
			t.Fatalf("pixels=%d: dispatched delta = %d, sse2 kernel = %d", pixels, got, want)
		}

		if scalar := deltaSSDSpanScalar(candidate, base, reference, pixels); got != scalar {
			t.Fatalf("pixels=%d: dispatched delta = %d, scalar = %d", pixels, got, scalar)
		}
	}
}
