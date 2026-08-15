package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// projectsDirName is the container for per-project stores. The legacy layout
// keeps its jobs directly under `<data-root>/jobs`, so the two never collide.
const projectsDirName = "projects"

var (
	errConflictingProject = errors.New("project in the request body and query string disagree")
	errUnknownProject     = errors.New("named projects require a configured data root")
)

// ProjectInfo is the API and UI projection of one project.
type ProjectInfo struct {
	Slug   string `json:"slug"`
	Jobs   int    `json:"jobs"`
	Legacy bool   `json:"legacy,omitempty"`
}

// projectRegistry owns one store per project slug. The store package stays
// project-unaware: each project is just an FSStore rooted at its own
// directory, which is why no changes to internal/store were required.
type projectRegistry struct {
	mu       sync.RWMutex
	dataRoot string
	stores   map[string]store.Store
	legacy   string
}

// newProjectRegistry seeds the registry with the default store and adopts any
// project directories already on disk. defaultStore may be nil when the server
// runs without checkpointing; dataRoot may be empty when the caller supplied a
// store directly, in which case new projects cannot be created.
func newProjectRegistry(dataRoot string, defaultStore store.Store) *projectRegistry {
	registry := &projectRegistry{
		dataRoot: dataRoot,
		stores:   make(map[string]store.Store),
	}
	if defaultStore != nil {
		registry.stores[app.DefaultProject] = defaultStore
		registry.legacy = app.DefaultProject
	}
	registry.discover()
	return registry
}

// discover adopts `<data-root>/projects/*` directories that already exist so a
// server restart sees every project without needing them re-declared.
func (r *projectRegistry) discover() {
	if r.dataRoot == "" {
		return
	}
	entries, err := os.ReadDir(filepath.Join(r.dataRoot, projectsDirName))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		slug := entry.Name()
		if app.ValidateProjectSlug(slug) != nil {
			continue
		}
		if _, err := r.GetOrCreate(slug); err != nil {
			continue
		}
	}
}

// makeProjectDir creates `<data-root>/projects/<slug>` one guarded level at a
// time. store.EnsureSecureSubdir refuses separators, traversal, and symlinks,
// so this is a second gate independent of app.ValidateProjectSlug rather than a
// bare filepath.Join of caller-supplied input.
func (r *projectRegistry) makeProjectDir(slug string) (string, error) {
	container, err := store.EnsureSecureSubdir(r.dataRoot, projectsDirName)
	if err != nil {
		return "", fmt.Errorf("create projects directory: %w", err)
	}
	dir, err := store.EnsureSecureSubdir(container, slug)
	if err != nil {
		return "", fmt.Errorf("create project directory: %w", err)
	}
	return dir, nil
}

// Get returns the store for an existing project.
func (r *projectRegistry) Get(slug string) (store.Store, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.stores[slug]
	return s, ok
}

// GetOrCreate returns the store for a slug, creating it on first use.
func (r *projectRegistry) GetOrCreate(slug string) (store.Store, error) {
	if err := app.ValidateProjectSlug(slug); err != nil {
		return nil, err
	}
	r.mu.RLock()
	existing, ok := r.stores[slug]
	r.mu.RUnlock()
	if ok {
		return existing, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.stores[slug]; ok {
		return existing, nil
	}
	if r.dataRoot == "" {
		return nil, fmt.Errorf("project %q is unknown and no data root is configured", slug)
	}
	dir, err := r.makeProjectDir(slug)
	if err != nil {
		return nil, err
	}
	created, err := store.NewFSStore(dir)
	if err != nil {
		return nil, fmt.Errorf("create project %q: %w", slug, err)
	}
	r.stores[slug] = created
	return created, nil
}

// Default returns the store used when a job names no project.
func (r *projectRegistry) Default() store.Store {
	s, _ := r.Get(app.DefaultProject)
	return s
}

// Slugs returns every known project slug in stable order.
func (r *projectRegistry) Slugs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	slugs := make([]string, 0, len(r.stores))
	for slug := range r.stores {
		slugs = append(slugs, slug)
	}
	sort.Strings(slugs)
	return slugs
}

// All returns a snapshot of every project store, keyed by slug.
func (r *projectRegistry) All() map[string]store.Store {
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot := make(map[string]store.Store, len(r.stores))
	for slug, s := range r.stores {
		snapshot[slug] = s
	}
	return snapshot
}
