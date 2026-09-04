package renderer

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit"
)

func TestPolishDirtySessionMatchesFullCanvas(t *testing.T) {
	t.Parallel()

	const width, height, circleCount = 128, 96, 112
	reference := randomNRGBA(width, height, 15_700)
	incumbent := tinyPolishParams(circleCount, width, height, 15_701)

	tests := []struct {
		name      string
		active    []int
		configure func([]float64, []float64, []int)
	}{
		{
			name:   "scattered",
			active: []int{3, 31, 67, 105},
			configure: func(old, candidate []float64, active []int) {
				for i, circle := range active {
					setCirclePosition(old, circle, 12+float64(i)*31, 14+float64(i%2)*61, 3)
					copy(candidate[circle*paramsPerCircle:(circle+1)*paramsPerCircle], old[circle*paramsPerCircle:(circle+1)*paramsPerCircle])
					candidate[circle*paramsPerCircle] += 2
				}
			},
		},
		{
			name:   "clustered",
			active: []int{14, 15, 16, 17},
			configure: func(old, candidate []float64, active []int) {
				for i, circle := range active {
					setCirclePosition(old, circle, 61+float64(i%2)*3, 45+float64(i/2)*3, 7)
					copy(candidate[circle*paramsPerCircle:(circle+1)*paramsPerCircle], old[circle*paramsPerCircle:(circle+1)*paramsPerCircle])
					candidate[circle*paramsPerCircle+1] += 2
				}
			},
		},
		{
			name:   "canvas-edge-crossing",
			active: []int{8, 42, 91},
			configure: func(old, candidate []float64, active []int) {
				positions := [][2]float64{{0, 0}, {127, 48}, {64, 95}}
				for i, circle := range active {
					setCirclePosition(old, circle, positions[i][0], positions[i][1], 4)
					copy(candidate[circle*paramsPerCircle:(circle+1)*paramsPerCircle], old[circle*paramsPerCircle:(circle+1)*paramsPerCircle])
					candidate[circle*paramsPerCircle] += []float64{4, -5, 3}[i]
				}
			},
		},
		{
			name:   "radius-growing",
			active: []int{55},
			configure: func(old, candidate []float64, active []int) {
				circle := active[0]
				setCirclePosition(old, circle, 64, 48, 1)
				copy(candidate[circle*paramsPerCircle:(circle+1)*paramsPerCircle], old[circle*paramsPerCircle:(circle+1)*paramsPerCircle])
				candidate[circle*paramsPerCircle+2] = 10
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			old := append([]float64(nil), incumbent...)
			candidate := append([]float64(nil), old...)
			test.configure(old, candidate, test.active)

			full := NewCPURenderer(reference, circleCount)
			full.SetThreads(1)
			baseline := cloneNRGBA(full.Render(old))

			baselineSSD, ok := fit.ExactSSD(baseline, reference)
			if !ok {
				t.Fatal("baseline SSD is not exact")
			}

			dirtyCPU := NewCPURenderer(reference, circleCount)
			dirtyCPU.SetThreads(1)
			dirty := newPolishDirtySession(dirtyCPU, baseline, baselineSSD, old, test.active).(*polishDirtySession)

			wantCost := full.Cost(candidate)
			wantImage := cloneNRGBA(full.Render(candidate))

			gotCost := dirty.Cost(candidate)
			if gotCost != wantCost {
				t.Fatalf("dirty cost = %.17g, full cost = %.17g", gotCost, wantCost)
			}

			if dirty.canvas.Rect != wantImage.Rect || dirty.canvas.Stride != wantImage.Stride || !bytes.Equal(dirty.canvas.Pix, wantImage.Pix) {
				t.Fatal("dirty recomposite differs from full render")
			}

			if dirty.fallbacks != 0 {
				t.Fatalf("dirty evaluator fell back %d times", dirty.fallbacks)
			}

			// A second candidate exercises restoration of the first candidate's
			// region before a different old/new union is composited.
			second := append([]float64(nil), old...)
			circle := test.active[len(test.active)-1]

			second[circle*paramsPerCircle+3] = 1 - second[circle*paramsPerCircle+3]
			if got, want := dirty.Cost(second), full.Cost(second); got != want {
				t.Fatalf("second dirty cost = %.17g, full cost = %.17g", got, want)
			}
		})
	}
}

