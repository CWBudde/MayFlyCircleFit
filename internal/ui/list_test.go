package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func jobListPageFixture() JobListPage {
	return JobListPage{
		Jobs: []JobListItem{
			{
				ID:          "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
				State:       "completed",
				RefPath:     "assets/reference/a-very-long-reference-path-that-cannot-wrap-on-a-space.png",
				Mode:        "joint",
				Circles:     64,
				Iterations:  1200,
				BestCost:    9.9,
				InitialCost: 22.2,
				StartTime:   time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC),
			},
			{
				ID:         "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
				State:      "failed",
				RefPath:    "assets/reference/b.png",
				Mode:       "batch",
				Circles:    16,
				Iterations: 10,
				Error:      "renderer unavailable",
				StartTime:  time.Date(2026, 8, 14, 9, 0, 0, 0, time.UTC),
			},
		},
		Total: 2,
	}
}

func renderJobListPage(t *testing.T, page JobListPage) string {
	t.Helper()

	var output bytes.Buffer

	err := JobList(page).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render job list page: %v", err)
	}

	return output.String()
}

// renderJobCard renders one card without the layout, so an assertion about
// what the card does *not* contain cannot be satisfied by the page chrome.
func renderJobCard(t *testing.T, job JobListItem) string {
	t.Helper()

	var output bytes.Buffer

	err := JobCard(job).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render job card: %v", err)
	}

	return output.String()
}

func TestJobListPageRenders(t *testing.T) {
	body := renderJobListPage(t, jobListPageFixture())

	for _, marker := range []string{
		`Optimization Jobs`,
		`data-island="job-list"`,
		`id="job-list-page"`,
		`/jobs/aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa`,
		`<strong>Reference:</strong>`,
		`renderer unavailable`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("job list page missing %q", marker)
		}
	}
}

// TestJobListRowsWrapOnNarrowViewports pins the shared wrap vocabulary. Every
// row replaced here was a space-between flex row with no flex-wrap, which
// collided with itself well before a phone's width.
func TestJobListRowsWrapOnNarrowViewports(t *testing.T) {
	if !strings.Contains(renderJobListPage(t, jobListPageFixture()), `class="row-between"`) {
		t.Error("job list page header does not wrap")
	}

	body := renderJobCard(t, jobListPageFixture().Jobs[0])

	for _, marker := range []string{
		`class="row-between"`,
		`class="row-between row-between-top"`,
		`class="meta-row"`,
		`class="row-end"`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("job list page missing %q", marker)
		}
	}

	if strings.Contains(body, "justify-content: space-between") {
		t.Error("job list page still lays out a row with an inline space-between instead of .row-between")
	}

	// The reference path has no break opportunity of its own, so it is what
	// pushes the card wider than the viewport if it is allowed to stay one word.
	if !strings.Contains(body, "overflow-wrap: anywhere") {
		t.Error("job card footer does not let a long reference path break")
	}
}

// TestJobCardLiftIsCSSNotInlineJS guards the hover replacement: the inline
// handlers responded to a mouse only, so a card reached by keyboard got no
// feedback at all.
func TestJobCardLiftIsCSSNotInlineJS(t *testing.T) {
	body := renderJobCard(t, jobListPageFixture().Jobs[0])

	if !strings.Contains(body, `class="card-link"`) {
		t.Error("job card link does not use the shared .card-link lift")
	}

	for _, forbidden := range []string{"onmouseover", "onmouseout"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("job card still carries inline %s JavaScript", forbidden)
		}
	}
}

// TestJobCardMetadataIsNamedForAssistiveTech pins the labels that replaced the
// title attributes, which no touch device shows and no screen reader promises
// to announce.
func TestJobCardMetadataIsNamedForAssistiveTech(t *testing.T) {
	body := renderJobCard(t, jobListPageFixture().Jobs[0])

	for _, label := range []string{
		`<span class="sr-only">Optimization mode</span>`,
		`<span class="sr-only">Circle count</span>`,
		`<span class="sr-only">Iteration count</span>`,
	} {
		if !strings.Contains(body, label) {
			t.Errorf("job card metadata missing %q", label)
		}
	}

	for _, forbidden := range []string{`title="Mode"`, `title="Circles"`, `title="Iterations"`} {
		if strings.Contains(body, forbidden) {
			t.Errorf("job card still leans on %s to name a value", forbidden)
		}
	}

	// The visible abbreviation stays, but only for sighted readers; the
	// sr-only label already spells it out for everyone else.
	if !strings.Contains(body, `<strong aria-hidden="true">Mode:</strong>`) {
		t.Error("visible Mode label is not hidden from the accessibility tree")
	}
}

// TestJobListEmptyStateHidesDecorativeGlyph keeps the placeholder icon out of
// the accessibility tree; it carries no information the heading below it lacks.
func TestJobListEmptyStateHidesDecorativeGlyph(t *testing.T) {
	body := renderJobListPage(t, JobListPage{})

	if !strings.Contains(body, "No jobs yet") {
		t.Fatal("empty job list page does not render its empty state")
	}

	if !strings.Contains(body, `aria-hidden="true"`) {
		t.Error("empty-state glyph is not marked decorative")
	}
}

// TestJobCardUsesAccessibleSuccessText guards the contrast fix: the improvement
// line is text, and --success-color as text on the light surface is 2.54:1.
func TestJobCardUsesAccessibleSuccessText(t *testing.T) {
	body := renderJobCard(t, jobListPageFixture().Jobs[0])

	if !strings.Contains(body, "var(--success-text-strong)") {
		t.Error("job card does not use --success-text-strong for the improvement line")
	}

	if strings.Contains(body, "color: var(--success-color)") {
		t.Error("job card still uses --success-color as a text color")
	}
}
