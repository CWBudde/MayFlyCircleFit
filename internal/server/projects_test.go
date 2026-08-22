package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
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

	legacyStore, err := server.storeForJob(jobID)
	if err != nil {
		t.Fatalf("legacy job store: %v", err)
	}

	if legacyStore != persistence {
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

	projectStore, err := server.storeForJob(projectJob.ID)
	if err != nil {
		t.Fatalf("project job store: %v", err)
	}

	legacyStore, err := server.storeForJob(legacyJob.ID)
	if err != nil {
		t.Fatalf("legacy job store: %v", err)
	}

	if projectStore == legacyStore {
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

		err := json.NewDecoder(recorder.Body).Decode(&jobs)
		if err != nil {
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

	found := map[app.Project]int{}
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

// captureLogs redirects the default slog logger for the duration of one test.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	previous := slog.Default()

	slog.SetDefault(slog.New(slog.NewTextHandler(&buffer, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	return &buffer
}

// TestUnknownProjectDoesNotResolveToDefaultStore is the guard for the silent
// redirect: a slug the registry cannot resolve must fail, never fall back to
// the default project's directory.
func TestUnknownProjectDoesNotResolveToDefaultStore(t *testing.T) {
	root := t.TempDir()

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})

	unknownStore, err := server.storeForSlug("ghost")
	if err == nil {
		t.Fatalf("unknown slug resolved to %v, want an error", unknownStore)
	}

	if !errors.Is(err, errUnknownProject) {
		t.Fatalf("error = %v, want errUnknownProject", err)
	}

	if unknownStore != nil {
		t.Fatalf("unknown slug must resolve to no store, got %v", unknownStore)
	}

	// The same must hold through a job that names the unloadable project.
	orphan := server.jobManager.CreateJob("ghost", store.JobConfig{RefPath: "a.png"})

	jobStore, err := server.storeForJob(orphan.ID)
	if err == nil {
		t.Fatalf("job in an unresolvable project resolved to %v, want an error", jobStore)
	}

	if jobStore == persistence {
		t.Fatalf("job in an unresolvable project must not resolve to the default store")
	}

	// The default project keeps resolving to the injected store.
	defaultStore, err := server.storeForSlug(app.DefaultProject)
	if err != nil || defaultStore != persistence {
		t.Fatalf("default slug = %v, %v; want the injected store", defaultStore, err)
	}

	emptyStore, err := server.storeForSlug("")
	if err != nil || emptyStore != persistence {
		t.Fatalf("empty slug = %v, %v; want the injected store", emptyStore, err)
	}
}

// TestDeleteRejectsUnresolvableProject pins that a job whose project cannot be
// resolved is not deleted against the default project's store.
func TestDeleteRejectsUnresolvableProject(t *testing.T) {
	root := t.TempDir()

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})
	orphan := server.jobManager.CreateJob("ghost", store.JobConfig{RefPath: "a.png"})

	recorder := httptest.NewRecorder()
	server.handleDeleteJob(recorder, httptest.NewRequest(http.MethodDelete, "/api/v1/jobs/"+orphan.ID, nil), orphan.ID)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("delete status = %d, want 500", recorder.Code)
	}

	if _, ok := server.jobManager.GetJob(orphan.ID); !ok {
		t.Fatalf("job must survive a failed store resolution")
	}
}

// TestDiscoverLogsUnusableProjectDirectory is the guard for the silent skip: a
// directory that cannot become a project must leave a signal in the log.
func TestDiscoverLogsUnusableProjectDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, projectsDirName, "Alpha"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, projectsDirName, "stray.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	logs := captureLogs(t)

	registry := newProjectRegistry(root, persistence)
	if _, ok := registry.Get("Alpha"); ok {
		t.Fatalf("an invalid slug must not be adopted")
	}

	output := logs.String()
	if !strings.Contains(output, "Alpha") {
		t.Fatalf("skipped project directory was not logged: %s", output)
	}

	if !strings.Contains(output, "stray.txt") {
		t.Fatalf("skipped non-directory entry was not logged: %s", output)
	}
}

