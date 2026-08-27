package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

const viewerJobID = "123e4567-e89b-12d3-a456-426614174000"

func fixtureViewerData(mode string) ImageViewerData {
	return ImageViewerData{
		JobID:             viewerJobID,
		DefaultMode:       mode,
		BestRevision:      7,
		ReferenceImageURL: "/api/v1/jobs/" + viewerJobID + "/ref.png",
		BestImageURL:      "/api/v1/jobs/" + viewerJobID + "/best.png",
		DiffImageURL:      "/api/v1/jobs/" + viewerJobID + "/diff.png",
		JobState:          "running",
		MaxIterations:     500,
		CircleCount:       64,
		ReferenceWidth:    64,
		ReferenceHeight:   64,
		ReferenceSize:     1024,
		ShowMetadata:      true,
		ExtraClass:        "fixture-viewer",
	}
}

// TestImageViewerFallbackNeedsNoScript is the acceptance check for the port:
// what the server renders has to be a complete, readable side-by-side view with
// no script behind it. The five comparison modes now live in one place,
// web/src/ImageViewer.tsx, and every control that needs them went with it --
// leaving an inert radio or an inert slider on the page would tell a reader
// without JavaScript that something is available when it is not.
func TestImageViewerFallbackNeedsNoScript(t *testing.T) {
	t.Parallel()

	body := renderImageViewer(t, fixtureViewerData("side-by-side"))

	if strings.Contains(body, "<script") {
		t.Error("image viewer carries an inline script again")
	}

	for _, present := range []string{
		`data-view-mode="side-by-side"`,
		`data-view-panel="reference"`,
		`data-view-panel="best"`,
		`alt="Reference Image"`,
		`alt="Current Best Image"`,
		"/best.png?v=7",
		"<noscript>",
	} {
		if !strings.Contains(body, present) {
			t.Errorf("no-JavaScript fallback is missing %q", present)
		}
	}

	// Controls the fallback cannot operate, and panels it cannot reach.
	for _, absent := range []string{
		`name="view-mode"`,
		`aria-keyshortcuts=`,
		`data-view-panel="difference"`,
		`data-view-panel="overlay"`,
		`id="overlay-opacity"`,
		`id="heatmap-colormap"`,
		"heatmap-legend",
		"overlay-best-layer",
		"image-loading",
	} {
		if strings.Contains(body, absent) {
			t.Errorf("no-JavaScript fallback still renders the inert control %q", absent)
		}
	}
}

// TestImageViewerFallbackIgnoresTheCallerMode pins the reason DefaultMode no
// longer reaches data-view-mode. The fallback has only the reference and best
// panels, and the panel CSS keys off data-view-mode, so a caller asking for
// "overlay" without JavaScript would otherwise get a card with no image in it.
func TestImageViewerFallbackIgnoresTheCallerMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"reference", "best", "side-by-side", "difference", "overlay", "nonsense"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			body := renderImageViewer(t, fixtureViewerData(mode))
			if !strings.Contains(body, `data-view-mode="side-by-side"`) {
				t.Errorf("fallback for %q does not show the side-by-side pair", mode)
			}
			if !strings.Contains(body, `data-default-mode="`+imageViewerMode(mode)+`"`) {
				t.Errorf("island default mode for %q was not published", mode)
			}
		})
	}
}

// TestImageViewerPublishesIslandProps covers the other half of the card: the
// props the React viewer is rendered with all travel on this element, so an
// attribute that goes missing is a viewer that mounts with the wrong job, the
// wrong images or no metadata at all.
func TestImageViewerPublishesIslandProps(t *testing.T) {
	t.Parallel()

	body := renderImageViewer(t, fixtureViewerData("difference"))

	for _, marker := range []string{
		`data-job-id="` + viewerJobID + `"`,
		`data-job-state="running"`,
		`data-max-iters="500"`,
		`data-circle-count="64"`,
		`data-best-revision="7"`,
		`data-colormap="turbo"`,
		`data-reference-url="/api/v1/jobs/` + viewerJobID + `/ref.png"`,
		`data-best-url="/api/v1/jobs/` + viewerJobID + `/best.png"`,
		`data-diff-url="/api/v1/jobs/` + viewerJobID + `/diff.png"`,
		`data-show-metadata="true"`,
		`data-ref-dimensions="64 × 64 px"`,
		`data-ref-filesize="1.0 KiB"`,
		`data-ref-bytes="1024 bytes"`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("viewer mount point missing %q", marker)
		}
	}
}

