//go:build amd64

package fit

import "golang.org/x/sys/cpu"

// hostSSDKernels lists every SSD kernel this build can execute directly on this
// CPU, independent of which one dispatch installed.
//
// Calling the kernels directly is what lets the differential tests run on an
// ordinary AVX2 development machine. Gating them on the active tier instead
// meant the SSE2 kernel was benchmarked but never verified outside one CI step,
// while the CI step that did verify it could not also verify AVX2.
//
// SSE2 is unconditional because it is part of the amd64 baseline; AVX2 needs
// the feature bit, since a direct call would fault without it.
func hostSSDKernels() []ssdKernel {
	kernels := []ssdKernel{
		{tier: TierScalar, fn: fastSSD_Scalar},
		{tier: TierSSE2, fn: fastSSD_SSE2},
	}
	if cpu.X86.HasAVX2 {
		kernels = append(kernels, ssdKernel{tier: TierAVX2, fn: fastSSD_AVX2})
	}
	return kernels
}
