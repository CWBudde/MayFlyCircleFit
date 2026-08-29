package cmd

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/server"
)

//nolint:paralleltest // mutates the package-level version strings and command output, shared by every test here.
func TestVersionInfo(t *testing.T) {
	originalVersion, originalCommit, originalBuildDate := version, commit, buildDate

	t.Cleanup(func() {
		version, commit, buildDate = originalVersion, originalCommit, originalBuildDate
	})

	version = "1.2.3-rc.1"
	commit = "0123456789abcdef"
	buildDate = "2026-08-11T12:34:56+02:00"

	want := "1.2.3-rc.1 (commit 0123456789abcdef, built 2026-08-11T12:34:56+02:00)"
	if got := versionInfo(); got != want {
		t.Fatalf("versionInfo() = %q, want %q", got, want)
	}
}

//nolint:paralleltest // mutates the package-level version strings and command output, shared by every test here.
func TestVersionCommandWritesConfiguredOutput(t *testing.T) {
	originalVersion, originalCommit, originalBuildDate := version, commit, buildDate

	t.Cleanup(func() {
		version, commit, buildDate = originalVersion, originalCommit, originalBuildDate
	})

	version, commit, buildDate = "2.0.0", "abc123", "2026-08-11T10:00:00Z"
	var output bytes.Buffer
	versionCmd.SetOut(&output)
	t.Cleanup(func() { versionCmd.SetOut(nil) })

	err := runVersion(versionCmd, nil)
	if err != nil {
		t.Fatalf("runVersion: %v", err)
	}

	const want = "circlefit version 2.0.0 (commit abc123, built 2026-08-11T10:00:00Z)"

	if got := strings.TrimSpace(output.String()); got != want {
		t.Fatalf("version output = %q", got)
	}
}

//nolint:paralleltest // mutates the package-level version strings and command output, shared by every test here.
func TestVersionCommandVerboseOutput(t *testing.T) {
	originalVersion, originalCommit, originalBuildDate := version, commit, buildDate
	originalVerbose := versionVerbose

	t.Cleanup(func() {
		version, commit, buildDate = originalVersion, originalCommit, originalBuildDate
		versionVerbose = originalVerbose
	})

	version, commit, buildDate = "2.1.0", "123456", "2026-08-11T11:11:11Z"
	versionVerbose = true

	var output bytes.Buffer
	versionCmd.SetOut(&output)
	t.Cleanup(func() { versionCmd.SetOut(nil) })

	err := runVersion(versionCmd, nil)
	if err != nil {
		t.Fatalf("runVersion: %v", err)
	}

	var facts server.HostFacts

	err = json.Unmarshal(output.Bytes(), &facts)
	if err != nil {
		t.Fatalf("decode verbose payload: %v", err)
	}

	if facts.Version != "2.1.0" || facts.Commit != "123456" || facts.BuildDate != "2026-08-11T11:11:11Z" {
		t.Fatalf("host facts version/commit/build = (%q, %q, %q), want (2.1.0, 123456, 2026-08-11T11:11:11Z)", facts.Version, facts.Commit, facts.BuildDate)
	}

	if facts.GOOS != runtime.GOOS || facts.GOARCH != runtime.GOARCH {
		t.Fatalf("host facts goos/goarch = (%q, %q), want (%q, %q)", facts.GOOS, facts.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
}
