package renderer

import (
	"bytes"
	"image"
	"math/rand"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

func TestFixedCircleQ16Range(t *testing.T) {
	tests := []struct {
		name string
		c    fit.Circle
		ok   bool
	}{
		{name: "origin", c: fit.Circle{}, ok: true},
		{name: "fractional", c: fit.Circle{X: 12.25, Y: 31.75, R: 9.5}, ok: true},
		{name: "negative_center", c: fit.Circle{X: -1.25, Y: -2.5, R: 1}, ok: true},
		{name: "largest_safe_integer", c: fit.Circle{X: 32767, Y: 32767, R: 32767}, ok: true},
		{name: "x_overflow", c: fit.Circle{X: 32768, R: 1}},
		{name: "negative_radius", c: fit.Circle{R: -1}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, ok := newFixedCircleQ16(test.c)
			if ok != test.ok {
				t.Fatalf("newFixedCircleQ16(%+v) ok = %v, want %v", test.c, ok, test.ok)
			}
		})
	}
}

func TestFixedCircleQ16CoverageError(t *testing.T) {
	const (
		width  = 513
		height = 389
	)
	rng := rand.New(rand.NewSource(1013))
	changedRows := 0
	totalRows := 0

	for range 10_000 {
		c := fit.Circle{
			X: rng.Float64() * width,
			Y: rng.Float64() * height,
			R: 1 + rng.Float64()*256,
		}
		fixed, ok := newFixedCircleQ16(c)
		if !ok {
			t.Fatalf("ordinary circle unexpectedly outside Q16.16 range: %+v", c)
		}
		radiusSquared := c.R * c.R
		for y := 0; y < height; y++ {
			dy := float64(y) - c.Y
			remaining := radiusSquared - dy*dy
			if remaining < 0 {
				continue
			}
			wantStart, wantEnd := circleSpanFloat64(c.X, remaining, width)
			gotStart, gotEnd, intersects := fixed.span(y, width)
			if !intersects {
				gotStart, gotEnd = 0, 0
			}
			totalRows++
			if gotStart != wantStart || gotEnd != wantEnd {
				changedRows++
			}
		}
	}

	// This is an intentionally quantified approximation test, not an exactness
	// assertion. Q16.16 may change a tangent boundary, but it should remain rare.
	if changedRows > totalRows/100_000+1 {
		t.Fatalf("Q16.16 changed %d of %d intersecting rows; want at most 0.001%%", changedRows, totalRows)
	}
	t.Logf("Q16.16 changed %d of %d intersecting rows", changedRows, totalRows)
}

func TestCircleSpanFloat32CoverageError(t *testing.T) {
	const (
		width  = 513
		height = 389
	)
	rng := rand.New(rand.NewSource(1013))
	changedRows := 0
	totalRows := 0

	for range 10_000 {
		centerX64 := rng.Float64() * width
		centerY64 := rng.Float64() * height
		radius64 := 1 + rng.Float64()*256
		centerX32 := float32(centerX64)
		centerY32 := float32(centerY64)
		radius32 := float32(radius64)
		radiusSquared64 := radius64 * radius64
		radiusSquared32 := radius32 * radius32
		for y := 0; y < height; y++ {
			dy64 := float64(y) - centerY64
			remaining64 := radiusSquared64 - dy64*dy64
			if remaining64 < 0 {
				continue
			}
			wantStart, wantEnd := circleSpanFloat64(centerX64, remaining64, width)
			dy32 := float32(y) - centerY32
			remaining32 := radiusSquared32 - dy32*dy32
			gotStart, gotEnd := 0, 0
			if remaining32 >= 0 {
				gotStart, gotEnd = circleSpanFloat32Selected(centerX32, remaining32, width)
			}
			totalRows++
			if gotStart != wantStart || gotEnd != wantEnd {
				changedRows++
			}
		}
	}

	if changedRows > totalRows/100_000+1 {
		t.Fatalf("float32 changed %d of %d intersecting rows; want at most 0.001%%", changedRows, totalRows)
	}
	t.Logf("float32/%s changed %d of %d intersecting rows", circleSpanFloat32Backend, changedRows, totalRows)
}

func TestFixedCircleQ16ExactRepresentableBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		width    int
		c        fit.Circle
		rowStart int
		rowEnd   int
	}{
		{name: "radius_one_tangents", width: 17, c: fit.Circle{X: 8, Y: 8, R: 1}, rowStart: 6, rowEnd: 11},
		{name: "fractional", width: 37, c: fit.Circle{X: 18.25, Y: 12.75, R: 7.5}, rowStart: 3, rowEnd: 23},
		{name: "left_clipped", width: 33, c: fit.Circle{X: 1.5, Y: 10, R: 9.25}, rowStart: 0, rowEnd: 22},
		{name: "right_clipped", width: 33, c: fit.Circle{X: 31.5, Y: 10, R: 9.25}, rowStart: 0, rowEnd: 22},
		{name: "eight_pixel_batch_boundary", width: 65, c: fit.Circle{X: 32, Y: 16, R: 8}, rowStart: 7, rowEnd: 26},
		{name: "multiple_batches", width: 65, c: fit.Circle{X: 32, Y: 16, R: 17}, rowStart: 0, rowEnd: 34},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixed, ok := newFixedCircleQ16(test.c)
			if !ok {
				t.Fatal("representable test circle rejected")
			}
			radiusSquared := test.c.R * test.c.R
			for y := test.rowStart; y < test.rowEnd; y++ {
				dy := float64(y) - test.c.Y
				remaining := radiusSquared - dy*dy
				wantStart, wantEnd, wantIntersects := 0, 0, remaining >= 0
				if wantIntersects {
					wantStart, wantEnd = circleSpanFloat64(test.c.X, remaining, test.width)
					wantIntersects = wantEnd > wantStart
				}
				gotStart, gotEnd, gotIntersects := fixed.span(y, test.width)
				if gotStart != wantStart || gotEnd != wantEnd || gotIntersects != wantIntersects {
					t.Fatalf("row %d span = [%d,%d),%v; float64 = [%d,%d),%v", y, gotStart, gotEnd, gotIntersects, wantStart, wantEnd, wantIntersects)
				}
			}
		})
	}
}

