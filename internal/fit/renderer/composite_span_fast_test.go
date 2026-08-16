package renderer

import (
	"bytes"
	"image"
	"math/rand/v2"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

var fastSpanSizes = []int{0, 1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255, 256, 257}

// fastSpanFixture builds an opaque NRGBA row with one guard pixel on each side
// so a kernel writing outside its span is detected.
func fastSpanFixture(pixels int, seed uint64) []byte {
	pix := make([]byte, (pixels+2)*4)
	rng := rand.New(rand.NewPCG(seed, 0x66617374))
	for i := 0; i < len(pix); i += 4 {
		pix[i+0] = byte(rng.UintN(256))
		pix[i+1] = byte(rng.UintN(256))
		pix[i+2] = byte(rng.UintN(256))
		pix[i+3] = 255
	}
	return pix
}

// TestCompositeOpaqueSpanFastMatchesScalarOracle is the strict test: the SIMD
// kernels must reproduce the float32 reference bit for bit. Any difference is a
// kernel bug, not a precision artifact.
func TestCompositeOpaqueSpanFastMatchesScalarOracle(t *testing.T) {
	cases := []struct{ r, g, b, alpha float64 }{
		{0, 0, 0, 0},
		{1, 1, 1, 1},
		{0.2, 0.6, 0.9, 0.37},
		{0.5, 0.5, 0.5, 0.5},
		{0.13, 0.87, 0.41, 0.02},
		{0.99, 0.01, 0.5, 0.98},
	}

	for _, tc := range cases {
		for _, pixels := range fastSpanSizes {
			want := fastSpanFixture(pixels, uint64(pixels))
			got := bytes.Clone(want)

			compositeOpaqueSpanFastScalar(want, 4, pixels, tc.r, tc.g, tc.b, tc.alpha)
			compositeOpaqueSpanFast(got, 4, pixels, tc.r, tc.g, tc.b, tc.alpha)

			if !bytes.Equal(want, got) {
				t.Fatalf("backend %s, pixels=%d, color=(%v,%v,%v,%v):\n scalar=%v\n vector=%v",
					fastCompositeBackend, pixels, tc.r, tc.g, tc.b, tc.alpha, want, got)
			}
		}
	}
}

func TestCompositeOpaqueSpanFastRandomMatchesScalarOracle(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x1013, 0x73736532))
	const pixels = 261 // > any cutoff and not a multiple of 4 or 8

	for iteration := 0; iteration < 128; iteration++ {
		want := fastSpanFixture(pixels, uint64(iteration))
		got := bytes.Clone(want)

		r, g, b := rng.Float64(), rng.Float64(), rng.Float64()
		alpha := rng.Float64()

		compositeOpaqueSpanFastScalar(want, 4, pixels, r, g, b, alpha)
		compositeOpaqueSpanFast(got, 4, pixels, r, g, b, alpha)

		if !bytes.Equal(want, got) {
			t.Fatalf("backend %s, iteration %d, color=(%v,%v,%v,%v) mismatch",
				fastCompositeBackend, iteration, r, g, b, alpha)
		}
	}
}

// TestCompositeOpaqueSpanFastAlphaPreserved guards the lane trick that passes
// the alpha byte through with multiplier 1 and addend 0.
func TestCompositeOpaqueSpanFastAlphaPreserved(t *testing.T) {
	const pixels = 64
	pix := fastSpanFixture(pixels, 7)
	compositeOpaqueSpanFast(pix, 4, pixels, 0.3, 0.4, 0.5, 0.6)

	for i := 3; i < len(pix); i += 4 {
		if pix[i] != 255 {
			t.Fatalf("alpha at byte %d = %d, want 255 (backend %s)", i, pix[i], fastCompositeBackend)
		}
	}
}

