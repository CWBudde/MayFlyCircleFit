package fit

import (
	"errors"
	"fmt"
	"image"
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

var noopCleanup = func() {}

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

// SupportedBackends returns the list of backends understood by the factory.
func SupportedBackends() []Backend {
	return []Backend{BackendCPU, BackendOpenCL}
}

// NewRendererForBackend constructs the requested renderer and returns an optional cleanup hook.
func NewRendererForBackend(name string, reference *image.NRGBA, k int) (Renderer, func(), error) {
	backend := NormalizeBackend(name)

	switch backend {
	case BackendCPU:
		return NewCPURenderer(reference, k), noopCleanup, nil
	case BackendOpenCL:
		return newOpenCLRenderer(reference, k)
	default:
		return nil, noopCleanup, fmt.Errorf("%w: %s", ErrUnknownBackend, name)
	}
}
