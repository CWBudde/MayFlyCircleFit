//go:build !amd64

package renderer

// deltaSSDVectorized reports whether the staged incremental cost path has a
// vectorized delta-SSD kernel behind it.
//
// Non-amd64 targets keep the previous answer of false. ARM64 does have a NEON
// delta-SSD kernel, but the staged path was never measured there, and enabling
// it as a side effect of the amd64 SSE2 work would be an unmeasured behavior
// change. Turning it on for ARM64 needs its own profile-guided evaluation.
func deltaSSDVectorized() bool {
	return false
}