// TestImageViewerNeverMountsItself is the "one viewer implementation" check on
// the Go side. Both pages that render this card render it inside an island root
// that owns the whole subtree -- campaign-detail on /schedules/{id}, job-detail
// on /jobs/{id} -- and mounting an island replaces every child of its root. A
// mount point of the viewer's own would therefore be a React root over a node
// on its way out; both pages reach the same React component from their own
// island instead.
func TestImageViewerNeverMountsItself(t *testing.T) {
	t.Parallel()

	body := renderImageViewer(t, fixtureViewerData("side-by-side"))
	if strings.Contains(body, "data-island") {
		t.Error("viewer advertises a mount point inside another island's root")
	}

	// The element itself is unchanged otherwise: same card, same props.
	if !strings.Contains(body, `data-job-id="`+viewerJobID+`"`) {
		t.Error("viewer dropped its props along with the mount point")
	}
}

// TestJobDetailPageRendersExactlyOneViewer pins the count. The component owns
// the fixed id "image-viewer", so a second instance would duplicate an id.
func TestJobDetailPageRendersExactlyOneViewer(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer

	err := JobDetailPage(JobDetail{ID: viewerJobID, State: "running"}).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render job detail: %v", err)
	}

	if got := strings.Count(output.String(), `id="image-viewer"`); got != 1 {
		t.Errorf("job detail page has %d elements with id image-viewer, want 1", got)
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
			JobID:       viewerJobID,
		})
		if !strings.Contains(body, `class="card image-viewer card dashboard"`) {
			t.Fatalf("missing extra class composition: %s", body)
		}
	})
	t.Run("defaults side-by-side", func(t *testing.T) {
		body := renderImageViewer(t, ImageViewerData{
			JobID:       viewerJobID,
			DefaultMode: "",
		})
		if !strings.Contains(body, `data-default-mode="side-by-side"`) {
			t.Fatal("empty default mode did not fall back to side-by-side")
		}
	})
}

// TestImageViewerMetadataStringsTravelPreFormatted covers the three attributes
// that exist so formatFileSize does not need a TypeScript twin.
func TestImageViewerMetadataStringsTravelPreFormatted(t *testing.T) {
	t.Parallel()

	t.Run("present", func(t *testing.T) {
		t.Parallel()

		body := renderImageViewer(t, ImageViewerData{
			JobID: viewerJobID, ShowMetadata: true,
			ReferenceWidth: 640, ReferenceHeight: 480, ReferenceSize: 2048,
		})
		for _, marker := range []string{
			`data-ref-dimensions="640 × 480 px"`,
			`data-ref-filesize="2.0 KiB"`,
			`data-ref-bytes="2048 bytes"`,
			`<span>640 × 480 px</span>`,
			`<span title="2048 bytes">2.0 KiB</span>`,
		} {
			if !strings.Contains(body, marker) {
				t.Errorf("metadata rendering missing %q", marker)
			}
		}
	})

	t.Run("absent", func(t *testing.T) {
		t.Parallel()

		body := renderImageViewer(t, ImageViewerData{JobID: viewerJobID, ShowMetadata: true})
		if !strings.Contains(body, "Metadata unavailable") {
			t.Error("a viewer with no reference facts does not say so")
		}
		for _, marker := range []string{`data-ref-dimensions=""`, `data-ref-filesize=""`, `data-ref-bytes=""`} {
			if !strings.Contains(body, marker) {
				t.Errorf("empty metadata attribute missing: %q", marker)
			}
		}
	})

	t.Run("suppressed", func(t *testing.T) {
		t.Parallel()

		body := renderImageViewer(t, ImageViewerData{JobID: viewerJobID, ReferenceWidth: 8, ReferenceHeight: 8})
		if !strings.Contains(body, `data-show-metadata="false"`) {
			t.Error("ShowMetadata=false was not published to the island")
		}
		if strings.Contains(body, "image-metadata") {
			t.Error("metadata row rendered although the caller suppressed it")
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
		JobID:       viewerJobID,
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

	// The rules did not just disappear: Layout serves them to every page, and
	// they are now the island's styling as much as the fallback's.
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

	// The legend's own clip-rect block was a second .sr-only. Both renderings of
	// the fieldset use the shared class, so the duplicate is gone.
	if strings.Contains(layout.String(), ".view-mode-selector legend {") {
		t.Error("layout still hand-rolls a clip rect for the view-mode legend")
	}
}
