package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit/gpu"
)

func TestHostFactsFromMetadataDefaultsToDevMetadata(t *testing.T) {
	originalDiscovery := platformDiscovery
	t.Cleanup(func() {
		platformDiscovery = originalDiscovery
	})
	platformDiscovery = func() ([]gpu.PlatformInfo, error) {
		return nil, nil
	}

	facts := HostFactsFromMetadata(BuildMetadata{})
	if facts.Version != "dev" || facts.Commit != "unknown" || facts.BuildDate != "unknown" {
		t.Fatalf("build metadata = (%q, %q, %q), want dev/unknown/unknown", facts.Version, facts.Commit, facts.BuildDate)
	}
	if facts.GPU.State != GPUStateNoDevices {
		t.Fatalf("GPU state = %q, want %q", facts.GPU.State, GPUStateNoDevices)
	}
}

func TestHostFactsFromMetadataReportsGPUStates(t *testing.T) {
	platforms := []gpu.PlatformInfo{{
		Name:    "host",
		Vendor:  "vendor",
		Version: "1.0",
		Devices: []gpu.DeviceInfo{{
			Name:            "device",
			Vendor:          "vendor",
			Version:         "1.0",
			Type:            gpu.DeviceTypeGPU,
			MaxComputeUnits: 16,
		}},
	}}
	cases := []struct {
		name string
		fn   func() ([]gpu.PlatformInfo, error)
		want string
	}{
		{
			name: "not built",
			fn: func() ([]gpu.PlatformInfo, error) {
				return nil, gpu.ErrNotBuilt
			},
			want: GPUStateNotBuilt,
		},
		{
			name: "no devices",
			fn: func() ([]gpu.PlatformInfo, error) {
				return nil, gpu.ErrNoDevices
			},
			want: GPUStateNoDevices,
		},
		{
			name: "available",
			fn: func() ([]gpu.PlatformInfo, error) {
				return platforms, nil
			},
			want: GPUStateAvailable,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			originalDiscovery := platformDiscovery
			t.Cleanup(func() {
				platformDiscovery = originalDiscovery
			})
			platformDiscovery = testCase.fn

			facts := HostFactsFromMetadata(BuildMetadata{Version: "v1", Commit: "abc", BuildDate: "today"})
			if facts.GPU.State != testCase.want {
				t.Fatalf("state = %q, want %q", facts.GPU.State, testCase.want)
			}
			if testCase.want == GPUStateAvailable && len(facts.GPU.Platforms) != len(platforms) {
				t.Fatalf("platforms len = %d, want %d", len(facts.GPU.Platforms), len(platforms))
			}
		})
	}
}

func TestHandleSystem(t *testing.T) {
	testServer := NewServer("localhost:0", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system", nil)
	resp := httptest.NewRecorder()

	testServer.handleSystem(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusOK)
	}

	var facts HostFacts
	if err := json.NewDecoder(resp.Body).Decode(&facts); err != nil {
		t.Fatalf("decode system facts: %v", err)
	}
	if facts.Version != "dev" || facts.Commit != "unknown" || facts.BuildDate != "unknown" {
		t.Fatalf("build metadata = (%q, %q, %q)", facts.Version, facts.Commit, facts.BuildDate)
	}
	if facts.GOOS != runtime.GOOS || facts.GOARCH != runtime.GOARCH {
		t.Fatalf("facts runtime = (%q, %q), want (%q, %q)", facts.GOOS, facts.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
}

func TestHandleSystemMethodNotAllowed(t *testing.T) {
	testServer := NewServer("localhost:0", nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system", nil)
	resp := httptest.NewRecorder()

	testServer.handleSystem(resp, req)

	if resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusMethodNotAllowed)
	}
}