// TestDiscoverIgnoresDefaultProjectDirectory pins the alias: the default
// project is the legacy `<root>/jobs` tree, so a stray `projects/default`
// directory must never become a second store under the same name. Without the
// explicit skip the meaning depended on whether a store was injected.
func TestDiscoverIgnoresDefaultProjectDirectory(t *testing.T) {
	root := t.TempDir()

	shadow := filepath.Join(root, projectsDirName, string(app.DefaultProject))
	if err := os.MkdirAll(shadow, 0o755); err != nil {
		t.Fatal(err)
	}

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	logs := captureLogs(t)
	registry := newProjectRegistry(root, persistence)

	adopted, ok := registry.Get(app.DefaultProject)
	if !ok {
		t.Fatalf("the injected legacy store must stay registered as the default project")
	}

	if adopted != persistence {
		t.Fatalf("the default project resolved to the shadow directory instead of the legacy store")
	}

	if !strings.Contains(logs.String(), shadow) {
		t.Fatalf("the ignored default project directory was not logged: %s", logs.String())
	}

	// Without an injected store the shadow directory must not fill the gap
	// either, or the same path would mean two different things.
	bare := newProjectRegistry(root, nil)
	if _, ok := bare.Get(app.DefaultProject); ok {
		t.Fatalf("projects/default was adopted as the default project")
	}
}

// TestStoreFaultIsServerErrorWithoutPath covers the mislabeled-and-leaky
// failure: a project directory that cannot be created is a 500, and the
// response never carries the data root.
func TestStoreFaultIsServerErrorWithoutPath(t *testing.T) {
	root := t.TempDir()
	imageDir := t.TempDir()
	imagePath := filepath.Join(imageDir, "ref.png")
	createSimpleTestImage(t, imagePath)

	// A regular file where the projects container belongs makes every project
	// directory creation fail the way a read-only or NTFS root would.
	if err := os.WriteFile(filepath.Join(root, projectsDirName), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{
		DataRoot:   root,
		InputRoots: []string{imageDir},
	})

	body := fmt.Sprintf(`{"project":"christian","refPath":%q,"mode":"joint","circles":1,"iters":1,"popSize":20,"seed":1}`, imagePath)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(body))
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", recorder.Code, recorder.Body.String())
	}

	if strings.Contains(recorder.Body.String(), root) {
		t.Fatalf("response leaked the data root: %s", recorder.Body.String())
	}

	if !strings.Contains(recorder.Body.String(), "project_unavailable") {
		t.Fatalf("store fault used the wrong error code: %s", recorder.Body.String())
	}

	// A bad slug is still the client's fault and keeps its 400.
	recorder = httptest.NewRecorder()
	bad := fmt.Sprintf(`{"project":"Christian","refPath":%q,"mode":"joint","circles":1,"iters":1,"popSize":20,"seed":1}`, imagePath)
	request = httptest.NewRequest(http.MethodPost, "/api/v1/jobs", strings.NewReader(bad))
	server.Handler().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid slug status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
	}
}

