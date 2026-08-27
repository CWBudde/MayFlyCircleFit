package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
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

	assertPageMethods(t, server, "/")
}

// assertPageMethods pins the method contract of a server-rendered page: HEAD is
// served like GET, anything else is refused, and the refusal names both methods
// it would have accepted rather than only GET.
func assertPageMethods(t *testing.T, server *Server, path string) {
	t.Helper()

	headRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(headRecorder, httptest.NewRequest(http.MethodHead, path, nil))

	if got, want := headRecorder.Code, http.StatusOK; got != want {
		t.Fatalf("HEAD %s status = %d, want %d", path, got, want)
	}

	postRecorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(postRecorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader("")))

	if got, want := postRecorder.Code, http.StatusMethodNotAllowed; got != want {
		t.Fatalf("POST %s status = %d, want %d", path, got, want)
	}

	if got, want := postRecorder.Header().Get("Allow"), "GET, HEAD"; got != want {
		t.Fatalf("POST %s Allow = %q, want %q", path, got, want)
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

	assertPageMethods(t, server, "/jobs")

	imgPath := createTempRefImage(t)

	job := server.jobManager.CreateJob(app.DefaultProject, JobConfig{RefPath: imgPath, Mode: "batch", Circles: 1, Iters: 1, PopSize: 2, Seed: 42})

	err := server.enqueueJob(job.ID)
	if err != nil {
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

func TestRouting_SettingsPage(t *testing.T) {
	server := NewServer(":0", nil)
	shutdownTestServer(t, server)

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/settings", nil))

	if got, want := recorder.Code, http.StatusOK; got != want {
		t.Fatalf("GET /settings status = %d, want %d", got, want)
	}

	body := recorder.Body.String()
	for _, want := range []string{
		"settings-image-refresh",
		"settings-default-view-mode",
		"settings-default-colormap",
		"settings-visible-metrics",
		"settings-reset",
		// The storage keys moved into web/src/prefs.ts with the island; what
		// the served page has to carry is the mount point and the bundle that
		// fills it. internal/ui/settings_test.go pins the fallback markup.
		`data-island="settings"`,
		"<noscript>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("settings page missing %q, got %q", want, body)
		}
	}

	assertPageMethods(t, server, "/settings")

	notFound := httptest.NewRecorder()
	server.Handler().ServeHTTP(notFound, httptest.NewRequest(http.MethodGet, "/settings/extra", nil))

	if got, want := notFound.Code, http.StatusNotFound; got != want {
		t.Fatalf("GET /settings/extra status = %d, want %d", got, want)
	}
}
