package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/store"
)

// TestResumeGuardsOptimizerVersion pins the resume trust boundary added for the
// v0.5.1 bump: a checkpoint written by a different optimizer is refused rather
// than silently continued, a legacy checkpoint without a recorded version still
// resumes, and the explicit override gets past the refusal.
func TestResumeGuardsOptimizerVersion(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")
	createSimpleTestImage(t, imgPath)

	persistence, err := createTestStore(filepath.Join(tmpDir, "checkpoints"))
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions(":8080", persistence, ServerOptions{InputRoots: []string{tmpDir}})
	server.optimizerVersionOverride = "v0.5.1"

	shutdownTestServer(t, server)

	config := JobConfig{RefPath: imgPath, Mode: "joint", Circles: 1, Iters: 10, PopSize: 30, Seed: 11}
	params := []float64{1, 1, 1, 1, 0, 0, 1}

	// stoppedJobWithCheckpoint creates a cancelled job whose checkpoint records
	// the given optimizer version, which is the shape the fork-on-resume path
	// reads.
	stoppedJobWithCheckpoint := func(t *testing.T, recordedVersion string) string {
		t.Helper()

		job := server.jobManager.CreateJob(app.DefaultProject, config)

		err := server.jobManager.StartJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}

		err = server.jobManager.CancelJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}

		checkpoint := store.NewCheckpoint(job.ID, params, 600, 1000, 8, job.Config)
		checkpoint.Evaluations = 80
		checkpoint.OptimizerVersion = recordedVersion

		err = persistence.SaveCheckpoint(job.ID, checkpoint)
		if err != nil {
			t.Fatal(err)
		}

		return job.ID
	}

	resume := func(jobID, query string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+jobID+"/resume"+query, nil))

		return recorder
	}

	t.Run("matching version resumes", func(t *testing.T) {
		t.Parallel()

		recorder := resume(stoppedJobWithCheckpoint(t, "v0.5.1"), "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("resume status = %d, body %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("legacy checkpoint resumes", func(t *testing.T) {
		t.Parallel()

		recorder := resume(stoppedJobWithCheckpoint(t, ""), "")
		if recorder.Code != http.StatusOK {
			t.Fatalf("resume status = %d, body %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("mismatched version is refused", func(t *testing.T) {
		t.Parallel()

		recorder := resume(stoppedJobWithCheckpoint(t, "v0.4.0"), "")
		if recorder.Code != http.StatusConflict {
			t.Fatalf("resume status = %d, want %d, body %s", recorder.Code, http.StatusConflict, recorder.Body.String())
		}

		var payload struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}

		err := json.Unmarshal(recorder.Body.Bytes(), &payload)
		if err != nil {
			t.Fatalf("decode error envelope %q: %v", recorder.Body.String(), err)
		}

		if payload.Error.Code != "optimizer_version_mismatch" {
			t.Fatalf("error code = %q, body %s", payload.Error.Code, recorder.Body.String())
		}
	})

	t.Run("override resumes the mismatch", func(t *testing.T) {
		t.Parallel()

		recorder := resume(stoppedJobWithCheckpoint(t, "v0.4.0"), "?allowOptimizerMismatch=true")
		if recorder.Code != http.StatusOK {
			t.Fatalf("resume status = %d, body %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("malformed override does not disarm the guard", func(t *testing.T) {
		t.Parallel()

		recorder := resume(stoppedJobWithCheckpoint(t, "v0.4.0"), "?allowOptimizerMismatch=yes-please")
		if recorder.Code != http.StatusConflict {
			t.Fatalf("resume status = %d, want %d, body %s", recorder.Code, http.StatusConflict, recorder.Body.String())
		}
	})
}
