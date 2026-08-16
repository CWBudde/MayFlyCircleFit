package renderer

import (
	"context"
	"image/color"
	"runtime"
	"strconv"
	"testing"
)

// The production polishing shape, taken from the run these benchmarks exist to
// predict: a 512x512 reference, residual-region selection, and eight active
// circles. The candidate count is the one axis that is swept, because it is the
// only thing that decides whether a sweep is dominated by its fixed setup or by
// its evaluations, and the real run sits far to the right of anything that can
// be benchmarked repeatedly (popSize 200 x ~430 evaluations per Mayfly
// iteration x 800 iters x 2 epochs is roughly 690,000 candidates per sweep).
const (
	polishBenchmarkWidth         = 512
	polishBenchmarkHeight        = 512
	polishBenchmarkActiveSetSize = 8
)

var polishBenchmarkPoolWidths = []int{1, 8, 12, 48}

// productionPolishParams is benchmarkPolishParams with the radii scaled to the
// image, which matters for what these benchmarks measure. The 128x128 fixture's
// radius 4..10 circles cover about 200 pixels each, so at 512x512 the union of
// 512 of them is a fifth of the canvas and per-candidate cost is dominated by
// the full-canvas clear and SSD passes rather than by rasterizing circles. A
// real 512-circle fit of a 512x512 reference overdraws the canvas several times
// over, so the radii are scaled by the same factor as the image.
func productionPolishParams(circleCount, width, height int) []float64 {
	scale := float64(width) / 128
	params := make([]float64, 0, circleCount*paramsPerCircle)
	for i := range circleCount {
		params = append(params, circleParams(
			float64((i*37)%width)+0.5,
			float64((i*53)%height)+0.5,
			(4+float64(i%7))*scale,
			color.NRGBA{R: uint8(i * 3), G: uint8(i * 5), B: uint8(i * 7), A: 255},
			0.4+float64(i%4)/10,
		)...)
	}
	return params
}

// BenchmarkPolishSweepProductionShape measures one residual-region polish sweep
// at the production image size and active-set size, sweeping both the pool
// width and the number of candidates the optimizer evaluates.
//
// A sweep costs fixedCost + candidates*perCandidateCost. Only the second term
// is what the session pool parallelizes; the first -- residual-region active-set
// selection, which renders the vector once per circle, plus one baked-suffix
// session and canvas per pool slot -- is serial. Sweeping the candidate count
// over more than an order of magnitude separates the two terms, so the slope is
// the pool's real speedup and the intercept is what no width can improve. It is
// the slope that predicts a production sweep, which runs candidates in the
// hundreds of thousands and amortizes the intercept away entirely.
//
// Pool widths above GOMAXPROCS cannot overlap any further on the machine that
// runs the benchmark; the ns/candidate metric is what to compare, and the width
// that was actually achieved is min(width, GOMAXPROCS). Reporting a width above
// GOMAXPROCS as if it had run that wide would be wrong -- it only buys that many
// canvases, not that much overlap.
func BenchmarkPolishSweepProductionShape(b *testing.B) {
	ref := solidImage(polishBenchmarkWidth, polishBenchmarkHeight, color.NRGBA{R: 60, G: 120, B: 180, A: 255})
	b.Logf("GOMAXPROCS=%d, so any pool wider than that is reported for the record only", runtime.GOMAXPROCS(0))
	for _, circleCount := range []int{256, 512} {
		params := productionPolishParams(circleCount, polishBenchmarkWidth, polishBenchmarkHeight)
		for _, candidates := range []int{1000, 4000, 16000} {
			for _, workers := range polishBenchmarkPoolWidths {
				name := "circles-" + strconv.Itoa(circleCount) +
					"/candidates-" + strconv.Itoa(candidates) +
					"/width-" + strconv.Itoa(workers)
				b.Run(name, func(b *testing.B) {
					cpu := NewCPURenderer(ref, circleCount)
					cpu.SetThreads(1)
					b.ReportAllocs()
					for range b.N {
						_, err := PolishCircleBatchContext(context.Background(), cpu,
							&concurrentPolishOptimizer{workers: workers, candidates: candidates},
							params, BatchPolishOptions{
								ActiveSetSize: polishBenchmarkActiveSetSize,
								MaxSweeps:     1,
								Strategy:      BatchPolishResidualRegion,
							})
						if err != nil {
							b.Fatal(err)
						}
					}
					b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*candidates), "ns/candidate")
				})
			}
		}
	}
}

// BenchmarkPolishSweepPoolSetup measures only the fixed cost a sweep pays
// before its first candidate: one baked-suffix session per pool slot, each of
// which renders the fixed draw-order prefix and clones a full canvas. It is the
// cost that pooling adds outright, so it decides whether a short sweep is a net
// loss, and its allocation figure is what a 512x512 slot really holds.
//
// The prefix length is the one residual-region selection produces at this shape
// -- the strategy picks by image-space merit, so the first active circle sits
// near the front of the draw order and almost nothing is bakeable. Measured
// against BenchmarkPolishResidualRegionSelection, this cost is small: pooling
// adds tens of milliseconds to a sweep whose selection alone costs seconds, so
// a pool pays for itself within the first hundred candidates.
func BenchmarkPolishSweepPoolSetup(b *testing.B) {
	ref := solidImage(polishBenchmarkWidth, polishBenchmarkHeight, color.NRGBA{R: 60, G: 120, B: 180, A: 255})
	for _, circleCount := range []int{256, 512} {
		params := productionPolishParams(circleCount, polishBenchmarkWidth, polishBenchmarkHeight)
		const prefixCircles = 11
		for _, workers := range []int{1, 8, 48} {
			b.Run("circles-"+strconv.Itoa(circleCount)+"/width-"+strconv.Itoa(workers), func(b *testing.B) {
				cpu := NewCPURenderer(ref, circleCount)
				cpu.SetThreads(1)
				primary, primaryCleanup, ok := bakedSuffixSession(cpu, params, prefixCircles, circleCount)
				if !ok {
					b.Fatal("baked suffix session not applied")
				}
				defer primaryCleanup()
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					pool := newEvaluationPool(primary, params, workers, func() (Renderer, func(), error) {
						session, cleanup, baked := bakedSuffixSession(cpu, params, prefixCircles, circleCount)
						if !baked {
							return nil, nil, ErrStagedOptimizationUnsupported
						}
						return session, cleanup, nil
					})
					if pool.width() != workers {
						b.Fatalf("pool width = %d, want %d", pool.width(), workers)
					}
					pool.close()
				}
			})
		}
	}
}

// BenchmarkPolishResidualRegionSelection measures the other half of a sweep's
// fixed cost, which the session pool does not touch at all: residual-region
// selection renders the complete vector once per circle to rank each circle's
// influence on the worst tile, so it is serial and linear in the circle count.
func BenchmarkPolishResidualRegionSelection(b *testing.B) {
	ref := solidImage(polishBenchmarkWidth, polishBenchmarkHeight, color.NRGBA{R: 60, G: 120, B: 180, A: 255})
	for _, circleCount := range []int{256, 512} {
		params := productionPolishParams(circleCount, polishBenchmarkWidth, polishBenchmarkHeight)
		b.Run("circles-"+strconv.Itoa(circleCount), func(b *testing.B) {
			cpu := NewCPURenderer(ref, circleCount)
			cpu.SetThreads(1)
			b.ReportAllocs()
			for range b.N {
				if _, err := selectResidualRegionActiveSet(cpu, params, polishBenchmarkActiveSetSize, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
