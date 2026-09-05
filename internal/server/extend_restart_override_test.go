// in-package fixtures createSimpleTestImage and shutdownTestServer, the way
// every other test in this package does.
//
//nolint:testpackage // reaches server.jobManager, server.cancel and the
package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/store"
)

// A schedule step is documented to mirror this endpoint's request body field
// for field -- docs/schedule-format.md calls a step "a request you have already
// issued by hand", and docs/behavior-invariants.md makes the equivalence a
// tested invariant. steps[].restarts was added to the schedule format without
// its counterpart here, which left the documented hand-driven equivalent of a
// restart-varying campaign impossible to issue: DisallowUnknownFields rejects
// the key outright, and an extension otherwise inherits the parent's count with
// no way to vary it.
//
// These tests pin the counterpart in both shapes. The sign is the shape, so a
// test that only covered a positive count would leave the filling form -- the
// one a campaign actually wants per stage -- unguarded.
func TestExtendEndpointAppliesTheRestartOverride(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		body     string
		want     int
		wantCode int
	}{
		{
			name: "a fixed count is applied",
			body: `{"additionalCircles":2,"restarts":4}`,
			want: 4, wantCode: http.StatusCreated,
		},
		{
			// The negative shape is a cap of abs(N) times the stage's epoch
			// chain, filled with as many whole attempts as fit. It has to
			// survive the endpoint with its sign intact: dropping the sign
			// would silently turn a filling schedule into a fixed count.
			name: "a filling cap keeps its sign",
			body: `{"additionalCircles":2,"restarts":-32}`,
			want: -32, wantCode: http.StatusCreated,
		},
		{
			// Magnitude bounds are app.Normalize's job, exactly as they are for
			// the schedule's staged.Validate. The endpoint adds no second rule.
			name: "a count past the limit is refused",
			body: `{"additionalCircles":2,"restarts":100000}`,
			want: 0, wantCode: http.StatusBadRequest,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			server, sourceID := extendableSourceJob(t)

			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/api/v1/jobs/"+sourceID+"/extend", strings.NewReader(testCase.body))
			request.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(response, request)

			if response.Code != testCase.wantCode {
				t.Fatalf("extend status = %d, want %d; body=%s",
					response.Code, testCase.wantCode, response.Body.String())
			}

			if testCase.wantCode != http.StatusCreated {
				return
			}

			var payload struct {
				JobID string `json:"jobId"`
			}

			err := json.NewDecoder(response.Body).Decode(&payload)
			if err != nil {
				t.Fatal(err)
			}

			continuation, ok := server.jobManager.GetJob(payload.JobID)
			if !ok {
				t.Fatal("continuation job not found")
			}

			if continuation.Config.OptimizerRestarts != testCase.want {
				t.Errorf("continuation OptimizerRestarts = %d, want %d",
					continuation.Config.OptimizerRestarts, testCase.want)
			}
		})
	}
}

// TestExtendEndpointInheritsTheRestartCountWhenUnset is the other half of the
// override contract: an omitted field must leave the parent's count alone
// rather than renormalizing it to one. The schedule format makes the same
// promise for a step that does not name restarts.
func TestExtendEndpointInheritsTheRestartCountWhenUnset(t *testing.T) {
	t.Parallel()

	server, sourceID := extendableSourceJob(t)

	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/jobs/"+sourceID+"/extend", strings.NewReader(`{"additionalCircles":2}`))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("extend status = %d body=%s", response.Code, response.Body.String())
	}

	var payload struct {
		JobID string `json:"jobId"`
	}

	err := json.NewDecoder(response.Body).Decode(&payload)
	if err != nil {
		t.Fatal(err)
	}

	continuation, ok := server.jobManager.GetJob(payload.JobID)
	if !ok {
		t.Fatal("continuation job not found")
	}

	if continuation.Config.OptimizerRestarts != extendSourceRestarts {
		t.Errorf("continuation OptimizerRestarts = %d, want the parent's %d",
			continuation.Config.OptimizerRestarts, extendSourceRestarts)
	}
}

// extendSourceRestarts is the parent's count, deliberately not 1 so an
// inheritance test can tell "inherited" from "reset to the default".
const extendSourceRestarts = 3

// extendableSourceJob builds a completed, checkpointed job the extend endpoint
// will accept as a continuation source, and stops the worker so the created
// continuation stays pending and its configuration is stable to inspect.
func extendableSourceJob(t *testing.T) (*Server, string) {
	t.Helper()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "ref.png")
	createSimpleTestImage(t, imgPath)

	fsStore, err := store.NewFSStore(filepath.Join(tmpDir, "data"))
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions(":0", fsStore, ServerOptions{InputRoots: []string{tmpDir}})
	shutdownTestServer(t, server)

	config, err := app.Normalize(JobConfig{
		RefPath: imgPath, Mode: app.ModeBatch, Circles: 2, BatchSize: 2, Iters: 2,
		OptimizerEpochs: 1, PopSize: 20, Threads: 1, Seed: 42,
		OptimizerRestarts: extendSourceRestarts,
	})
	if err != nil {
		t.Fatal(err)
	}

	source := server.jobManager.CreateJob(app.DefaultProject, config)
	params := []float64{
		1, 1, 1, 1, 0, 0, 1,
		2, 2, 1, 0, 1, 0, 1,
	}

	err = server.jobManager.StartJob(source.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.CompleteJob(source.ID, 8000, 900000, params, 600, 1000, "completed")
	if err != nil {
		t.Fatal(err)
	}

	checkpoint := store.NewCheckpoint(source.ID, params, 600, 1000, 8000, config)
	checkpoint.Evaluations = 900000

	err = fsStore.SaveCheckpoint(source.ID, checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	server.cancel()

	return server, source.ID
}
