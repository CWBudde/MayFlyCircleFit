//go:build amd64

package renderer

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
	"golang.org/x/sys/cpu"
)

// TestFastCompositeKernelMatchesTier pins the kernel the fast compositor
// installs against the resolved tier.
//
// Its predecessor re-typed the init function it was testing - it read the same
// package variables the switch had just written and compared them to a
// transcription of that switch - and its doc comment claimed to pin the tier
// "compositeOpaqueSpanFast actually enters" while never calling that function.
// This version does call it, and compares against the kernel invoked directly.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestFastCompositeKernelMatchesTier(t *testing.T) {
	if got := fastCompositeKernel; got != fit.Tier() {
		t.Fatalf("fast composite kernel = %s, tier = %s", got, fit.Tier())
	}

	const pixels = 64
	const r, g, b, alpha = 0.2, 0.6, 0.9, 0.37

	got := fastSpanFixture(pixels, 0xfa57)
	want := bytes.Clone(got)
	compositeOpaqueSpanFast(got, 4, pixels, r, g, b, alpha)

	addR, addG, addB, mul := fastSpanConstants(r, g, b, alpha)

	switch fastCompositeKernel {
	case fit.TierAVX2:
		addend := [8]float32{addR, addG, addB, 0, addR, addG, addB, 0}
		multiplier := [8]float32{mul, mul, mul, 1, mul, mul, mul, 1}
		compositeSpanFastAVX2(&want[4], pixels/8, &addend[0], &multiplier[0])
	case fit.TierSSE2:
		addend := [4]float32{addR, addG, addB, 0}
		multiplier := [4]float32{mul, mul, mul, 1}
		compositeSpanFastSSE2(&want[4], pixels/4, &addend[0], &multiplier[0])
	default:
		compositeOpaqueSpanFastScalar(want, 4, pixels, r, g, b, alpha)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("dispatched fast span differs from the %s kernel invoked directly", fastCompositeKernel)
	}
}

// TestCompositeSpanFastAVX2DirectMatchesScalarOracle is the AVX2 twin of the
// SSE2 direct test below. It was missing: the only CI step that ran this
// package set GODEBUG=cpu.avx2=off, so the AVX2 kernel had no direct coverage
// anywhere.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestCompositeSpanFastAVX2DirectMatchesScalarOracle(t *testing.T) {
	if !cpu.X86.HasAVX2 {
		t.Skip("host CPU lacks AVX2")
	}

	rng := rand.New(rand.NewPCG(0xa1e2, 0x61767832))
	for iteration := range 128 {
		pixels := 8 * (1 + rng.IntN(40))
		want := fastSpanFixture(pixels, uint64(iteration))
		got := bytes.Clone(want)

		r, g, b := rng.Float64(), rng.Float64(), rng.Float64()
		alpha := rng.Float64()

		compositeOpaqueSpanFastScalar(want, 4, pixels, r, g, b, alpha)

		addR, addG, addB, mul := fastSpanConstants(r, g, b, alpha)
		addend := [8]float32{addR, addG, addB, 0, addR, addG, addB, 0}
		multiplier := [8]float32{mul, mul, mul, 1, mul, mul, mul, 1}
		compositeSpanFastAVX2(&got[4], pixels/8, &addend[0], &multiplier[0])

		if !bytes.Equal(want, got) {
			t.Fatalf("avx2 direct, iteration %d, %d px, color=(%v,%v,%v,%v) mismatch",
				iteration, pixels, r, g, b, alpha)
		}
	}
}

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
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
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

//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestCompositeSpanFastSSE2DirectRandomMatchesScalarOracle(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x5e2, 0x73736532))
	const pixels = 256

	for iteration := range 128 {
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
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
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
