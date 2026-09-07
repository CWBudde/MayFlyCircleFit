//nolint:testpackage // exercises unexported optimizer construction and the unexported HTTP handlers
package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/opt"
	"github.com/cwbudde/circlefit/internal/store"
)

// Field names and values this file writes into request bodies more than once.
const (
	fieldRefPath       = "refPath"
	fieldMode          = "mode"
	fieldCircles       = "circles"
	fieldIters         = "iters"
	fieldPopSize       = "popSize"
	fieldOptimizer     = "optimizer"
	fieldSeed          = "seed"
	modeJoint          = "joint"
	modeBatch          = "batch"
	optimizerCMAES     = "cmaes"
	optimizerDragonfly = "dragonfly"
	caseAbsent         = "absent"
	codeInvalidConfig  = "invalid_config"
)

// TestNewStageOptimizerSelectsTheConfiguredEngine pins the server half of the
// engine decision, including the value a job payload or checkpoint written
// before the optimizer field existed carries.
func TestNewStageOptimizerSelectsTheConfiguredEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		optimizer app.Optimizer
		variant   app.Variant
		dragonfly bool
		cmaes     bool
	}{
		{name: caseAbsent, optimizer: "", variant: app.VariantStandard},
		{name: "mayfly", optimizer: app.OptimizerMayfly, variant: app.VariantAOBLMOA},
		{name: optimizerDragonfly, optimizer: app.OptimizerDragonfly, dragonfly: true},
		{name: optimizerCMAES, optimizer: app.OptimizerCMAES, cmaes: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			optimizer, err := newStageOptimizer(store.JobConfig{
				Optimizer: test.optimizer,
				Variant:   test.variant,
				Iters:     10,
				PopSize:   20,
			}, nil, 7)
			if err != nil {
				t.Fatalf("newStageOptimizer() error = %v", err)
			}

			_, isDragonfly := optimizer.(*opt.DragonflyAdapter)
			if isDragonfly != test.dragonfly {
				t.Fatalf("optimizer = %T, want dragonfly = %v", optimizer, test.dragonfly)
			}

			_, isCMAES := optimizer.(*opt.CMAESAdapter)
			if isCMAES != test.cmaes {
				t.Fatalf("optimizer = %T, want cmaes = %v", optimizer, test.cmaes)
			}

			if !test.dragonfly && !test.cmaes {
				if _, ok := optimizer.(*opt.MayflyAdapter); !ok {
					t.Fatalf("optimizer = %T, want *opt.MayflyAdapter", optimizer)
				}
			}
		})
	}
}

func TestCreateJobAcceptsCMAESConfiguration(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	reference := filepath.Join(tmpDir, "ref.png")
	createSimpleTestImage(t, reference)

	server := NewServerWithOptions("localhost:8080", nil, ServerOptions{InputRoots: []string{tmpDir}})
	body := map[string]any{
		fieldRefPath:      reference,
		fieldMode:         modeJoint,
		fieldCircles:      5,
		fieldIters:        10,
		fieldPopSize:      20,
		fieldOptimizer:    optimizerCMAES,
		"initialSigma":    0.2,
		"covarianceMode":  "block",
		"activeCMA":       false,
		"restartStrategy": "bipop",
	}

	response := postEngineJob(t, server, body)
	if response.Code != http.StatusCreated && response.Code != http.StatusOK {
		t.Fatalf("status = %d, want a created job: %s", response.Code, response.Body.String())
	}

	var created struct {
		Config app.JobConfig `json:"config"`
	}

	err := json.Unmarshal(response.Body.Bytes(), &created)
	if err != nil {
		t.Fatalf("decode created job: %v", err)
	}

	if created.Config.ResolvedOptimizer() != app.OptimizerCMAES ||
		created.Config.ResolvedCMAESInitialSigma() != 0.2 ||
		created.Config.ResolvedCMAESCovarianceMode() != app.CMAESCovarianceBlock ||
		created.Config.ResolvedCMAESActive() ||
		created.Config.ResolvedCMAESRestartStrategy() != app.CMAESRestartBIPOP {
		t.Fatalf("created CMA-ES config = %+v", created.Config)
	}
}

