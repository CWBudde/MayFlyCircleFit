package renderer

import (
	"image"
	"image/color"
	"reflect"
	"runtime"
	"strconv"
	"testing"
)

// selectionParityCircles is the fixture the parity tests share: enough circles
// to be split across every worker the host offers, small enough relative to the
// canvas that most of them miss any single grid tile, and irregular enough that
// draw order matters.
const (
	selectionParityWidth   = 96
	selectionParityHeight  = 96
	selectionParityCircles = 48
)

func selectionParityFixture(t testing.TB) (*image.NRGBA, []float64) {
	t.Helper()
	reference := solidImage(selectionParityWidth, selectionParityHeight, color.NRGBA{R: 60, G: 120, B: 180, A: 255})
	for y := range selectionParityHeight {
		for x := range selectionParityWidth {
			reference.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*3 + y) % 256),
				G: uint8((y * 5) % 256),
				B: uint8((x ^ y) % 256),
				A: 255,
			})
		}
	}
	return reference, productionPolishParams(selectionParityCircles, selectionParityWidth, selectionParityHeight)
}

func selectionParityRenderer(reference *image.NRGBA, threads int) *CPURenderer {
	cpu := NewCPURenderer(reference, selectionParityCircles)
	cpu.SetThreads(threads)
	return cpu
}

// TestAuditCircleBatchChunkedMatchesSerial pins the contract the parallel walk
// rests on: splitting the draw order across sessions changes throughput and
// nothing else. It fails if a chunk's rebuilt prefix ever differs from the one
// the serial walk accumulated.
func TestAuditCircleBatchChunkedMatchesSerial(t *testing.T) {
	reference, params := selectionParityFixture(t)
	workers := runtime.GOMAXPROCS(0)
	if workers < 2 {
		t.Skip("chunked audit needs more than one rendering thread")
	}

	serial, err := AuditCircleBatch(selectionParityRenderer(reference, 1), params)
	if err != nil {
		t.Fatalf("serial AuditCircleBatch() error = %v", err)
	}
	parallelRenderer := selectionParityRenderer(reference, workers)
	plan, release := planAudit(parallelRenderer, params, selectionParityCircles)
	release()
	if len(plan.steppers) < 2 || len(plan.chunks) < len(plan.steppers) {
		t.Fatalf("planAudit() gave %d steppers and %d chunks at GOMAXPROCS=%d, want a queue wider than one worker",
			len(plan.steppers), len(plan.chunks), workers)
	}
	chunked, err := AuditCircleBatch(parallelRenderer, params)
	if err != nil {
		t.Fatalf("chunked AuditCircleBatch() error = %v", err)
	}
	if !reflect.DeepEqual(chunked, serial) {
		t.Fatalf("chunked audit = %+v, want serial audit %+v", chunked, serial)
	}
}

// TestAuditChunksCoverDrawOrder checks the property the queue depends on and
// that no worker checks at run time: the runs are contiguous, non-overlapping,
// and together cover every draw slot exactly once.
func TestAuditChunksCoverDrawOrder(t *testing.T) {
	for _, circleCount := range []int{1, 2, 3, 8, 32, 256, 512} {
		for _, count := range []int{1, 2, 3, 4, 8, 12, 48, 1000} {
			chunks := auditChunks(circleCount, count)
			if len(chunks) == 0 {
				t.Fatalf("circles %d count %d produced no chunks", circleCount, count)
			}
			previousEnd := 0
			for _, chunk := range chunks {
				if chunk.start != previousEnd || chunk.end <= chunk.start || chunk.end > circleCount {
					t.Fatalf("circles %d count %d chunk %+v does not continue from %d", circleCount, count, chunk, previousEnd)
				}
				previousEnd = chunk.end
			}
			if previousEnd != circleCount {
				t.Fatalf("circles %d count %d covered %d slots, want %d", circleCount, count, previousEnd, circleCount)
			}
		}
	}
}

// fullRenderRenderer hides in-place compositing, so callers that would take a
// row-band shortcut fall back to rendering the whole canvas. It is how the
// tests below compare the shortcut against the definition it optimizes.
type fullRenderRenderer struct {
	Renderer
}

// TestRegionInfluenceEnergiesMatchFullRenders is the evidence for the claim the
// row-band shortcut rests on: a circle cannot change a pixel outside its own
// raster, so restricting both the render and the comparison to region
// intersected with that raster leaves every energy unchanged.
func TestRegionInfluenceEnergiesMatchFullRenders(t *testing.T) {
	reference, params := selectionParityFixture(t)
	candidates := make([]int, selectionParityCircles)
	for i := range candidates {
		candidates[i] = i
	}
	regions := []image.Rectangle{
		image.Rect(0, 0, 24, 24),
		image.Rect(24, 24, 48, 48),
		image.Rect(72, 72, 96, 96),
		image.Rect(0, 0, selectionParityWidth, selectionParityHeight),
	}

	for _, threads := range []int{1, runtime.GOMAXPROCS(0)} {
		for _, region := range regions {
			name := "threads-" + strconv.Itoa(threads) + "/region-" + region.String()
			t.Run(name, func(t *testing.T) {
				banded := selectionParityRenderer(reference, threads)
				full := selectionParityRenderer(reference, threads)
				fullImage := cloneNRGBA(banded.Render(params))
				want := regionInfluenceEnergies(fullRenderRenderer{Renderer: full}, params, fullImage, region, candidates)
				got := regionInfluenceEnergies(banded, params, fullImage, region, candidates)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("row-band energies = %v, want full-render energies %v", got, want)
				}
			})
		}
	}
}

