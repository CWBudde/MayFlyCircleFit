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

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/opt"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// Field names and values this file writes into request bodies more than once.
const (
	fieldRefPath      = "refPath"
	fieldMode         = "mode"
	fieldCircles      = "circles"
	fieldIters        = "iters"
	fieldPopSize      = "popSize"
	modeJoint         = "joint"
	modeBatch         = "batch"
	codeInvalidConfig = "invalid_config"
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
	}{
		{name: "absent", optimizer: "", variant: app.VariantStandard},
		{name: "mayfly", optimizer: app.OptimizerMayfly, variant: app.VariantAOBLMOA},
		{name: "dragonfly", optimizer: app.OptimizerDragonfly, dragonfly: true},
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

			if !test.dragonfly {
				if _, ok := optimizer.(*opt.MayflyAdapter); !ok {
					t.Fatalf("optimizer = %T, want *opt.MayflyAdapter", optimizer)
				}
			}
		})
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

	t.Run("polishingEnabled", func(t *testing.T) {
		t.Parallel()

		body := base()
		body[fieldMode] = modeBatch
		body["batchSize"] = 5
		body["polishingEnabled"] = true

		response := postEngineJob(t, server, body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
		}

		if !strings.Contains(response.Body.String(), "polishingEnabled") {
			t.Errorf("error %q does not name polishingEnabled", response.Body.String())
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
