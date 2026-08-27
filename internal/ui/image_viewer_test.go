package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestImageViewerSupportsAllModes(t *testing.T) {
	views := []struct {
		name      string
		mode      string
		inputID   string
		shortcuts string
		heading   string
	}{
		{name: "reference", mode: "reference", inputID: "view-mode-reference", shortcuts: `aria-keyshortcuts="1"`, heading: "Reference"},
		{name: "best", mode: "best", inputID: "view-mode-best", shortcuts: `aria-keyshortcuts="2"`, heading: "Current Best"},
		{name: "side-by-side", mode: "side-by-side", inputID: "view-mode-side-by-side", shortcuts: `aria-keyshortcuts="3"`, heading: ""},
		{name: "difference", mode: "difference", inputID: "view-mode-difference", shortcuts: `aria-keyshortcuts="4"`, heading: "Difference Heatmap"},
		{name: "overlay", mode: "overlay", inputID: "view-mode-overlay", shortcuts: `aria-keyshortcuts="5"`, heading: "Best over Reference"},
	}

	for _, view := range views {
		t.Run(view.name, func(t *testing.T) {
			body := renderImageViewer(t, ImageViewerData{
				JobID:             "123e4567-e89b-12d3-a456-426614174000",
				DefaultMode:       view.mode,
				BestRevision:      7,
				ReferenceImageURL: "/api/v1/jobs/123e4567-e89b-12d3-a456-426614174000/ref.png",
				BestImageURL:      "/api/v1/jobs/123e4567-e89b-12d3-a456-426614174000/best.png",
				DiffImageURL:      "/api/v1/jobs/123e4567-e89b-12d3-a456-426614174000/diff.png",
				JobState:          "running",
				MaxIterations:     500,
				ReferenceWidth:    64,
				ReferenceHeight:   64,
				ReferenceSize:     1024,
				ShowMetadata:      true,
				ExtraClass:        "fixture-viewer",
			})

			if !strings.Contains(body, `data-view-mode="`+view.mode+`"`) {
				t.Fatalf("missing default mode marker for %s", view.mode)
			}

			if !strings.Contains(body, view.shortcuts) {
				t.Fatalf("missing shortcut for mode %s", view.mode)
			}

			if !strings.Contains(body, `id="`+view.inputID+`"`) {
				t.Fatalf("missing radio input for %s", view.mode)
			}

			if !strings.Contains(body, `value="`+view.mode+`"`) {
				t.Fatalf("missing mode value for %s", view.mode)
			}

			switch view.mode {
			case "reference", "best", "difference", "overlay":
				if !strings.Contains(body, `data-view-panel="`+view.mode+`"`) {
					t.Fatalf("missing view panel for %s", view.mode)
				}
			case "side-by-side":
				if !strings.Contains(body, `data-view-panel="reference"`) || !strings.Contains(body, `data-view-panel="best"`) {
					t.Fatal("side-by-side mode did not expose reference and best panels")
				}
			}

			if view.heading != "" && !strings.Contains(body, view.heading) {
				t.Fatalf("missing heading for %s", view.mode)
			}

			inputStart := strings.Index(body, `id="`+view.inputID+`"`)
			if inputStart < 0 {
				t.Fatalf("could not find %s input", view.inputID)
			}

			inputEnd := strings.Index(body[inputStart:], ">")
			if inputEnd < 0 {
				t.Fatal("radio input was truncated")
			}

			inputTag := body[inputStart : inputStart+inputEnd+2]
			if !strings.Contains(inputTag, "checked") {
				t.Fatalf("expected checked marker for %s, got %q", view.mode, inputTag)
			}

			for _, scriptMarker := range []string{
				"initializeViewMode();",
				"initializeImageState(\"reference-image\", \"reference-image-loading\", \"reference-image-error\")",
				"initializeHeatmapColormap();",
				"initializeOverlayOpacity();",
			} {
				if !strings.Contains(body, scriptMarker) {
					t.Fatalf("missing viewer script marker %q", scriptMarker)
				}
			}
		})
	}
}

func renderImageViewer(t *testing.T, data ImageViewerData) string {
	t.Helper()

	var output bytes.Buffer

	err := ImageViewer(data).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render image viewer: %v", err)
	}

	return output.String()
}

func TestImageViewerSrcAddsCacheBustingRevision(t *testing.T) {
	t.Run("without revision", func(t *testing.T) {
		if got, want := imageViewerSrc("/api/v1/jobs/abc/best.png", 0), "/api/v1/jobs/abc/best.png"; got != want {
			t.Fatalf("imageViewerSrc without revision = %q, want %q", got, want)
		}
	})
	t.Run("with revision", func(t *testing.T) {
		if got, want := imageViewerSrc("/api/v1/jobs/abc/best.png", 7), "/api/v1/jobs/abc/best.png?v=7"; got != want {
			t.Fatalf("imageViewerSrc with revision = %q, want %q", got, want)
		}
	})
	t.Run("with existing query", func(t *testing.T) {
		if got, want := imageViewerSrc("/api/v1/jobs/abc/diff.png?colormap=turbo", 9), "/api/v1/jobs/abc/diff.png?colormap=turbo&v=9"; got != want {
			t.Fatalf("imageViewerSrc with query = %q, want %q", got, want)
		}
	})
}

