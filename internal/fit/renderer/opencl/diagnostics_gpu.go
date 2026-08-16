//go:build gpu

package opencl

import "github.com/cwbudde/mayflycirclefit/internal/fit/gpu"

// The accessors below expose the renderer's caching and degradation state.
// They exist because the renderer package owns the backend wiring and its
// tests assert on that state across the package boundary.

// Runtime returns the OpenCL platform and device this renderer runs on.
func (r *Renderer) Runtime() *gpu.Runtime { return r.runtime }

// Degraded reports whether the renderer has permanently fallen back to the CPU.
func (r *Renderer) Degraded() bool { return r.degraded }

// Evaluations returns the number of device evaluations performed so far.
func (r *Renderer) Evaluations() uint64 { return r.evaluations }

// DeviceValid reports whether a device-side cost result is cached.
func (r *Renderer) DeviceValid() bool { return r.deviceValid }

// DeviceHash returns the parameter hash of the cached device result.
func (r *Renderer) DeviceHash() uint64 { return r.deviceHash }

// ImageValid reports whether the rendered output has been materialized on the
// host. Cost defers this readback, so it stays false until Render asks for it.
func (r *Renderer) ImageValid() bool { return r.imageValid }

// LocalSize returns the reduction workgroup size selected for this device.
func (r *Renderer) LocalSize() int { return r.localSize }
