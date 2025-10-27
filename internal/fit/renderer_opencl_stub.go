package fit

import (
	"fmt"
	"image"
)

func newOpenCLRenderer(_ *image.NRGBA, _ int) (Renderer, func(), error) {
	return nil, noopCleanup, fmt.Errorf("%w: build without GPU tag", ErrBackendUnavailable)
}
