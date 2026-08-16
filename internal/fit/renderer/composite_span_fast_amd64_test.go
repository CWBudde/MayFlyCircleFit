//go:build amd64

package renderer

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// TestCompositeSpanFastSSE2DirectMatchesScalarOracle covers the SSE2 assembly
// body regardless of the host CPU. Dispatch installs SSE2 only when AVX2 is
// absent, so on an AVX2 runner the kernel is never reached through
// compositeOpaqueSpanFast and would otherwise go untested. SSE2 is baseline on
// amd64, so calling it directly is always safe here.
//
// The kernel consumes whole four-pixel batches, reads its constants with
// unaligned 16-byte loads and writes with unaligned 16-byte stores, so the only
// preconditions are batches*4 pixels of storage and four float32 lanes of
// addend and multiplier.
func TestCompositeSpanFastSSE2DirectMatchesScalarOracle(t *testing.T) {
	cases := []struct{ r, g, b, alpha float64 }{
		{0, 0, 0, 0},
		{1, 1, 1, 1},
		{0.2, 0.6, 0.9, 0.37},
		{0.5, 0.5, 0.5, 0.5},
		{0.13, 0.87, 0.41, 0.02},
		{0.99, 0.01, 0.5, 0.98},
	}

	// Only multiples of four: the kernel has no tail handling of its own.
	for _, tc := range cases {
		for _, pixels := range []int{4, 8, 12, 16, 32, 64, 256, 260} {
			want := fastSpanFixture(pixels, uint64(pixels)+0x55e2)
			got := bytes.Clone(want)

			compositeOpaqueSpanFastScalar(want, 4, pixels, tc.r, tc.g, tc.b, tc.alpha)

			addR, addG, addB, mul := fastSpanConstants(tc.r, tc.g, tc.b, tc.alpha)
			addend := [4]float32{addR, addG, addB, 0}
			multiplier := [4]float32{mul, mul, mul, 1}
			compositeSpanFastSSE2(&got[4], pixels/4, &addend[0], &multiplier[0])

			if !bytes.Equal(want, got) {
				t.Fatalf("sse2 direct, pixels=%d, color=(%v,%v,%v,%v):\n scalar=%v\n sse2  =%v",
					pixels, tc.r, tc.g, tc.b, tc.alpha, want, got)
			}
		}
	}
}

func TestCompositeSpanFastSSE2DirectRandomMatchesScalarOracle(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x5e2, 0x73736532))
	const pixels = 256

	for iteration := 0; iteration < 128; iteration++ {
		want := fastSpanFixture(pixels, uint64(iteration))
		got := bytes.Clone(want)

		r, g, b := rng.Float64(), rng.Float64(), rng.Float64()
		alpha := rng.Float64()

		compositeOpaqueSpanFastScalar(want, 4, pixels, r, g, b, alpha)

		addR, addG, addB, mul := fastSpanConstants(r, g, b, alpha)
		addend := [4]float32{addR, addG, addB, 0}
		multiplier := [4]float32{mul, mul, mul, 1}
		compositeSpanFastSSE2(&got[4], pixels/4, &addend[0], &multiplier[0])

		if !bytes.Equal(want, got) {
			t.Fatalf("sse2 direct, iteration %d, color=(%v,%v,%v,%v) mismatch", iteration, r, g, b, alpha)
		}
	}
}

// TestCompositeSpanFastSSE2DirectZeroBatches guards the early exit and, with
// the guard pixels in the fixture, that nothing outside the span is written.
func TestCompositeSpanFastSSE2DirectZeroBatches(t *testing.T) {
	pix := fastSpanFixture(4, 0x2e0)
	want := bytes.Clone(pix)

	addend := [4]float32{1, 2, 3, 0}
	multiplier := [4]float32{0.5, 0.5, 0.5, 1}
	compositeSpanFastSSE2(&pix[4], 0, &addend[0], &multiplier[0])

	if !bytes.Equal(want, pix) {
		t.Fatal("zero batches must not modify any byte")
	}
}
