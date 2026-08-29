package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/store"
)

// wireTime is a fixed instant so the expected JSON below can be a literal.
var wireTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// TestProjectTypeKeepsJSONWireFormat pins the one thing app.Project must not
// change. It is a named string type, so it marshals and unmarshals exactly like
// a string; the assertions are against literal expected JSON rather than a
// round trip through the same types, because a round trip would still pass if
// the shape had changed on both sides.
func TestProjectTypeKeepsJSONWireFormat(t *testing.T) {
	t.Parallel()

	t.Run("job status response", func(t *testing.T) {
		t.Parallel()

		end := wireTime.Add(time.Minute)
		response := jobStatusResponse{
			ID:               "12345678-1234-4234-8234-123456789abc",
			Project:          "christian",
			State:            StateCompleted,
			Config:           store.JobConfig{RefPath: "a.png", Mode: app.ModeJoint, Circles: 1, Iters: 2, PopSize: 30, Seed: 7},
			RequestedCircles: 1,
			ActualCircles:    1,
			BestCost:         1.5,
			BestRevision:     3,
			InitialCost:      9.5,
			Iterations:       2,
			Evaluations:      60,
			Termination:      "completed",
			Elapsed:          60,
			CPS:              1,
			StartTime:        wireTime,
			EndTime:          &end,
		}
		const want = `{"id":"12345678-1234-4234-8234-123456789abc","project":"christian",` +
			`"state":"completed","config":{"refPath":"a.png","mode":"joint","circles":1,"iters":2,"popSize":30,"seed":7},` +
			`"requestedCircles":1,"actualCircles":1,` +
			`"bestCost":1.5,"bestRevision":3,"initialCost":9.5,"psnr":null,"iterations":2,"evaluations":60,` +
			`"termination":"completed","elapsed":60,"cps":1,` +
			`"startTime":"2026-01-02T03:04:05Z","endTime":"2026-01-02T03:05:05Z"}`
		assertJSON(t, response, want)
	})

	t.Run("jobs list", func(t *testing.T) {
		t.Parallel()

		jobs := []*Job{{
			ID:               "12345678-1234-4234-8234-123456789abc",
			Project:          "christian",
			State:            StateCompleted,
			Config:           store.JobConfig{RefPath: "a.png", Mode: app.ModeJoint, Circles: 1, Iters: 2, PopSize: 30, Seed: 7},
			RequestedCircles: 1,
			ActualCircles:    1,
			BestCost:         1.5,
			InitialCost:      9.5,
			Iterations:       2,
			Evaluations:      60,
			StartTime:        wireTime,
		}}
		const want = `[{"id":"12345678-1234-4234-8234-123456789abc","project":"christian",` +
			`"state":"completed","config":{"refPath":"a.png","mode":"joint","circles":1,"iters":2,"popSize":30,"seed":7},` +
			`"requestedCircles":1,"actualCircles":1,` +
			`"bestCost":1.5,"initialCost":9.5,"iterations":2,"evaluations":60,` +
			`"startTime":"2026-01-02T03:04:05Z"}]`
		assertJSON(t, jobs, want)
	})

	t.Run("projects endpoint", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()

		persistence, err := store.NewFSStore(root)
		if err != nil {
			t.Fatal(err)
		}

		server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})

		_, err = server.ensureProject("christian")
		if err != nil {
			t.Fatal(err)
		}

		server.jobManager.CreateJob("christian", store.JobConfig{RefPath: "b.png"})

		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", recorder.Code)
		}

		const want = `[{"slug":"christian","jobCount":1},{"slug":"default","default":true,"jobCount":0}]`
		if got := strings.TrimSpace(recorder.Body.String()); got != want {
			t.Fatalf("projects body:\n got %s\nwant %s", got, want)
		}
	})

	t.Run("job unmarshals a plain string project", func(t *testing.T) {
		t.Parallel()

		// The reverse direction: a client's bytes must still decode into the
		// typed field without any custom unmarshaller.
		var job Job

		err := json.Unmarshal([]byte(`{"id":"x","project":"christian"}`), &job)
		if err != nil {
			t.Fatal(err)
		}

		if job.Project != app.Project("christian") {
			t.Fatalf("decoded project = %q, want %q", job.Project, "christian")
		}
	})

	t.Run("create request unmarshals a plain string project", func(t *testing.T) {
		t.Parallel()

		var request createJobRequest

		err := json.Unmarshal([]byte(`{"project":"christian","refPath":"a.png"}`), &request)
		if err != nil {
			t.Fatal(err)
		}

		if request.Project != "christian" {
			t.Fatalf("decoded project = %q, want %q", request.Project, "christian")
		}
	})
}

func assertJSON(t *testing.T, value any, want string) {
	t.Helper()

	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	if string(encoded) != want {
		t.Fatalf("JSON wire format changed:\n got %s\nwant %s", encoded, want)
	}
}
