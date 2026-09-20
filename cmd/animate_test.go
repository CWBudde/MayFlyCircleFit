// The animate tests set the command's package-level flag variables directly,
// which only an internal test package can reach. score_test.go and
// schedule_test.go do the same for the same reason.
//
//nolint:testpackage // reaches the package-level animate flags the command binds its own flags to.
package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/anim"
	"github.com/cwbudde/circlefit/internal/store"
	"github.com/spf13/cobra"
)

// animateSpecs is the arrangement every test here animates: two circles well
// inside a 32x32 canvas, in a fixed back-to-front order.
const animateSpecs = `[
  {"x": 12, "y": 12, "r": 6, "color": "#ff0000", "opacity": 1},
  {"x": 20, "y": 18, "r": 4, "color": "#0000ff", "opacity": 0.5}
]`

// withAnimateFlags sets the command's package-level flags and restores them,
// the same pattern the score and schedule tests use.
func withAnimateFlags(t *testing.T, apply func()) {
	t.Helper()

	saved := animateFlagState()

	apply()

	t.Cleanup(func() { restoreAnimateFlags(saved) })
}

type animateFlags struct {
	ref, circles, checkpoint, canvas, outDir, style, background, mp4 string
	scale, margin                                                    float64
	halfLife, maxActive, frames, outro, fps                          int
	reverse                                                          bool
}

func animateFlagState() animateFlags {
	return animateFlags{
		ref: animateRefPath, circles: animateCirclesPath, checkpoint: animateCheckpointPath,
		canvas: animateCanvasPath, outDir: animateOutDir, style: animateStyle,
		background: animateBackground, mp4: animateMP4Path,
		scale: animateScale, margin: animateMargin,
		halfLife: animateHalfLife, maxActive: animateMaxActive, frames: animateFrames,
		outro: animateOutro, fps: animateFPS, reverse: animateReverse,
	}
}

func restoreAnimateFlags(saved animateFlags) {
	animateRefPath, animateCirclesPath, animateCheckpointPath = saved.ref, saved.circles, saved.checkpoint
	animateCanvasPath, animateOutDir, animateStyle = saved.canvas, saved.outDir, saved.style
	animateBackground, animateMP4Path = saved.background, saved.mp4
	animateScale, animateMargin = saved.scale, saved.margin
	animateHalfLife, animateMaxActive, animateFrames = saved.halfLife, saved.maxActive, saved.frames
	animateOutro, animateFPS, animateReverse = saved.outro, saved.fps, saved.reverse
}

// animateFixture lays out a reference image and a circle list, and points the
// flags at them with the defaults the flag definitions supply.
func animateFixture(t *testing.T, style string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	refPath := filepath.Join(dir, "ref.png")
	writeScoreFixture(t, refPath)

	circlesPath := filepath.Join(dir, "circles.json")

	err := os.WriteFile(circlesPath, []byte(animateSpecs), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(dir, "frames")

	withAnimateFlags(t, func() {
		defaults := anim.DefaultOptions()
		animateRefPath, animateCirclesPath, animateCheckpointPath = refPath, circlesPath, ""
		animateCanvasPath, animateOutDir, animateStyle = "", outDir, style
		animateBackground, animateMP4Path = "#FFFFFF", ""
		animateScale, animateMargin = 1, 0
		animateHalfLife, animateMaxActive, animateFrames = 0, defaults.MaxActive, 0
		animateOutro, animateFPS, animateReverse = 0, 30, false
	})

	return dir, outDir
}

// animateTestCommand is a stand-in for the cobra command runAnimate is given,
// with its output discarded: the command reports progress through its own
// writer and takes its context from there.
func animateTestCommand() *cobra.Command {
	command := &cobra.Command{}
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)

	return command
}

func frameNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read frames: %v", err)
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names
}

//nolint:paralleltest // mutates the package-level animate flags, which every test in this package shares.
func TestAnimateWritesOneFrameForEachCircleAndTheOpeningCanvas(t *testing.T) {
	_, outDir := animateFixture(t, string(anim.StyleStatic))

	err := runAnimate(animateTestCommand(), nil)
	if err != nil {
		t.Fatalf("runAnimate: %v", err)
	}

	names := frameNames(t, outDir)

	want := []string{"frame-000000.png", "frame-000001.png", "frame-000002.png"}
	if len(names) != len(want) {
		t.Fatalf("wrote %v, want %v", names, want)
	}

	for i, name := range names {
		if name != want[i] {
			t.Errorf("frame %d is %q, want %q", i, name, want[i])
		}
	}
}

// A growing style is where the frame count stops being one per circle, and
// where the command has to size its renderer for more than a single circle at a
// time. Static alone would exercise neither.
//
//nolint:paralleltest // mutates the package-level animate flags, which every test in this package shares.
func TestAnimateCascadeWritesMoreFramesThanCircles(t *testing.T) {
	_, outDir := animateFixture(t, string(anim.StyleCascade))

	err := runAnimate(animateTestCommand(), nil)
	if err != nil {
		t.Fatalf("runAnimate: %v", err)
	}

	names := frameNames(t, outDir)
	if len(names) <= 3 {
		t.Fatalf("cascade wrote %d frames for two circles; it must tween between them", len(names))
	}

	if names[len(names)-1] != fmt.Sprintf(frameNamePattern, len(names)-1) {
		t.Errorf("last frame is %q, which does not continue the numbering", names[len(names)-1])
	}
}

