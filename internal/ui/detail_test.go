package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestJobDetailPageViewModes(t *testing.T) {
	job := JobDetail{
		ID:        "12345678-1234-1234-1234-123456789abc",
		State:     "pending",
		StartTime: time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC),
		RefWidth:  640,
		RefHeight: 480,
		RefSize:   2048,
	}

	var output bytes.Buffer
	if err := JobDetailPage(job).Render(context.Background(), &output); err != nil {
		t.Fatalf("render job detail: %v", err)
	}
	body := output.String()

	for _, marker := range []string{
		`data-view-mode="side-by-side"`,
		`name="view-mode" value="reference" aria-keyshortcuts="1"`,
		`name="view-mode" value="best" aria-keyshortcuts="2"`,
		`name="view-mode" value="side-by-side" aria-keyshortcuts="3" checked`,
		`name="view-mode" value="difference" aria-keyshortcuts="4"`,
		`data-view-panel="reference"`,
		`data-view-panel="best"`,
		`data-view-panel="difference"`,
		`mayflycirclefit.viewMode`,
		`initializeImageState('best-image'`,
		`id="best-image-error"`,
		`id="diff-image-error"`,
		`id="heatmap-colormap"`,
		`<option value="turbo" selected>Turbo</option>`,
		`<option value="magma">Magma</option>`,
		`diff.png?colormap=turbo&amp;t=`,
		`id="heatmap-legend-gradient"`,
		`Mean absolute RGB error per pixel`,
		`selectedHeatmapColormap()`,
		`640 × 480 px`,
		`title="2048 bytes">2.0 KiB`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("rendered detail page missing %q", marker)
		}
	}
}

func TestJobDetailPageMetadataUnavailable(t *testing.T) {
	job := JobDetail{
		ID:        "12345678-1234-1234-1234-123456789abc",
		State:     "pending",
		StartTime: time.Date(2026, time.August, 13, 9, 0, 0, 0, time.UTC),
	}

	var output bytes.Buffer
	if err := JobDetailPage(job).Render(context.Background(), &output); err != nil {
		t.Fatalf("render job detail: %v", err)
	}
	if !strings.Contains(output.String(), "Metadata unavailable") {
		t.Error("rendered detail page should report unavailable reference metadata")
	}
}

func TestFormatFileSize(t *testing.T) {
	tests := []struct {
		size int64
		want string
	}{
		{size: 0, want: "0 B"},
		{size: 1023, want: "1023 B"},
		{size: 1024, want: "1.0 KiB"},
		{size: 2 * 1024 * 1024, want: "2.0 MiB"},
		{size: 3 * 1024 * 1024 * 1024, want: "3.0 GiB"},
	}

	for _, test := range tests {
		if got := formatFileSize(test.size); got != test.want {
			t.Errorf("formatFileSize(%d) = %q, want %q", test.size, got, test.want)
		}
	}
}