// TestSelectPolishingActiveSetMatchesSerial is the acceptance check for Task
// 15.2: every strategy must return the same active set, replacement set, and
// region however wide the selection ran.
func TestSelectPolishingActiveSetMatchesSerial(t *testing.T) {
	reference, params := selectionParityFixture(t)
	workers := runtime.GOMAXPROCS(0)
	if workers < 2 {
		t.Skip("parallel selection needs more than one rendering thread")
	}
	strategies := []BatchPolishStrategy{
		BatchPolishWeakestReplacement,
		BatchPolishHybridOverlap,
		BatchPolishResidualRegion,
		BatchPolishContiguousWindow,
	}
	// Visit state steers every strategy's tie-breaks, so the two runs have to
	// start from the same history, not merely from the same parameters.
	visited := map[int]bool{0: true, 5: true}
	visits := map[int]int{3: 1, 9: 2, 17: 1}
	selectWith := func(threads int, strategy BatchPolishStrategy) polishingActiveSet {
		t.Helper()
		visitedCopy := make(map[int]bool, len(visited))
		for region := range visited {
			visitedCopy[region] = true
		}
		visitsCopy := make(map[int]int, len(visits))
		for circle, count := range visits {
			visitsCopy[circle] = count
		}
		base := selectionParityRenderer(reference, threads)
		selection, err := selectPolishingActiveSet(
			base, &incumbentAuditCache{session: base},
			params, 8, strategy, visitedCopy, visitsCopy, false,
		)
		if err != nil {
			t.Fatalf("selectPolishingActiveSet(%s, threads %d) error = %v", strategy, threads, err)
		}
		return selection
	}

	for _, strategy := range strategies {
		t.Run(string(strategy), func(t *testing.T) {
			serial := selectWith(1, strategy)
			parallel := selectWith(workers, strategy)
			if !reflect.DeepEqual(parallel, serial) {
				t.Fatalf("selection at %d threads = %+v, want serial selection %+v", workers, parallel, serial)
			}
		})
	}
}

// BenchmarkPolishSelectionByCircleCount is the acceptance measurement for Task
// 15.2: residual-region selection cost per sweep against the circle count, at
// one rendering thread and at the host's full width.
//
// Selection is quadratic in the circle count -- it ranks every circle by
// rendering the vector without it -- so the per-circle metric is what shows
// whether the work per circle actually fell rather than merely being spread
// out.
func BenchmarkPolishSelectionByCircleCount(b *testing.B) {
	reference := solidImage(polishBenchmarkWidth, polishBenchmarkHeight, color.NRGBA{R: 60, G: 120, B: 180, A: 255})
	b.Logf("GOMAXPROCS=%d", runtime.GOMAXPROCS(0))
	for _, circleCount := range []int{32, 128, 512} {
		params := productionPolishParams(circleCount, polishBenchmarkWidth, polishBenchmarkHeight)
		for _, threads := range []int{1, runtime.GOMAXPROCS(0)} {
			name := "circles-" + strconv.Itoa(circleCount) + "/threads-" + strconv.Itoa(threads)
			b.Run(name, func(b *testing.B) {
				cpu := NewCPURenderer(reference, circleCount)
				cpu.SetThreads(threads)
				b.ReportAllocs()
				for range b.N {
					if _, err := selectResidualRegionActiveSet(cpu, &incumbentAuditCache{session: cpu}, params, polishBenchmarkActiveSetSize, nil); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*circleCount), "ns/circle")
			})
		}
	}
}

// BenchmarkRegionInfluenceEnergies isolates the row-band shortcut from the
// chunked walk by measuring the influence loop alone against the full-canvas
// renders it replaces, at one thread so neither path is also being parallelized.
func BenchmarkRegionInfluenceEnergies(b *testing.B) {
	reference := solidImage(polishBenchmarkWidth, polishBenchmarkHeight, color.NRGBA{R: 60, G: 120, B: 180, A: 255})
	region := gridRegion(reference.Bounds(), 1, 1, residualPolishGridSize, residualPolishGridSize)
	for _, circleCount := range []int{32, 128, 512} {
		params := productionPolishParams(circleCount, polishBenchmarkWidth, polishBenchmarkHeight)
		candidates := make([]int, circleCount)
		for i := range candidates {
			candidates[i] = i
		}
		cpu := NewCPURenderer(reference, circleCount)
		cpu.SetThreads(1)
		fullImage := cloneNRGBA(cpu.Render(params))
		for _, banded := range []bool{false, true} {
			base := Renderer(cpu)
			name := "circles-" + strconv.Itoa(circleCount) + "/full-render"
			if banded {
				name = "circles-" + strconv.Itoa(circleCount) + "/row-band"
			} else {
				base = fullRenderRenderer{Renderer: cpu}
			}
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					regionInfluenceEnergies(base, params, fullImage, region, candidates)
				}
				b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*circleCount), "ns/circle")
			})
		}
	}
}
