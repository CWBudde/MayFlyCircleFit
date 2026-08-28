//go:build !gpu

//nolint:testpackage // asserts the unexported build-tag split behind SupportedBackends
package renderer

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// A build without the gpu tag carries only the OpenCL stub. What it must not do
// is advertise the backend anyway: the server publishes SupportedBackends in
// its system payload and the dashboard reads it, so listing a backend that
// cannot start turns an answerable question into a job that fails minutes later
// on a worker.
func TestSupportedBackendsOmitsOpenCLWithoutTheGPUTag(t *testing.T) {
	t.Parallel()

	backends := SupportedBackends()

	if !slices.Contains(backends, BackendCPU) {
		t.Fatalf("SupportedBackends() = %v, want it to contain %q", backends, BackendCPU)
	}

	if slices.Contains(backends, BackendOpenCL) {
		t.Fatalf("SupportedBackends() = %v, want %q absent in a build without the gpu tag", backends, BackendOpenCL)
	}
}

func TestBackendAvailableSeparatesUnbuiltFromUnknown(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		backend Backend
		wantErr error
	}{
		{name: "cpu is always constructible", backend: BackendCPU, wantErr: nil},
		{name: "an alias resolves before the check", backend: "CPU", wantErr: nil},
		{name: "opencl is known but not built", backend: BackendOpenCL, wantErr: ErrBackendUnavailable},
		{name: "a gpu alias is the same backend", backend: "gpu", wantErr: ErrBackendUnavailable},
		{name: "a name nobody knows is a request error", backend: "vulkan", wantErr: ErrUnknownBackend},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := BackendAvailable(testCase.backend)
			if testCase.wantErr == nil {
				if err != nil {
					t.Fatalf("BackendAvailable(%q) = %v, want nil", testCase.backend, err)
				}

				return
			}

			// The two are distinguished because callers answer them
			// differently: an unknown name is the client's mistake, an unbuilt
			// backend is this deployment's.
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("BackendAvailable(%q) = %v, want %v", testCase.backend, err, testCase.wantErr)
			}
		})
	}
}

func TestBackendAvailableSaysTheBuildIsTheReason(t *testing.T) {
	t.Parallel()

	err := BackendAvailable(BackendOpenCL)
	if err == nil {
		t.Fatal("BackendAvailable(opencl) = nil in a build without the gpu tag")
	}

	// A message that blamed the device would send the reader to a driver they
	// cannot fix the problem with.
	if !strings.Contains(err.Error(), "build") {
		t.Fatalf("error = %v, want it to name the build as the reason", err)
	}
}
