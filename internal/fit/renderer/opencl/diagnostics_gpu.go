//go:build gpu

package opencl

import "github.com/cwbudde/circlefit/internal/fit/gpu"

// The accessors below expose the renderer's caching and degradation state.
// They exist because the renderer package owns the backend wiring and its
// tests assert on that state across the package boundary.

// Runtime returns the OpenCL platform and device this renderer runs on.
func (r *Renderer) Runtime() *gpu.Runtime { return r.runtime }

// Degraded reports whether this renderer, the renderer it was derived from, or
// any session sharing their device has permanently fallen back to the CPU. The
// answer is about the run rather than one Renderer value, because the staged
// pipelines evaluate through independent sessions.
func (r *Renderer) Degraded() bool { return r.degraded.Load() }

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

// ProgramBuilds returns how many times the kernel source has been compiled on
// the engine backing this renderer. Compiling it was the dominant part of
// per-session setup, so the count is the direct evidence that a renderer and
// every session derived from it share one engine and compile once, rather than
// each rebuilding the program for its own circle count. It reports zero once
// the renderer has been torn down and no longer holds an engine.
func (r *Renderer) ProgramBuilds() uint64 {
	if r.engine == nil {
		return 0
	}

	return r.engine.builds
}
