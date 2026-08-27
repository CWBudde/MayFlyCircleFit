//go:build amd64

package renderer

import "github.com/cwbudde/circlefit/internal/fit"

// deltaSSDVectorized reports whether the staged incremental cost path has a
// vectorized delta-SSD kernel behind it that is fast enough to pay for the
// path's overhead.
//
// The staged path replays a retained canvas and pays for dirty-span
// bookkeeping, so it only wins when deltaSSDSpan is enough faster than a full
// re-render to cover that. Whether it does is decided by the crossover
// constants in incremental_cost.go, and those constants are documented there as
// modelling native AVX2 measurements. Extending them to a half-width kernel is
// exactly the unmeasured behavior change staged_incremental_generic.go declines
// to make for NEON, so it was measured rather than assumed.
//
// BenchmarkIncrementalCostCrossover, 256x256, single thread, median of three
// 200ms runs, delta speedup over a full re-render:
//
//	radius            4     8    16    32    48    64    80    96   112   128
//	AVX2 (Ryzen 5)  3.65  3.79  2.71  1.83  1.41  1.24  0.93  0.86  0.90  1.00
//	SSE2 (no-AVX2)  3.33  3.09  2.50  1.75  1.34  1.15  1.05  0.99  0.92  0.92
//
// The curves have the same shape and the SSE2 crossover sits slightly later, at
// radius 96 against roughly 72 for AVX2, so the AVX2-tuned constants abandon
// the staged path a little earlier than SSE2 would prefer. That direction is
// safe: it gives up a small amount of the win rather than choosing a slower
// path. SSE2 therefore takes the staged path too.
//
// The SSE2 column is from a genuine no-AVX2 CPU (QEMU Virtual CPU, sse4_2 and
// no AVX, 64 vCPU), not from an AVX2 host under GODEBUG=cpu.avx2=off. The
// latter has the right instruction set and the wrong microarchitecture, and is
// not evidence about a machine that ships without AVX2.
func deltaSSDVectorized() bool {
	return deltaSSDKernel != fit.TierScalar
}
