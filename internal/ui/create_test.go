package ui_test

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/ui"
)

// testCreateLimits stands in for what the server projects from internal/app.
// The values are deliberately not the production ones: this package must not
// grow its own copy of them, and a test that asserted the real numbers here
// would be exactly that copy. What the tests below check is that the page
// writes whatever it is handed. internal/server owns the check that the numbers
// handed over are app's; see TestCreateJobLimitsComeFromTheServerBounds.
func testCreateLimits() ui.CreateJobLimits {
	return ui.CreateJobLimits{
		MaxCircles:                 111,
		MaxIterations:              222,
		MinPopulation:              33,
		MaxPopulation:              444,
		MaxOptimizerEpochs:         55,
		MaxBatchSize:               66,
		MaxPolishingSweeps:         77,
		MaxConvergencePatience:     88,
		MinConvergenceThreshold:    0.0002,
		MaxConvergenceThreshold:    0.2,
		MinPolishingMinImprovement: 1e-9,
		DefaultInitialSigma:        0.3,
		MaxOptimizerRestarts:       44,
		// Deliberately not the real 512 and 7: the page must state whatever it
		// is handed, and 99/9 makes the quotient it derives checkable at 11.
		MaxCMAESFullDimensions: 99,
		ParametersPerCircle:    9,
		DefaultBatchSize:       4,
	}
}

func renderCreatePage(t *testing.T, errorMsg, project string) string {
	t.Helper()

	var output bytes.Buffer

	err := ui.CreateJobPage(ui.CreateJobPageData{
		ErrorMessage: errorMsg,
		Project:      project,
		Limits:       testCreateLimits(),
	}).Render(context.Background(), &output)
	if err != nil {
		t.Fatalf("render create page: %v", err)
	}

	return output.String()
}

// TestCreateJobPageMountsTheIslandOverAWorkingForm is the fallback contract:
// mounting replaces the mount point's contents, so the complete form that posts
// to /create has to be inside it, and the seed the island reads has to be there
// too.
func TestCreateJobPageMountsTheIslandOverAWorkingForm(t *testing.T) {
	t.Parallel()

	body := renderCreatePage(t, "", "")

	island := strings.Index(body, `data-island="create-job"`)
	if island < 0 {
		t.Fatal("the create page renders no create-job mount point, so the island can never mount")
	}

	seed := strings.Index(body, `id="create-job-page"`)
	if seed < island {
		t.Error("the create-job-page seed is not inside the mount point, where the island reads it")
	}

	form := strings.Index(body, `<form method="POST" action="/create"`)
	if form < island {
		t.Error("the form that posts to /create is outside the mount point, so mounting would leave two forms on the page")
	}

	if closing := strings.Index(body[form:], "</form>"); closing < 0 {
		t.Error("the fallback form is not closed")
	}
}

// TestCreateJobPageTakesItsBoundsFromTheLimits pins that no bound is written
// into the markup as a literal. Every one of these numbers is unique to
// testCreateLimits, so a hard-coded attribute cannot satisfy the check by
// coincidence.
func TestCreateJobPageTakesItsBoundsFromTheLimits(t *testing.T) {
	t.Parallel()

	body := renderCreatePage(t, "", "")

	for _, want := range []string{
		`max="111"`, // circles
		`max="222"`, // iterations, polishing iterations, polishing stagnation
		`min="33"`,  // population, polishing population
		`max="444"`, // population, polishing population
		`max="55"`,  // optimizer epochs, polishing epochs
		`max="66"`,  // batch size, polishing active set
		`max="77"`,  // polishing sweeps
		`max="88"`,  // convergence patience
		`min="0.0002"`,
		`max="0.2"`,
		`min="0.000000001"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the create page does not render %s from its limits", want)
		}
	}
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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	body := renderCreatePage(t, "", "")
	if !strings.Contains(body, `<span aria-hidden="true">💡</span>`) {
		t.Error("the tips heading emoji is part of the heading's accessible name")
	}
}

// TestCreateJobPageKeepsTheProjectSlug is the behavior the hidden field exists
// for, re-asserted here because the section markup around it changed.
func TestCreateJobPageKeepsTheProjectSlug(t *testing.T) {
	t.Parallel()

	body := renderCreatePage(t, "", "experiments")
	if !strings.Contains(body, `<input type="hidden" name="project" value="experiments">`) {
		t.Error("the project slug is not carried through the form")
	}
}
