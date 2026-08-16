package renderer

import (
	"fmt"
	"image"
	"image/color"
	"math/rand"
	"strconv"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

func TestDeltaSSDSpanMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(10_016))
	for _, pixels := range []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 255, 256, 257} {
		t.Run(fmt.Sprintf("%d", pixels), func(t *testing.T) {
			candidate := make([]byte, pixels*4)
			base := make([]byte, pixels*4)
			reference := make([]byte, pixels*4)
			_, _ = rng.Read(candidate)
			_, _ = rng.Read(base)
			_, _ = rng.Read(reference)
			got := deltaSSDSpan(candidate, base, reference, pixels)
			want := deltaSSDSpanScalar(candidate, base, reference, pixels)
			if got != want {
				t.Fatalf("%s delta = %d, scalar = %d", deltaSSDBackend, got, want)
			}
		})
	}
}

func TestDeltaSSDSpanSignedExtremes(t *testing.T) {
	const pixels = 257
	black := make([]byte, pixels*4)
	white := make([]byte, pixels*4)
	for offset := 0; offset < len(white); offset += 4 {
		white[offset+0] = 255
		white[offset+1] = 255
		white[offset+2] = 255
	}
	want := int64(pixels * 3 * 255 * 255)
	if got := deltaSSDSpan(white, black, black, pixels); got != want {
		t.Fatalf("positive delta = %d, want %d", got, want)
	}
	if got := deltaSSDSpan(black, white, black, pixels); got != -want {
		t.Fatalf("negative delta = %d, want %d", got, -want)
	}
}

func TestDirtySpanSetMergesHalfOpenIntervals(t *testing.T) {
	var dirty dirtySpanSet
	dirty.reset(3, 6)
	for _, span := range []dirtySpan{
		{start: 30, end: 40},
		{start: 10, end: 20},
		{start: 20, end: 30}, // Adjacent intervals merge.
		{start: 5, end: 8},
		{start: 7, end: 12},
		{start: 50, end: 60},
	} {
		dirty.add(1, span.start, span.end)
	}

	want := []dirtySpan{{start: 5, end: 40}, {start: 50, end: 60}}
	if !dirty.normalize() {
		t.Fatal("span normalization failed")
	}
	got := dirty.row(1)
	if len(got) != len(want) {
		t.Fatalf("merged row = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("merged row = %#v, want %#v", got, want)
		}
	}
	if pixels, spans := dirty.metrics(); pixels != 45 || spans != 2 {
		t.Fatalf("metrics = (%d pixels, %d spans), want (45, 2)", pixels, spans)
	}
}

func TestDirtySpanSetRandomizedUnion(t *testing.T) {
	const width, height = 97, 23
	rng := rand.New(rand.NewSource(1016))
	var dirty dirtySpanSet
	dirty.reset(height, 2_000)
	want := make([][]bool, height)
	for y := range want {
		want[y] = make([]bool, width)
	}

	for range 2_000 {
		y := rng.Intn(height)
		start := rng.Intn(width)
		end := start + 1 + rng.Intn(width-start)
		dirty.add(y, start, end)
		for x := start; x < end; x++ {
			want[y][x] = true
		}
	}

	gotPixels := 0
	if !dirty.normalize() {
		t.Fatal("span normalization failed")
	}
	for y := 0; y < height; y++ {
		row := dirty.row(y)
		previousEnd := -1
		for _, span := range row {
			if span.start <= previousEnd {
				t.Fatalf("row %d is not strictly separated: %#v", y, row)
			}
			previousEnd = span.end
			for x := span.start; x < span.end; x++ {
				if !want[y][x] {
					t.Fatalf("unexpected dirty pixel (%d,%d)", x, y)
				}
				gotPixels++
			}
		}
	}
	wantPixels := 0
	for _, row := range want {
		for _, covered := range row {
			if covered {
				wantPixels++
			}
		}
	}
	if gotPixels != wantPixels {
		t.Fatalf("dirty pixels = %d, want %d", gotPixels, wantPixels)
	}
}