func TestCPURendererQ16FallbackMatchesFloat64(t *testing.T) {
	const size = 5
	reference := image.NewNRGBA(image.Rect(0, 0, size, size))
	circle := fit.Circle{X: 2, Y: 2, R: 32768, CR: 0.25, CG: 0.5, CB: 0.75, Opacity: 0.5}
	params := encodeCircles([]fit.Circle{circle})

	fallback := NewCPURenderer(reference, 1)
	fallback.SetThreads(1)
	oracle := NewCPURenderer(reference, 1)
	oracle.SetThreads(1)
	oracle.forceFloatGeometry = true

	if got, want := fallback.Render(params), oracle.Render(params); !bytes.Equal(got.Pix, want.Pix) {
		t.Fatal("out-of-range Q16.16 circle did not retain float64 rendering")
	}
}

func BenchmarkCircleSpanGeometry(b *testing.B) {
	circles := []struct {
		name             string
		c                fit.Circle
		rowStart, rowEnd int
	}{
		{name: "R5", c: fit.Circle{X: 256.125, Y: 193.875, R: 5.25}},
		{name: "R25", c: fit.Circle{X: 256.125, Y: 193.875, R: 25.25}},
		{name: "R100", c: fit.Circle{X: 256.125, Y: 193.875, R: 100.25}},
		{name: "R100_row_shard", c: fit.Circle{X: 256.125, Y: 193.875, R: 100.25}, rowStart: 160, rowEnd: 224},
		{name: "R256_clipped", c: fit.Circle{X: 31.125, Y: 193.875, R: 256.25}},
	}

	for _, test := range circles {
		fixed, ok := newFixedCircleQ16(test.c)
		if !ok {
			b.Fatal("benchmark circle outside Q16.16 range")
		}
		minY := max(0, int(test.c.Y-test.c.R))
		maxY := min(389, int(test.c.Y+test.c.R+1))
		if test.rowEnd > test.rowStart {
			minY = max(minY, test.rowStart)
			maxY = min(maxY, test.rowEnd)
		}
		radiusSquared64 := test.c.R * test.c.R
		center32 := float32(test.c.X)
		y32 := float32(test.c.Y)
		radiusSquared32 := float32(test.c.R) * float32(test.c.R)

		b.Run(test.name+"/float64", func(b *testing.B) {
			widthSum := 0
			b.ReportAllocs()
			for range b.N {
				for y := minY; y < maxY; y++ {
					dy := float64(y) - test.c.Y
					remaining := radiusSquared64 - dy*dy
					if remaining >= 0 {
						xStart, xEnd := circleSpanFloat64(test.c.X, remaining, 513)
						widthSum += xEnd - xStart
					}
				}
			}
			geometryBenchmarkSink = widthSum
		})

		b.Run(test.name+"/float32", func(b *testing.B) {
			widthSum := 0
			b.ReportAllocs()
			for range b.N {
				for y := minY; y < maxY; y++ {
					dy := float32(y) - y32
					remaining := radiusSquared32 - dy*dy
					if remaining >= 0 {
						xStart, xEnd := circleSpanFloat32(center32, remaining, 513)
						widthSum += xEnd - xStart
					}
				}
			}
			geometryBenchmarkSink = widthSum
		})

		b.Run(test.name+"/float32_selected_"+circleSpanFloat32Backend, func(b *testing.B) {
			widthSum := 0
			b.ReportAllocs()
			for range b.N {
				for y := minY; y < maxY; y++ {
					dy := float32(y) - y32
					remaining := radiusSquared32 - dy*dy
					if remaining >= 0 {
						xStart, xEnd := circleSpanFloat32Selected(center32, remaining, 513)
						widthSum += xEnd - xStart
					}
				}
			}
			geometryBenchmarkSink = widthSum
		})

		b.Run(test.name+"/q16.16", func(b *testing.B) {
			widthSum := 0
			b.ReportAllocs()
			for range b.N {
				for y := minY; y < maxY; y++ {
					xStart, xEnd, intersects := fixed.span(y, 513)
					if intersects {
						widthSum += xEnd - xStart
					}
				}
			}
			geometryBenchmarkSink = widthSum
		})
	}
}

func BenchmarkCPURendererGeometry(b *testing.B) {
	const (
		width   = 512
		height  = 512
		circles = 100
	)
	reference := image.NewNRGBA(image.Rect(0, 0, width, height))
	params := deterministicParams(circles, width, height, 1013)

	for _, test := range []struct {
		name         string
		forceFloat   bool
		forceFloat32 bool
	}{
		{name: "float64_oracle", forceFloat: true},
		{name: "float32_" + circleSpanFloat32Backend, forceFloat32: true},
		{name: "q16.16"},
	} {
		b.Run(test.name, func(b *testing.B) {
			renderer := NewCPURenderer(reference, circles)
			renderer.SetThreads(1)
			renderer.forceFloatGeometry = test.forceFloat
			renderer.forceFloat32Geometry = test.forceFloat32
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				renderer.Render(params)
			}
		})
	}
}

var geometryBenchmarkSink int
