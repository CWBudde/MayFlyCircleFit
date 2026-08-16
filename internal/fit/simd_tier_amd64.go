//go:build amd64

package fit

import "golang.org/x/sys/cpu"

// detectTier picks the widest kernel set this CPU can execute.
func detectTier() SIMDTier {
	switch {
	case cpu.X86.HasAVX2:
		return TierAVX2
	case cpu.X86.HasSSE2:
		return TierSSE2
	default:
		return TierScalar
	}
}

// tierSupported reports whether this build has kernels for the tier and this
// CPU can execute them. Forcing a narrower tier than the CPU offers is the
// supported way to test a fallback; forcing a wider one would fault, so it is
// rejected instead.
//
// SSE2 is unconditionally true because it is part of the amd64 baseline. That
// is also why GODEBUG cannot reach the scalar tier here: x/sys/cpu marks sse2
// Required on this architecture and restores the bit after processing GODEBUG.
func tierSupported(tier SIMDTier) bool {
	switch tier {
	case TierScalar, TierSSE2:
		return true
	case TierAVX2:
		return cpu.X86.HasAVX2
	case TierNEON:
		return false
	default:
		return false
	}
}
