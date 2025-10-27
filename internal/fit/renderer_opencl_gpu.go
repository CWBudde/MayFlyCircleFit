//go:build gpu

package fit

import (
	"fmt"
	"image"

	"github.com/cwbudde/mayflycirclefit/internal/fit/gpu"
)

func newOpenCLRenderer(reference *image.NRGBA, k int) (Renderer, func(), error) {
	rt, err := gpu.InitOpenCL()
	if err != nil {
		return nil, noopCleanup, fmt.Errorf("%w: %v", ErrBackendUnavailable, err)
	}

	cleanup := func() {
		rt.Close()
	}

	return nil, cleanup, fmt.Errorf("%w: OpenCL backend scaffolding in place; renderer pending implementation", ErrBackendNotImplemented)
}
