package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// writeLegacyCheckpoint plants a job in the pre-project `<root>/jobs` layout.
func writeLegacyCheckpoint(t *testing.T, root, jobID string) {
	t.Helper()
	legacy, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := &store.Checkpoint{
		SchemaVersion: 2,
		JobID:         jobID,
		BestParams:    []float64{1, 2, 3, 4, 5, 6, 7},
		BestCost:      431.9,
		InitialCost:   41458.8,
		Iterations:    10,
		Termination:   "completed",
		Timestamp:     time.Now(),
		Config: store.JobConfig{
			RefPath: "example/Ref.png",
			Mode:    app.ModeBatch,
			Circles: 1,
			Iters:   10,
			PopSize: 30,
		},
	}
	if err := legacy.SaveCheckpoint(jobID, checkpoint); err != nil {
		t.Fatal(err)
	}
}

// TestLegacyLayoutRestoresWithoutMigration is the direct guard for the existing
// Ref.png jobs: a data root that has only `jobs/` and no `projects/` must
// restore every job and attribute it to the default project.
func TestLegacyLayoutRestoresWithoutMigration(t *testing.T) {
	root := t.TempDir()
	jobID := "12345678-1234-4234-8234-123456789abc"
	writeLegacyCheckpoint(t, root, jobID)

	if _, err := os.Stat(filepath.Join(root, projectsDirName)); !os.IsNotExist(err) {
		t.Fatalf("projects directory must not exist before any project is created")
	}

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})

	job, ok := server.jobManager.GetJob(jobID)
	if !ok {
		t.Fatalf("legacy job was not restored")
	}
	if job.Project != app.DefaultProject {
		t.Fatalf("legacy job project = %q, want %q", job.Project, app.DefaultProject)
	}
	if server.storeForJob(jobID) != persistence {
		t.Fatalf("legacy job must resolve to the legacy store")
	}

	// Restoring must not have created the projects container.
	if _, err := os.Stat(filepath.Join(root, projectsDirName)); !os.IsNotExist(err) {
		t.Fatalf("restore must not create the projects directory")
	}
}

// TestProjectIsolation verifies artifacts land under the right project and that
// filtering separates them.
func TestProjectIsolation(t *testing.T) {
	root := t.TempDir()
	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})

	legacyJob := server.jobManager.CreateJob(app.DefaultProject, store.JobConfig{RefPath: "a.png"})
	if _, err := server.ensureProject("christian"); err != nil {
		t.Fatal(err)
	}
	projectJob := server.jobManager.CreateJob("christian", store.JobConfig{RefPath: "b.png"})

	// The project store must be a distinct directory under projects/.
	projectDir := filepath.Join(root, projectsDirName, "christian", "jobs")
	if info, err := os.Stat(projectDir); err != nil || !info.IsDir() {
		t.Fatalf("project jobs directory not created: %v", err)
	}
	if server.storeForJob(projectJob.ID) == server.storeForJob(legacyJob.ID) {
		t.Fatalf("jobs in different projects must not share a store")
	}

	// Filtering.
	all := server.jobManager.ListJobs()
	if got := len(filterJobsByProject(all, "christian")); got != 1 {
		t.Fatalf("christian job count = %d, want 1", got)
	}
	if got := len(filterJobsByProject(all, app.DefaultProject)); got != 1 {
		t.Fatalf("default job count = %d, want 1", got)
	}
}

func TestListJobsProjectFilterAndValidation(t *testing.T) {
	root := t.TempDir()
	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})
	if _, err := server.ensureProject("christian"); err != nil {
		t.Fatal(err)
	}
	server.jobManager.CreateJob(app.DefaultProject, store.JobConfig{RefPath: "a.png"})
	server.jobManager.CreateJob("christian", store.JobConfig{RefPath: "b.png"})

	cases := []struct {
		query string
		want  int
		code  int
	}{
		{"", 2, http.StatusOK},
		{"?project=all", 2, http.StatusOK},
		{"?project=christian", 1, http.StatusOK},
		{"?project=nonexistent", 0, http.StatusOK},
	}
	for _, tc := range cases {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs"+tc.query, nil)
		server.Handler().ServeHTTP(recorder, request)
		if recorder.Code != tc.code {
			t.Fatalf("%q status = %d, want %d", tc.query, recorder.Code, tc.code)
		}
		var jobs []*Job
		if err := json.NewDecoder(recorder.Body).Decode(&jobs); err != nil {
			t.Fatalf("%q decode: %v", tc.query, err)
		}
		if len(jobs) != tc.want {
			t.Fatalf("%q returned %d jobs, want %d", tc.query, len(jobs), tc.want)
		}
	}

	// A traversal attempt is rejected and creates nothing.
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/jobs?project=../../etc", nil)
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("traversal slug status = %d, want 400", recorder.Code)
	}
}

func TestProjectsEndpoint(t *testing.T) {
	root := t.TempDir()
	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})
	if _, err := server.ensureProject("christian"); err != nil {
		t.Fatal(err)
	}
	server.jobManager.CreateJob("christian", store.JobConfig{RefPath: "b.png"})

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var projects []projectResponse
	if err := json.NewDecoder(recorder.Body).Decode(&projects); err != nil {
		t.Fatal(err)
	}
	found := map[string]int{}
	for _, p := range projects {
		found[p.Slug] = p.Jobs
	}
	if _, ok := found[app.DefaultProject]; !ok {
		t.Fatalf("default project missing from %v", found)
	}
	if found["christian"] != 1 {
		t.Fatalf("christian job count = %d, want 1", found["christian"])
	}

	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/projects", nil))
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want 405", recorder.Code)
	}
}

// TestCreateJobAcceptsProjectAndStillRejectsTypos pins the embedded-envelope
// behavior: `project` is accepted alongside the promoted JobConfig fields while
// DisallowUnknownFields still rejects a genuine typo.
func TestCreateJobAcceptsProjectAndStillRejectsTypos(t *testing.T) {
	root := t.TempDir()
	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}
	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{
		DataRoot:   root,
		InputRoots: []string{"testdata"},
	})

	body := `{"project":"christian","refPath":"nope.png","circles":1,"iters":1,"popSize":30}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
	server.Handler().ServeHTTP(recorder, request)
	// The ref path is bogus, so this fails at path resolution — the point is
	// that it got past JSON decoding rather than being rejected as unknown.
	if recorder.Code == http.StatusBadRequest && strings.Contains(recorder.Body.String(), "invalid JSON") {
		t.Fatalf("project field was rejected by the decoder: %s", recorder.Body.String())
	}

	typo := `{"projekt":"christian","refPath":"nope.png"}`
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(typo))
	server.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status = %d, want 400", recorder.Code)
	}
}

func TestResolveRequestedProjectConflict(t *testing.T) {
	server := NewServerWithOptions("localhost:0", nil, ServerOptions{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs?project=a", nil)
	if _, err := server.resolveRequestedProject("b", request); err == nil {
		t.Fatalf("conflicting project must be rejected")
	}
	if slug, err := server.resolveRequestedProject("a", request); err != nil || slug != "a" {
		t.Fatalf("matching project = %q, %v", slug, err)
	}
	if slug, err := server.resolveRequestedProject("", request); err != nil || slug != "a" {
		t.Fatalf("query fallback = %q, %v", slug, err)
	}
}
