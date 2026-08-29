package renderer

import (
	"bytes"
	"image"
	"math/rand/v2"
	"strconv"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
)

// fastSpanSizes deliberately starts at 1. A zero-pixel entry was a tautology in
// every test that used it: neither function touches the fixture's guard pixels,
// so both sides trivially matched whatever the kernel did.
var fastSpanSizes = []int{1, 2, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255, 256, 257}

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

// requireFastVectorKernel skips a kernel-parity test where compositeOpaqueSpanFast
// *is* compositeOpaqueSpanFastScalar - on arm64 and every other target without a
// float32 kernel. Comparing a function against itself is a tautology, and a
// tautology that passes is worse than a skip: it reads as coverage.
func requireFastVectorKernel(t *testing.T) {
	t.Helper()

	if fastCompositeKernel == fit.TierScalar {
		t.Skipf("no float32 span kernel at tier %s; the comparison would be scalar against itself", fit.Tier())
	}
}

// TestCompositeOpaqueSpanFastMatchesScalarOracle is the strict test: the SIMD
// kernels must reproduce the float32 reference bit for bit. Any difference is a
// kernel bug, not a precision artifact.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestCompositeOpaqueSpanFastMatchesScalarOracle(t *testing.T) {
	requireFastVectorKernel(t)

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
					fastCompositeKernel, pixels, tc.r, tc.g, tc.b, tc.alpha, want, got)
			}
		}
	}
}

//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestCompositeOpaqueSpanFastRandomMatchesScalarOracle(t *testing.T) {
	requireFastVectorKernel(t)

	rng := rand.New(rand.NewPCG(0x1013, 0x73736532))
	const pixels = 261 // > any cutoff and not a multiple of 4 or 8

	for iteration := range 128 {
		want := fastSpanFixture(pixels, uint64(iteration))
		got := bytes.Clone(want)

		r, g, b := rng.Float64(), rng.Float64(), rng.Float64()
		alpha := rng.Float64()

		compositeOpaqueSpanFastScalar(want, 4, pixels, r, g, b, alpha)
		compositeOpaqueSpanFast(got, 4, pixels, r, g, b, alpha)

		if !bytes.Equal(want, got) {
			t.Fatalf("backend %s, iteration %d, color=(%v,%v,%v,%v) mismatch",
				fastCompositeKernel, iteration, r, g, b, alpha)
		}
	}
}

// TestCompositeOpaqueSpanFastAlphaPreserved guards the lane trick that passes
// the alpha byte through with multiplier 1 and addend 0.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestCompositeOpaqueSpanFastAlphaPreserved(t *testing.T) {
	requireFastVectorKernel(t)

	const pixels = 64
	pix := fastSpanFixture(pixels, 7)
	compositeOpaqueSpanFast(pix, 4, pixels, 0.3, 0.4, 0.5, 0.6)

	for i := 3; i < len(pix); i += 4 {
		if pix[i] != 255 {
			t.Fatalf("alpha at byte %d = %d, want 255 (backend %s)", i, pix[i], fastCompositeKernel)
		}
	}
}

