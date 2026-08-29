package renderer

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"math/rand/v2"
	"testing"
)

func TestCompositeOpaqueSpanMatchesPixelPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		r, g, b, alpha float64
	}{
		{name: "transparent", r: 0.13, g: 0.57, b: 0.91, alpha: 0},
		{name: "fractional", r: 0.13, g: 0.57, b: 0.91, alpha: 0.37},
		{name: "half", r: 1, g: 0.5, b: 0, alpha: 0.5},
		{name: "opaque", r: 0.99, g: 0.01, b: 0.49, alpha: 1},
	}
	sizes := []int{0, 1, 2, 3, 4, 7, 8, 9, 15, 16, 17, 31, 32, 33, 255, 256, 257}

	for _, test := range tests {
		for _, pixels := range sizes {
			t.Run(fmt.Sprintf("%s/%d", test.name, pixels), func(t *testing.T) {
				t.Parallel()

				const prefixPixels = 1
				got := makeOpaqueSpanFixture(prefixPixels + pixels + 1)
				want := append([]byte(nil), got...)
				offset := prefixPixels * 4

				wantImage := &image.NRGBA{
					Pix:    want[offset : offset+pixels*4],
					Stride: pixels * 4,
					Rect:   image.Rect(0, 0, pixels, 1),
				}
				for x := range pixels {
					compositePixel(wantImage, x, 0, test.r, test.g, test.b, test.alpha)
				}

				compositeOpaqueSpanColor(got, offset, pixels, test.r, test.g, test.b, test.alpha)

				if !bytes.Equal(got, want) {
					for i := range got {
						if got[i] != want[i] {
							t.Fatalf("backend %s differs at byte %d: got %d, want %d\ngot span:  %v\nwant span: %v", compositeSpanKernel, i, got[i], want[i], got[offset:offset+pixels*4], want[offset:offset+pixels*4])
						}
					}
				}
			})
		}
	}
}

func TestCompositeOpaqueSpanRandomMatchesPixelPath(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(0x1012, 0x4e454f4e))

	for iteration := range 128 {
		const pixels = 265

		got := makeOpaqueSpanFixture(pixels)
		for i := range pixels {
			got[i*4+0] = uint8(rng.Uint32())
			got[i*4+1] = uint8(rng.Uint32())
			got[i*4+2] = uint8(rng.Uint32())
		}

		want := append([]byte(nil), got...)
		r, g, b, alpha := rng.Float64(), rng.Float64(), rng.Float64(), rng.Float64()

		wantImage := &image.NRGBA{Pix: want, Stride: pixels * 4, Rect: image.Rect(0, 0, pixels, 1)}
		for x := range pixels {
			compositePixel(wantImage, x, 0, r, g, b, alpha)
		}

		compositeOpaqueSpanColor(got, 0, pixels, r, g, b, alpha)

		if !bytes.Equal(got, want) {
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("iteration %d, backend %s differs at byte %d: got %d, want %d", iteration, compositeSpanKernel, i, got[i], want[i])
				}
			}
		}
	}
}

func TestCompositeOpaqueSpanPairMatchesSeparateSpans(t *testing.T) {
	t.Parallel()

	const (
		rowPixels  = 273
		spanStart  = 5
		spanPixels = 257
	)
	firstOffset := spanStart * 4
	secondOffset := rowPixels*4 + firstOffset

	for _, test := range []struct {
		name           string
		r, g, b, alpha float64
	}{
		{name: "transparent", r: 0.13, g: 0.57, b: 0.91},
		{name: "fractional", r: 0.13, g: 0.57, b: 0.91, alpha: 0.37},
		{name: "opaque", r: 0.99, g: 0.01, b: 0.49, alpha: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := makeOpaqueSpanFixture(rowPixels * 2)
			want := append([]byte(nil), got...)
			compositeOpaqueSpanColor(want, firstOffset, spanPixels, test.r, test.g, test.b, test.alpha)
			compositeOpaqueSpanColor(want, secondOffset, spanPixels, test.r, test.g, test.b, test.alpha)

			blend := newSpanBlend(test.r, test.g, test.b, test.alpha)
			compositeOpaqueSpanPair(&blend, got, firstOffset, secondOffset, spanPixels, test.r, test.g, test.b, test.alpha)

			if !bytes.Equal(got, want) {
				t.Fatal("paired span compositor differs from two ordinary spans")
			}
		})
	}
}

func TestPixelsAreOpaque(t *testing.T) {
	t.Parallel()

	opaque := []byte{1, 2, 3, 255, 4, 5, 6, 255}
	if !pixelsAreOpaque(opaque) {
		t.Fatal("opaque pixels reported as translucent")
	}

	opaque[7] = 254
	if pixelsAreOpaque(opaque) {
		t.Fatal("translucent pixel reported as opaque")
	}
}