// TestNewStageOptimizerDeclinesParallelEvaluationWithoutSessions pins that a
// Dragonfly job asks the renderer, not its own configuration, how wide it may
// evaluate. A nil renderer serves no independent sessions.
func TestNewStageOptimizerDeclinesParallelEvaluationWithoutSessions(t *testing.T) {
	t.Parallel()

	optimizer, err := newStageOptimizer(store.JobConfig{
		Optimizer:          app.OptimizerDragonfly,
		Iters:              10,
		PopSize:            20,
		ParallelEvaluation: true,
		EvaluationWorkers:  8,
	}, nil, 7)
	if err != nil {
		t.Fatalf("newStageOptimizer() error = %v", err)
	}

	if width := opt.ParallelEvaluationWidth(optimizer); width != 1 {
		t.Errorf("ParallelEvaluationWidth() = %d, want 1", width)
	}
}

func TestCMAESVersionAndNameFollowTheEngine(t *testing.T) {
	t.Parallel()

	server := NewServer("localhost:0", nil)
	if got := server.optimizerVersion(app.OptimizerCMAES); got != opt.CMAESLibraryVersion() {
		t.Errorf("optimizerVersion(cmaes) = %q, want %q", got, opt.CMAESLibraryVersion())
	}

	if got := optimizerLibraryName(app.OptimizerCMAES); got != "CMA-ES" {
		t.Errorf("optimizerLibraryName(cmaes) = %q, want CMA-ES", got)
	}
}

// TestCreateJobAcceptsDragonflyAndRefusesMayflyOnlyFields drives the engine
// selection through the JSON API, which is the surface a campaign is actually
// submitted through. A MayFly-only field must come back as the API error
// envelope naming the field, not be accepted and silently dropped.
func TestCreateJobAcceptsDragonflyAndRefusesMayflyOnlyFields(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	reference := filepath.Join(tmpDir, "ref.png")
	createSimpleTestImage(t, reference)

	server := NewServerWithOptions("localhost:8080", nil, ServerOptions{InputRoots: []string{tmpDir}})

	base := func() map[string]any {
		return map[string]any{
			fieldRefPath: reference, fieldMode: modeJoint, fieldCircles: 5,
			fieldIters: 10, fieldPopSize: 20, "optimizer": "dragonfly",
		}
	}

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()

		body := base()
		// Parallel evaluation is not MayFly-only: the adapter implements it,
		// so a campaign can be configured the same way for both engines.
		body["parallelEvaluation"] = true
		body["evaluationWorkers"] = 2

		response := postEngineJob(t, server, body)
		if response.Code != http.StatusCreated && response.Code != http.StatusOK {
			t.Fatalf("status = %d, want a created job: %s", response.Code, response.Body.String())
		}

		var created struct {
			Config app.JobConfig `json:"config"`
		}

		err := json.Unmarshal(response.Body.Bytes(), &created)
		if err != nil {
			t.Fatalf("decode created job: %v", err)
		}

		if created.Config.Optimizer != app.OptimizerDragonfly {
			t.Errorf("created optimizer = %q, want %q", created.Config.Optimizer, app.OptimizerDragonfly)
		}

		if created.Config.Variant != "" {
			t.Errorf("created variant = %q, want it empty for an engine with no variants", created.Config.Variant)
		}
	})

	refused := []struct {
		name  string
		field string
		value any
	}{
		{name: "variant", field: "variant", value: "desma"},
		{name: "crossoverCount", field: "crossoverCount", value: 40},
		{name: "danceDamp", field: "danceDamp", value: 0.5},
		{name: "aquilaWeight", field: "aquilaWeight", value: 0.5},
		{name: "oppositionProbability", field: "oppositionProbability", value: 0.5},
	}

	for _, test := range refused {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			body := base()
			body[test.field] = test.value

			response := postEngineJob(t, server, body)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
			}

			var decoded apiErrorResponse

			err := json.Unmarshal(response.Body.Bytes(), &decoded)
			if err != nil {
				t.Fatalf("response %q is not the API error envelope: %v", response.Body.String(), err)
			}

			if decoded.Error.Code != codeInvalidConfig {
				t.Errorf("error code = %q, want %q", decoded.Error.Code, codeInvalidConfig)
			}

			if !strings.Contains(decoded.Error.Message, test.field) {
				t.Errorf("error message = %q, want it to name %q", decoded.Error.Message, test.field)
			}
		})
	}

	// Polishing is deliberately not in the refused set. A sweep names its own
	// engine, so a Dragonfly base may enable one and it runs under MayFly
	// unless polishingOptimizer says otherwise.
	t.Run("polishingEnabled", func(t *testing.T) {
		t.Parallel()

		body := base()
		body[fieldMode] = modeBatch
		body["batchSize"] = 5
		body["polishingEnabled"] = true

		response := postEngineJob(t, server, body)
		if response.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
		}

		if !strings.Contains(response.Body.String(), `"polishingOptimizer":"mayfly"`) {
			t.Errorf("response %q does not resolve the polishing engine to mayfly", response.Body.String())
		}
	})

	// The one engine a sweep may not name is still refused, and the refusal
	// names the field the caller wrote rather than the base engine.
	t.Run("polishingOptimizerDragonfly", func(t *testing.T) {
		t.Parallel()

		body := base()
		body[fieldMode] = modeBatch
		body["batchSize"] = 5
		body["polishingEnabled"] = true
		body["polishingOptimizer"] = optimizerDragonfly

		response := postEngineJob(t, server, body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
		}

		if !strings.Contains(response.Body.String(), "polishingOptimizer") {
			t.Errorf("error %q does not name polishingOptimizer", response.Body.String())
		}
	})
}