func TestPolishDirtySessionFallsBackForLargeAffectedRegion(t *testing.T) {
	t.Parallel()

	const width, height, circleCount = 128, 96, 16
	reference := randomNRGBA(width, height, 15_710)
	incumbent := tinyPolishParams(circleCount, width, height, 15_711)
	candidate := append([]float64(nil), incumbent...)
	setCirclePosition(candidate, 4, width/2, height/2, 128)

	full := NewCPURenderer(reference, circleCount)
	baseline := cloneNRGBA(full.Render(incumbent))

	baselineSSD, ok := fit.ExactSSD(baseline, reference)
	if !ok {
		t.Fatal("baseline SSD is not exact")
	}

	dirty := newPolishDirtySession(NewCPURenderer(reference, circleCount), baseline, baselineSSD, incumbent, []int{4}).(*polishDirtySession)
	if got, want := dirty.Cost(candidate), full.Cost(candidate); got != want {
		t.Fatalf("fallback cost = %.17g, full cost = %.17g", got, want)
	}

	if dirty.fallbacks != 1 || dirty.fallbackRate() != 1 {
		t.Fatalf("fallbacks = %d, rate = %.3f, want 1 and 1", dirty.fallbacks, dirty.fallbackRate())
	}
}

func BenchmarkPolishCandidateCost(b *testing.B) {
	for _, circleCount := range []int{512, 2_111} {
		b.Run(fmt.Sprintf("circles=%d", circleCount), func(b *testing.B) {
			const width, height, activeCount = 512, 512, 100
			reference := randomNRGBA(width, height, int64(15_720+circleCount))
			incumbent := tinyPolishParams(circleCount, width, height, int64(15_730+circleCount))
			active := scatteredActiveCircles(circleCount, activeCount)

			candidate := append([]float64(nil), incumbent...)
			for i, circle := range active {
				candidate[circle*paramsPerCircle] += float64(i%3-1) * 0.5
			}

			baselineRenderer := NewCPURenderer(reference, circleCount)
			baselineRenderer.SetThreads(1)
			baseline := cloneNRGBA(baselineRenderer.Render(incumbent))

			baselineSSD, ok := fit.ExactSSD(baseline, reference)
			if !ok {
				b.Fatal("baseline SSD is not exact")
			}

			b.Run("dirty", func(b *testing.B) {
				cpu := NewCPURenderer(reference, circleCount)
				cpu.SetThreads(1)
				dirty := newPolishDirtySession(cpu, baseline, baselineSSD, incumbent, active).(*polishDirtySession)
				affected := polishAffectedFraction(dirty, candidate)

				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					polishCandidateCostSink = dirty.Cost(candidate)
				}

				b.StopTimer()
				b.ReportMetric(100*affected, "affected-%")
				b.ReportMetric(100*dirty.fallbackRate(), "fallback-%")
			})

			b.Run("full", func(b *testing.B) {
				full := NewCPURenderer(reference, circleCount)
				full.SetThreads(1)
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					polishCandidateCostSink = full.Cost(candidate)
				}
			})

			b.Run("large-radius-fallback", func(b *testing.B) {
				large := append([]float64(nil), candidate...)
				large[active[0]*paramsPerCircle+2] = width
				cpu := NewCPURenderer(reference, circleCount)
				cpu.SetThreads(1)
				dirty := newPolishDirtySession(cpu, baseline, baselineSSD, incumbent, active).(*polishDirtySession)
				affected := polishAffectedFraction(dirty, large)

				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					polishCandidateCostSink = dirty.Cost(large)
				}

				b.StopTimer()
				b.ReportMetric(100*affected, "affected-%")
				b.ReportMetric(100*dirty.fallbackRate(), "fallback-%")
			})

			b.Run("large-radius-full", func(b *testing.B) {
				large := append([]float64(nil), candidate...)
				large[active[0]*paramsPerCircle+2] = width
				full := NewCPURenderer(reference, circleCount)
				full.SetThreads(1)
				b.ReportAllocs()
				b.ResetTimer()

				for range b.N {
					polishCandidateCostSink = full.Cost(large)
				}
			})
		})
	}
}

