package cmd

import (
	"strings"
	"testing"
)

//nolint:paralleltest // reads the package-level command flags, which every test in this package mutates.
func TestServeBackendFlagDefaultsToCPU(t *testing.T) {
	flag := serveCmd.Flags().Lookup("backend")
	if flag == nil {
		t.Fatal("serve command has no --backend flag")
	}

	if flag.DefValue != "cpu" {
		t.Fatalf("--backend default = %q, want cpu", flag.DefValue)
	}

	if !strings.Contains(flag.Usage, "gpu") {
		t.Fatalf("--backend usage = %q, want alias help", flag.Usage)
	}
}
