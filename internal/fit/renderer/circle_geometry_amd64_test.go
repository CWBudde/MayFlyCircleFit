//go:build amd64

package renderer

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"golang.org/x/sys/cpu"
)

func TestCircleSpanFloat32AVX2MatchesScalar(t *testing.T) {
	if !cpu.X86.HasAVX2 {
		t.Skip("AVX2 unavailable")
	}

	tests := []struct {
		center, remaining float32
		width             int
	}{
		{center: 16.125, remaining: 0, width: 33},
		{center: 16.125, remaining: 1, width: 33},
		{center: 16.125, remaining: 49, width: 33},
		{center: 16.125, remaining: 64, width: 33},
		{center: 16.125, remaining: 65, width: 33},
		{center: 1.25, remaining: 400, width: 33},
		{center: 31.25, remaining: 400, width: 33},
	}
	for _, test := range tests {
		wantStart, wantEnd := circleSpanFloat32(test.center, test.remaining, test.width)
		gotStart, gotEnd := circleSpanFloat32AVX2(test.center, test.remaining, test.width)
		if gotStart != wantStart || gotEnd != wantEnd {
			t.Fatalf("circleSpanFloat32AVX2(%v, %v, %d) = [%d,%d), want [%d,%d)",
				test.center, test.remaining, test.width, gotStart, gotEnd, wantStart, wantEnd)
		}
	}

	rng := rand.New(rand.NewSource(1013))
	for range 100_000 {
		width := rng.Intn(2048) + 1
		center := rng.Float32() * float32(width-1)
		radius := rng.Float32() * float32(width)
		remaining := radius * radius * rng.Float32()
		wantStart, wantEnd := circleSpanFloat32(center, remaining, width)
		gotStart, gotEnd := circleSpanFloat32AVX2(center, remaining, width)
		if gotStart != wantStart || gotEnd != wantEnd {
			t.Fatalf("random circleSpanFloat32AVX2(%v, %v, %d) = [%d,%d), want [%d,%d)",
				center, remaining, width, gotStart, gotEnd, wantStart, wantEnd)
		}
	}
}

// circleSpanFloat32SSE2Cases covers the geometry the span search has to get
// exactly right: fully interior circles, both clip edges, empty rows, tiny and
// sub-pixel radii, centers outside the raster, and widths that are and are not
// multiples of four.
var circleSpanFloat32SSE2Cases = []struct {
	name              string
	center, remaining float32
	width             int
}{
	{name: "interior", center: 16.125, remaining: 49, width: 33},
	{name: "interior_width_multiple_of_four", center: 16.125, remaining: 49, width: 32},
	{name: "interior_width_not_multiple_of_four", center: 17.125, remaining: 49, width: 35},
	{name: "radius_zero", center: 16.125, remaining: 0, width: 33},
	{name: "radius_one", center: 16.125, remaining: 1, width: 33},
	{name: "subpixel_radius_quarter", center: 16.125, remaining: 0.0625, width: 33},
	{name: "subpixel_radius_half", center: 16.5, remaining: 0.25, width: 33},
	{name: "batch_boundary_low", center: 16.125, remaining: 64, width: 33},
	{name: "batch_boundary_high", center: 16.125, remaining: 65, width: 33},
	{name: "clipped_left", center: 1.25, remaining: 400, width: 33},
	{name: "clipped_right", center: 31.25, remaining: 400, width: 33},
	{name: "clipped_both", center: 16.125, remaining: 4096, width: 33},
	{name: "clipped_both_width_four", center: 2.125, remaining: 4096, width: 4},
	{name: "row_outside_circle", center: 16.125, remaining: -1, width: 33},
	{name: "row_far_outside_circle", center: 16.125, remaining: -1024, width: 33},
	{name: "center_negative", center: -12.75, remaining: 400, width: 33},
	{name: "center_negative_row_outside", center: -12.75, remaining: -4, width: 33},
	{name: "center_negative_far", center: -300.5, remaining: 400, width: 33},
	{name: "center_beyond_width", center: 44.75, remaining: 400, width: 33},
	{name: "center_beyond_width_far", center: 512.25, remaining: 400, width: 33},
	{name: "center_beyond_width_row_outside", center: 44.75, remaining: -4, width: 33},
	{name: "width_one", center: 0.25, remaining: 400, width: 1},
	{name: "width_three", center: 1.25, remaining: 400, width: 3},
}

