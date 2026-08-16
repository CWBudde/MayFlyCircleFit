//go:build amd64

package renderer

import (
	"fmt"
	"math/rand"
	"testing"
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
func TestDeltaSSDSpanSSE2MatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(20_251))
	for _, pixels := range deltaSSDSSE2PixelCounts {
		t.Run(fmt.Sprintf("%d", pixels), func(t *testing.T) {
			for round := 0; round < 8; round++ {
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

// TestDeltaSSDSpanSSE2SelectedWithoutAVX2 documents the dispatch ladder. It only
// asserts when the process actually selected the SSE2 backend.
func TestDeltaSSDSpanSSE2SelectedWithoutAVX2(t *testing.T) {
	if deltaSSDBackend != "sse2" {
		t.Skipf("backend is %q, not sse2", deltaSSDBackend)
	}
	if deltaSSDAVX2Enabled {
		t.Fatal("AVX2 gate is enabled while the backend reports sse2")
	}
	if !deltaSSDSSE2Enabled {
		t.Fatal("SSE2 gate is disabled while the backend reports sse2")
	}
	rng := rand.New(rand.NewSource(20_253))
	candidate := make([]byte, 257*4)
	base := make([]byte, 257*4)
	reference := make([]byte, 257*4)
	_, _ = rng.Read(candidate)
	_, _ = rng.Read(base)
	_, _ = rng.Read(reference)
	for _, pixels := range deltaSSDSSE2PixelCounts {
		got := deltaSSDSpan(candidate, base, reference, pixels)
		want := deltaSSDSpanScalar(candidate, base, reference, pixels)
		if got != want {
			t.Fatalf("pixels=%d: dispatched delta = %d, scalar = %d", pixels, got, want)
		}
	}
}
