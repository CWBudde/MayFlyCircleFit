//nolint:testpackage // reaches server.jobManager, server.cancel and the guard
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

// recordedVersion is the version the fixture's checkpoint claims produced it.
// It differs from runningVersion, which is what the server pretends to link, so
// every continuation below crosses a library boundary unless it is allowed to.
const recordedVersion = "v0.0.1"

// continuationSourceJob is a completed two-circle batch job with a checkpoint on
// disk, whose recorded optimizer versions the caller chooses. The server it
// returns reports runningVersion for every engine, so a checkpoint recording
// anything else is a mismatch the guard has to see.
//
// It mirrors extendableSourceJob rather than reusing it because the point here
// is the recorded version, which that fixture does not let a caller set.
func continuationSourceJob(t *testing.T, base, polishing string, config JobConfig) (*Server, string) {
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
	server.optimizerVersionOverride = runningVersion

	config.RefPath = imgPath
	config.Mode = app.ModeBatch
	config.Circles = 2
	config.BatchSize = 2
	config.Iters = 2
	config.OptimizerEpochs = 1
	config.PopSize = 20
	config.Threads = 1
	config.Seed = 42

	normalized, err := app.Normalize(config)
	if err != nil {
		t.Fatal(err)
	}

	source := server.jobManager.CreateJob(app.DefaultProject, normalized)
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

	checkpoint := store.NewCheckpoint(source.ID, params, 600, 1000, 8000, normalized)
	checkpoint.Evaluations = 900000
	// Set after construction: a test binary carries no module information, so
	// the versions NewCheckpoint derives cannot disagree with anything.
	checkpoint.OptimizerVersion = base
	checkpoint.PolishingOptimizerVersion = polishing

	err = fsStore.SaveCheckpoint(source.ID, checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	server.cancel()

	return server, source.ID
}

// TestContinuationRefusesAnOptimizerVersionMismatch is the hole the guard had.
// An extend freezes the prefix it inherits and a polish rewrites part of it, so
// both continue a cost some other library produced; neither consulted the guard
// that an explicit resume of the very same checkpoint runs, which let a server
// upgraded between stages carry a chain across a behaviour-changing boundary
// and record the new version as though the run were comparable.
func TestContinuationRefusesAnOptimizerVersionMismatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		path string
		body string
	}{
		{path: "/extend", body: `{"additionalCircles":2}`},
		{path: "/polish", body: `{}`},
	}

	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			t.Parallel()

			server, sourceID := continuationSourceJob(t, recordedVersion, "", JobConfig{})

			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
				"/api/v1/jobs/"+sourceID+testCase.path, strings.NewReader(testCase.body))
			request.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(response, request)

			if response.Code != http.StatusConflict {
				t.Fatalf("%s status = %d, want %d; body=%s",
					testCase.path, response.Code, http.StatusConflict, response.Body.String())
			}

			if !strings.Contains(response.Body.String(), recordedVersion) {
				t.Errorf("refusal %q does not name the recorded version", response.Body.String())
			}
		})
	}
}

// TestContinuationAllowsAnOverriddenMismatch keeps the refusal from being a dead
// end. The override is the same query parameter the resume endpoint already
// answers to, so an operator who has decided the boundary does not matter says
// so the same way for every continuation.
func TestContinuationAllowsAnOverriddenMismatch(t *testing.T) {
	t.Parallel()

	server, sourceID := continuationSourceJob(t, recordedVersion, "", JobConfig{})

	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/jobs/"+sourceID+"/extend?allowOptimizerMismatch=true",
		strings.NewReader(`{"additionalCircles":2}`))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("extend status = %d, want %d; body=%s",
			response.Code, http.StatusCreated, response.Body.String())
	}
}

