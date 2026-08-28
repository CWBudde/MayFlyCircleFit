//go:build gpu

package renderer

import (
	"errors"
	"fmt"
	"image"

	"github.com/cwbudde/circlefit/internal/fit/renderer/opencl"
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
		return nil, cleanup, classifySessionError(err)
	}
	return openCLAdapter{session}, cleanup, nil
}

// classifySessionError normalises a session failure onto this package's error
// vocabulary, and the distinction it draws is not cosmetic.
//
// Sessions allocate device resources of their own, so they fail the same ways
// construction does, and a device failure has to arrive as ErrBackendUnavailable
// or a staged or parallel stage reports something errors.Is cannot recognise.
// But a rejected argument is not a device failure. Reporting one as an
// unavailable backend sends the reader after a driver problem -- the server
// renders it to a client as "renderer backend unavailable: base canvas must be
// fully opaque" -- and no fallback fixes a canvas the caller got wrong.
//
// ErrInvalidOptimizationInput is the right class because the pipelines already
// use it for exactly this mistake: OptimizeBatchAppendFromCanvasContext rejects
// a mismatched retained canvas with it before any session exists, so the two
// backends now report the same class for the same error.
func classifySessionError(err error) error {
	if errors.Is(err, opencl.ErrInvalidSessionInput) {
		return fmt.Errorf("%w: %w", ErrInvalidOptimizationInput, err)
	}

	return fmt.Errorf("%w: %w", ErrBackendUnavailable, err)
}

// newSessionWithCanvas and initialCanvas complete accumulatedSessionFactory.
// Like newSession they exist here because the interface is unexported and no
// other package can implement it.
//
// Satisfying that interface is what moves the staged pipelines off replaying
// the retained prefix on every evaluation, and it also switches on the retained
// canvas in finishStagedResult, the baked-prefix sessions in polishing, and
// OptimizeBatchAppendFromCanvasContext, which previously refused this backend.
func (a openCLAdapter) newSessionWithCanvas(canvas *image.NRGBA, circleCount int) (Renderer, func(), error) {
	session, cleanup, err := a.NewSessionWithCanvas(canvas, circleCount)
	if err != nil {
		return nil, cleanup, classifySessionError(err)
	}

	return openCLAdapter{session}, cleanup, nil
}

func (a openCLAdapter) initialCanvas() *image.NRGBA {
	return a.InitialCanvas()
}

// NewOpenCLRenderer creates an OpenCL GPU-based renderer
func NewOpenCLRenderer(reference *image.NRGBA, k int) (Renderer, func(), error) {
	newFallback := func(ref, canvas *image.NRGBA, circles int) opencl.Fallback {
		// A nil canvas means white. An accumulated staged session passes the
		// retained canvas, and the fallback has to start from it: degradation is
		// silent, so a white fallback there would answer with costs for a
		// different image and nothing would say so.
		if canvas == nil {
			return NewCPURenderer(ref, circles)
		}

		return NewCPURendererWithCanvas(ref, canvas, circles)
	}

	r, cleanup, err := opencl.New(reference, k, newFallback)
	if err != nil {
		return nil, cleanup, fmt.Errorf("%w: %w", ErrBackendUnavailable, err)
	}
	return openCLAdapter{r}, cleanup, nil
}
