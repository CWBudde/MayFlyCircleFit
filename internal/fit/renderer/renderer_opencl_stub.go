package renderer

import (
	"fmt"
	"image"
)

// NewOpenCLRenderer creates an OpenCL GPU-based renderer (stub for non-GPU builds)
func NewOpenCLRenderer(_ *image.NRGBA, _ int) (Renderer, func(), error) {
	return nil, noopCleanup, fmt.Errorf("%w: build without GPU tag", ErrBackendUnavailable)
}
