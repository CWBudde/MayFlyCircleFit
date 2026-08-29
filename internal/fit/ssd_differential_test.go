package fit

import (
	"fmt"
	"image/color"
	"math/rand/v2"
	"testing"
)

// ssdKernel is one directly-callable SSD implementation. hostSSDKernels
// enumerates the ones this build and CPU can run; see the per-architecture
// files next to this one.
type ssdKernel struct {
	tier SIMDTier
	fn   func(a, b []uint8, stride, width, height int) float64
}

// TestSSDKernelsAgreeExactly is the cross-backend differential test. Every
// kernel this host can execute sees the same bytes in the same process, and all
// of them must return the identical float64.
//
// Exact equality is the right comparison, not a tolerance. Each kernel reduces
// an integer sum and converts once at the end, so any difference at all is a
// defect rather than a rounding artifact. The previous helper
// (CompareSSDImplementations) compared the active backend against scalar within
// a tolerance and had no callers.
func TestSSDKernelsAgreeExactly(t *testing.T) {
	t.Parallel()

	kernels := hostSSDKernels()
	if len(kernels) < 2 {
		t.Logf("only the %s kernel is executable here; the comparison is degenerate", kernels[0].tier)
	}

	// Widths straddle every vector width in the tree: 4 (SSE2/NEON), 8 (AVX2),
	// and their remainders, plus sizes large enough to exercise several
	// iterations of the row loop.
	widths := []int{1, 2, 3, 4, 5, 7, 8, 9, 11, 12, 15, 16, 17, 23, 24, 25, 31, 32, 33, 63, 64, 65, 127, 128, 129}
	const height = 6

	for _, width := range widths {
		t.Run(fmt.Sprintf("width_%d", width), func(t *testing.T) {
			t.Parallel()

			a := randomNRGBA(width, height, 100)
			b := randomNRGBA(width, height, 200)

			want := fastSSD_Scalar(a.Pix, b.Pix, a.Stride, width, height)
			for _, kernel := range kernels {
				got := kernel.fn(a.Pix, b.Pix, a.Stride, width, height)
				if got != want {
					t.Errorf("%s = %.0f, scalar = %.0f", kernel.tier, got, want)
				}
			}
		})
	}
}

// TestSSDKernelsAgreeOnMaximumDifference pins the accumulator width of every
// kernel at once. Black against white is the largest per-channel difference
// there is, so a lane that narrows too early shows up here and nowhere else.
//
// 512x512 totals 100270080000, which no 32-bit lane can hold: the SSE2 kernel
// reaches it only because it widens per row rather than per iteration.
func TestSSDKernelsAgreeOnMaximumDifference(t *testing.T) {
	t.Parallel()

	const width, height = 512, 512
	black := solidColorNRGBA(width, height, color.NRGBA{A: 0})
	white := solidColorNRGBA(width, height, color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	want := float64(width) * float64(height) * 3 * 255 * 255
	for _, kernel := range hostSSDKernels() {
		got := kernel.fn(black.Pix, white.Pix, black.Stride, width, height)
		if got != want {
			t.Errorf("%s = %.0f, exact = %.0f", kernel.tier, got, want)
		}
	}
}

// TestSSDKernelsIgnoreAlpha proves no kernel reads the alpha byte, which the
// cost function depends on and which a lane-shuffle bug would break.
func TestSSDKernelsIgnoreAlpha(t *testing.T) {
	t.Parallel()

	const width, height = 37, 5

	a := randomNRGBA(width, height, 7)
	b := randomNRGBA(width, height, 8)
	want := fastSSD_Scalar(a.Pix, b.Pix, a.Stride, width, height)

	// Scramble only the alpha bytes; every kernel must return the same total.
	source := rand.New(rand.NewPCG(9, 9))
	for i := 3; i < len(a.Pix); i += 4 {
		a.Pix[i] = uint8(source.UintN(256))
		b.Pix[i] = uint8(source.UintN(256))
	}

	for _, kernel := range hostSSDKernels() {
		got := kernel.fn(a.Pix, b.Pix, a.Stride, width, height)
		if got != want {
			t.Errorf("%s changed to %.0f after alpha was scrambled (want %.0f)", kernel.tier, got, want)
		}
	}
}

// TestSSDKernelsHandleStridePadding covers a gap the rest of the suite left
// open: every other test allocates a fresh image.NRGBA, so the kernels have
// only ever seen a tightly packed buffer whose rows start at a Go allocation
// boundary. A row stride wider than the row, and a row that does not start at
// offset zero, are both reachable from sub-images in the renderer.
//
//nolint:paralleltest // the subtests draw from one seeded random source
func TestSSDKernelsHandleStridePadding(t *testing.T) {
	source := rand.New(rand.NewPCG(4242, 1))

	for _, width := range []int{1, 3, 4, 5, 8, 9, 16, 17, 33} {
		for _, padPixels := range []int{1, 2, 5} {
			//nolint:paralleltest // the subtests draw from one seeded random source
			for _, leadPixels := range []int{0, 1, 3} {
				name := fmt.Sprintf("width_%d/pad_%d/lead_%d", width, padPixels, leadPixels)
				t.Run(name, func(t *testing.T) {
					const height = 4
					stride := (width + padPixels) * 4
					lead := leadPixels * 4

					a := make([]uint8, lead+stride*height)
					b := make([]uint8, lead+stride*height)

					for i := range a {
						a[i] = uint8(source.UintN(256))
						b[i] = uint8(source.UintN(256))
					}

					want := fastSSD_Scalar(a[lead:], b[lead:], stride, width, height)
					for _, kernel := range hostSSDKernels() {
						got := kernel.fn(a[lead:], b[lead:], stride, width, height)
						if got != want {
							t.Errorf("%s = %.0f, scalar = %.0f", kernel.tier, got, want)
						}
					}
				})
			}
		}
	}
}

// TestSSDKernelsAgreeOnRandomShapes is the randomized sweep behind the fixed
// tables above. It is seeded, so a failure reproduces exactly.
func TestSSDKernelsAgreeOnRandomShapes(t *testing.T) {
	t.Parallel()

	kernels := hostSSDKernels()
	source := rand.New(rand.NewPCG(20260816, 3))

	for round := range 400 {
		width := 1 + source.IntN(70)
		height := 1 + source.IntN(6)
		stride := width * 4

		a := make([]uint8, stride*height)
		b := make([]uint8, stride*height)

		for i := range a {
			a[i] = uint8(source.UintN(256))
			b[i] = uint8(source.UintN(256))
		}

		want := fastSSD_Scalar(a, b, stride, width, height)
		for _, kernel := range kernels {
			if got := kernel.fn(a, b, stride, width, height); got != want {
				t.Fatalf("round %d (%dx%d): %s = %.0f, scalar = %.0f", round, width, height, kernel.tier, got, want)
			}
		}
	}
}
