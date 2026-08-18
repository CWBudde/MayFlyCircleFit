package gpu

import "errors"

// Both sentinels live in this untagged file so that callers outside the package
// can compare against either one in both builds. Each build returns only the one
// that can actually happen: the stub always fails with ErrNotBuilt, and the GPU
// build never returns it.
var (
	// ErrNotBuilt indicates the binary was built without GPU support.
	ErrNotBuilt = errors.New("opencl support requires building with '-tags gpu'")
	// ErrNoDevices indicates that no usable OpenCL devices were found.
	ErrNoDevices = errors.New("no OpenCL devices found")
)

// DeviceType describes the class of an OpenCL device.
type DeviceType string

const (
	DeviceTypeGPU         DeviceType = "GPU"
	DeviceTypeCPU         DeviceType = "CPU"
	DeviceTypeAccelerator DeviceType = "Accelerator"
	DeviceTypeDefault     DeviceType = "Default"
	DeviceTypeUnknown     DeviceType = "Unknown"
)

// DeviceInfo captures metadata about an OpenCL device.
type DeviceInfo struct {
	Name            string
	Vendor          string
	Version         string
	Type            DeviceType
	MaxComputeUnits uint32
}

// PlatformInfo captures metadata about an OpenCL platform and its devices.
type PlatformInfo struct {
	Name    string
	Vendor  string
	Version string
	Devices []DeviceInfo
}
