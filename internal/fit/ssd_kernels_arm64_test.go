//go:build arm64

package fit

import "golang.org/x/sys/cpu"

// hostSSDKernels lists every SSD kernel this build can execute directly on this
// CPU. See the amd64 twin.
func hostSSDKernels() []ssdKernel {
	kernels := []ssdKernel{{tier: TierScalar, fn: fastSSD_Scalar}}
	if cpu.ARM64.HasASIMD {
		kernels = append(kernels, ssdKernel{tier: TierNEON, fn: fastSSD_NEON})
	}
	return kernels
}
