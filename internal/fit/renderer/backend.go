package renderer

import (
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"
)

// Backend identifies a renderer implementation.
type Backend string

const (
	BackendCPU    Backend = "cpu"
	BackendOpenCL Backend = "opencl"
)

var (
	// ErrUnknownBackend is returned when the name does not match a known backend.
	ErrUnknownBackend = errors.New("unknown renderer backend")
	// ErrBackendUnavailable indicates the backend is not available in this build.
	ErrBackendUnavailable = errors.New("renderer backend unavailable")
	// ErrBackendNotImplemented indicates the backend is known but not yet implemented.
	ErrBackendNotImplemented = errors.New("renderer backend not implemented")
)

// NormalizeBackend maps arbitrary user input to a canonical backend identifier.
func NormalizeBackend(name string) Backend {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "cpu":
		return BackendCPU
	case "gpu", "opencl", "cl":
		return BackendOpenCL
	default:
		return Backend(name)
	}
}

// SupportedBackends returns the backends this binary can actually construct.
//
// It reports what the build carries, not what the factory can name: a build
// without the gpu tag has only the OpenCL stub, and listing OpenCL there would
// advertise a backend whose every job fails once it reaches a worker.
func SupportedBackends() []Backend {
	return builtBackends()
}

// BackendAvailable reports whether name can be constructed in this build.
//
// It separates the two ways a backend request goes wrong, because callers
// answer them differently: a name nobody knows is a client mistake, while a
// known backend this build does not carry is a deployment fact. Both are
// decided from the build alone -- no device is probed -- so the answer is the
// same on every host running this binary, and a configuration validated here
// stays valid wherever its checkpoint is resumed.
func BackendAvailable(name Backend) error {
	backend := NormalizeBackend(string(name))

	if slices.Contains(builtBackends(), backend) {
		return nil
	}

	if backend == BackendCPU || backend == BackendOpenCL {
		return fmt.Errorf("%w: %s is not available in this build", ErrBackendUnavailable, backend)
	}

	return fmt.Errorf("%w: %s", ErrUnknownBackend, name)
}

// NewRendererForBackend constructs the requested renderer and returns an optional cleanup hook.
func NewRendererForBackend(name string, reference *image.NRGBA, k int) (Renderer, func(), error) {
	backend := NormalizeBackend(name)

	switch backend {
	case BackendCPU:
		return NewCPURenderer(reference, k), noopCleanup, nil
	case BackendOpenCL:
		return NewOpenCLRenderer(reference, k)
	default:
		return nil, noopCleanup, fmt.Errorf("%w: %s", ErrUnknownBackend, name)
	}
}
