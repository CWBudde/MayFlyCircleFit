package opt

import (
	"runtime/debug"
	"sync"
)

// mayflyModulePath is the module the adapter optimizes with. Reading the
// version from the build info rather than a hand-maintained constant keeps the
// reported value honest across a dependency bump: a constant can drift from
// go.mod, a build-info lookup cannot.
const mayflyModulePath = "github.com/cwbudde/mayfly"

// unknownLibraryVersion is reported when the build carries no module
// information, which happens for binaries built with -buildvcs=false in some
// toolchains and for tests run from an unbuilt module cache.
const unknownLibraryVersion = "unknown"

var libraryVersion = sync.OnceValue(func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return unknownLibraryVersion
	}

	for _, dep := range info.Deps {
		if dep.Path != mayflyModulePath {
			continue
		}
		// A replace directive points at the module actually compiled in, which
		// is the one whose behavior the cost reflects.
		if dep.Replace != nil && dep.Replace.Version != "" {
			return dep.Replace.Version
		}

		if dep.Version != "" {
			return dep.Version
		}
	}

	return unknownLibraryVersion
})

// LibraryVersion reports the MayFly module version compiled into this binary.
//
// It exists because the optimizer version is a comparability boundary: v0.5.0
// scales the crossover offspring count with the population where v0.4.0 held it
// at an absolute 20, so two campaigns with identical seeds and renderer
// settings are not comparable across the bump. Nothing else in a run record
// distinguishes them, so the version has to be reported somewhere a run log can
// capture it.
func LibraryVersion() string { return libraryVersion() }
