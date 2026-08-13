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
				const prefixPixels = 1
				got := makeOpaqueSpanFixture(prefixPixels + pixels + 1)
				want := append([]byte(nil), got...)
				offset := prefixPixels * 4

				wantImage := &image.NRGBA{
					Pix:    want[offset : offset+pixels*4],
					Stride: pixels * 4,
					Rect:   image.Rect(0, 0, pixels, 1),
				}
				for x := 0; x < pixels; x++ {
					compositePixel(wantImage, x, 0, test.r, test.g, test.b, test.alpha)
				}

				compositeOpaqueSpan(got, offset, pixels, test.r, test.g, test.b, test.alpha)
				if !bytes.Equal(got, want) {
					for i := range got {
						if got[i] != want[i] {
							t.Fatalf("backend %s differs at byte %d: got %d, want %d\ngot span:  %v\nwant span: %v", compositeSpanBackend, i, got[i], want[i], got[offset:offset+pixels*4], want[offset:offset+pixels*4])
						}
					}
				}
			})
		}
	}
}

func TestCompositeOpaqueSpanRandomMatchesPixelPath(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x1012, 0x4e454f4e))
	for iteration := 0; iteration < 128; iteration++ {
		const pixels = 265
		got := makeOpaqueSpanFixture(pixels)
		for i := 0; i < pixels; i++ {
			got[i*4+0] = uint8(rng.Uint32())
			got[i*4+1] = uint8(rng.Uint32())
			got[i*4+2] = uint8(rng.Uint32())
		}
		want := append([]byte(nil), got...)
		r, g, b, alpha := rng.Float64(), rng.Float64(), rng.Float64(), rng.Float64()
		wantImage := &image.NRGBA{Pix: want, Stride: pixels * 4, Rect: image.Rect(0, 0, pixels, 1)}
		for x := 0; x < pixels; x++ {
			compositePixel(wantImage, x, 0, r, g, b, alpha)
		}

		compositeOpaqueSpan(got, 0, pixels, r, g, b, alpha)
		if !bytes.Equal(got, want) {
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("iteration %d, backend %s differs at byte %d: got %d, want %d", iteration, compositeSpanBackend, i, got[i], want[i])
				}
			}
		}
	}
}

func TestCompositeOpaqueSpanPairMatchesSeparateSpans(t *testing.T) {
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
			got := makeOpaqueSpanFixture(rowPixels * 2)
			want := append([]byte(nil), got...)
			compositeOpaqueSpan(want, firstOffset, spanPixels, test.r, test.g, test.b, test.alpha)
			compositeOpaqueSpan(want, secondOffset, spanPixels, test.r, test.g, test.b, test.alpha)

			compositeOpaqueSpanPair(got, firstOffset, secondOffset, spanPixels, test.r, test.g, test.b, test.alpha)
			if !bytes.Equal(got, want) {
				t.Fatal("paired span compositor differs from two ordinary spans")
			}
		})
	}
}

func TestPixelsAreOpaque(t *testing.T) {
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
			benchmarkCompositeOpaqueSpan(b, pixels, compositeOpaqueSpanScalar)
		})
		b.Run(fmt.Sprintf("auto_%s/%d", compositeSpanBackend, pixels), func(b *testing.B) {
			benchmarkCompositeOpaqueSpan(b, pixels, compositeOpaqueSpan)
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
	params := randomParams(circles, width, height)

	for _, test := range []struct {
		name         string
		opaqueCanvas bool
	}{
		{name: "pixel_loop", opaqueCanvas: false},
		{name: "horizontal_span_" + compositeSpanBackend, opaqueCanvas: true},
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
			for i := 0; i < b.N; i++ {
				_ = renderer.Render(params)
			}
		})
	}
}

func benchmarkCompositeOpaqueSpan(b *testing.B, pixels int, composite func([]byte, int, int, float64, float64, float64, float64)) {
	pix := makeOpaqueSpanFixture(pixels)
	b.ReportAllocs()
	b.SetBytes(int64(pixels * 4))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		composite(pix, 0, pixels, 0.13, 0.57, 0.91, 0.37)
	}
}

func makeOpaqueSpanFixture(pixels int) []byte {
	pix := make([]byte, pixels*4)
	for i := 0; i < pixels; i++ {
		pix[i*4+0] = byte(i*37 + 11)
		pix[i*4+1] = byte(i*73 + 29)
		pix[i*4+2] = byte(i*109 + 47)
		pix[i*4+3] = 255
	}
	return pix
}