func TestCircleSpanFloat32SSE2MatchesScalar(t *testing.T) {
	if !cpu.X86.HasSSE2 {
		t.Skip("SSE2 unavailable")
	}

	for _, test := range circleSpanFloat32SSE2Cases {
		t.Run(test.name, func(t *testing.T) {
			wantStart, wantEnd := circleSpanFloat32(test.center, test.remaining, test.width)
			gotStart, gotEnd := circleSpanFloat32SSE2Unchecked(test.center, test.remaining, test.width)
			if gotStart != wantStart || gotEnd != wantEnd {
				t.Fatalf("circleSpanFloat32SSE2(%v, %v, %d) = [%d,%d), want [%d,%d)",
					test.center, test.remaining, test.width, gotStart, gotEnd, wantStart, wantEnd)
			}
		})
	}

	rng := rand.New(rand.NewSource(1013))
	for range 100_000 {
		width := rng.Intn(2048) + 1
		center := rng.Float32() * float32(width-1)
		radius := rng.Float32() * float32(width)
		remaining := radius * radius * rng.Float32()
		wantStart, wantEnd := circleSpanFloat32(center, remaining, width)
		gotStart, gotEnd := circleSpanFloat32SSE2Unchecked(center, remaining, width)
		if gotStart != wantStart || gotEnd != wantEnd {
			t.Fatalf("random circleSpanFloat32SSE2(%v, %v, %d) = [%d,%d), want [%d,%d)",
				center, remaining, width, gotStart, gotEnd, wantStart, wantEnd)
		}
	}

	// Centers outside the raster and rows that miss the circle entirely.
	rng = rand.New(rand.NewSource(20250816))
	for range 100_000 {
		width := rng.Intn(64) + 1
		center := (rng.Float32()*4 - 2) * float32(width)
		radius := rng.Float32() * float32(width)
		remaining := radius*radius*rng.Float32()*2 - float32(width)
		wantStart, wantEnd := circleSpanFloat32(center, remaining, width)
		gotStart, gotEnd := circleSpanFloat32SSE2Unchecked(center, remaining, width)
		if gotStart != wantStart || gotEnd != wantEnd {
			t.Fatalf("offset circleSpanFloat32SSE2(%v, %v, %d) = [%d,%d), want [%d,%d)",
				center, remaining, width, gotStart, gotEnd, wantStart, wantEnd)
		}
	}
}

// TestCircleSpanFloat32SSE2DispatchMatchesScalar exercises the gated wrapper,
// which falls back to scalar when SSE2 is unavailable or disabled by env.
func TestCircleSpanFloat32SSE2DispatchMatchesScalar(t *testing.T) {
	for _, test := range circleSpanFloat32SSE2Cases {
		wantStart, wantEnd := circleSpanFloat32(test.center, test.remaining, test.width)
		gotStart, gotEnd := circleSpanFloat32SSE2(test.center, test.remaining, test.width)
		if gotStart != wantStart || gotEnd != wantEnd {
			t.Fatalf("%s: circleSpanFloat32SSE2 dispatch = [%d,%d), want [%d,%d)",
				test.name, gotStart, gotEnd, wantStart, wantEnd)
		}
	}
}

func TestCircleSpanFloat32Backend(t *testing.T) {
	want := "scalar"
	switch {
	case cpu.X86.HasAVX2:
		want = "avx2"
	case cpu.X86.HasSSE2:
		want = "sse2"
	}
	if fit.SIMDDisabledByEnv() {
		want = "scalar"
	}
	if circleSpanFloat32Backend != want {
		t.Fatalf("float32 geometry backend = %q, want %q", circleSpanFloat32Backend, want)
	}
}

