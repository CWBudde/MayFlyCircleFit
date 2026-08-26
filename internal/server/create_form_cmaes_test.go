//nolint:testpackage // drives the unexported job creation form handler
package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

// Form field names the CMA-ES section of the creation form submits.
const (
	fieldInitialSigma    = "initialSigma"
	fieldCovarianceMode  = "covarianceMode"
	fieldActiveCMA       = "activeCMA"
	fieldRestartStrategy = "restartStrategy"
)

// cmaesCreateForm is a valid CMA-ES submission of the job creation form. The
// caller overrides the fields under test. Every input the form renders is
// always submitted, because the page carries no JavaScript that could hide the
// CMA-ES section for another engine.
func cmaesCreateForm(reference string) url.Values {
	return url.Values{
		fieldRefPath:         {reference},
		fieldMode:            {modeJoint},
		fieldCircles:         {"5"},
		fieldIters:           {"2"},
		fieldPopSize:         {"20"},
		fieldSeed:            {"1"},
		fieldOptimizer:       {optimizerCMAES},
		fieldInitialSigma:    {"0.25"},
		fieldCovarianceMode:  {string(app.CMAESCovarianceFull)},
		fieldActiveCMA:       {"on"},
		fieldRestartStrategy: {string(app.CMAESRestartNone)},
	}
}

// submitCreateForm posts the creation form the way the browser does.
func submitCreateForm(t *testing.T, server *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest(http.MethodPost, "/create", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	recorder := httptest.NewRecorder()
	server.handleCreatePagePost(recorder, request)

	return recorder
}

// createdFormJobConfig resolves the job a successful submission redirected to.
func createdFormJobConfig(t *testing.T, server *Server, recorder *httptest.ResponseRecorder) JobConfig {
	t.Helper()

	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303: %s", recorder.Code, recorder.Body.String())
	}

	jobID := strings.TrimPrefix(recorder.Header().Get("Location"), "/jobs/")

	job, ok := server.jobManager.GetJob(jobID)
	if !ok {
		t.Fatalf("job %q not found", jobID)
	}

	return job.Config
}

// newCreateFormServer builds a server the creation form can submit to, with the
// reference image it will name.
func newCreateFormServer(t *testing.T) (*Server, string) {
	t.Helper()

	inputDir := t.TempDir()
	reference := filepath.Join(inputDir, "ref.png")
	createSimpleTestImage(t, reference)

	server := NewServerWithOptions(":0", nil, ServerOptions{
		InputRoots: []string{inputDir},
		DataRoot:   t.TempDir(),
	})
	shutdownTestServer(t, server)

	return server, reference
}

