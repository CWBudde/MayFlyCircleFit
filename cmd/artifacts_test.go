package cmd

import (
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func testImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))

	for y := range 8 {
		for x := range 8 {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 32), G: uint8(y * 32), B: 128, A: 255})
		}
	}

	return img
}

func TestWritePNGRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.png")

	want := testImage()
	if err := writePNG(path, want); err != nil {
		t.Fatalf("writePNG() error = %v, want nil", err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open written file: %v", err)
	}
	defer file.Close()

	got, err := png.Decode(file)
	if err != nil {
		t.Fatalf("decode written file: %v", err)
	}

	if got.Bounds() != want.Bounds() {
		t.Fatalf("bounds = %v, want %v", got.Bounds(), want.Bounds())
	}
}

// TestWritePNGReportsAFullFilesystem is the disk-full scenario. /dev/full
// accepts the open and fails every write with ENOSPC, which is what a real full
// filesystem does, so this exercises the path without needing a loopback mount.
//
// On this device the failure surfaces at the encode and the subsequent close
// succeeds; the close-only case is covered by
// TestEncodePNGReportsACloseFailure, which no ordinary file reproduces on
// demand. What this test pins down is that the error reaches the caller as a
// *os.PathError wrapping ENOSPC, which is what the CLI matches on to suggest
// freeing space.
func TestWritePNGReportsAFullFilesystem(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/dev/full is a Linux device")
	}

	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skipf("/dev/full unavailable: %v", err)
	}

	err := writePNG("/dev/full", testImage())
	if err == nil {
		t.Fatal("writePNG(/dev/full) = nil, want an error: a full filesystem must not be reported as success")
	}

	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("writePNG(/dev/full) error = %v, want it to wrap ENOSPC so the CLI can suggest freeing space", err)
	}

	var pathError *os.PathError
	if !errors.As(err, &pathError) {
		t.Fatalf("writePNG(/dev/full) error = %v, want a *os.PathError naming the path", err)
	}
}

func TestWritePNGReportsAnUncreatablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-dir", "out.png")

	err := writePNG(path, testImage())
	if err == nil {
		t.Fatal("writePNG() = nil, want an error for a path whose directory does not exist")
	}

	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("writePNG() error = %v, want it to wrap os.ErrNotExist", err)
	}
}

// failOnCloseWriter accepts every write and fails only at Close, which is how a
// deferred write-back reports a filesystem that could not take the bytes.
type failOnCloseWriter struct {
	written int
	closed  bool
	err     error
}

func (w *failOnCloseWriter) Write(p []byte) (int, error) {
	w.written += len(p)
	return len(p), nil
}

func (w *failOnCloseWriter) Close() error {
	w.closed = true
	return w.err
}

// TestEncodePNGReportsACloseFailure is the regression test for a deferred close
// that drops its error: every byte is accepted, so the encode succeeds, and
// only the close says the image never landed.
func TestEncodePNGReportsACloseFailure(t *testing.T) {
	destination := &failOnCloseWriter{err: syscall.ENOSPC}

	err := encodePNG(destination, "out.png", testImage())
	if err == nil {
		t.Fatal("encodePNG() = nil, want the close failure: a truncated image must not be reported as success")
	}

	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("encodePNG() error = %v, want it to wrap the close error", err)
	}

	if !strings.Contains(err.Error(), "out.png") {
		t.Fatalf("encodePNG() error = %v, want it to name the path", err)
	}

	if destination.written == 0 {
		t.Fatal("encodePNG() wrote nothing, so the close error was not reached through a successful encode")
	}
}

// TestEncodePNGKeepsTheFirstErrorAndStillCloses guards against the close error
// masking the real cause when both fail.
func TestEncodePNGKeepsTheFirstErrorAndStillCloses(t *testing.T) {
	encodeErr := errors.New("encode exploded")
	destination := &errorOnWriteCloser{writeErr: encodeErr, closeErr: syscall.ENOSPC}

	err := encodePNG(destination, "out.png", testImage())
	if !errors.Is(err, encodeErr) {
		t.Fatalf("encodePNG() error = %v, want the encode failure to survive the close failure", err)
	}

	if !destination.closed {
		t.Fatal("encodePNG() did not close the destination on the encode path, leaking the handle")
	}
}

type errorOnWriteCloser struct {
	writeErr error
	closeErr error
	closed   bool
}

func (w *errorOnWriteCloser) Write([]byte) (int, error) { return 0, w.writeErr }

func (w *errorOnWriteCloser) Close() error {
	w.closed = true
	return w.closeErr
}
