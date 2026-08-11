package cmd

import (
	"bytes"
	"strings"
	"testing"
)

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

func TestVersionCommandWritesConfiguredOutput(t *testing.T) {
	originalVersion, originalCommit, originalBuildDate := version, commit, buildDate
	t.Cleanup(func() {
		version, commit, buildDate = originalVersion, originalCommit, originalBuildDate
	})

	version, commit, buildDate = "2.0.0", "abc123", "2026-08-11T10:00:00Z"
	var output bytes.Buffer
	versionCmd.SetOut(&output)
	t.Cleanup(func() { versionCmd.SetOut(nil) })

	versionCmd.Run(versionCmd, nil)
	if got := strings.TrimSpace(output.String()); got != "mayflycirclefit version 2.0.0 (commit abc123, built 2026-08-11T10:00:00Z)" {
		t.Fatalf("version output = %q", got)
	}
}
