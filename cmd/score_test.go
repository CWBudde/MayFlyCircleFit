package cmd

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// writeScoreFixture paints a 32x32 canvas that is white with a red square, so a
// red circle over the square is a large, unambiguous improvement.
func writeScoreFixture(t *testing.T, path string) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
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
	if err := os.WriteFile(arrayPath, []byte(
		`[{"x": 16, "y": 16, "r": 4, "color": "#ff0000"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	documentPath := filepath.Join(dir, "campaign.json")
	if err := os.WriteFile(documentPath, []byte(
		`{"base": {"initialCircles": [{"x": 16, "y": 16, "r": 4, "color": "#ff0000"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{arrayPath, documentPath} {
		specs, err := loadCircleSpecs(path)
		if err != nil {
			t.Fatalf("loadCircleSpecs(%s) error = %v", path, err)
		}
		if len(specs) != 1 || specs[0].R != 4 {
			t.Fatalf("loadCircleSpecs(%s) = %+v, want one circle of radius 4", path, specs)
		}
	}

	outPath := filepath.Join(dir, "preview.png")
	withScoreFlags(t, refPath, documentPath, outPath)
	if err := runScore(nil, nil); err != nil {
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
	if err := os.WriteFile(circlesPath, []byte(
		`[{"x": 9000, "y": 16, "r": 4, "color": "#ff0000"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	withScoreFlags(t, refPath, circlesPath, "")
	if err := runScore(nil, nil); err == nil {
		t.Fatal("runScore() accepted a circle outside the canvas")
	}
}

func TestScoreRejectsAnUnparseableColour(t *testing.T) {
	dir := t.TempDir()
	refPath := filepath.Join(dir, "ref.png")
	writeScoreFixture(t, refPath)
	circlesPath := filepath.Join(dir, "circles.json")
	if err := os.WriteFile(circlesPath, []byte(
		`[{"x": 16, "y": 16, "r": 4, "color": "red"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	withScoreFlags(t, refPath, circlesPath, "")
	if err := runScore(nil, nil); err == nil {
		t.Fatal("runScore() accepted a colour that is not hex")
	}
}
