//go:build gpu

package renderer

// builtBackends lists the backends this binary can actually construct. A build
// with the gpu tag carries the cgo OpenCL renderer, so both are constructible.
func builtBackends() []Backend {
	return []Backend{BackendCPU, BackendOpenCL}
}
