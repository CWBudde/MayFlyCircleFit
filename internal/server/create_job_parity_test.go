//nolint:testpackage // drives the unexported job creation handlers on both admission paths
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
)

// The Go half of the create-page parity check.
//
// The job creation page keeps two admission paths, and they do not read a blank
// field the same way. The templ form posts strings to /create, where an empty
// one is resolved against the defaults before a JobConfig exists; the island
// posts JSON to /api/v1/jobs, where the raw body decides which keys the caller
// wrote and a value ApplyDefaults would replace is refused outright. This test
// is what keeps the difference from reaching the stored configuration: it
// submits each contract case to both real handlers and compares the JobConfig
// that came out.
//
// The contract itself is web/src/create-job-parity.json, which also drives
// web/src/createJobBody.test.ts. Neither implementation is checked against the
// other -- no test process can reach both languages -- so both are checked
// against the file, exactly as the state-badge parity pair does.

// createJobParityPath is the shared fixture, reached from the package
// directory. It lives beside the TypeScript because that is the side that
// imports it directly.
const createJobParityPath = "../../web/src/create-job-parity.json"

type createJobContract struct {
	Note                 string          `json:"note"`
	ReferencePlaceholder string          `json:"referencePlaceholder"`
	Cases                []createJobCase `json:"cases"`
}

// createJobCase is one submission in both of its shapes. Form is what the
// browser would send to /create, with an unchecked checkbox absent and an
// emptied number an empty string; Body is the JSON the island builds from it.
// RandomSeed marks a case whose seed is zero, where the two runs necessarily
// resolve different effective seeds.
type createJobCase struct {
	Name       string                     `json:"name"`
	Note       string                     `json:"note"`
	Form       map[string]string          `json:"form"`
	Body       map[string]json.RawMessage `json:"body"`
	RandomSeed bool                       `json:"randomSeed"`
}

func loadCreateJobContract(t *testing.T) createJobContract {
	t.Helper()

	raw, err := os.ReadFile(filepath.Clean(createJobParityPath))
	if err != nil {
		t.Fatalf("read the shared create-job contract: %v", err)
	}

	var contract createJobContract
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatalf("decode the shared create-job contract: %v", err)
	}

	if len(contract.Cases) == 0 {
		t.Fatal("the create-job contract names no cases")
	}

	return contract
}

// submitCreateJobAPI posts a body to the JSON admission path and returns the
// created job's identifier.
func submitCreateJobAPI(t *testing.T, server *Server, body []byte) string {
	t.Helper()

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/jobs", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")

	recorder := httptest.NewRecorder()
	server.handleCreateJob(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/jobs = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}

	var created struct {
		ID string `json:"id"`
	}

	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode the created job: %v", err)
	}

	return created.ID
}

func storedConfig(t *testing.T, server *Server, jobID string) JobConfig {
	t.Helper()

	job, ok := server.jobManager.GetJob(jobID)
	if !ok {
		t.Fatalf("job %q not found", jobID)
	}

	return job.Config
}

// TestCreateJobIslandAndFormStoreTheSameConfiguration is the acceptance check
// for the create-page island: for every case in the shared contract, the form
// the fallback posts and the body the island posts have to produce one stored
// configuration -- for the fields left blank, for the fields set explicitly to
// zero, and for the CMA-ES section the fallback always submits and neither path
// may pass to another engine.
func TestCreateJobIslandAndFormStoreTheSameConfiguration(t *testing.T) {
	t.Parallel()

	contract := loadCreateJobContract(t)

	for _, testCase := range contract.Cases {
		t.Run(testCase.Name, func(t *testing.T) {
			t.Parallel()

			server, reference := newCreateFormServer(t)

			form := url.Values{}

			for name, value := range testCase.Form {
				form.Set(name, strings.ReplaceAll(value, contract.ReferencePlaceholder, reference))
			}

			formConfig := createdFormJobConfig(t, server, submitCreateForm(t, server, form))

			body := make(map[string]json.RawMessage, len(testCase.Body))
			for name, value := range testCase.Body {
				body[name] = json.RawMessage(
					strings.ReplaceAll(string(value), contract.ReferencePlaceholder, reference))
			}

			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("encode the island body: %v", err)
			}

			apiConfig := storedConfig(t, server, submitCreateJobAPI(t, server, encoded))

			// A zero seed asks each run to draw its own, so the two effective
			// seeds differ by design. Everything else, the requested seed
			// included, still has to match.
			if testCase.RandomSeed {
				if formConfig.EffectiveSeed == 0 || apiConfig.EffectiveSeed == 0 {
					t.Error("a zero seed must be resolved to a nonzero effective seed on both paths")
				}

				formConfig.EffectiveSeed, apiConfig.EffectiveSeed = 0, 0
			}

			if !reflect.DeepEqual(formConfig, apiConfig) {
				t.Errorf("the form and the island stored different configurations:\nform: %+v\nisland: %+v",
					formConfig, apiConfig)
			}
		})
	}
}

