package server

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// projectsDirName is the container for per-project stores. The legacy layout
// keeps its jobs directly under `<data-root>/jobs`, so the two never collide.
const projectsDirName = "projects"

var (
	errConflictingProject = errors.New("project in the request body and query string disagree")
	// errUnknownProject reports a slug that the registry cannot resolve. It is
	// deliberately not a fallback to the default project: see storeForSlug.
	errUnknownProject = errors.New("unknown project")
)

// projectRegistry owns one store per project slug. The store package stays
// project-unaware: each project is just an FSStore rooted at its own
// directory, which is why no changes to internal/store were required.
type projectRegistry struct {
	mu       sync.RWMutex
	dataRoot string
	stores   map[app.Project]store.Store
}

// newProjectRegistry seeds the registry with the default store and adopts any
// project directories already on disk. defaultStore may be nil when the server
// runs without checkpointing; dataRoot may be empty when the caller supplied a
// store directly, in which case new projects cannot be created.
func newProjectRegistry(dataRoot string, defaultStore store.Store) *projectRegistry {
	registry := &projectRegistry{
		dataRoot: dataRoot,
		stores:   make(map[app.Project]store.Store),
	}
	if defaultStore != nil {
		registry.stores[app.DefaultProject] = defaultStore
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

	container := filepath.Join(r.dataRoot, projectsDirName)

	entries, err := os.ReadDir(container)
	if err != nil {
		// A missing container is the ordinary pre-project layout, not a fault.
		if !errors.Is(err, os.ErrNotExist) {
			slog.Error("Unable to read the projects container; no project is visible",
				"directory", container, "error", err)
		}

		return
	}

	for _, entry := range entries {
		// Boundary: a directory name read off disk is untrusted input. It becomes
		// an app.Project only here, immediately before ValidateProjectSlug.
		slug := app.Project(entry.Name())
		if !entry.IsDir() {
			// Stray files never were projects, so this is informational only.
			slog.Debug("Ignoring non-directory entry in the projects container",
				"directory", container, "entry", string(slug))

			continue
		}

		err := app.ValidateProjectSlug(slug)
		if err != nil {
			slog.Warn("Ignoring project directory with an unusable name; its jobs stay hidden",
				"directory", filepath.Join(container, string(slug)), "error", err)

			continue
		}

		if slug == app.DefaultProject {
			// The default project is always the legacy `<data-root>/jobs` tree,
			// so this directory can only be a stray one. Adopting it would give
			// the same name two meanings, differing by whether the server was
			// built with an injected store; refusing it keeps the alias single.
			slog.Warn("Ignoring the reserved default project directory; the default project is the legacy jobs tree",
				"directory", filepath.Join(container, string(slug)))

			continue
		}

		if _, err := r.GetOrCreate(slug); err != nil {
			// The directory is a valid project that failed to open. Its jobs
			// disappear from the API and UI, so this is a fault, not a skip.
			slog.Error("Unable to adopt project directory; its jobs stay hidden",
				"directory", filepath.Join(container, string(slug)), "error", err)

			continue
		}
	}
}

// makeProjectDir creates `<data-root>/projects/<slug>` one guarded level at a
// time. store.EnsureSecureSubdir refuses separators, traversal, and symlinks,
// so this is a second gate independent of app.ValidateProjectSlug rather than a
// bare filepath.Join of caller-supplied input.
func (r *projectRegistry) makeProjectDir(slug app.Project) (string, error) {
	container, err := store.EnsureSecureSubdir(r.dataRoot, projectsDirName)
	if err != nil {
		return "", fmt.Errorf("create projects directory: %w", err)
	}

	dir, err := store.EnsureSecureSubdir(container, string(slug))
	if err != nil {
		return "", fmt.Errorf("create project directory: %w", err)
	}

	return dir, nil
}

// Get returns the store for an existing project.
func (r *projectRegistry) Get(slug app.Project) (store.Store, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.stores[slug]

	return s, ok
}

// GetOrCreate returns the store for a slug, creating it on first use.
func (r *projectRegistry) GetOrCreate(slug app.Project) (store.Store, error) {
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

// Slugs returns every known project slug in stable order.
func (r *projectRegistry) Slugs() []app.Project {
	r.mu.RLock()
	defer r.mu.RUnlock()

	slugs := make([]app.Project, 0, len(r.stores))
	for slug := range r.stores {
		slugs = append(slugs, slug)
	}

	slices.Sort(slugs)

	return slugs
}