func TestImageViewerClassesAndModeDefaults(t *testing.T) {
	t.Run("adds extra class", func(t *testing.T) {
		body := renderImageViewer(t, ImageViewerData{
			ExtraClass:  "card dashboard",
			DefaultMode: "reference",
			JobID:       "123e4567-e89b-12d3-a456-426614174000",
		})
		if !strings.Contains(body, `class="card image-viewer card dashboard"`) {
			t.Fatalf("missing extra class composition: %s", body)
		}
	})
	t.Run("defaults side-by-side", func(t *testing.T) {
		body := renderImageViewer(t, ImageViewerData{
			JobID:       "123e4567-e89b-12d3-a456-426614174000",
			DefaultMode: "",
		})
		if !strings.Contains(body, `data-view-mode="side-by-side"`) {
			t.Fatal("empty default mode did not fall back to side-by-side")
		}
	})
}

// TestImageViewerHasNoLocalStyleBlock pins where the viewer's CSS lives.
//
// The campaign page renders this component inside the `campaign-detail` island
// root, and mounting an island calls createRoot(root).render(...), which
// replaces every child of that root -- a component-local <style> block
// included. The viewer then painted with no view-mode, frame or focus styling
// at all on /schedules/{id}. The vocabulary belongs in Layout, outside every
// island root, so a block reappearing here is the bug coming back.
func TestImageViewerHasNoLocalStyleBlock(t *testing.T) {
	t.Parallel()

	body := renderImageViewer(t, ImageViewerData{
		JobID:       "123e4567-e89b-12d3-a456-426614174000",
		DefaultMode: "side-by-side",
	})

	if strings.Contains(body, "<style>") {
		t.Error("image viewer carries a component-local <style> block again")
	}

	for _, rule := range []string{".view-mode-selector {", imageFrameRule, ".heatmap-legend {"} {
		if strings.Contains(body, rule) {
			t.Errorf("image viewer still declares %q locally", rule)
		}
	}

	// The rules did not just disappear: Layout serves them to every page.
	var layout bytes.Buffer

	err := Layout("Images").Render(context.Background(), &layout)
	if err != nil {
		t.Fatalf("render layout: %v", err)
	}

	for _, rule := range []string{
		".view-mode-selector {",
		".view-mode-option input:checked + label {",
		imageFrameRule,
		".image-state {",
		".overlay-best-layer {",
		".heatmap-legend {",
		`.image-viewer[data-view-mode="side-by-side"] .image-view-panels {`,
	} {
		if !strings.Contains(layout.String(), rule) {
			t.Errorf("layout does not supply %q", rule)
		}
	}
}

// TestImageViewerAnnouncesStateChanges covers the parts of the viewer a reader
// only meets through the accessibility tree.
func TestImageViewerAnnouncesStateChanges(t *testing.T) {
	t.Parallel()

	body := renderImageViewer(t, ImageViewerData{
		JobID:       "123e4567-e89b-12d3-a456-426614174000",
		DefaultMode: "overlay",
		JobState:    "running",
	})

	// The fieldset caption uses the shared visually-hidden class rather than a
	// hand-rolled clip rect that only existed in the deleted style block.
	if !strings.Contains(body, `<legend class="sr-only">Image view mode</legend>`) {
		t.Error("view-mode legend is not visually hidden through .sr-only")
	}

	// The slider writes the percentage into this output; aria-live="off" made
	// that change silent for a reader who cannot see the number move.
	opacityReadout := `id="overlay-opacity-value" class="overlay-opacity-value" ` +
		`for="overlay-opacity" aria-live="polite"`
	if !strings.Contains(body, opacityReadout) {
		t.Error("overlay opacity readout does not announce its value")
	}

	// Each spinner toggles display only, so without a role its appearance and
	// disappearance are invisible to a reader.
	for _, id := range []string{"reference-image", "best-image", "diff-image", "overlay-best-image"} {
		marker := `<div id="` + id + `-loading" class="image-state image-loading" role="status"`
		if !strings.Contains(body, marker) {
			t.Errorf("loading state for %s is not announced: missing %q", id, marker)
		}
	}

	// Every radio takes a digit shortcut, so every radio has to advertise one.
	for _, shortcut := range []string{
		`value="reference" aria-keyshortcuts="1"`,
		`value="best" aria-keyshortcuts="2"`,
		`value="side-by-side" aria-keyshortcuts="3"`,
		`value="difference" aria-keyshortcuts="4"`,
		`value="overlay" aria-keyshortcuts="5"`,
	} {
		if !strings.Contains(body, shortcut) {
			t.Errorf("view-mode radio missing %q", shortcut)
		}
	}
}