// TestCreatePageExposesCMAESSettings pins that the form actually renders the
// four CMA-ES inputs, with the default step size and the full-covariance
// dimension limit the templ file spells out as literals.
func TestCreatePageExposesCMAESSettings(t *testing.T) {
	t.Parallel()

	server := NewServer(":0", nil)

	recorder := httptest.NewRecorder()
	server.handleCreatePage(recorder, httptest.NewRequest(http.MethodGet, "/create", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	body := recorder.Body.String()
	for _, want := range []string{
		`name="` + fieldInitialSigma + `"`,
		`name="` + fieldCovarianceMode + `"`,
		`name="` + fieldActiveCMA + `"`,
		`name="` + fieldRestartStrategy + `"`,
		`value="` + string(app.CMAESCovarianceSeparable) + `"`,
		`value="` + string(app.CMAESCovarianceBlock) + `"`,
		`value="` + string(app.CMAESRestartIPOP) + `"`,
		`value="` + string(app.CMAESRestartBIPOP) + `"`,
		// The default and the limit are literals in create.templ; these two
		// assertions are what keeps them from drifting from app.
		fmt.Sprintf(`value="%g"`, app.DefaultCMAESInitialSigma),
		strconv.Itoa(app.MaxCMAESFullDimensions),
	} {
		if !containsString(body, want) {
			t.Errorf("create page is missing %q", want)
		}
	}
}

// TestCreateFormRoundTripsCMAESSettings drives every covariance mode against
// every restart strategy through the form the dashboard actually posts, and
// reads the result back through the same accessors the job detail page uses.
func TestCreateFormRoundTripsCMAESSettings(t *testing.T) {
	t.Parallel()

	server, reference := newCreateFormServer(t)

	modes := []app.CMAESCovarianceMode{
		app.CMAESCovarianceFull,
		app.CMAESCovarianceSeparable,
		app.CMAESCovarianceBlock,
	}
	strategies := []app.CMAESRestartStrategy{
		app.CMAESRestartNone,
		app.CMAESRestartIPOP,
		app.CMAESRestartBIPOP,
	}

	for _, mode := range modes {
		for _, strategy := range strategies {
			t.Run(string(mode)+"-"+string(strategy), func(t *testing.T) {
				form := cmaesCreateForm(reference)
				form.Set(fieldCovarianceMode, string(mode))
				form.Set(fieldRestartStrategy, string(strategy))

				config := createdFormJobConfig(t, server, submitCreateForm(t, server, form))
				if config.ResolvedOptimizer() != app.OptimizerCMAES {
					t.Errorf("optimizer = %q, want cmaes", config.ResolvedOptimizer())
				}

				if config.ResolvedCMAESInitialSigma() != 0.25 {
					t.Errorf("initial sigma = %v, want 0.25", config.ResolvedCMAESInitialSigma())
				}

				if config.ResolvedCMAESCovarianceMode() != mode {
					t.Errorf("covariance mode = %q, want %q", config.ResolvedCMAESCovarianceMode(), mode)
				}

				if !config.ResolvedCMAESActive() {
					t.Error("active adaptation = false, want true for a checked box")
				}

				if config.ResolvedCMAESRestartStrategy() != strategy {
					t.Errorf("restart strategy = %q, want %q", config.ResolvedCMAESRestartStrategy(), strategy)
				}
			})
		}
	}

	// An unchecked checkbox is absent from the submission entirely, which is
	// the only way the form can ask for adaptation to be off.
	t.Run("unchecked active adaptation", func(t *testing.T) {
		form := cmaesCreateForm(reference)
		form.Del(fieldActiveCMA)

		config := createdFormJobConfig(t, server, submitCreateForm(t, server, form))
		if config.ResolvedCMAESActive() {
			t.Error("active adaptation = true, want false for an unchecked box")
		}
	})

	// An emptied number input means "leave the default", not zero, which
	// app.Normalize would refuse as a non-positive step size.
	t.Run("empty initial sigma keeps the default", func(t *testing.T) {
		form := cmaesCreateForm(reference)
		form.Set(fieldInitialSigma, "")

		config := createdFormJobConfig(t, server, submitCreateForm(t, server, form))
		if config.ResolvedCMAESInitialSigma() != app.DefaultCMAESInitialSigma {
			t.Errorf("initial sigma = %v, want %v",
				config.ResolvedCMAESInitialSigma(), app.DefaultCMAESInitialSigma)
		}
	})
}

// TestCreateFormDropsCMAESSettingsForOtherEngines pins the reason the handler
// reads the CMA-ES section conditionally. The form has no JavaScript, so it
// submits those inputs for every engine, and app.Normalize refuses a
// CMA-ES-only field on a job that does not run CMA-ES rather than ignoring it.
// Carrying them through would make the form unable to create a MayFly job.
func TestCreateFormDropsCMAESSettingsForOtherEngines(t *testing.T) {
	t.Parallel()

	server, reference := newCreateFormServer(t)

	tests := []struct {
		name      string
		optimizer string
	}{
		{name: "mayfly", optimizer: string(app.OptimizerMayfly)},
		{name: "absent", optimizer: ""},
		{name: "dragonfly", optimizer: string(app.OptimizerDragonfly)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			form := cmaesCreateForm(reference)
			form.Set(fieldOptimizer, test.optimizer)
			// Values a user could leave behind after switching the engine away
			// from CMA-ES.
			form.Set(fieldInitialSigma, "0.9")
			form.Set(fieldCovarianceMode, string(app.CMAESCovarianceBlock))
			form.Set(fieldRestartStrategy, string(app.CMAESRestartBIPOP))

			config := createdFormJobConfig(t, server, submitCreateForm(t, server, form))
			if config.InitialSigma != nil {
				t.Errorf("InitialSigma = %v, want unset", *config.InitialSigma)
			}

			if config.ActiveCMA != nil {
				t.Errorf("ActiveCMA = %v, want unset", *config.ActiveCMA)
			}

			if config.CovarianceMode != "" {
				t.Errorf("CovarianceMode = %q, want unset", config.CovarianceMode)
			}

			if config.RestartStrategy != "" {
				t.Errorf("RestartStrategy = %q, want unset", config.RestartStrategy)
			}
		})
	}
}

// TestCreateFormRejectsInvalidCMAESSettings pins that a bad CMA-ES value comes
// back as the re-rendered form naming the field, rather than being silently
// defaulted or creating a job that cannot run.
func TestCreateFormRejectsInvalidCMAESSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		field   string
		value   string
		message string
	}{
		{name: "zero sigma", field: fieldInitialSigma, value: "0", message: fieldInitialSigma},
		{name: "negative sigma", field: fieldInitialSigma, value: "-0.5", message: fieldInitialSigma},
		{
			name: "non-numeric sigma", field: fieldInitialSigma, value: "wide",
			message: fieldInitialSigma + " must be a number",
		},
		{
			name: "unknown covariance mode", field: fieldCovarianceMode, value: "diagonal",
			message: fieldCovarianceMode,
		},
		{
			name: "unknown restart strategy", field: fieldRestartStrategy, value: "nipop",
			message: fieldRestartStrategy,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			server, reference := newCreateFormServer(t)

			form := cmaesCreateForm(reference)
			form.Set(test.field, test.value)

			recorder := submitCreateForm(t, server, form)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want the form re-rendered with 200", recorder.Code)
			}

			if !containsString(recorder.Body.String(), test.message) {
				t.Fatalf("expected %q in the rendered error page, got:\n%s", test.message, recorder.Body.String())
			}

			if jobs := server.jobManager.ListJobs(); len(jobs) != 0 {
				t.Fatalf("rejected submission created %d job(s)", len(jobs))
			}
		})
	}
}
