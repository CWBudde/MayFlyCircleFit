//go:build !amd64 && !arm64

package fit

// detectTier has no vector kernels to choose from on these architectures.
func detectTier() SIMDTier {
	return TierScalar
}

// tierSupported accepts only the scalar tier, so MAYFLY_SIMD_TIER=avx2 fails
// loudly on a 386 or wasm build instead of appearing to succeed.
func tierSupported(tier SIMDTier) bool {
	return tier == TierScalar
}
