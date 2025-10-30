package gpu

import (
	"fmt"
	"unsafe"
)

// Runtime is a placeholder when GPU support is not compiled.
type Runtime struct{}

// ErrNotBuilt indicates the binary was built without GPU support.
var ErrNotBuilt = fmt.Errorf("opencl support requires building with '-tags gpu'")

// InitOpenCL returns an error when GPU support is not compiled in.
func InitOpenCL() (*Runtime, error) {
	return nil, ErrNotBuilt
}

// Close is a no-op without GPU support.
func (r *Runtime) Close() {}

// EnumeratePlatforms returns an error when GPU support is not compiled in.
func EnumeratePlatforms() ([]PlatformInfo, error) {
	return nil, ErrNotBuilt
}

// ContextPtr exposes the underlying OpenCL context pointer (nil without GPU support).
func (r *Runtime) ContextPtr() unsafe.Pointer { return nil }

// QueuePtr exposes the underlying OpenCL command queue pointer (nil without GPU support).
func (r *Runtime) QueuePtr() unsafe.Pointer { return nil }

// DevicePtr exposes the selected OpenCL device pointer (nil without GPU support).
func (r *Runtime) DevicePtr() unsafe.Pointer { return nil }