func TestIncrementalCostMatchesFullImageSSD(t *testing.T) {
	const width, height = 79, 61
	reference := randomNRGBA(width, height, 42)
	opaqueCanvas := randomNRGBA(width, height, 7)
	for offset := 3; offset < len(opaqueCanvas.Pix); offset += 4 {
		opaqueCanvas.Pix[offset] = 255
	}
	translucentCanvas := randomNRGBA(width, height, 8)

	tests := []struct {
		name   string
		canvas *image.NRGBA
		params []float64
	}{
		{name: "white_single", params: deterministicParams(1, width, height, 101)},
		{name: "opaque_retained_batch", canvas: opaqueCanvas, params: deterministicParams(5, width, height, 102)},
		{name: "translucent_retained_batch", canvas: translucentCanvas, params: deterministicParams(5, width, height, 103)},
		{name: "transparent", canvas: opaqueCanvas, params: encodeCircles([]fit.Circle{{X: 20, Y: 20, R: 15}})},
		{name: "clipped", canvas: opaqueCanvas, params: encodeCircles([]fit.Circle{{X: 0, Y: 0, R: 45, CR: 1, Opacity: 0.75}})},
		{name: "overlapping", canvas: opaqueCanvas, params: encodeCircles([]fit.Circle{
			{X: 35, Y: 30, R: 25, CR: 1, Opacity: 0.5},
			{X: 42, Y: 30, R: 25, CG: 1, Opacity: 0.5},
			{X: 38, Y: 36, R: 25, CB: 1, Opacity: 0.5},
		})},
	}

	for _, test := range tests {
		for _, threads := range []int{1, 4} {
			t.Run(test.name+"/threads="+strconv.Itoa(threads), func(t *testing.T) {
				circleCount := len(test.params) / paramsPerCircle
				full := newCostTestRenderer(reference, test.canvas, circleCount)
				incremental := newCostTestRenderer(reference, test.canvas, circleCount)
				full.SetThreads(threads)
				incremental.SetThreads(threads)
				incremental.incrementalCostMode = incrementalCostForce

				want := full.Cost(test.params)
				got := incremental.Cost(test.params)
				if got != want {
					t.Fatalf("incremental cost = %.17g, full cost = %.17g", got, want)
				}
			})
		}
	}
}