// BenchmarkPolishDirtyCrossover keeps the fallback boundary evidence close to
// the policy. It forces the dirty evaluator past that boundary so a change in
// circle traversal, compositing, or SSD dispatch can reveal a new crossover.
//
// Three arms per radius, because the forced path alone cannot say whether the
// shipped gates sit in the right place. "dirty-forced" disables both gates and
// measures the dirty path wherever it is asked to run, which is what locates
// the crossover. "dirty-shipped" leaves the gates at their production values
// and so measures what a candidate of that size actually costs, fallback
// included. "full" is the control. Comparing shipped against full across the
// radius range is what shows whether dispatch is choosing the cheaper path at
// each fraction, rather than assuming it from the forced numbers.
func BenchmarkPolishDirtyCrossover(b *testing.B) {
	for _, circleCount := range []int{512, 2_111} {
		for _, radius := range []float64{1, 2, 3, 4, 6, 8, 10, 12, 16, 20, 24} {
			b.Run(fmt.Sprintf("circles=%d/radius=%.0f", circleCount, radius), func(b *testing.B) {
				const width, height, activeCount = 512, 512, 100
				reference := randomNRGBA(width, height, int64(15_740+circleCount)+int64(radius))
				incumbent := tinyPolishParams(circleCount, width, height, int64(15_750+circleCount))
				active := scatteredActiveCircles(circleCount, activeCount)

				candidate := append([]float64(nil), incumbent...)

				for i, circle := range active {
					x := 8 + float64((i*47)%(width-16))
					y := 8 + float64((i*83)%(height-16))
					setCirclePosition(incumbent, circle, x, y, radius)
					copy(candidate[circle*paramsPerCircle:(circle+1)*paramsPerCircle], incumbent[circle*paramsPerCircle:(circle+1)*paramsPerCircle])
					candidate[circle*paramsPerCircle] += 0.5
				}

				full := NewCPURenderer(reference, circleCount)
				full.SetThreads(1)
				baseline := cloneNRGBA(full.Render(incumbent))

				baselineSSD, ok := fit.ExactSSD(baseline, reference)
				if !ok {
					b.Fatal("baseline SSD is not exact")
				}

				cpu := NewCPURenderer(reference, circleCount)
				cpu.SetThreads(1)
				dirty := newPolishDirtySession(cpu, baseline, baselineSSD, incumbent, active).(*polishDirtySession)
				dirty.maxFraction = 2          // measure; never fall back
				dirty.preflightMaxFraction = 2 // measure; never fall back
				affected := polishAffectedFraction(dirty, candidate)

				shippedCPU := NewCPURenderer(reference, circleCount)
				shippedCPU.SetThreads(1)

				shipped, ok := newPolishDirtySession(
					shippedCPU, baseline, baselineSSD, incumbent, active).(*polishDirtySession)
				if !ok {
					b.Fatal("shipped dirty session not installed")
				}

				b.Run("dirty-forced", func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()

					for range b.N {
						polishCandidateCostSink = dirty.Cost(candidate)
					}

					b.StopTimer()
					b.ReportMetric(100*affected, "affected-%")
				})
				b.Run("dirty-shipped", func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()

					for range b.N {
						polishCandidateCostSink = shipped.Cost(candidate)
					}

					b.StopTimer()
					b.ReportMetric(100*affected, "affected-%")
					b.ReportMetric(100*shipped.fallbackRate(), "fallback-%")
				})
				b.Run("full", func(b *testing.B) {
					b.ReportAllocs()
					b.ResetTimer()

					for range b.N {
						polishCandidateCostSink = full.Cost(candidate)
					}
				})
			})
		}
	}
}

func tinyPolishParams(circleCount, width, height int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))

	circles := make([]fit.Circle, circleCount)
	for i := range circles {
		circles[i] = fit.Circle{
			X:       rng.Float64() * float64(width-1),
			Y:       rng.Float64() * float64(height-1),
			R:       1 + rng.Float64()*0.2,
			CR:      rng.Float64(),
			CG:      rng.Float64(),
			CB:      rng.Float64(),
			Opacity: 0.25 + rng.Float64()*0.75,
		}
	}

	return encodeCircles(circles)
}

func scatteredActiveCircles(circleCount, activeCount int) []int {
	active := make([]int, activeCount)

	start := circleCount / 10
	for i := range active {
		active[i] = start + i*(circleCount-start-1)/max(1, activeCount-1)
	}

	return active
}

func setCirclePosition(params []float64, circle int, x, y, radius float64) {
	offset := circle * paramsPerCircle
	params[offset+0] = x
	params[offset+1] = y
	params[offset+2] = radius
}

func polishAffectedFraction(session *polishDirtySession, params []float64) float64 {
	dirty := dirtySpanSet{}
	dirty.reset(session.height, max(1, 2*len(session.activeCircles)))
	incumbent := fit.ParamVector{Data: session.incumbent, K: session.k, Width: session.width, Height: session.height}

	candidate := fit.ParamVector{Data: params, K: session.k, Width: session.width, Height: session.height}
	for _, circle := range session.activeCircles {
		session.collectCircleSpans(incumbent.DecodeCircle(circle), &dirty)
		session.collectCircleSpans(candidate.DecodeCircle(circle), &dirty)
	}

	pixels, _ := dirty.metrics()

	return float64(pixels) / float64(session.width*session.height)
}

var polishCandidateCostSink float64
