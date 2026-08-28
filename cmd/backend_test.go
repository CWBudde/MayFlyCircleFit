package cmd

import (
	"strings"
	"testing"

	"github.com/cwbudde/circlefit/internal/app"
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

// TestBackendProvenanceNote pins what a one-shot run prints when the backend it
// got is not the backend it asked for. A CLI run has no job resource, so this
// line is the only place the substitution is recorded.
func TestBackendProvenanceNote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		requested app.Backend
		effective app.Backend
		degraded  bool
		want      string
	}{
		{
			name:      "clean run says nothing",
			requested: app.BackendOpenCL,
			effective: app.BackendOpenCL,
			want:      "",
		},
		{
			name:      "cpu run says nothing",
			requested: app.BackendCPU,
			effective: app.BackendCPU,
			want:      "",
		},
		{
			name:      "fallback names the request it could not honour",
			requested: app.BackendOpenCL,
			effective: app.BackendCPU,
			want:      "Backend: cpu (requested opencl, unavailable)",
		},
		{
			name:      "degradation outranks a matching backend",
			requested: app.BackendOpenCL,
			effective: app.BackendOpenCL,
			degraded:  true,
			want:      "Backend: opencl (degraded to CPU mid-run)",
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got := backendProvenanceNote(testCase.requested, testCase.effective, testCase.degraded)
			if testCase.want == "" {
				if got != "" {
					t.Fatalf("note = %q, want empty", got)
				}

				return
			}

			if !strings.HasPrefix(got, testCase.want) {
				t.Fatalf("note = %q, want prefix %q", got, testCase.want)
			}

			if !strings.Contains(got, "comparable") {
				t.Fatalf("note = %q, want it to say the cost is not comparable", got)
			}
		})
	}
}