func TestFixedCircleQ16AVX2MatchesScalar(t *testing.T) {
	if !cpu.X86.HasAVX2 {
		t.Skip("AVX2 unavailable")
	}

	rng := rand.New(rand.NewSource(101316))
	for i := range 100_000 {
		width := 8 + rng.Intn(2041)
		c := fit.Circle{
			X: rng.Float64() * float64(width),
			Y: rng.Float64() * 1024,
			R: 0.25 + rng.Float64()*512,
		}
		fixed, ok := newFixedCircleQ16(c)
		if !ok {
			t.Fatalf("ordinary circle outside Q16.16 range: %+v", c)
		}
		y := rng.Intn(2048) - 512

		wantStart, wantEnd, wantIntersects := fixed.span(y, width)
		gotStart, gotEnd, gotIntersects := fixed.spanAVX2(y, width)
		if gotStart != wantStart || gotEnd != wantEnd || gotIntersects != wantIntersects {
			t.Fatalf("case %d: spanAVX2(y=%d, width=%d, circle=%+v) = [%d,%d), %v; want [%d,%d), %v",
				i, y, width, c, gotStart, gotEnd, gotIntersects, wantStart, wantEnd, wantIntersects)
		}
	}
}

func BenchmarkCircleSpanFloat32AVX2Direct(b *testing.B) {
	if !cpu.X86.HasAVX2 {
		b.Skip("AVX2 unavailable")
	}
	for _, radius := range []float32{5.25, 25.25, 100.25, 256.25} {
		remaining := radius * radius
		b.Run("scalar_R"+benchmarkFloatName(radius), func(b *testing.B) {
			for range b.N {
				xStart, xEnd := circleSpanFloat32(256.125, remaining, 513)
				geometryBenchmarkSink = xEnd - xStart
			}
		})
		b.Run("avx2_R"+benchmarkFloatName(radius), func(b *testing.B) {
			for range b.N {
				xStart, xEnd := circleSpanFloat32AVX2Kernel(256.125, remaining, 256, 513)
				geometryBenchmarkSink = xEnd - xStart
			}
		})
	}
}

func BenchmarkCircleSpanFloat32SSE2Direct(b *testing.B) {
	if !cpu.X86.HasSSE2 {
		b.Skip("SSE2 unavailable")
	}
	for _, radius := range []float32{5.25, 25.25, 100.25, 256.25} {
		remaining := radius * radius
		b.Run("scalar_R"+benchmarkFloatName(radius), func(b *testing.B) {
			for range b.N {
				xStart, xEnd := circleSpanFloat32(256.125, remaining, 513)
				geometryBenchmarkSink = xEnd - xStart
			}
		})
		b.Run("sse2_R"+benchmarkFloatName(radius), func(b *testing.B) {
			for range b.N {
				xStart, xEnd := circleSpanFloat32SSE2Kernel(256.125, remaining, 256, 513)
				geometryBenchmarkSink = xEnd - xStart
			}
		})
	}
}

func BenchmarkCircleSpanQ16AVX2Direct(b *testing.B) {
	if !cpu.X86.HasAVX2 {
		b.Skip("AVX2 unavailable")
	}
	for _, radius := range []float64{5.25, 25.25, 100.25, 256.25} {
		fixed, ok := newFixedCircleQ16(fit.Circle{X: 256.125, Y: 0, R: radius})
		if !ok {
			b.Fatal("benchmark circle outside Q16.16 range")
		}
		name := benchmarkFloatName(float32(radius))
		b.Run("scalar_R"+name, func(b *testing.B) {
			for range b.N {
				xStart, xEnd, _ := fixed.span(0, 513)
				geometryBenchmarkSink = xEnd - xStart
			}
		})
		b.Run("avx2_R"+name, func(b *testing.B) {
			for range b.N {
				xStart, xEnd, _ := fixed.spanAVX2(0, 513)
				geometryBenchmarkSink = xEnd - xStart
			}
		})
	}
}

func benchmarkFloatName(value float32) string {
	return fmt.Sprintf("%g", value)
}
