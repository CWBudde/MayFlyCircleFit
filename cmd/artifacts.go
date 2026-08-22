package cmd

import (
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
)

// writePNG encodes img to path as PNG.
func writePNG(path string, img image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}

	return encodePNG(file, path, img)
}

// encodePNG encodes img into an already-opened destination and closes it,
// reporting whichever step failed first.
//
// Closing is part of writing, not cleanup. An os.File hands its bytes to the
// kernel as they are written, so a full local filesystem usually surfaces at
// the encode; but a write that is only acknowledged later — a network mount, a
// filesystem that defers its allocation — reports at close instead. A deferred
// close discards that error, and the command then exits 0 having written a
// truncated image. The destination is taken as an io.WriteCloser so the
// close-only failure can be tested, which no ordinary file reproduces on
// demand.
func encodePNG(destination io.WriteCloser, path string, img image.Image) (err error) {
	defer func() {
		closeErr := destination.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("close %s: %w", path, closeErr)
		}
	}()

	encodeErr := png.Encode(destination, img)
	if encodeErr != nil {
		return encodeErr
	}

	return nil
}