func postEngineJob(t *testing.T, server *Server, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("json.Marshal(%v): %v", body, err)
	}

	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/v1/jobs", bytes.NewReader(encoded))
	response := httptest.NewRecorder()
	server.handleCreateJob(response, request)

	return response
}

// TestPolishEndpointContinuesACMAESParent covers the request the whole change
// exists to make possible: a completed CMA-ES job handed to a polishing sweep.
//
// The endpoint inherits the parent's configuration wholesale, so before
// polishingOptimizer existed such a request arrived at app.Validate with
// polishing enabled, no way to turn it off, and a refusal. It now succeeds, and
// the sweep runs under the engine the request names rather than the parent's.
func TestPolishEndpointContinuesACMAESParent(t *testing.T) {
	t.Parallel()

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
		RefPath: imgPath, Mode: app.ModeBatch, Circles: 1, BatchSize: 1, Iters: 2,
		PopSize: 20, Threads: 1, Seed: 42, Optimizer: app.OptimizerCMAES,
	})
	if err != nil {
		t.Fatal(err)
	}

	source := server.jobManager.CreateJob(app.DefaultProject, config)
	params := []float64{1, 1, 1, 1, 0, 0, 1}

	err = server.jobManager.StartJob(source.ID)
	if err != nil {
		t.Fatal(err)
	}

	err = server.jobManager.CompleteJob(source.ID, 8000, 900000, params, 600, 1000, "completed")
	if err != nil {
		t.Fatal(err)
	}

	checkpoint := store.NewCheckpoint(source.ID, params, 600, 1000, 8000, config)

	err = fsStore.SaveCheckpoint(source.ID, checkpoint)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequestWithContext(t.Context(),
		http.MethodPost, "/api/v1/jobs/"+source.ID+"/polish",
		strings.NewReader(`{"optimizer":"cmaes","sigma":0.01}`))
	request.Header.Set("Content-Type", "application/json")

	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("polish status = %d, want 201: %s", response.Code, response.Body.String())
	}

	var decoded struct {
		JobID string `json:"jobId"`
	}

	err = json.Unmarshal(response.Body.Bytes(), &decoded)
	if err != nil {
		t.Fatalf("response %q is not the polish envelope: %v", response.Body.String(), err)
	}

	polished, ok := server.jobManager.GetJob(decoded.JobID)
	if !ok {
		t.Fatalf("polish job %q was not created", decoded.JobID)
	}

	if got := polished.Config.ResolvedPolishingOptimizer(); got != app.OptimizerCMAES {
		t.Errorf("ResolvedPolishingOptimizer() = %q, want %q", got, app.OptimizerCMAES)
	}

	if got := polished.Config.ResolvedPolishingSigma(); got != 0.01 {
		t.Errorf("ResolvedPolishingSigma() = %v, want 0.01", got)
	}

	// The base engine is inherited from the parent and untouched by the
	// override, which is what keeps the two decisions independent.
	if got := polished.Config.ResolvedOptimizer(); got != app.OptimizerCMAES {
		t.Errorf("ResolvedOptimizer() = %q, want %q", got, app.OptimizerCMAES)
	}
}

