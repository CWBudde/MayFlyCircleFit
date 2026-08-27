package cmd

import (
	"errors"

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
