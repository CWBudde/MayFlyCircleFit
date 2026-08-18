package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

func TestRouting_RootIsDashboard(t *testing.T) {
	server := NewServer(":0", nil)
	shutdownTestServer(t, server)

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("GET / status = %d, want %d", got, want)
	}
	if got := recorder.Body.String(); !strings.Contains(got, "<h1") || !strings.Contains(got, "Dashboard") {
		t.Fatalf("GET / should render dashboard page, got %q", got)
	}
}

func TestRouting_JobsListAndJobDetailRoutes(t *testing.T) {
	server := NewServer(":0", nil)
	shutdownTestServer(t, server)

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jobs", nil))
	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("GET /jobs status = %d, want %d", got, want)
	}
	if !strings.Contains(recorder.Body.String(), "Optimization Jobs") {
		t.Fatalf("jobs page missing heading, got %q", recorder.Body.String())
	}
	if got := recorder.Header().Get("Location"); got != "" {
		t.Fatalf("GET /jobs redirected unexpectedly to %s", got)
	}

	jobsPathRecorder := httptest.NewRecorder()
	jobsPostReq := httptest.NewRequest(http.MethodPost, "/jobs", strings.NewReader(""))
	server.Handler().ServeHTTP(jobsPathRecorder, jobsPostReq)
	if got, want := jobsPathRecorder.Code, http.StatusMethodNotAllowed; got != want {
		t.Fatalf("POST /jobs status = %d, want %d", got, want)
	}

	imgPath := createTempRefImage(t)
	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: imgPath, Mode: "batch", Circles: 1, Iters: 1, PopSize: 2, Seed: 42})
	if err := server.enqueueJob(job.ID); err != nil {
		t.Fatalf("enqueue job %s: %v", job.ID, err)
	}

	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/jobs/"+job.ID, nil))
	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("GET /jobs/<id> status = %d, want %d", got, want)
	}
	if !strings.Contains(recorder.Body.String(), job.ID) {
		t.Fatalf("jobs detail response missing job id")
	}
}

func createTempRefImage(t *testing.T) string {
	t.Helper()
	img := t.TempDir() + "/ref.png"
	createSimpleTestImage(t, img)
	return img
}
