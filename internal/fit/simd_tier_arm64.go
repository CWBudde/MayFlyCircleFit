//go:build arm64

package fit

import "golang.org/x/sys/cpu"

// detectTier picks the widest kernel set this CPU can execute.
func detectTier() SIMDTier {
	if cpu.ARM64.HasASIMD {
		return TierNEON
	}
	return TierScalar
}

// tierSupported reports whether this build has kernels for the tier and this
// CPU can execute them. ASIMD is architecturally mandatory on arm64, so the
// NEON case is false only under GODEBUG=cpu.all=off, which is how the
// fallback-path tests reach it.
func tierSupported(tier SIMDTier) bool {
	switch tier {
	case TierScalar:
		return true
	case TierNEON:
		return cpu.ARM64.HasASIMD
	case TierSSE2, TierAVX2:
		return false
	default:
		return false
	}
}
