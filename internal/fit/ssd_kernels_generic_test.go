//go:build !amd64 && !arm64

package fit

// hostSSDKernels has only the scalar kernel on these architectures, so the
// differential tests degenerate to scalar-against-itself. They still run, which
// keeps the shared helpers compiling everywhere.
func hostSSDKernels() []ssdKernel {
	return []ssdKernel{{tier: TierScalar, fn: fastSSD_Scalar}}
}
