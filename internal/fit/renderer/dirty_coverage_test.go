package renderer

import (
	"bytes"
	"image"
	"math/rand"
	"sort"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
)

type dirtyTestSpan struct {
	start int
	end   int
}

type dirtyCoverageMetrics struct {
	dirtyPixels int
	dirtyRows   int
	rawSpans    int
	mergedSpans int
}

// TestDirtySpanCoverageMetrics records the geometric work available to an
// incremental cost path. Opaque black circles over a white canvas make every
// geometrically dirty pixel change, which also checks the test-only span union
// against production rendering.
func TestDirtySpanCoverageMetrics(t *testing.T) {
	const width, height = 256, 256

	circle := func(x, y, radius float64) fit.Circle {
		return fit.Circle{X: x, Y: y, R: radius, Opacity: 1}
	}
	repeated := func(count int, c fit.Circle) []fit.Circle {
		circles := make([]fit.Circle, count)
		for i := range circles {
			circles[i] = c
		}

		return circles
	}

	disjoint := []fit.Circle{
		circle(48, 48, 32),
		circle(208, 48, 32),
		circle(48, 208, 32),
		circle(208, 208, 32),
	}
	clustered := []fit.Circle{
		circle(112, 112, 32),
		circle(144, 112, 32),
		circle(112, 144, 32),
		circle(144, 144, 32),
	}

	rng := rand.New(rand.NewSource(1016))

	batchPool := make([]fit.Circle, 32)
	for i := range batchPool {
		batchPool[i] = circle(16+rng.Float64()*224, 16+rng.Float64()*224, 16)
	}

	tests := []struct {
		category string
		name     string
		circles  []fit.Circle
	}{
		{category: "radius", name: "R4", circles: []fit.Circle{circle(128, 128, 4)}},
		{category: "radius", name: "R8", circles: []fit.Circle{circle(128, 128, 8)}},
		{category: "radius", name: "R16", circles: []fit.Circle{circle(128, 128, 16)}},
		{category: "radius", name: "R32", circles: []fit.Circle{circle(128, 128, 32)}},
		{category: "radius", name: "R64", circles: []fit.Circle{circle(128, 128, 64)}},
		{category: "radius", name: "R96", circles: []fit.Circle{circle(128, 128, 96)}},
		{category: "radius", name: "R128", circles: []fit.Circle{circle(128, 128, 128)}},
		{category: "clipping_R64", name: "center", circles: []fit.Circle{circle(128, 128, 64)}},
		{category: "clipping_R64", name: "edge", circles: []fit.Circle{circle(0, 128, 64)}},
		{category: "clipping_R64", name: "corner", circles: []fit.Circle{circle(0, 0, 64)}},
		{category: "overlap_K4_R32", name: "coincident", circles: repeated(4, circle(128, 128, 32))},
		{category: "overlap_K4_R32", name: "clustered", circles: clustered},
		{category: "overlap_K4_R32", name: "disjoint", circles: disjoint},
		{category: "batch_R16", name: "K1", circles: batchPool[:1]},
		{category: "batch_R16", name: "K2", circles: batchPool[:2]},
		{category: "batch_R16", name: "K4", circles: batchPool[:4]},
		{category: "batch_R16", name: "K8", circles: batchPool[:8]},
		{category: "batch_R16", name: "K16", circles: batchPool[:16]},
		{category: "batch_R16", name: "K32", circles: batchPool[:32]},
	}

	for _, test := range tests {
		t.Run(test.category+"/"+test.name, func(t *testing.T) {
			metrics := measureDirtyCoverage(test.circles, width, height)

			changedPixels := renderChangedPixels(test.circles, width, height)
			if changedPixels != metrics.dirtyPixels {
				t.Fatalf("changed pixels = %d, dirty-span union = %d", changedPixels, metrics.dirtyPixels)
			}

			if metrics.mergedSpans > metrics.rawSpans {
				t.Fatalf("merged spans = %d exceeds raw spans = %d", metrics.mergedSpans, metrics.rawSpans)
			}

			t.Logf(
				"dirty=%d/%d (%.3f%%) rows=%d/%d (%.3f%%) spans=%d/%d merged/raw (%.3f%%)",
				metrics.dirtyPixels,
				width*height,
				100*float64(metrics.dirtyPixels)/float64(width*height),
				metrics.dirtyRows,
				height,
				100*float64(metrics.dirtyRows)/height,
				metrics.mergedSpans,
				metrics.rawSpans,
				100*float64(metrics.mergedSpans)/float64(metrics.rawSpans),
			)
		})
	}
}

func measureDirtyCoverage(circles []fit.Circle, width, height int) dirtyCoverageMetrics {
	rows := make([][]dirtyTestSpan, height)
	metrics := dirtyCoverageMetrics{}

	for _, c := range circles {
		if c.Opacity == 0 {
			continue
		}

		minY := max(0, int(c.Y-c.R))
		maxY := min(height, int(c.Y+c.R+1))
		fixed, useFixed := newFixedCircleQ16(c)
		radiusSquared := c.R * c.R

		for y := minY; y < maxY; y++ {
			var xStart, xEnd int

			if useFixed {
				var intersects bool

				xStart, xEnd, intersects = fixed.span(y, width)
				if !intersects {
					continue
				}
			} else {
				dy := float64(y) - c.Y

				remaining := radiusSquared - dy*dy
				if remaining < 0 {
					continue
				}

				xStart, xEnd = circleSpanFloat64(c.X, remaining, width)
				xStart = max(0, xStart)

				xEnd = min(width, xEnd)
				if xEnd <= xStart {
					continue
				}
			}

			rows[y] = append(rows[y], dirtyTestSpan{start: xStart, end: xEnd})
			metrics.rawSpans++
		}
	}

	for _, spans := range rows {
		if len(spans) == 0 {
			continue
		}

		metrics.dirtyRows++

		sort.Slice(spans, func(i, j int) bool {
			if spans[i].start == spans[j].start {
				return spans[i].end < spans[j].end
			}

			return spans[i].start < spans[j].start
		})

		merged := spans[0]
		for _, span := range spans[1:] {
			if span.start <= merged.end {
				merged.end = max(merged.end, span.end)
				continue
			}

			metrics.dirtyPixels += merged.end - merged.start
			metrics.mergedSpans++
			merged = span
		}

		metrics.dirtyPixels += merged.end - merged.start
		metrics.mergedSpans++
	}

	return metrics
}

func renderChangedPixels(circles []fit.Circle, width, height int) int {
	base := image.NewNRGBA(image.Rect(0, 0, width, height))
	for offset := 0; offset < len(base.Pix); offset += 4 {
		base.Pix[offset+0] = 255
		base.Pix[offset+1] = 255
		base.Pix[offset+2] = 255
		base.Pix[offset+3] = 255
	}

	reference := image.NewNRGBA(base.Bounds())
	renderer := NewCPURendererWithCanvas(reference, base, len(circles))
	renderer.SetThreads(1)

	params := make([]float64, len(circles)*paramsPerCircle)

	vector := fit.ParamVector{Data: params, K: len(circles), Width: width, Height: height}
	for i, c := range circles {
		vector.EncodeCircle(i, c)
	}

	rendered := renderer.Render(params)

	changed := 0

	for offset := 0; offset < len(base.Pix); offset += 4 {
		if !bytes.Equal(base.Pix[offset:offset+4], rendered.Pix[offset:offset+4]) {
			changed++
		}
	}

	return changed
}
