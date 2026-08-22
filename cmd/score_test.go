package cmd

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// writeScoreFixture paints a 32x32 canvas that is white with a red square, so a
// red circle over the square is a large, unambiguous improvement.
func writeScoreFixture(t *testing.T, path string) {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))

	for y := range 32 {
		for x := range 32 {
			img.Set(x, y, color.NRGBA{255, 255, 255, 255})
		}
	}

	for y := 12; y < 20; y++ {
		for x := 12; x < 20; x++ {
			img.Set(x, y, color.NRGBA{255, 0, 0, 255})
		}
	}

	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

// withScoreFlags sets the command's package-level flags and restores them, the
// same pattern the schedule tests use.
func withScoreFlags(t *testing.T, ref, circles, out string) {
	t.Helper()

	previousRef, previousCircles, previousOut := scoreRefPath, scoreCirclesPath, scoreOutPath
	scoreRefPath, scoreCirclesPath, scoreOutPath = ref, circles, out

	t.Cleanup(func() {
		scoreRefPath, scoreCirclesPath, scoreOutPath = previousRef, previousCircles, previousOut
	})
}

func TestScoreReadsABareCircleArrayAndASchedule(t *testing.T) {
	dir := t.TempDir()
	refPath := filepath.Join(dir, "ref.png")
	writeScoreFixture(t, refPath)

	arrayPath := filepath.Join(dir, "circles.json")

	err := os.WriteFile(arrayPath, []byte(
		`[{"x": 16, "y": 16, "r": 4, "color": "#ff0000"}]`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	documentPath := filepath.Join(dir, "campaign.json")

	err = os.WriteFile(documentPath, []byte(
		`{"base": {"initialCircles": [{"x": 16, "y": 16, "r": 4, "color": "#ff0000"}]}}`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{arrayPath, documentPath} {
		specs, canvasPath, err := loadCircleSpecs(path)
		if err != nil {
			t.Fatalf("loadCircleSpecs(%s) error = %v", path, err)
		}

		if len(specs) != 1 || specs[0].R != 4 {
			t.Fatalf("loadCircleSpecs(%s) = %+v, want one circle of radius 4", path, specs)
		}

		if canvasPath != "" {
			t.Fatalf("loadCircleSpecs(%s) canvas = %q, want none", path, canvasPath)
		}
	}

	outPath := filepath.Join(dir, "preview.png")
	withScoreFlags(t, refPath, documentPath, outPath)

	err = runScore(nil, nil)
	if err != nil {
		t.Fatalf("runScore() error = %v", err)
	}

	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("--out did not write a file: %v", err)
	}
}

func TestScoreRefusesACircleOutsideTheCanvas(t *testing.T) {
	dir := t.TempDir()
	refPath := filepath.Join(dir, "ref.png")
	writeScoreFixture(t, refPath)

	circlesPath := filepath.Join(dir, "circles.json")

	err := os.WriteFile(circlesPath, []byte(
		`[{"x": 9000, "y": 16, "r": 4, "color": "#ff0000"}]`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	withScoreFlags(t, refPath, circlesPath, "")

	err = runScore(nil, nil)
	if err == nil {
		t.Fatal("runScore() accepted a circle outside the canvas")
	}
}

func TestScoreRejectsAnUnparseableColour(t *testing.T) {
	dir := t.TempDir()
	refPath := filepath.Join(dir, "ref.png")
	writeScoreFixture(t, refPath)

	circlesPath := filepath.Join(dir, "circles.json")

	err := os.WriteFile(circlesPath, []byte(
		`[{"x": 16, "y": 16, "r": 4, "color": "red"}]`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	withScoreFlags(t, refPath, circlesPath, "")

	err = runScore(nil, nil)
	if err == nil {
		t.Fatal("runScore() accepted a colour that is not hex")
	}
}

// TestScoreHonorsTheScheduleCanvas pins the reason score reads canvasPath: the
// worker starts from that canvas, so a cost measured against white would
// describe an arrangement the campaign never runs. The fixture makes the two
// answers impossible to confuse -- the canvas is the reference itself, and the
// one circle is white on an already-white corner, so the faithful cost is zero
// while the same arrangement on white still carries the whole red square.
func TestScoreHonorsTheScheduleCanvas(t *testing.T) {
	dir := t.TempDir()
	refPath := filepath.Join(dir, "ref.png")
	writeScoreFixture(t, refPath)

	plainPath := filepath.Join(dir, "plain.json")
	if err := os.WriteFile(plainPath, []byte(
		`{"base": {"initialCircles": [{"x": 2, "y": 2, "r": 1, "color": "#ffffff"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	canvasPath := filepath.Join(dir, "campaign.json")

	document := `{"base": {"canvasPath": ` + strconv.Quote(refPath) +
		`, "initialCircles": [{"x": 2, "y": 2, "r": 1, "color": "#ffffff"}]}}`
	if err := os.WriteFile(canvasPath, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}

	specs, canvas, err := loadCircleSpecs(canvasPath)
	if err != nil {
		t.Fatal(err)
	}

	if canvas != refPath {
		t.Fatalf("loadCircleSpecs canvas = %q, want %q", canvas, refPath)
	}

	ref, err := loadScoreReference(refPath)
	if err != nil {
		t.Fatal(err)
	}

	params, err := specs.ToParams()
	if err != nil {
		t.Fatal(err)
	}

	seeded, err := scoreRenderer(ref, canvas, len(specs))
	if err != nil {
		t.Fatal(err)
	}

	if cost := seeded.Cost(params); cost != 0 {
		t.Fatalf("cost against the schedule canvas = %v, want 0", cost)
	}

	white, err := scoreRenderer(ref, "", len(specs))
	if err != nil {
		t.Fatal(err)
	}

	if cost := white.Cost(params); cost == 0 {
		t.Fatal("cost against white was 0, so the fixture cannot tell the two canvases apart")
	}

	// The end-to-end path must reach the same renderer, not just the helper.
	withScoreFlags(t, refPath, canvasPath, "")

	if err := runScore(nil, nil); err != nil {
		t.Fatalf("runScore() error = %v", err)
	}

	withScoreFlags(t, refPath, plainPath, "")

	if err := runScore(nil, nil); err != nil {
		t.Fatalf("runScore() error = %v", err)
	}
}

// TestScoreRejectsACanvasOfTheWrongSize keeps a mismatch an error: the renderer
// constructor panics on it, and a path the operator typed is an input.
func TestScoreRejectsACanvasOfTheWrongSize(t *testing.T) {
	dir := t.TempDir()
	refPath := filepath.Join(dir, "ref.png")
	writeScoreFixture(t, refPath)

	ref, err := loadScoreReference(refPath)
	if err != nil {
		t.Fatal(err)
	}

	smallPath := filepath.Join(dir, "small.png")
	small := image.NewNRGBA(image.Rect(0, 0, 16, 16))

	file, err := os.Create(smallPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := png.Encode(file, small); err != nil {
		t.Fatal(err)
	}

	file.Close()

	if _, err := scoreRenderer(ref, smallPath, 1); err == nil {
		t.Fatal("scoreRenderer() accepted a canvas smaller than the reference")
	}

	if _, err := scoreRenderer(ref, filepath.Join(dir, "absent.png"), 1); err == nil {
		t.Fatal("scoreRenderer() accepted a canvas that does not exist")
	}
}