func TestCPURendererDetectsOpaqueCanvas(t *testing.T) {
	t.Parallel()

	reference := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	if renderer := NewCPURenderer(reference, 1); !renderer.opaqueCanvas {
		t.Fatal("white canvas was not marked opaque")
	}

	canvas := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	canvas.SetNRGBA(0, 0, color.NRGBA{R: 1, G: 2, B: 3, A: 255})
	canvas.SetNRGBA(1, 0, color.NRGBA{R: 4, G: 5, B: 6, A: 255})

	if renderer := NewCPURendererWithCanvas(reference, canvas, 1); !renderer.opaqueCanvas {
		t.Fatal("opaque custom canvas was not marked opaque")
	}

	canvas.SetNRGBA(1, 0, color.NRGBA{R: 4, G: 5, B: 6, A: 254})

	if renderer := NewCPURendererWithCanvas(reference, canvas, 1); renderer.opaqueCanvas {
		t.Fatal("translucent custom canvas was marked opaque")
	}
}

func BenchmarkCompositeOpaqueSpan(b *testing.B) {
	for _, pixels := range []int{8, 16, 64, 256} {
		b.Run(fmt.Sprintf("scalar/%d", pixels), func(b *testing.B) {
			benchmarkCompositeOpaqueSpan(b, pixels, false)
		})
		b.Run(fmt.Sprintf("auto_%s/%d", compositeSpanKernel, pixels), func(b *testing.B) {
			benchmarkCompositeOpaqueSpan(b, pixels, true)
		})
	}
}

func BenchmarkCPURendererOpaqueSpan(b *testing.B) {
	const (
		width   = 512
		height  = 512
		circles = 100
	)
	reference := randomNRGBA(width, height, 42)
	params := benchmarkParams(circles, width, height, 20260816)

	for _, test := range []struct {
		name         string
		opaqueCanvas bool
	}{
		{name: "pixel_loop", opaqueCanvas: false},
		{name: "horizontal_span_" + compositeSpanKernel.String(), opaqueCanvas: true},
	} {
		b.Run(test.name, func(b *testing.B) {
			renderer := NewCPURenderer(reference, circles)
			renderer.SetThreads(1)
			// A false flag deliberately selects the previous per-pixel loop.
			// The actual white canvas remains opaque, so compositePixel still
			// takes its exact opaque-destination path.
			renderer.opaqueCanvas = test.opaqueCanvas

			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				_ = renderer.Render(params)
			}
		})
	}
}

// benchmarkCompositeOpaqueSpan times one span length, dispatched or scalar.
//
// It takes a flag rather than the compositor as a func value on purpose. A func
// parameter forces the blend to be reached through an indirect call, which
// defeats //go:noescape and moves the constant block to the heap - the mistake
// docs/exact-span-compositors.md records as making the kernel measure five to
// nine times slower than scalar. The blend is built here, outside the timed
// loop, because that is where production builds it: once per circle, not once
// per span.
func benchmarkCompositeOpaqueSpan(b *testing.B, pixels int, dispatched bool) {
	b.Helper()

	const (
		r     = 0.13
		g     = 0.57
		blue  = 0.91
		alpha = 0.37
	)

	pix := makeOpaqueSpanFixture(pixels)
	blend := newSpanBlend(r, g, blue, alpha)

	b.ReportAllocs()
	b.SetBytes(int64(pixels * 4))
	b.ResetTimer()

	if dispatched {
		for range b.N {
			compositeOpaqueSpan(&blend, pix, 0, pixels, r, g, blue, alpha)
		}

		return
	}

	for range b.N {
		compositeOpaqueSpanScalar(pix, 0, pixels, r, g, blue, alpha)
	}
}

// compositeOpaqueSpanColor builds the per-circle blend for a single span.
//
// Production builds it once per circle and reuses it for every row; a parity
// test composites one span at a time and has no circle to hang it on. The block
// is a pure function of the colour, so building it per call is arithmetically
// identical - it is only wasteful, which is what the tests do not measure.
// Benchmarks must not use this shim.
func compositeOpaqueSpanColor(pix []byte, offset, pixels int, r, g, b, alpha float64) {
	blend := newSpanBlend(r, g, b, alpha)
	compositeOpaqueSpan(&blend, pix, offset, pixels, r, g, b, alpha)
}

func makeOpaqueSpanFixture(pixels int) []byte {
	pix := make([]byte, pixels*4)
	for i := range pixels {
		pix[i*4+0] = byte(i*37 + 11)
		pix[i*4+1] = byte(i*73 + 29)
		pix[i*4+2] = byte(i*109 + 47)
		pix[i*4+3] = 255
	}

	return pix
}
