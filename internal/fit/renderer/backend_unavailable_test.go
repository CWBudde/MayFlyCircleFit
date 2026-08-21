//go:build !gpu

package renderer

import (
	"errors"
	"strings"
	"testing"
)

// TestGPUUnavailableInAPortableBuild is the GPU-unavailable scenario for the
// build that ships to everyone without OpenCL. The requirement is not that it
// fails — it must — but how: with a cleanup that is safe to call, a sentinel
// callers can classify on, and a message that says the build is the reason
// rather than blaming the device or the request.
//
// This is the only GPU failure a CPU-only CI job can exercise. The device-level
// failures (no platform, no device, a failed context) go through the same
// ErrBackendUnavailable normalisation in renderer_opencl_gpu.go and need a
// prepared OpenCL runner.
func TestGPUUnavailableInAPortableBuild(t *testing.T) {
	rend, cleanup, err := NewOpenCLRenderer(failureTestReference(), 4)

	if err == nil {
		t.Fatal("NewOpenCLRenderer() = nil error in a build without the gpu tag")
	}
	if rend != nil {
		t.Fatalf("NewOpenCLRenderer() returned a renderer %T alongside its error", rend)
	}
	if cleanup == nil {
		t.Fatal("NewOpenCLRenderer() returned a nil cleanup, so a caller's deferred release panics on the failure path")
	}
	cleanup() // Must be safe even though construction failed.

	if !errors.Is(err, ErrBackendUnavailable) {
		t.Fatalf("error = %v, want ErrBackendUnavailable so callers can fall back rather than string-match", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "gpu tag") {
		t.Fatalf("error = %v, want it to name the missing build tag as the reason", err)
	}
}
