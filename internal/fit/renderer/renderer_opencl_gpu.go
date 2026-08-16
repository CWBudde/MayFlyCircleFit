//go:build gpu

package renderer

import (
	"fmt"
	"image"

	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer/opencl"
)

// openCLAdapter wires the OpenCL renderer into this package.
//
// The OpenCL renderer lives in its own package because Go forbids Plan 9
// assembly in a package that uses cgo, and this package carries the SIMD
// kernels. The adapter supplies the one thing the split cannot cross:
// rendererSessionFactory requires the unexported newSession method, which no
// other package can implement.
type openCLAdapter struct {
	*opencl.Renderer
}

func (a openCLAdapter) newSession(circleCount int) (Renderer, func(), error) {
	session, cleanup, err := a.Renderer.NewSession(circleCount)
	if err != nil {
		return nil, cleanup, err
	}
	return openCLAdapter{session}, cleanup, nil
}

// NewOpenCLRenderer creates an OpenCL GPU-based renderer
func NewOpenCLRenderer(reference *image.NRGBA, k int) (Renderer, func(), error) {
	newFallback := func(ref *image.NRGBA, circles int) opencl.Fallback {
		return NewCPURenderer(ref, circles)
	}

	r, cleanup, err := opencl.New(reference, k, newFallback)
	if err != nil {
		return nil, cleanup, fmt.Errorf("%w: %v", ErrBackendUnavailable, err)
	}
	return openCLAdapter{r}, cleanup, nil
}
