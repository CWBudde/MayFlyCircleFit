package anim_test

import (
	"errors"
	"image"
	"runtime"
	"sync"
	"testing"

	"github.com/cwbudde/circlefit/internal/anim"
)

func TestWorkersResolvesARequestAgainstTheCoreCount(t *testing.T) {
	t.Parallel()

	cores := runtime.GOMAXPROCS(0)

	cases := map[string]struct {
		requested int
		want      int
	}{
		"zero means every core":     {0, cores},
		"negative means every core": {-4, cores},
		"one is honoured":           {1, 1},
		"above the cap is clamped":  {cores + 100, cores},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := anim.Workers(testCase.requested)
			if got != testCase.want {
				t.Errorf("Workers(%d) = %d, want %d", testCase.requested, got, testCase.want)
			}
		})
	}
}

// frame builds a distinguishable one-pixel image, so a pool that mixed up its
// indices would be caught rather than looking like a reordering.
func frame(index int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	img.Pix[0] = uint8(index & 0xFF)

	return img
}

func TestSinkPoolDeliversEveryFrameWithItsOwnIndex(t *testing.T) {
	t.Parallel()

	const frames = 200

	var (
		mu   sync.Mutex
		seen = map[int]uint8{}
	)

	pool := anim.NewSinkPool(4, func(index int, img *image.NRGBA) error {
		mu.Lock()
		defer mu.Unlock()

		seen[index] = img.Pix[0]

		return nil
	})

	for i := range frames {
		err := pool.Submit(i, frame(i))
		if err != nil {
			t.Fatalf("Submit(%d): %v", i, err)
		}
	}

	err := pool.Close()
	if err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(seen) != frames {
		t.Fatalf("the pool delivered %d frames, want %d", len(seen), frames)
	}

	for i := range frames {
		if seen[i] != uint8(i) {
			t.Errorf("frame %d arrived carrying image %d", i, seen[i])
		}
	}
}

// A sink that fails has to stop the render rather than let it write another
// three thousand frames, and it must not leave Submit blocked on a queue no
// worker is draining.
func TestSinkPoolReportsAFailureAndDoesNotBlock(t *testing.T) {
	t.Parallel()

	failure := errors.New("disk full")
	pool := anim.NewSinkPool(2, func(int, *image.NRGBA) error { return failure })

	// Far more frames than the queue holds: if a failed worker stopped
	// receiving, this loop would deadlock instead of returning the error.
	var submitErr error

	for i := range 1000 {
		submitErr = pool.Submit(i, frame(i))
		if submitErr != nil {
			break
		}
	}

	if !errors.Is(submitErr, failure) {
		t.Errorf("Submit eventually returned %v, want the sink's error", submitErr)
	}

	closeErr := pool.Close()
	if !errors.Is(closeErr, failure) {
		t.Errorf("Close returned %v, want the sink's error", closeErr)
	}
}

// Close is deferred by its caller and also checked, so calling it twice has to
// be harmless rather than a second close of the queue.
func TestSinkPoolCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	pool := anim.NewSinkPool(2, func(int, *image.NRGBA) error { return nil })

	err := pool.Close()
	if err != nil {
		t.Fatalf("first Close: %v", err)
	}

	err = pool.Close()
	if err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