// TestJobStatusResponseCarriesProject covers the projection gap: create and
// list serialize the Job itself and so always exposed the project, but the
// single-job endpoints build jobStatusResponse by hand and used to drop it,
// leaving a client holding only a job ID unable to learn who owns it.
func TestJobStatusResponseCarriesProject(t *testing.T) {
	root := t.TempDir()

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})
	if _, err := server.ensureProject("christian"); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		project app.Project
		// storeEmpty forces the stored job's project back to the empty string
		// after creation. CreateJob normalizes "" to the default project, so
		// passing an empty slug in is not enough to reach the handler with one:
		// without this the case would be an exact duplicate of "default
		// project" and would still pass if the response stopped normalizing.
		storeEmpty bool
		want       app.Project
	}{
		{name: "named project", project: "christian", want: "christian"},
		{name: "default project", project: app.DefaultProject, want: app.DefaultProject},
		// A job restored from a pre-project checkpoint carries no slug and must
		// still report the default project rather than an empty string.
		{name: "legacy empty slug", project: app.DefaultProject, storeEmpty: true, want: app.DefaultProject},
	} {
		t.Run(tc.name, func(t *testing.T) {
			job := server.jobManager.CreateJob(tc.project, store.JobConfig{RefPath: "a.png"})
			if tc.storeEmpty {
				err := server.jobManager.UpdateJob(job.ID, func(stored *Job) {
					stored.Project = ""
				})
				if err != nil {
					t.Fatal(err)
				}
			}

			for _, path := range []string{
				"/api/v1/jobs/" + job.ID,
				"/api/v1/jobs/" + job.ID + "/status",
			} {
				recorder := httptest.NewRecorder()
				server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

				if recorder.Code != http.StatusOK {
					t.Fatalf("%s status = %d, want 200", path, recorder.Code)
				}
				// Decoding into a plain string field is deliberate: it is the
				// wire-format check that app.Project still marshals as a string.
				var decoded struct {
					ID      string `json:"id"`
					Project string `json:"project"`
				}

				err := json.Unmarshal(recorder.Body.Bytes(), &decoded)
				if err != nil {
					t.Fatalf("%s: %v", path, err)
				}

				if decoded.ID != job.ID {
					t.Fatalf("%s id = %q, want %q", path, decoded.ID, job.ID)
				}

				if decoded.Project != string(tc.want) {
					t.Fatalf("%s project = %q, want %q", path, decoded.Project, tc.want)
				}
			}
		})
	}
}