// TestCompositeOpaqueSpanFastWithinToleranceOfExact is the accuracy contract of
// the opt-in path: within +/-1 per channel of the exact float64 compositor.
//
// It sweeps every byte value against several thousand randomised colours rather
// than checking five hand-picked ones. That distinction matters, because the
// error is not gradual precision loss - it is a tie-breaking flip at
// half-integer boundaries introduced by regrouping (fg + (p/255)*bg)*255 into
// (fg*255 + 0.5) + p*bg. Whether a given colour trips it depends on where its
// products land relative to .5, so sparse sampling can easily miss a colour
// class entirely.
//
// The test also reports how often the bound is actually reached, so a change
// that widens the error without breaking the bound is visible rather than
// silent.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestCompositeOpaqueSpanFastWithinToleranceOfExact(t *testing.T) {
	source := rand.New(rand.NewPCG(0x71c, 0xacc))

	// Every byte value, once per colour, in one span.
	const pixels = 256

	base := make([]byte, (pixels+2)*4)
	for i := range pixels {
		value := byte(i)
		base[4+i*4+0] = value
		base[4+i*4+1] = value
		base[4+i*4+2] = value
		base[4+i*4+3] = 255
	}

	colours := make([]struct{ r, g, b, alpha float64 }, 0, 2048)
	// Corners and near-degenerate alphas first, then a randomised sweep.
	colours = append(colours, []struct{ r, g, b, alpha float64 }{
		{0, 0, 0, 0},
		{1, 1, 1, 1},
		{0, 0, 0, 1},
		{1, 1, 1, 0},
		{0.5, 0.5, 0.5, 0.5},
		{0.2, 0.6, 0.9, 0.37},
		{0.13, 0.87, 0.41, 0.02},
		{0.99, 0.01, 0.5, 0.98},
		{1, 0, 0, 1.0 / 255},
		{1, 1, 1, 254.0 / 255},
	}...)
	for range 2000 {
		colours = append(colours, struct{ r, g, b, alpha float64 }{
			source.Float64(), source.Float64(), source.Float64(), source.Float64(),
		})
	}

	offByOne, total := 0, 0

	for _, c := range colours {
		exact := bytes.Clone(base)
		fast := bytes.Clone(base)

		compositeOpaqueSpanScalar(exact, 4, pixels, c.r, c.g, c.b, c.alpha)
		compositeOpaqueSpanFast(fast, 4, pixels, c.r, c.g, c.b, c.alpha)

		for i := range exact {
			diff := int(exact[i]) - int(fast[i])
			total++

			switch diff {
			case 0:
			case -1, 1:
				offByOne++
			default:
				t.Fatalf("colour=(%v,%v,%v,%v) byte %d: exact=%d fast=%d (diff %d)",
					c.r, c.g, c.b, c.alpha, i, exact[i], fast[i], diff)
			}
		}
	}

	t.Logf("+/-1 on %d of %d channel writes (%.3f%%) over %d colours x every byte value",
		offByOne, total, 100*float64(offByOne)/float64(total), len(colours))
}

// TestCompositeOpaqueSpanFastDoesNotAccumulate checks the property the +/-1
// bound depends on when circles overlap, which the single-span test cannot see:
// that stacking composites does not compound the error.
//
// It holds because each layer contracts the existing value by (1-alpha), so an
// inherited one-unit difference shrinks rather than adding to the next layer's.
// Two hundred layers at a low alpha is the worst case for that argument.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
func TestCompositeOpaqueSpanFastDoesNotAccumulate(t *testing.T) {
	const pixels = 256

	exact := make([]byte, pixels*4)
	for i := range pixels {
		exact[i*4+0] = byte(i)
		exact[i*4+1] = byte(255 - i)
		exact[i*4+2] = byte((i * 7) % 256)
		exact[i*4+3] = 255
	}

	fast := bytes.Clone(exact)

	source := rand.New(rand.NewPCG(0x1a7e, 0x57ac))
	for layer := range 200 {
		alpha := 1.0/255 + source.Float64()*0.05
		r, g, b := source.Float64(), source.Float64(), source.Float64()
		compositeOpaqueSpanScalar(exact, 0, pixels, r, g, b, alpha)
		compositeOpaqueSpanFast(fast, 0, pixels, r, g, b, alpha)

		for i := range exact {
			if diff := int(exact[i]) - int(fast[i]); diff < -1 || diff > 1 {
				t.Fatalf("layer %d byte %d: exact=%d fast=%d (diff %d)", layer, i, exact[i], fast[i], diff)
			}
		}
	}
}

// TestCPURendererFastCompositingDefaultsOff protects the opt-in guarantee: a
// renderer built the ordinary way must produce byte-identical output to before
// this feature existed.
//
//nolint:paralleltest // shares the process-global SIMD tier the forced-tier tests mutate
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
	for _, pixels := range []int{2, 4, 8, 16, 64, 256, 1024} {
		pix := fastSpanFixture(pixels, 99)

		b.Run("exact_float64/"+strconv.Itoa(pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))

			for b.Loop() {
				compositeOpaqueSpanScalar(pix, 4, pixels, 0.3, 0.6, 0.9, 0.45)
			}
		})

		// The comparison that matters is fast against the *dispatched* exact
		// path, not against the scalar loop. Benchmarking a lossy kernel only
		// against scalar float64 overstates its value wherever an exact vector
		// kernel exists, which on amd64 it now does.
		b.Run("exact_"+compositeSpanKernel.String()+"/"+strconv.Itoa(pixels), func(b *testing.B) {
			blend := newSpanBlend(0.3, 0.6, 0.9, 0.45)

			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))

			for b.Loop() {
				compositeOpaqueSpan(&blend, pix, 4, pixels, 0.3, 0.6, 0.9, 0.45)
			}
		})

		b.Run("fast_"+fastCompositeKernel.String()+"/"+strconv.Itoa(pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))

			for b.Loop() {
				compositeOpaqueSpanFast(pix, 4, pixels, 0.3, 0.6, 0.9, 0.45)
			}
		})
	}
}