// The two input routes describe the same arrangement, so they have to produce
// the same pixels. This is what makes --checkpoint usable on a campaign result
// without first exporting it to a spec file.
//
//nolint:paralleltest // mutates the package-level animate flags, which every test in this package shares.
func TestAnimateAgreesBetweenACircleListAndACheckpoint(t *testing.T) {
	dir, outDir := animateFixture(t, string(anim.StyleStatic))

	err := runAnimate(animateTestCommand(), nil)
	if err != nil {
		t.Fatalf("runAnimate from circles: %v", err)
	}

	fromCircles, err := os.ReadFile(filepath.Join(outDir, "frame-000002.png"))
	if err != nil {
		t.Fatal(err)
	}

	// The same two circles as a checkpoint's best solution. Colours are exact
	// channels here because that is the form a run records.
	checkpoint := store.NewCheckpoint("11111111-1111-1111-1111-111111111111", []float64{
		12, 12, 6, 1, 0, 0, 1,
		20, 18, 4, 0, 0, 1, 0.5,
	}, 1, 2, 1, store.JobConfig{Circles: 2})

	encoded, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	checkpointPath := filepath.Join(dir, "checkpoint.json")

	err = os.WriteFile(checkpointPath, encoded, 0o600)
	if err != nil {
		t.Fatal(err)
	}

	animateCirclesPath, animateCheckpointPath = "", checkpointPath
	animateOutDir = filepath.Join(dir, "from-checkpoint")

	err = runAnimate(animateTestCommand(), nil)
	if err != nil {
		t.Fatalf("runAnimate from checkpoint: %v", err)
	}

	fromCheckpoint, err := os.ReadFile(filepath.Join(animateOutDir, "frame-000002.png"))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(fromCircles, fromCheckpoint) {
		t.Error("the same arrangement animated differently from a checkpoint than from a circle list")
	}
}

// Scaling a base canvas would resample pixels the fit never saw, so the
// combination is refused rather than approximated.
//
//nolint:paralleltest // mutates the package-level animate flags, which every test in this package shares.
func TestAnimateRefusesAScaledBaseCanvas(t *testing.T) {
	dir, _ := animateFixture(t, string(anim.StyleStatic))

	canvasPath := filepath.Join(dir, "canvas.png")
	writeScoreFixture(t, canvasPath)

	animateCanvasPath = canvasPath
	animateScale = 2

	err := runAnimate(animateTestCommand(), nil)
	if err == nil {
		t.Fatal("scaling a base canvas was accepted")
	}

	if !strings.Contains(err.Error(), "--canvas cannot be combined with --scale") {
		t.Errorf("got %v, want a refusal naming --canvas and --scale", err)
	}
}

//nolint:paralleltest // mutates the package-level animate flags, which every test in this package shares.
func TestAnimateRefusesAnArrangementTheCanvasCannotHold(t *testing.T) {
	dir, _ := animateFixture(t, string(anim.StyleStatic))

	outside := filepath.Join(dir, "outside.json")

	err := os.WriteFile(outside, []byte(`[{"x": 900, "y": 900, "r": 4, "color": "#ff0000"}]`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	animateCirclesPath = outside

	err = runAnimate(animateTestCommand(), nil)
	if err == nil || !strings.Contains(err.Error(), "outside the bounds") {
		t.Errorf("got %v, want a refusal naming the bounds", err)
	}
}

// The encode itself needs ffmpeg, which a checkout is not entitled to assume,
// so the argument vector is asserted instead. It is the part that goes wrong:
// the input pattern has to match the names the frames were actually written
// under, and yuv420p has to be given even dimensions to work with.
//
//nolint:paralleltest // mutates the package-level animate flags, which every test in this package shares.
func TestFfmpegArgumentsMatchTheFramesOnDisk(t *testing.T) {
	_, outDir := animateFixture(t, string(anim.StyleStatic))

	animateMP4Path = filepath.Join(outDir, "fit.mp4")
	animateFPS = 24

	args := ffmpegArgs()
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"-framerate 24",
		filepath.Join(outDir, "frame-%06d.png"),
		"-pix_fmt yuv420p",
		"scale=trunc(iw/2)*2:trunc(ih/2)*2",
		animateMP4Path,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("ffmpeg arguments %q are missing %q", joined, want)
		}
	}
}

// A missing ffmpeg fails the command rather than quietly skipping the encode,
// and says what to run once it is installed. The frames are already on disk by
// then, which is why the message is worth reading.
func TestAnimateReportsAMissingFfmpeg(t *testing.T) {
	_, outDir := animateFixture(t, string(anim.StyleStatic))

	animateMP4Path = filepath.Join(outDir, "fit.mp4")

	t.Setenv("PATH", t.TempDir())

	err := runAnimate(animateTestCommand(), nil)
	if err == nil {
		t.Skip("ffmpeg resolved despite an empty PATH")
	}

	if !strings.Contains(err.Error(), "needs ffmpeg") {
		t.Fatalf("got %v, want a report that ffmpeg is missing", err)
	}

	if len(frameNames(t, outDir)) == 0 {
		t.Error("the frames were discarded; they are finished work and must survive the missing encoder")
	}
}
