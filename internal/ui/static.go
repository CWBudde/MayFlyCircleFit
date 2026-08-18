package ui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// StaticPrefix is the URL prefix the embedded assets are served under.
const StaticPrefix = "/static/"

// bundleName is the entry bundle produced by scripts/bundle-web.sh. It is
// committed to the repository, exactly like internal/ui/*_templ.go, so that
// `go build ./...` never needs node or npm.
const bundleName = "dashboard.js"

//go:embed static
var staticFS embed.FS

// staticAsset is one embedded file together with the identity the cache
// headers are keyed on.
type staticAsset struct {
	content     []byte
	contentType string
	etag        string
	// version is the short content hash used in asset URLs. A request that
	// asks for the version it gets is safe to cache forever; one that does
	// not must revalidate, because it may be a stale URL from a cached page.
	version string
}

// staticAssets maps a name below StaticPrefix to its embedded content. It is
// built once at init because the embedded set cannot change at runtime.
var staticAssets = loadStaticAssets()

func loadStaticAssets() map[string]staticAsset {
	assets := make(map[string]staticAsset)
	entries, err := fs.ReadDir(staticFS, "static")
	if err != nil {
		// The directory is embedded, so a failure here is a build-time
		// mistake rather than a runtime condition worth reporting.
		panic("ui: reading embedded static assets: " + err.Error())
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		content, err := staticFS.ReadFile(path.Join("static", name))
		if err != nil {
			panic("ui: reading embedded static asset " + name + ": " + err.Error())
		}
		sum := sha256.Sum256(content)
		version := hex.EncodeToString(sum[:])[:16]
		assets[name] = staticAsset{
			content:     content,
			contentType: staticContentType(name),
			etag:        `"` + version + `"`,
			version:     version,
		}
	}
	return assets
}

// staticContentType keeps the served type explicit rather than sniffed. A
// module script served as text/plain is refused by the browser, and content
// sniffing is exactly the guess this boundary should not be making.
func staticContentType(name string) string {
	switch path.Ext(name) {
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".map", ".json":
		return "application/json; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// BundleURL returns the versioned URL of the island bundle. The query
// parameter is the content hash, so a new bundle is a new URL and a browser
// never has to be told to forget the old one.
func BundleURL() string {
	asset, ok := staticAssets[bundleName]
	if !ok {
		return StaticPrefix + bundleName
	}
	return StaticPrefix + bundleName + "?v=" + asset.version
}

// StaticHandler serves the embedded assets under StaticPrefix. Only files that
// are actually embedded are served: there is no directory listing and no path
// below the prefix reaches the filesystem, so the handler adds no new way to
// read the host even though the server trusts its local caller.
func StaticHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(r.URL.Path, StaticPrefix)
		asset, ok := staticAssets[name]
		if !ok || name == "" || strings.Contains(name, "/") {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", asset.contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("ETag", asset.etag)
		if r.URL.Query().Get("v") == asset.version {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}

		// A zero modification time keeps ServeContent from emitting a
		// Last-Modified header the embedded file cannot honestly supply;
		// the ETag above is the validator.
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(asset.content))
	})
}
