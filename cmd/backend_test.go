package cmd

import (
	"strings"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

func TestParseBackendFlag(t *testing.T) {
	t.Run("canonical", func(t *testing.T) {
		backend, err := parseBackendFlag("opencl")
		if err != nil {
			t.Fatalf("parseBackendFlag() error = %v", err)
		}

		if backend != app.BackendOpenCL {
			t.Fatalf("backend = %q, want %q", backend, app.BackendOpenCL)
		}
	})
	t.Run("alias", func(t *testing.T) {
		for _, raw := range []string{"gpu", "cl", "GPU", "OPENCL"} {
			backend, err := parseBackendFlag(raw)
			if err != nil {
				t.Fatalf("parseBackendFlag(%q) error = %v", raw, err)
			}

			if backend != app.BackendOpenCL {
				t.Fatalf("parseBackendFlag(%q) = %q, want %q", raw, backend, app.BackendOpenCL)
			}
		}
	})
	t.Run("cpu", func(t *testing.T) {
		backend, err := parseBackendFlag("cpu")
		if err != nil {
			t.Fatalf("parseBackendFlag() error = %v", err)
		}

		if backend != app.BackendCPU {
			t.Fatalf("backend = %q, want %q", backend, app.BackendCPU)
		}
	})
	t.Run("empty_is_cpu", func(t *testing.T) {
		backend, err := parseBackendFlag("")
		if err != nil {
			t.Fatalf("parseBackendFlag() error = %v", err)
		}

		if backend != app.BackendCPU {
			t.Fatalf("backend = %q, want %q", backend, app.BackendCPU)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		backend, err := parseBackendFlag("quantum")
		if err == nil {
			t.Fatalf("parseBackendFlag() = %q, want error", backend)
		}

		if !strings.Contains(err.Error(), "cpu, opencl") {
			t.Fatalf("error = %q, want backend message", err)
		}
	})
}
