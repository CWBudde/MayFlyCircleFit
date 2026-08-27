package fit

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
)

// SIMDTier names the instruction set every runtime-dispatched kernel in this
// process is allowed to use. It is resolved once, from one place, and every
// dispatch site reads it instead of consulting golang.org/x/sys/cpu itself.
//
// Before this existed each kernel re-derived its own ladder from the CPU
// feature bits and recorded the outcome in its own way: a typed enum here, a
// free-form string there, a pair of mutually exclusive booleans somewhere else.
// Nothing kept those answers consistent, and nothing could ask the process as a
// whole which tier it was running. Both properties are needed to test a tier
// that the host CPU is not, which is the entire point of having a fallback
// tier at all.
type SIMDTier uint8

// Tiers are ordered by preference within an architecture. Ordering across
// architectures is meaningless — TierNEON is never comparable to TierAVX2,
// because no CPU offers both — so only tierSupported decides what may be
// selected here.
const (
	TierScalar SIMDTier = iota
	TierSSE2
	TierAVX2
	TierNEON
)

// String returns the wire name used by simdTierEnv and by the CI gates that
// assert which tier a job selected. Do not change these spellings.
func (t SIMDTier) String() string {
	switch t {
	case TierAVX2:
		return "avx2"
	case TierSSE2:
		return "sse2"
	case TierNEON:
		return "neon"
	case TierScalar:
		return "scalar"
	default:
		return "unknown"
	}
}

// ParseSIMDTier maps a wire name to a tier. It does not check whether the tier
// is reachable on this architecture; tierSupported does that.
func ParseSIMDTier(name string) (SIMDTier, bool) {
	for _, tier := range []SIMDTier{TierScalar, TierSSE2, TierAVX2, TierNEON} {
		if name == tier.String() {
			return tier, true
		}
	}

	return TierScalar, false
}

const (
	// simdTierEnv pins the tier for the whole process. It replaces the
	// GODEBUG-plus-CIRCLEFIT_DISABLE_SIMD pair that could only express "off" and
	// "whatever cpu.avx2=off happens to leave behind": golang.org/x/sys/cpu
	// registers sse2 with Required on amd64 and ORs that requirement back in,
	// so GODEBUG can neither reach the scalar tier there nor name a tier
	// directly.
	simdTierEnv = "CIRCLEFIT_SIMD_TIER"

	// simdDisableEnv is the older scalar-only lever. It means exactly
	// CIRCLEFIT_SIMD_TIER=scalar. Both names carried a MAYFLY_ prefix until the
	// project was renamed; the old spellings are inert now rather than
	// deprecated, so a script still setting one silently gets autodetection.
	simdDisableEnv = "CIRCLEFIT_DISABLE_SIMD"
)

var (
	forcedTier   atomic.Pointer[SIMDTier]
	resolvedTier atomic.Pointer[SIMDTier]

	tierMu        sync.Mutex
	tierConsumers []func(SIMDTier)
)

// Tier reports the instruction set this process dispatches to. The result is
// cached; callers may treat it as constant outside of tests that call
// SetForcedTier.
func Tier() SIMDTier {
	if forced := forcedTier.Load(); forced != nil {
		return *forced
	}

	if resolved := resolvedTier.Load(); resolved != nil {
		return *resolved
	}

	return resolveTierSlow()
}

// resolveTierSlow is split out so Tier stays small enough to inline.
func resolveTierSlow() SIMDTier {
	tierMu.Lock()
	defer tierMu.Unlock()

	if resolved := resolvedTier.Load(); resolved != nil {
		return *resolved
	}

	tier := tierFromEnvironment()
	resolvedTier.Store(&tier)

	return tier
}

// tierFromEnvironment applies the operator overrides and otherwise detects the
// tier from CPU features. A request this architecture cannot honor is a hard
// error: silently substituting a different tier would make a CI gate that asks
// for SSE2 pass while measuring AVX2, which is the failure this whole mechanism
// exists to prevent.
func tierFromEnvironment() SIMDTier {
	if requested, ok := os.LookupEnv(simdTierEnv); ok {
		tier, valid := ParseSIMDTier(requested)
		if !valid {
			panic(fmt.Sprintf("%s=%q is not a tier name (want avx2, sse2, neon, or scalar)", simdTierEnv, requested))
		}

		if !tierSupported(tier) {
			panic(fmt.Sprintf("%s=%q is not reachable on this build (supported: %s)", simdTierEnv, requested, supportedTierNames()))
		}

		return tier
	}

	if os.Getenv(simdDisableEnv) == "1" {
		return TierScalar
	}

	return detectTier()
}

// SetForcedTier pins the tier and re-runs every registered dispatch site so the
// change takes effect in the running process.
//
// This is a test and benchmark facility. It is not safe to call while another
// goroutine is rendering or computing a cost, because it swaps the kernel
// function pointers those goroutines read.
func SetForcedTier(tier SIMDTier) {
	if !tierSupported(tier) {
		panic(fmt.Sprintf("tier %s is not reachable on this build (supported: %s)", tier, supportedTierNames()))
	}

	forcedTier.Store(&tier)
	notifyTierConsumers(tier)
}

// ResetTierDetection drops a forced tier and restores the detected one. Pair it
// with SetForcedTier through t.Cleanup.
func ResetTierDetection() {
	forcedTier.Store(nil)
	notifyTierConsumers(Tier())
}

// RegisterTierConsumer installs a dispatch site. The function is called
// immediately with the current tier, and again whenever SetForcedTier or
// ResetTierDetection changes it. Dispatch sites call this from init instead of
// selecting a kernel inline, which is what lets one process exercise every
// tier.
func RegisterTierConsumer(install func(SIMDTier)) {
	tierMu.Lock()

	tierConsumers = append(tierConsumers, install)
	tierMu.Unlock()

	install(Tier())
}

func notifyTierConsumers(tier SIMDTier) {
	tierMu.Lock()
	consumers := make([]func(SIMDTier), len(tierConsumers))
	copy(consumers, tierConsumers)
	tierMu.Unlock()

	for _, install := range consumers {
		install(tier)
	}
}

func supportedTierNames() string {
	names := ""

	for _, tier := range []SIMDTier{TierScalar, TierSSE2, TierAVX2, TierNEON} {
		if !tierSupported(tier) {
			continue
		}

		if names != "" {
			names += ", "
		}

		names += tier.String()
	}

	return names
}