func TestIncrementalCostSignedDeltaDirections(t *testing.T) {
	const width, height = 17, 13
	black := image.NewNRGBA(image.Rect(0, 0, width, height))
	white := image.NewNRGBA(black.Bounds())
	for y := range height {
		for x := range width {
			black.SetNRGBA(x, y, color.NRGBA{A: 255})
			white.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}

	tests := []struct {
		name      string
		reference *image.NRGBA
		canvas    *image.NRGBA
		circle    fit.Circle
	}{
		{name: "decrease", reference: black, canvas: white, circle: fit.Circle{X: 8, Y: 6, R: 100, Opacity: 1}},
		{name: "increase", reference: black, canvas: black, circle: fit.Circle{X: 8, Y: 6, R: 100, CR: 1, CG: 1, CB: 1, Opacity: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := encodeCircles([]fit.Circle{test.circle})
			full := NewCPURendererWithCanvas(test.reference, test.canvas, 1)
			incremental := NewCPURendererWithCanvas(test.reference, test.canvas, 1)
			incremental.incrementalCostMode = incrementalCostForce
			if got, want := incremental.Cost(params), full.Cost(params); got != want {
				t.Fatalf("incremental cost = %.17g, full cost = %.17g", got, want)
			}
		})
	}
}

func TestIncrementalCostRandomizedParity(t *testing.T) {
	const width, height = 67, 53
	reference := randomNRGBA(width, height, 501)
	rng := rand.New(rand.NewSource(1016))
	for iteration := range 250 {
		circleCount := 1 + rng.Intn(8)
		canvas := randomNRGBA(width, height, int64(1_000+iteration))
		params := deterministicParams(circleCount, width, height, int64(2_000+iteration))
		full := NewCPURendererWithCanvas(reference, canvas, circleCount)
		incremental := NewCPURendererWithCanvas(reference, canvas, circleCount)
		incremental.incrementalCostMode = incrementalCostForce
		if iteration%2 == 0 {
			full.SetThreads(4)
			incremental.SetThreads(4)
		}
		if got, want := incremental.Cost(params), full.Cost(params); got != want {
			t.Fatalf("iteration %d: incremental cost = %.17g, full cost = %.17g", iteration, got, want)
		}
	}
}

func TestIncrementalCostFallbackAndSessionRules(t *testing.T) {
	reference := randomNRGBA(32, 24, 42)
	renderer := NewCPURenderer(reference, 1)
	renderer.incrementalCostMode = incrementalCostAuto
	custom := func(_, _ *image.NRGBA) float64 { return 42 }
	renderer.SetCostFunc(custom)
	if renderer.fastCostSelected {
		t.Fatal("custom cost did not disable incremental FastMSE selection")
	}
	if got := renderer.Cost(deterministicParams(1, 32, 24, 99)); got != 42 {
		t.Fatalf("custom fallback cost = %v, want 42", got)
	}

	session, cleanup, err := renderer.newSession(1)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	child := session.(*CPURenderer)
	if child.fastCostSelected || child.incrementalCostMode != incrementalCostAuto {
		t.Fatalf("child rules = (fast=%v, mode=%d), want (false, auto)", child.fastCostSelected, child.incrementalCostMode)
	}

	renderer.UseFastCost()
	if !renderer.fastCostSelected {
		t.Fatal("UseFastCost did not restore incremental FastMSE selection")
	}
	renderer.initialSSDValid = false
	params := deterministicParams(1, 32, 24, 100)
	if got, want := renderer.Cost(params), fit.FastMSECost(renderer.Render(params), reference); got != want {
		t.Fatalf("invalid-base fallback = %.17g, full cost = %.17g", got, want)
	}
}

func TestIncrementalCostWorthwhilePolicy(t *testing.T) {
	var dirty dirtySpanSet
	dirty.reset(100, 101)
	dirty.add(0, 0, 10)
	if !incrementalCostWorthwhile(&dirty, 10_000) {
		t.Fatal("small contiguous dirty area rejected")
	}
	for y := range 100 {
		dirty.add(y, 0, 100)
	}
	if incrementalCostWorthwhile(&dirty, 10_000) {
		t.Fatal("full-image dirty area accepted")
	}
}

func TestIncrementalCostPreflightPolicy(t *testing.T) {
	renderer := NewCPURenderer(randomNRGBA(256, 256, 42), 1)
	small := encodeCircles([]fit.Circle{{X: 128, Y: 128, R: 64, Opacity: 1}})
	if !renderer.incrementalCandidateWorthwhile(small) {
		t.Fatal("measured small-circle case rejected")
	}
	large := encodeCircles([]fit.Circle{{X: 128, Y: 128, R: 96, Opacity: 1}})
	if renderer.incrementalCandidateWorthwhile(large) {
		t.Fatal("measured large-circle fallback case accepted")
	}
	transparent := encodeCircles([]fit.Circle{{X: 128, Y: 128, R: 128}})
	if !renderer.incrementalCandidateWorthwhile(transparent) {
		t.Fatal("transparent circle rejected")
	}

	smallRenderer := NewCPURenderer(randomNRGBA(64, 64, 43), 1)
	smallAccepted := encodeCircles([]fit.Circle{{X: 32, Y: 32, R: 12, Opacity: 1}})
	if !smallRenderer.incrementalCandidateWorthwhile(smallAccepted) {
		t.Fatal("small-image measured winner rejected")
	}
	smallRejected := encodeCircles([]fit.Circle{{X: 32, Y: 32, R: 16, Opacity: 1}})
	if smallRenderer.incrementalCandidateWorthwhile(smallRejected) {
		t.Fatal("small-image measured loser accepted")
	}
}

func TestIncrementalStagedSessionEligibility(t *testing.T) {
	reference := randomNRGBA(64, 64, 44)
	smallSingle := NewCPURenderer(reference, 1)
	if !smallSingle.incrementalStagedSessionEligible() {
		t.Fatal("small single-circle stage rejected")
	}
	smallBatch := NewCPURenderer(reference, 5)
	if smallBatch.incrementalStagedSessionEligible() {
		t.Fatal("small multi-circle stage accepted")
	}
	smallBatch.stagedIncremental = true
	session, cleanup, err := smallBatch.newSessionWithCanvas(smallBatch.initialCanvas(), 5)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if mode := session.(*CPURenderer).incrementalCostMode; mode != incrementalCostDisabled {
		t.Fatalf("small batch session mode = %d, want disabled", mode)
	}
	largeBatch := NewCPURenderer(randomNRGBA(256, 256, 46), 5)
	if !largeBatch.incrementalStagedSessionEligible() {
		t.Fatal("large multi-circle stage rejected")
	}
}

func newCostTestRenderer(reference, canvas *image.NRGBA, circles int) *CPURenderer {
	if canvas == nil {
		return NewCPURenderer(reference, circles)
	}
	return NewCPURendererWithCanvas(reference, canvas, circles)
}

func BenchmarkDeltaSSDSpan(b *testing.B) {
	for _, pixels := range []int{1, 2, 4, 8, 16, 32, 64, 128, 256} {
		candidate := make([]byte, pixels*4)
		base := make([]byte, pixels*4)
		reference := make([]byte, pixels*4)
		rng := rand.New(rand.NewSource(int64(pixels)))
		_, _ = rng.Read(candidate)
		_, _ = rng.Read(base)
		_, _ = rng.Read(reference)
		b.Run(fmt.Sprintf("scalar/%d", pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 12))
			for range b.N {
				rendererDeltaSink = deltaSSDSpanScalar(candidate, base, reference, pixels)
			}
		})
		b.Run(fmt.Sprintf("auto_%s/%d", deltaSSDBackend, pixels), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 12))
			for range b.N {
				rendererDeltaSink = deltaSSDSpan(candidate, base, reference, pixels)
			}
		})
	}
}

var rendererDeltaSink int64
