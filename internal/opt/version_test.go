package opt

import (
	"runtime/debug"
	"testing"
)

// TestLibraryVersionMatchesBuildInfo pins the reported version to the module
// the test binary actually links, so a dependency bump that forgets to update
// the documentation still reports the truth.
func TestLibraryVersionMatchesBuildInfo(t *testing.T) {
	t.Parallel()

	got := LibraryVersion()
	if got == "" {
		t.Fatal("LibraryVersion returned an empty string")
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		if got != unknownLibraryVersion {
			t.Fatalf("LibraryVersion() = %q without build info, want %q", got, unknownLibraryVersion)
		}

		return
	}

	want := unknownLibraryVersion

	for _, dep := range info.Deps {
		if dep.Path != mayflyModulePath {
			continue
		}

		want = dep.Version

		if dep.Replace != nil && dep.Replace.Version != "" {
			want = dep.Replace.Version
		}
	}

	if got != want {
		t.Fatalf("LibraryVersion() = %q, want %q", got, want)
	}
}

// TestLibraryVersionIsStable guards the cached lookup: a second call must not
// re-read build info into a different answer.
func TestLibraryVersionIsStable(t *testing.T) {
	t.Parallel()

	if first, second := LibraryVersion(), LibraryVersion(); first != second {
		t.Fatalf("LibraryVersion() returned %q then %q", first, second)
	}
}

// TestCMAESLibraryVersionMatchesBuildInfo pins the CMA-ES checkpoint guard to
// the dependency actually linked into the consumer.
func TestCMAESLibraryVersionMatchesBuildInfo(t *testing.T) {
	t.Parallel()

	got := CMAESLibraryVersion()
	if got == "" {
		t.Fatal("CMAESLibraryVersion returned an empty string")
	}

	info, ok := debug.ReadBuildInfo()
	if !ok {
		if got != unknownLibraryVersion {
			t.Fatalf("CMAESLibraryVersion() = %q without build info, want %q",
				got, unknownLibraryVersion)
		}

		return
	}

	want := unknownLibraryVersion

	for _, dep := range info.Deps {
		if dep.Path != cmaesModulePath {
			continue
		}

		want = dep.Version
		if dep.Replace != nil && dep.Replace.Version != "" {
			want = dep.Replace.Version
		}
	}

	if got != want {
		t.Fatalf("CMAESLibraryVersion() = %q, want %q", got, want)
	}
}

func TestCMAESLibraryVersionIsStable(t *testing.T) {
	t.Parallel()

	if first, second := CMAESLibraryVersion(), CMAESLibraryVersion(); first != second {
		t.Fatalf("CMAESLibraryVersion() returned %q then %q", first, second)
	}
}