// TestCreateJobContractCoversTheHazard pins that the contract keeps exercising
// what the task exists for. A parity suite whose cases all set every field
// would pass while proving nothing: the interesting inputs are a field left
// blank, a field the island must omit because the defaults would replace its
// zero, and a field whose explicit zero has to travel as a zero.
func TestCreateJobContractCoversTheHazard(t *testing.T) {
	t.Parallel()

	contract := loadCreateJobContract(t)

	var blank, omittedZero, explicitZero bool

	for _, testCase := range contract.Cases {
		for name, value := range testCase.Form {
			if value != "" {
				continue
			}

			blank = true

			if _, written := testCase.Body[name]; written {
				t.Errorf("%s: the island sends %q although the form left it blank", testCase.Name, name)
			}
		}

		if testCase.Form["batchSize"] == "0" {
			if _, written := testCase.Body["batchSize"]; written {
				t.Errorf("%s: batchSize 0 means the automatic default and must not be written", testCase.Name)
			}

			omittedZero = true
		}

		for _, name := range []string{"seed", "stopMinIters", "stopStagnationIters"} {
			if testCase.Form[name] != "0" {
				continue
			}

			written, ok := testCase.Body[name]
			if !ok || string(written) != "0" {
				t.Errorf("%s: %s is an explicit zero the defaults leave alone and must be written as 0", testCase.Name, name)
				continue
			}

			explicitZero = true
		}
	}

	if !blank {
		t.Error("no contract case leaves a field blank")
	}

	if !omittedZero {
		t.Error("no contract case exercises a zero the island has to omit")
	}

	if !explicitZero {
		t.Error("no contract case exercises a zero the island has to send")
	}
}

// TestCreateJobLimitsComeFromTheServerBounds is the other half of "one set of
// limits": the page's min/max attributes and the island's are written from
// ui.CreateJobLimits, so this is where those numbers are pinned to internal/app
// rather than typed a second time.
func TestCreateJobLimitsComeFromTheServerBounds(t *testing.T) {
	t.Parallel()

	want := ui.CreateJobLimits{
		MaxCircles:                 app.MaxCircles,
		MaxIterations:              app.MaxIterations,
		MinPopulation:              app.MinPopulation,
		MaxPopulation:              app.MaxPopulation,
		MaxOptimizerEpochs:         app.MaxOptimizerEpochs,
		MaxBatchSize:               app.MaxBatchSize,
		MaxPolishingSweeps:         app.MaxPolishingSweeps,
		MaxConvergencePatience:     maxConvergencePatience,
		MinConvergenceThreshold:    minConvergenceThreshold,
		MaxConvergenceThreshold:    maxConvergenceThreshold,
		MinPolishingMinImprovement: minPolishingMinImprovement,
		DefaultInitialSigma:        app.DefaultCMAESInitialSigma,
	}

	if got := createJobLimits(); got != want {
		t.Errorf("createJobLimits() = %+v, want %+v", got, want)
	}

	// The form handler and the browser have to refuse the same convergence
	// values, which is the one bound the form states more strictly than
	// app.Validate does.
	if minConvergenceThreshold <= 0 || maxConvergenceThreshold > 1 {
		t.Errorf("the convergence threshold bounds [%g, %g] are outside what app.Validate accepts",
			minConvergenceThreshold, maxConvergenceThreshold)
	}
}

// TestCreateJobPageSeedsTheIslandWithItsLimits checks the delivery: the limits
// only reach the island if the rendered page carries them inside the mount
// point.
func TestCreateJobPageSeedsTheIslandWithItsLimits(t *testing.T) {
	t.Parallel()

	server := NewServer(":0", nil)

	recorder := httptest.NewRecorder()
	server.handleCreatePage(recorder,
		httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/create", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	body := recorder.Body.String()

	seeded, err := json.Marshal(ui.CreateJobPageData{Limits: createJobLimits()})
	if err != nil {
		t.Fatalf("marshal the page seed: %v", err)
	}

	if !strings.Contains(body, string(seeded)) {
		t.Errorf("the create page does not seed the island with %s", seeded)
	}
}