// TestNewPolishOptimizerSelectsTheConfiguredEngine pins the sweep's own engine
// decision, which is deliberately separate from the base stage's.
//
// The absent case matters most: every checkpoint written before
// polishingOptimizer existed carries no value, and must keep running the
// standard-variant MayFly population the figures in
// docs/polishing-budget-report.md describe.
func TestNewPolishOptimizerSelectsTheConfiguredEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		base     app.Optimizer
		polisher app.Optimizer
		cmaes    bool
	}{
		{name: caseAbsent, base: "", polisher: ""},
		{name: "cmaesBaseKeepsMayflyPolisher", base: app.OptimizerCMAES, polisher: ""},
		{name: "mayflyBaseWithCMAESPolisher", base: app.OptimizerMayfly, polisher: app.OptimizerCMAES, cmaes: true},
		{name: "cmaesBoth", base: app.OptimizerCMAES, polisher: app.OptimizerCMAES, cmaes: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			optimizer, err := newPolishOptimizer(store.JobConfig{
				Optimizer:          test.base,
				Variant:            app.VariantStandard,
				PolishingOptimizer: test.polisher,
				PolishingIters:     10,
				PolishingPopSize:   20,
			}, nil, 7)
			if err != nil {
				t.Fatalf("newPolishOptimizer() error = %v", err)
			}

			_, isCMAES := optimizer.(*opt.CMAESAdapter)
			if isCMAES != test.cmaes {
				t.Fatalf("optimizer = %T, want cmaes = %v", optimizer, test.cmaes)
			}

			if !test.cmaes {
				if _, ok := optimizer.(*opt.MayflyAdapter); !ok {
					t.Fatalf("optimizer = %T, want *opt.MayflyAdapter", optimizer)
				}
			}
		})
	}
}

// TestPolishContinuationProfileDefaultsToTheRecordedSigma pins the value an
// unset configuration polishes at, so a checkpoint written before the field
// existed searches the width every recorded figure was measured under.
func TestPolishContinuationProfileDefaultsToTheRecordedSigma(t *testing.T) {
	t.Parallel()

	if got := polishContinuationProfile(store.JobConfig{}).Sigma; got != app.DefaultPolishingSigma {
		t.Errorf("sigma = %v, want %v", got, app.DefaultPolishingSigma)
	}

	if got := polishContinuationProfile(store.JobConfig{PolishingSigma: 0.005}).Sigma; got != 0.005 {
		t.Errorf("sigma = %v, want 0.005", got)
	}
}
