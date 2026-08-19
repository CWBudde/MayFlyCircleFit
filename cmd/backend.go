package cmd

import (
	"fmt"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
)

func parseBackendFlag(raw string) (app.Backend, error) {
	switch backend := renderer.NormalizeBackend(raw); backend {
	case renderer.BackendCPU:
		return app.BackendCPU, nil
	case renderer.BackendOpenCL:
		return app.BackendOpenCL, nil
	default:
		return "", fmt.Errorf("backend must be one of: cpu, opencl (aliases: gpu, cl)")
	}
}