// TestExtendCarriesTheInheritedPolishingVersion pins the other half of the
// provenance. The extend path turns polishing off while retaining the polished
// prefix, so the continuation's own configuration no longer names the sweep's
// library -- but that library's parameters are still in the cost, and the
// checkpoint has to keep saying so or a later resume checks only the base
// engine.
func TestExtendCarriesTheInheritedPolishingVersion(t *testing.T) {
	t.Parallel()

	server, sourceID := continuationSourceJob(t, runningVersion, runningVersion, JobConfig{
		Optimizer:          app.OptimizerCMAES,
		PolishingEnabled:   true,
		PolishingOptimizer: app.OptimizerMayfly,
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/v1/jobs/"+sourceID+"/extend", strings.NewReader(`{"additionalCircles":2}`))
	request.Header.Set("Content-Type", "application/json")
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("extend status = %d, want %d; body=%s",
			response.Code, http.StatusCreated, response.Body.String())
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

	if continuation.Config.PolishingEnabled {
		t.Fatal("the extend fixture no longer turns polishing off, so it tests nothing")
	}

	if continuation.InheritedPolishingVersion != runningVersion {
		t.Errorf("InheritedPolishingVersion = %q, want %q",
			continuation.InheritedPolishingVersion, runningVersion)
	}

	// The durable half: the job field is only useful because applyJobLineage
	// writes it onto every checkpoint the continuation goes on to produce.
	checkpoint := store.NewCheckpoint(continuation.ID, continuation.BestParams,
		continuation.BestCost, continuation.InitialCost, continuation.Iterations, continuation.Config)
	applyJobLineage(checkpoint, continuation)

	if checkpoint.PolishingOptimizerVersion != runningVersion {
		t.Errorf("checkpoint PolishingOptimizerVersion = %q, want %q",
			checkpoint.PolishingOptimizerVersion, runningVersion)
	}
}

// TestApplyJobLineageKeepsTheJobsOwnPolishingVersion is the precedence rule. A
// job that polishes records the library it actually ran, and that reading is
// the more specific one: the inherited value only fills a field the
// configuration left empty.
func TestApplyJobLineageKeepsTheJobsOwnPolishingVersion(t *testing.T) {
	t.Parallel()

	checkpoint := &store.Checkpoint{PolishingOptimizerVersion: runningVersion}
	applyJobLineage(checkpoint, &Job{InheritedPolishingVersion: recordedVersion})

	if checkpoint.PolishingOptimizerVersion != runningVersion {
		t.Errorf("PolishingOptimizerVersion = %q, want the job's own %q",
			checkpoint.PolishingOptimizerVersion, runningVersion)
	}
}

// TestGuardCheckpointVersionsReadsAnInheritedPolishingVersion closes the loop.
// Once an extend records the sweep's library without enabling polishing,
// SecondaryOptimizer stops answering for that checkpoint -- so a guard that
// asked only the configuration would skip exactly the field the extend went to
// the trouble of carrying.
func TestGuardCheckpointVersionsReadsAnInheritedPolishingVersion(t *testing.T) {
	t.Parallel()

	server := &Server{optimizerVersionOverride: runningVersion}
	checkpoint := &store.Checkpoint{
		OptimizerVersion:          runningVersion,
		PolishingOptimizerVersion: recordedVersion,
		Config: store.JobConfig{
			Optimizer:          app.OptimizerCMAES,
			PolishingOptimizer: app.OptimizerMayfly,
			PolishingEnabled:   false,
		},
	}

	_, err := server.guardCheckpointVersions(checkpoint, false)
	if err == nil {
		t.Fatal("guardCheckpointVersions() accepted an inherited polishing mismatch")
	}

	if !strings.Contains(err.Error(), recordedVersion) {
		t.Errorf("error %q does not name the recorded polishing version", err)
	}
}

// TestGuardCheckpointVersionsIgnoresASameEngineInheritedVersion keeps the new
// fallback from inventing a second library. Where the sweep ran the base
// engine, OptimizerVersion already speaks for it and checking the same library
// twice would only invite the two readings to disagree.
func TestGuardCheckpointVersionsIgnoresASameEngineInheritedVersion(t *testing.T) {
	t.Parallel()

	server := &Server{optimizerVersionOverride: runningVersion}
	checkpoint := &store.Checkpoint{
		OptimizerVersion:          runningVersion,
		PolishingOptimizerVersion: recordedVersion,
		Config: store.JobConfig{
			Optimizer:          app.OptimizerMayfly,
			PolishingOptimizer: app.OptimizerMayfly,
			PolishingEnabled:   false,
		},
	}

	warnings, err := server.guardCheckpointVersions(checkpoint, false)
	if err != nil {
		t.Fatalf("guardCheckpointVersions() error = %v", err)
	}

	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}
