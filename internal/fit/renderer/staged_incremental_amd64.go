//go:build amd64

package renderer

// deltaSSDVectorized reports whether the staged incremental cost path has a
// vectorized delta-SSD kernel behind it.
//
// The staged path replays a retained canvas and pays for dirty-span bookkeeping,
// so it only wins when deltaSSDSpan is vectorized. On amd64 that is now true for
// both AVX2 and SSE2; before the SSE2 kernel existed this was an AVX2-only
// check, which left every non-AVX2 amd64 host on the slower non-staged path.
func deltaSSDVectorized() bool {
	return deltaSSDBackend != "scalar"
}
