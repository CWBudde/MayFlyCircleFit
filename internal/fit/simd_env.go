package fit

import "os"

// simdDisableEnv forces every runtime-dispatched kernel onto its scalar
// implementation when set to "1".
//
// GODEBUG=cpu.all=off cannot express this on amd64: golang.org/x/sys/cpu marks
// SSE2 as Required on that architecture, so cpu.X86.HasSSE2 stays true even
// with every maskable feature disabled. Without an explicit opt-out there would
// be no way to exercise the complete scalar fallback on amd64 once an SSE2
// kernel exists.
const simdDisableEnv = "MAYFLY_DISABLE_SIMD"

// SIMDDisabledByEnv reports whether the scalar fallback was requested through
// the environment. Dispatch reads it once during package initialization; it is
// never consulted from a hot path.
func SIMDDisabledByEnv() bool {
	return os.Getenv(simdDisableEnv) == "1"
}
