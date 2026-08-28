package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/fit/renderer"
)

func parseBackendFlag(raw string) (app.Backend, error) {
	switch backend := renderer.NormalizeBackend(raw); backend {
	case renderer.BackendCPU:
		return app.BackendCPU, nil
	case renderer.BackendOpenCL:
		return app.BackendOpenCL, nil
	default:
		return "", errors.New("backend must be one of: cpu, opencl (aliases: gpu, cl)")
	}
}

// parseBackendFallbackFlag reads --backend-fallback. Empty means no fallback,
// which is the default: a backend that cannot start fails the run rather than
// quietly producing numbers from a different one.
func parseBackendFallbackFlag(raw string) (app.Backend, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}

	if backend := renderer.NormalizeBackend(raw); backend == renderer.BackendCPU {
		return app.BackendCPU, nil
	}

	return "", errors.New("backend-fallback must be cpu when set")
}

// requireAvailableBackend rejects a backend this build cannot construct. It
// answers from the build alone and probes no device, so it says nothing about
// whether a driver or a card is present.
func requireAvailableBackend(backend app.Backend) error {
	err := renderer.BackendAvailable(renderer.Backend(backend))
	if err != nil {
		return fmt.Errorf("invalid backend: %w", err)
	}

	return nil
}

// backendProvenanceNote reports what actually ran when that is not what the run
// asked for, and returns an empty string when the two agree. A server job
// carries this as effectiveBackend and backendDegraded; a one-shot CLI run has
// no job resource, and the two backends do not produce comparable costs -- the
// device accumulates the SSD in float32 against a float64 CPU path -- so a
// substitution recorded only in a log line would leave the printed cost
// unexplained.
func backendProvenanceNote(requested, effective app.Backend, degraded bool) string {
	switch {
	case degraded:
		return fmt.Sprintf("Backend: %s (degraded to CPU mid-run) - this cost mixes device and "+
			"CPU arithmetic and is comparable with neither backend", effective)
	case effective != requested:
		return fmt.Sprintf("Backend: %s (requested %s, unavailable) - this cost is not "+
			"comparable with a %s run", effective, requested, requested)
	default:
		return ""
	}
}
