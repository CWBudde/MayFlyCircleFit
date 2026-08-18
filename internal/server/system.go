package server

import (
	"errors"
	"runtime"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/fit/gpu"
	"github.com/cwbudde/mayflycirclefit/internal/fit/renderer"
)

// PlatformDiscovery reports OpenCL platforms visible to the process.
var platformDiscovery = gpu.EnumeratePlatforms

const (
	// GPUStateAvailable means at least one OpenCL platform reported at least one device.
	GPUStateAvailable = "available"
	// GPUStateNoDevices means no usable OpenCL devices were found.
	GPUStateNoDevices = "no_devices"
	// GPUStateNotBuilt means the binary lacks GPU support.
	GPUStateNotBuilt = "not_built"
	// GPUStateError means OpenCL probing failed unexpectedly.
	GPUStateError = "error"
)

// BuildMetadata records what the binary was built from.
// It is injected from cmd so server code does not depend on command internals.
type BuildMetadata struct {
	Version   string
	Commit    string
	BuildDate string
}

// HostFacts is the JSON payload for GET /api/v1/system.
type HostFacts struct {
	Version            string             `json:"version"`
	Commit             string             `json:"commit"`
	BuildDate          string             `json:"buildDate"`
	GOOS               string             `json:"goos"`
	GOARCH             string             `json:"goarch"`
	GOMAXPROCS         int                `json:"gomaxProcs"`
	GoVersion          string             `json:"goVersion"`
	SIMD               string             `json:"simd"`
	ActiveSSDKernel    string             `json:"activeSSDKernel"`
	ActiveSADKernel    string             `json:"activeSADKernel"`
	CompositingBackend string             `json:"compositingBackend"`
	FastCompositing    string             `json:"fastCompositingBackend"`
	SupportedBackends  []renderer.Backend `json:"supportedBackends"`
	GPU                GPUInfo            `json:"gpu"`
}

// GPUInfo reports OpenCL availability and probe state.
type GPUInfo struct {
	State     string             `json:"state"`
	Error     string             `json:"error,omitempty"`
	Platforms []gpu.PlatformInfo `json:"platforms,omitempty"`
}

func normalizeBuildMetadata(meta BuildMetadata) BuildMetadata {
	if meta.Version == "" {
		meta.Version = "dev"
	}
	if meta.Commit == "" {
		meta.Commit = "unknown"
	}
	if meta.BuildDate == "" {
		meta.BuildDate = "unknown"
	}
	return meta
}

func collectGPUInfo() GPUInfo {
	platforms, err := platformDiscovery()
	if err != nil {
		switch {
		case errors.Is(err, gpu.ErrNotBuilt):
			return GPUInfo{State: GPUStateNotBuilt, Error: err.Error()}
		case errors.Is(err, gpu.ErrNoDevices):
			return GPUInfo{State: GPUStateNoDevices, Error: err.Error()}
		default:
			return GPUInfo{State: GPUStateError, Error: err.Error()}
		}
	}

	deviceCount := 0
	for _, platform := range platforms {
		deviceCount += len(platform.Devices)
	}
	if deviceCount == 0 {
		return GPUInfo{State: GPUStateNoDevices}
	}
	return GPUInfo{State: GPUStateAvailable, Platforms: platforms}
}

// HostFactsFromMetadata gathers current host/runtime capabilities.
func HostFactsFromMetadata(meta BuildMetadata) HostFacts {
	meta = normalizeBuildMetadata(meta)
	return HostFacts{
		Version:            meta.Version,
		Commit:             meta.Commit,
		BuildDate:          meta.BuildDate,
		GOOS:               runtime.GOOS,
		GOARCH:             runtime.GOARCH,
		GOMAXPROCS:         runtime.GOMAXPROCS(0),
		GoVersion:          runtime.Version(),
		SIMD:               fit.Tier().String(),
		ActiveSSDKernel:    fit.ActiveSSDKernel().String(),
		ActiveSADKernel:    fit.ActiveSADKernel().String(),
		CompositingBackend: renderer.CompositingBackend(),
		FastCompositing:    renderer.FastCompositingBackend(),
		SupportedBackends:  renderer.SupportedBackends(),
		GPU:                collectGPUInfo(),
	}
}
