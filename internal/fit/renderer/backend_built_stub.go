//go:build !gpu

package renderer

// builtBackends lists the backends this binary can actually construct. Without
// the gpu tag NewOpenCLRenderer is the stub that always reports
// ErrBackendUnavailable, so OpenCL is deliberately absent: advertising a
// backend that cannot start only defers the failure to run time.
func builtBackends() []Backend {
	return []Backend{BackendCPU}
}
