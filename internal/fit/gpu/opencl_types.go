package gpu

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