// TestCompositeOpaqueSpanFastWithinToleranceOfExact documents the accuracy
// contract of the opt-in path: within +/-1 per channel of the exact float64
// compositor, never byte-identical by requirement.
func TestCompositeOpaqueSpanFastWithinToleranceOfExact(t *testing.T) {
	cases := []struct{ r, g, b, alpha float64 }{
		{0, 0, 0, 0},
		{1, 1, 1, 1},
		{0.2, 0.6, 0.9, 0.37},
		{0.5, 0.5, 0.5, 0.5},
		{0.13, 0.87, 0.41, 0.02},
	}

	for _, tc := range cases {
		for _, pixels := range fastSpanSizes {
			exact := fastSpanFixture(pixels, uint64(pixels)+1000)
			fast := bytes.Clone(exact)

			compositeOpaqueSpanScalar(exact, 4, pixels, tc.r, tc.g, tc.b, tc.alpha)
			compositeOpaqueSpanFast(fast, 4, pixels, tc.r, tc.g, tc.b, tc.alpha)

			for i := range exact {
				diff := int(exact[i]) - int(fast[i])
				if diff < -1 || diff > 1 {
					t.Fatalf("pixels=%d color=(%v,%v,%v,%v) byte %d: exact=%d fast=%d (diff %d)",
						pixels, tc.r, tc.g, tc.b, tc.alpha, i, exact[i], fast[i], diff)
				}
			}
		}
	}
}

// TestCPURendererFastCompositingDefaultsOff protects the opt-in guarantee: a
// renderer built the ordinary way must produce byte-identical output to before
// this feature existed.
func TestCPURendererFastCompositingDefaultsOff(t *testing.T) {
	reference := image.NewNRGBA(image.Rect(0, 0, 64, 64))

	circles := []fit.Circle{
		{X: 32, Y: 32, R: 20, CR: 0.9, CG: 0.2, CB: 0.4, Opacity: 0.6},
		{X: 20, Y: 40, R: 14, CR: 0.1, CG: 0.7, CB: 0.5, Opacity: 0.35},
	}
	params := make([]float64, 0, len(circles)*7)
	for _, c := range circles {
		params = append(params, c.X, c.Y, c.R, c.CR, c.CG, c.CB, c.Opacity)
	}

	r := NewCPURenderer(reference, len(circles))
	if r.fastCompositing {
		t.Fatal("fastCompositing must default to false")
	}
	// Render silently returns the untouched canvas when the parameter vector
	// does not match Dim(). Without this guard the comparison below degrades
	// into two identical blank canvases and stops exercising either
	// compositor, which is exactly how this test once passed vacuously.
	if len(params) != r.Dim() {
		t.Fatalf("params length %d does not match renderer Dim() %d; the render would be skipped", len(params), r.Dim())
	}

	exact := bytes.Clone(r.Render(params).Pix)

	fastRenderer := NewCPURenderer(reference, len(circles))
	fastRenderer.fastCompositing = true
	fast := fastRenderer.Render(params).Pix

	// The circles must actually mark the canvas; a blank result would again
	// make the byte comparison meaningless.
	blank := bytes.Clone(NewCPURenderer(reference, len(circles)).canvas.Pix)
	if bytes.Equal(exact, blank) {
		t.Fatal("exact renderer produced the untouched background; the compositor never ran")
	}
	if bytes.Equal(fast, blank) {
		t.Fatal("fast renderer produced the untouched background; the compositor never ran")
	}

	if len(exact) != len(fast) {
		t.Fatalf("length mismatch: %d vs %d", len(exact), len(fast))
	}
	for i := range exact {
		if diff := int(exact[i]) - int(fast[i]); diff < -1 || diff > 1 {
			t.Fatalf("byte %d: exact=%d fast=%d", i, exact[i], fast[i])
		}
	}
}

func BenchmarkCompositeOpaqueSpanFast(b *testing.B) {
	for _, pixels := range []int{8, 16, 64, 256, 1024} {
		pix := fastSpanFixture(pixels, 99)

		b.Run("exact_float64/"+itoa(pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))
			for b.Loop() {
				compositeOpaqueSpanScalar(pix, 4, pixels, 0.3, 0.6, 0.9, 0.45)
			}
		})

		b.Run("fast_"+fastCompositeBackend+"/"+itoa(pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))
			for b.Loop() {
				compositeOpaqueSpanFast(pix, 4, pixels, 0.3, 0.6, 0.9, 0.45)
			}
		})
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
