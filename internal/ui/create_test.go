package ui

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
)

func renderCreatePage(t *testing.T, errorMsg, project string) string {
	t.Helper()

	var output bytes.Buffer

	err := CreateJobPage(errorMsg, project).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render create page: %v", err)
	}

	return output.String()
}

var (
	formControlPattern  = regexp.MustCompile(`<(?:input|select|textarea)\b[^>]*>`)
	idAttributePattern  = regexp.MustCompile(`\bid="([^"]+)"`)
	forAttributePattern = regexp.MustCompile(`\bfor="([^"]+)"`)
)

// TestCreateJobPageLabelsEveryControl is the form's baseline: a control with an
// id but no label pointing at it is an unnamed control, and this page has around
// thirty of them. The four checkboxes wrap their input in the label, which alone
// would name them, but they carry a matching for= as well so that this check can
// stay a single mechanical rule over the whole form.
func TestCreateJobPageLabelsEveryControl(t *testing.T) {
	body := renderCreatePage(t, "", "")

	labelled := map[string]bool{}
	for _, match := range forAttributePattern.FindAllStringSubmatch(body, -1) {
		labelled[match[1]] = true
	}

	controls := 0

	for _, control := range formControlPattern.FindAllString(body, -1) {
		id := idAttributePattern.FindStringSubmatch(control)
		if id == nil {
			// The hidden project field carries no id and needs no label.
			continue
		}

		controls++

		if !labelled[id[1]] {
			t.Errorf("control %q has no label referencing it", id[1])
		}
	}

	if controls < 25 {
		t.Fatalf("found %d identified controls, so the scan is not seeing the form", controls)
	}
}

// TestCreateJobPageMarksRequiredFieldsWithoutRelyingOnColor covers WCAG 1.3.1:
// the red asterisk conveys "required" through colour and a glyph alone, so the
// fact has to reach the accessibility tree by another route.
func TestCreateJobPageMarksRequiredFieldsWithoutRelyingOnColor(t *testing.T) {
	body := renderCreatePage(t, "", "")

	for _, id := range []string{"refPath", "optimizer", "mode", "circles", "iters", "popSize"} {
		control := regexp.MustCompile(`<(?:input|select)\b[^>]*\bid="` + id + `"[^>]*>`).FindString(body)
		if control == "" {
			t.Fatalf("required control %q not rendered", id)
		}

		if !strings.Contains(control, `aria-required="true"`) {
			t.Errorf("required control %q is not marked aria-required", id)
		}
	}

	markers := strings.Count(body, `<span class="sr-only"> (required)</span>`)
	if markers != 6 {
		t.Errorf(`"(required)" text rendered %d times, want one per required field (6)`, markers)
	}

	if hidden := strings.Count(body, `aria-hidden="true">*</span>`); hidden != 6 {
		t.Errorf("visual asterisk hidden from the accessibility tree %d times, want 6", hidden)
	}
}

// TestCreateJobPageGridsClampToTheContainer guards the phone overflow: an
// auto-fit track keeps its stated minimum even when the container is narrower
// than it, so `minmax(200px, 1fr)` pushes the page sideways at 320px however
// many columns fit.
func TestCreateJobPageGridsClampToTheContainer(t *testing.T) {
	body := renderCreatePage(t, "", "")

	grids := strings.Count(body, "minmax(min(200px, 100%), 1fr)")
	if grids != 5 {
		t.Errorf("clamped parameter grids = %d, want 5", grids)
	}

	if strings.Contains(body, "minmax(200px,") {
		t.Error("a parameter grid still declares an unclamped 200px minimum track")
	}
}

// TestCreateJobPageGroupsControlsInFieldsets checks that the standalone
// checkboxes are announced with the section they belong to. "Enable SSIM" says
// nothing on its own about which run it applies to.
func TestCreateJobPageGroupsControlsInFieldsets(t *testing.T) {
	body := renderCreatePage(t, "", "")

	if fieldsets := strings.Count(body, "<fieldset"); fieldsets != 7 {
		t.Errorf("form sections rendered as fieldsets = %d, want 7", fieldsets)
	}

	if legends := strings.Count(body, "<legend"); legends != 7 {
		t.Errorf("fieldset legends = %d, want 7", legends)
	}

	for _, section := range []string{
		"Reference Image", "Optimization Parameters", "CMA-ES", "Active-set Polishing",
		"Convergence Settings", "Early Stopping (Optimizer)", "Advanced Metrics",
	} {
		if !strings.Contains(body, section) {
			t.Errorf("section %q is missing from the form", section)
		}
	}
}

// TestCreateJobPageAnnouncesValidationErrors covers the submit-and-fail path:
// the server re-renders the whole page, so without a live region the rejection
// is a silent change of content.
func TestCreateJobPageAnnouncesValidationErrors(t *testing.T) {
	body := renderCreatePage(t, "circles must be at least 1", "")
	if !strings.Contains(body, `role="alert"`) {
		t.Error("validation error is not announced as an alert")
	}

	if !strings.Contains(body, "circles must be at least 1") {
		t.Error("validation error text is missing")
	}

	if clean := renderCreatePage(t, "", ""); strings.Contains(clean, `role="alert"`) {
		t.Error("a form with no error still renders an alert region")
	}
}

// TestCreateJobPageHidesDecorativeEmoji keeps the tips heading's accessible name
// to the word it is about.
func TestCreateJobPageHidesDecorativeEmoji(t *testing.T) {
	body := renderCreatePage(t, "", "")
	if !strings.Contains(body, `<span aria-hidden="true">💡</span>`) {
		t.Error("the tips heading emoji is part of the heading's accessible name")
	}
}

// TestCreateJobPageKeepsTheProjectSlug is the behavior the hidden field exists
// for, re-asserted here because the section markup around it changed.
func TestCreateJobPageKeepsTheProjectSlug(t *testing.T) {
	body := renderCreatePage(t, "", "experiments")
	if !strings.Contains(body, `<input type="hidden" name="project" value="experiments">`) {
		t.Error("the project slug is not carried through the form")
	}
}