// TestNamedProjectJobSurvivesRestart is the round trip the whole feature exists
// for: a job placed in a named project over HTTP must come back in that same
// project after the process restarts, reading only what is on disk.
func TestNamedProjectJobSurvivesRestart(t *testing.T) {
	root := t.TempDir()

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})

	// The job is created directly rather than over HTTP: what this test pins is
	// the restart, and the restored side reads only the checkpoint written
	// below plus the directory it sits in. Creation over HTTP is covered by
	// TestCreateJobAcceptsProjectAndStillRejectsTypos.
	if _, err := server.ensureProject("christian"); err != nil {
		t.Fatal(err)
	}

	projectStore, err := server.storeForSlug("christian")
	if err != nil {
		t.Fatal(err)
	}

	job := server.jobManager.CreateJob("christian", store.JobConfig{RefPath: "example/Ref.png"})

	checkpoint := &store.Checkpoint{
		SchemaVersion: 2,
		JobID:         job.ID,
		BestParams:    []float64{1, 2, 3, 4, 5, 6, 7},
		BestCost:      12.5,
		Iterations:    3,
		Termination:   "completed",
		Timestamp:     time.Now(),
		Config:        store.JobConfig{RefPath: "example/Ref.png", Mode: app.ModeJoint, Circles: 1, Iters: 10, PopSize: 30},
	}
	if err := projectStore.SaveCheckpoint(job.ID, checkpoint); err != nil {
		t.Fatal(err)
	}

	// Restart: a brand-new server over the same data root, sharing no state.
	restartedPersistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	restarted := NewServerWithOptions("localhost:0", restartedPersistence, ServerOptions{DataRoot: root})

	restored, ok := restarted.jobManager.GetJob(job.ID)
	if !ok {
		t.Fatalf("job %s did not survive the restart", job.ID)
	}

	if restored.Project != "christian" {
		t.Fatalf("restored project = %q, want %q", restored.Project, "christian")
	}

	// It must resolve to the project's own store, not the legacy tree.
	restoredStore, err := restarted.storeForJob(job.ID)
	if err != nil {
		t.Fatalf("restored job store: %v", err)
	}

	if restoredStore == restartedPersistence {
		t.Fatalf("restored job resolved to the legacy store instead of its project store")
	}

	// And the filter must find it under its project and only there.
	recorder := httptest.NewRecorder()
	restarted.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/jobs?project=christian", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("filtered list status = %d, want 200", recorder.Code)
	}

	var listed []struct {
		ID      string `json:"id"`
		Project string `json:"project"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}

	if len(listed) != 1 || listed[0].ID != job.ID || listed[0].Project != "christian" {
		t.Fatalf("filtered list = %+v, want exactly job %s in christian", listed, job.ID)
	}

	recorder = httptest.NewRecorder()
	restarted.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/jobs?project="+string(app.DefaultProject), nil))

	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}

	if len(listed) != 0 {
		t.Fatalf("default project listed %d jobs, want 0", len(listed))
	}
}

// TestProjectsEndpointOrderingIsStable guards the union in handleProjects:
// Slugs() is sorted, but projects known only from in-memory jobs are appended
// in Go map order, which is randomized per run.
func TestProjectsEndpointOrderingIsStable(t *testing.T) {
	root := t.TempDir()

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})

	// Registered out of order, and deliberately only as jobs so they reach the
	// response through the map-ordered union rather than through Slugs().
	for _, slug := range []app.Project{"zebra", "alpha", "mango", "beta", "yak"} {
		server.jobManager.CreateJob(slug, store.JobConfig{RefPath: "a.png"})
	}

	var first []string

	for i := range 20 {
		recorder := httptest.NewRecorder()
		server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil))

		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", recorder.Code)
		}

		var decoded []projectResponse

		err := json.Unmarshal(recorder.Body.Bytes(), &decoded)
		if err != nil {
			t.Fatal(err)
		}

		got := make([]string, len(decoded))
		for j, p := range decoded {
			got[j] = string(p.Slug)
		}

		if i == 0 {
			first = got
			if !sort.StringsAreSorted(got) {
				t.Fatalf("projects are not sorted: %v", got)
			}

			continue
		}

		if strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("ordering changed between requests: %v then %v", first, got)
		}
	}
}

// TestCreateFormCarriesProjectThroughPost covers the plumbing gap: the create
// form posts to a bare "/create", which discards the query string the page was
// opened with, so without a hidden field every job created from the UI landed
// in the default project no matter which project the user came from.
func TestCreateFormCarriesProjectThroughPost(t *testing.T) {
	root := t.TempDir()

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/create?project=christian", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /create status = %d, want 200", recorder.Code)
	}

	body := recorder.Body.String()
	if !strings.Contains(body, `name="project"`) || !strings.Contains(body, `value="christian"`) {
		t.Fatalf("create form does not carry the project into the POST body")
	}

	// The default project needs no hidden field, and emitting one would make
	// "default" indistinguishable from a project a user actually chose.
	recorder = httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/create", nil))

	if strings.Contains(recorder.Body.String(), `name="project"`) {
		t.Fatalf("bare /create must not emit a project field")
	}
}

// TestCreateFormEchoesProjectOnValidationError keeps a rejected submission from
// silently relocating the job: the re-rendered form must still name the project
// the user was creating in.
func TestCreateFormEchoesProjectOnValidationError(t *testing.T) {
	root := t.TempDir()

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})

	form := strings.NewReader("project=christian&mode=joint")
	request := httptest.NewRequest(http.MethodPost, "/create", form)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, request)

	// refPath is missing, so this must re-render the form rather than redirect.
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (re-rendered form)", recorder.Code)
	}

	if !strings.Contains(recorder.Body.String(), `value="christian"`) {
		t.Fatalf("validation error dropped the project from the re-rendered form")
	}
}

// writeProjectCheckpoint plants a terminal job in `<root>/projects/<slug>/jobs`.
func writeProjectCheckpoint(t *testing.T, root string, slug app.Project, jobID string) {
	t.Helper()

	dir := filepath.Join(root, projectsDirName, string(slug))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	projectStore, err := store.NewFSStore(dir)
	if err != nil {
		t.Fatal(err)
	}

	checkpoint := &store.Checkpoint{
		SchemaVersion: 2,
		JobID:         jobID,
		BestParams:    []float64{1, 2, 3, 4, 5, 6, 7},
		BestCost:      12.5,
		Iterations:    3,
		Termination:   "completed",
		Timestamp:     time.Now(),
		Config:        store.JobConfig{RefPath: "example/Ref.png", Mode: app.ModeJoint, Circles: 1, Iters: 10, PopSize: 30},
	}
	if err := projectStore.SaveCheckpoint(jobID, checkpoint); err != nil {
		t.Fatal(err)
	}
}

// TestCrossProjectDuplicateJobIDIsDiagnosable covers the vanishing job: the job
// manager is keyed by ID alone, so the same UUID under two projects on disk can
// only register once. The refusal is correct — the second copy must not
// overwrite the first — but it used to be reported as a generic "Unable to
// register persisted job" warning, which named one project and never said the
// cause was a collision. The operator saw a job disappear and had nothing to
// go looking for.
func TestCrossProjectDuplicateJobIDIsDiagnosable(t *testing.T) {
	root := t.TempDir()
	jobID := "12345678-1234-4234-8234-123456789abc"
	// Deliberately written out of alphabetical order: the sorted scan order is
	// what makes the winner reproducible, not the order the copies were made.
	writeProjectCheckpoint(t, root, "zulu", jobID)
	writeProjectCheckpoint(t, root, "alpha", jobID)

	persistence, err := store.NewFSStore(root)
	if err != nil {
		t.Fatal(err)
	}

	logs := captureLogs(t)
	server := NewServerWithOptions("localhost:0", persistence, ServerOptions{DataRoot: root})

	// (a) The ID registers exactly once.
	jobs := server.jobManager.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("registered %d jobs, want exactly 1: %+v", len(jobs), jobs)
	}

	if jobs[0].ID != jobID {
		t.Fatalf("registered job ID = %q, want %q", jobs[0].ID, jobID)
	}

	// (b) The alphabetically-first project owns it, on every run.
	if jobs[0].Project != app.Project("alpha") {
		t.Fatalf("surviving job project = %q, want %q", jobs[0].Project, "alpha")
	}

	survivingStore, err := server.storeForJob(jobID)
	if err != nil {
		t.Fatalf("surviving job store: %v", err)
	}

	if alphaStore, _ := server.projects.Get("alpha"); survivingStore != alphaStore {
		t.Fatalf("surviving job did not resolve to alpha's own store")
	}

	// (c) The log identifies the collision and names both projects.
	output := logs.String()
	if !strings.Contains(output, "collision") {
		t.Fatalf("log does not identify the failure as a collision: %s", output)
	}

	if !strings.Contains(output, "owning_project=alpha") {
		t.Fatalf("log does not name the project that kept the ID: %s", output)
	}

	if !strings.Contains(output, "skipped_project=zulu") {
		t.Fatalf("log does not name the project whose copy was dropped: %s", output)
	}

	if !strings.Contains(output, "level=ERROR") {
		t.Fatalf("a job disappearing must be logged at Error, not skipped quietly: %s", output)
	}

	if !strings.Contains(output, "remain on disk") {
		t.Fatalf("log does not say the skipped copy's artifacts are still on disk: %s", output)
	}

	if !strings.Contains(output, jobID) {
		t.Fatalf("log does not name the colliding job ID: %s", output)
	}

	// The skipped project's checkpoint is untouched: detection only, with no
	// rename and no migration.
	if _, err := os.Stat(filepath.Join(root, projectsDirName, "zulu", "jobs", jobID)); err != nil {
		t.Fatalf("the skipped project's artifacts must stay on disk: %v", err)
	}
}
