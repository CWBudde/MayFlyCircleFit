//go:build gpu

package gpu

/*
#cgo LDFLAGS: -lOpenCL
#define CL_TARGET_OPENCL_VERSION 120
#define CL_USE_DEPRECATED_OPENCL_1_2_APIS
#include <CL/cl.h>

static const char* mayfly_cl_error_string(cl_int status) {
	switch (status) {
	case CL_SUCCESS: return "CL_SUCCESS";
	case CL_DEVICE_NOT_FOUND: return "CL_DEVICE_NOT_FOUND";
	case CL_DEVICE_NOT_AVAILABLE: return "CL_DEVICE_NOT_AVAILABLE";
	case CL_COMPILER_NOT_AVAILABLE: return "CL_COMPILER_NOT_AVAILABLE";
	case CL_MEM_OBJECT_ALLOCATION_FAILURE: return "CL_MEM_OBJECT_ALLOCATION_FAILURE";
	case CL_OUT_OF_RESOURCES: return "CL_OUT_OF_RESOURCES";
	case CL_OUT_OF_HOST_MEMORY: return "CL_OUT_OF_HOST_MEMORY";
	case CL_PROFILING_INFO_NOT_AVAILABLE: return "CL_PROFILING_INFO_NOT_AVAILABLE";
	case CL_MEM_COPY_OVERLAP: return "CL_MEM_COPY_OVERLAP";
	case CL_IMAGE_FORMAT_MISMATCH: return "CL_IMAGE_FORMAT_MISMATCH";
	case CL_IMAGE_FORMAT_NOT_SUPPORTED: return "CL_IMAGE_FORMAT_NOT_SUPPORTED";
	case CL_BUILD_PROGRAM_FAILURE: return "CL_BUILD_PROGRAM_FAILURE";
	case CL_MAP_FAILURE: return "CL_MAP_FAILURE";
	case CL_INVALID_VALUE: return "CL_INVALID_VALUE";
	case CL_INVALID_DEVICE_TYPE: return "CL_INVALID_DEVICE_TYPE";
	case CL_INVALID_PLATFORM: return "CL_INVALID_PLATFORM";
	case CL_INVALID_DEVICE: return "CL_INVALID_DEVICE";
	case CL_INVALID_CONTEXT: return "CL_INVALID_CONTEXT";
	case CL_INVALID_QUEUE_PROPERTIES: return "CL_INVALID_QUEUE_PROPERTIES";
	case CL_INVALID_COMMAND_QUEUE: return "CL_INVALID_COMMAND_QUEUE";
	case CL_INVALID_HOST_PTR: return "CL_INVALID_HOST_PTR";
	case CL_INVALID_MEM_OBJECT: return "CL_INVALID_MEM_OBJECT";
	case CL_INVALID_IMAGE_FORMAT_DESCRIPTOR: return "CL_INVALID_IMAGE_FORMAT_DESCRIPTOR";
	case CL_INVALID_IMAGE_SIZE: return "CL_INVALID_IMAGE_SIZE";
	case CL_INVALID_SAMPLER: return "CL_INVALID_SAMPLER";
	case CL_INVALID_BINARY: return "CL_INVALID_BINARY";
	case CL_INVALID_BUILD_OPTIONS: return "CL_INVALID_BUILD_OPTIONS";
	case CL_INVALID_PROGRAM: return "CL_INVALID_PROGRAM";
	case CL_INVALID_PROGRAM_EXECUTABLE: return "CL_INVALID_PROGRAM_EXECUTABLE";
	case CL_INVALID_KERNEL_NAME: return "CL_INVALID_KERNEL_NAME";
	case CL_INVALID_KERNEL_DEFINITION: return "CL_INVALID_KERNEL_DEFINITION";
	case CL_INVALID_KERNEL: return "CL_INVALID_KERNEL";
	case CL_INVALID_ARG_INDEX: return "CL_INVALID_ARG_INDEX";
	case CL_INVALID_ARG_VALUE: return "CL_INVALID_ARG_VALUE";
	case CL_INVALID_ARG_SIZE: return "CL_INVALID_ARG_SIZE";
	case CL_INVALID_KERNEL_ARGS: return "CL_INVALID_KERNEL_ARGS";
	case CL_INVALID_WORK_DIMENSION: return "CL_INVALID_WORK_DIMENSION";
	case CL_INVALID_WORK_GROUP_SIZE: return "CL_INVALID_WORK_GROUP_SIZE";
	case CL_INVALID_WORK_ITEM_SIZE: return "CL_INVALID_WORK_ITEM_SIZE";
	case CL_INVALID_GLOBAL_OFFSET: return "CL_INVALID_GLOBAL_OFFSET";
	case CL_INVALID_EVENT_WAIT_LIST: return "CL_INVALID_EVENT_WAIT_LIST";
	case CL_INVALID_EVENT: return "CL_INVALID_EVENT";
	case CL_INVALID_OPERATION: return "CL_INVALID_OPERATION";
	case CL_INVALID_GL_OBJECT: return "CL_INVALID_GL_OBJECT";
	case CL_INVALID_BUFFER_SIZE: return "CL_INVALID_BUFFER_SIZE";
	case CL_INVALID_MIP_LEVEL: return "CL_INVALID_MIP_LEVEL";
	default: return "CL_UNKNOWN_ERROR";
	}
}

static cl_command_queue mayfly_create_queue(cl_context ctx, cl_device_id device, cl_int *status) {
#if CL_TARGET_OPENCL_VERSION >= 200
	const cl_queue_properties props[] = {0};
	return clCreateCommandQueueWithProperties(ctx, device, props, status);
#else
	return clCreateCommandQueue(ctx, device, 0, status);
#endif
}
*/
import "C"

import (
	"errors"
	"fmt"
	"unsafe"
)

// Runtime owns the OpenCL context and command queue.
type Runtime struct {
	platformID C.cl_platform_id
	deviceID   C.cl_device_id
	context    C.cl_context
	queue      C.cl_command_queue
	Platform   PlatformInfo
	Device     DeviceInfo
}

// ErrNoDevices indicates that no usable OpenCL devices were found.
var ErrNoDevices = errors.New("no OpenCL devices found")

// InitOpenCL selects a device (GPU preferred, then CPU) and creates a context.
func InitOpenCL() (*Runtime, error) {
	records, err := enumeratePlatformRecords()
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, ErrNoDevices
	}

	type selection struct {
		platform platformRecord
		device   deviceRecord
	}

	var chosen *selection

	// Prefer GPU
	for _, platform := range records {
		for _, device := range platform.devices {
			if device.info.Type == DeviceTypeGPU {
				chosen = &selection{platform: platform, device: device}
				break
			}
		}
		if chosen != nil {
			break
		}
	}

	// Fallback to CPU
	if chosen == nil {
		for _, platform := range records {
			for _, device := range platform.devices {
				if device.info.Type == DeviceTypeCPU {
					chosen = &selection{platform: platform, device: device}
					break
				}
			}
			if chosen != nil {
				break
			}
		}
	}

	// Fallback to first available
	if chosen == nil {
		for _, platform := range records {
			if len(platform.devices) == 0 {
				continue
			}
			chosen = &selection{platform: platform, device: platform.devices[0]}
			break
		}
	}

	if chosen == nil {
		return nil, ErrNoDevices
	}

	var status C.cl_int

	context := C.clCreateContext(nil, 1, &chosen.device.id, nil, nil, &status)
	if status != C.CL_SUCCESS {
		return nil, statusError("clCreateContext", status)
	}

	queue := C.mayfly_create_queue(context, chosen.device.id, &status)
	if status != C.CL_SUCCESS {
		C.clReleaseContext(context)
		return nil, statusError("clCreateCommandQueue", status)
	}

	return &Runtime{
		platformID: chosen.platform.id,
		deviceID:   chosen.device.id,
		context:    context,
		queue:      queue,
		Platform:   chosen.platform.info,
		Device:     chosen.device.info,
	}, nil
}

// Close releases OpenCL resources.
func (r *Runtime) Close() {
	if r == nil {
		return
	}
	if r.queue != nil {
		C.clReleaseCommandQueue(r.queue)
		r.queue = nil
	}
	if r.context != nil {
		C.clReleaseContext(r.context)
		r.context = nil
	}
}

// EnumeratePlatforms returns discovered platforms with their devices.
func EnumeratePlatforms() ([]PlatformInfo, error) {
	records, err := enumeratePlatformRecords()
	if err != nil {
		return nil, err
	}

	out := make([]PlatformInfo, len(records))
	for i, platform := range records {
		devices := make([]DeviceInfo, len(platform.devices))
		for j, device := range platform.devices {
			devices[j] = device.info
		}
		info := platform.info
		info.Devices = devices
		out[i] = info
	}
	return out, nil
}

type platformRecord struct {
	id      C.cl_platform_id
	info    PlatformInfo
	devices []deviceRecord
}

type deviceRecord struct {
	id   C.cl_device_id
	info DeviceInfo
}

func enumeratePlatformRecords() ([]platformRecord, error) {
	var count C.cl_uint
	status := C.clGetPlatformIDs(0, nil, &count)
	if status != C.CL_SUCCESS {
		return nil, statusError("clGetPlatformIDs(count)", status)
	}
	if count == 0 {
		return nil, nil
	}

	platformIDs := make([]C.cl_platform_id, int(count))
	status = C.clGetPlatformIDs(count, &platformIDs[0], nil)
	if status != C.CL_SUCCESS {
		return nil, statusError("clGetPlatformIDs(list)", status)
	}

	records := make([]platformRecord, 0, int(count))
	for _, pid := range platformIDs {
		name, err := getPlatformString(pid, C.CL_PLATFORM_NAME)
		if err != nil {
			return nil, err
		}
		vendor, err := getPlatformString(pid, C.CL_PLATFORM_VENDOR)
		if err != nil {
			return nil, err
		}
		version, err := getPlatformString(pid, C.CL_PLATFORM_VERSION)
		if err != nil {
			return nil, err
		}

		rec := platformRecord{
			id: pid,
			info: PlatformInfo{
				Name:    name,
				Vendor:  vendor,
				Version: version,
			},
		}

		devices, err := enumerateDevices(pid)
		if err != nil {
			if errors.Is(err, ErrNoDevices) {
				records = append(records, rec)
				continue
			}
			return nil, err
		}

		rec.devices = devices
		rec.info.Devices = make([]DeviceInfo, len(devices))
		for i, device := range devices {
			rec.info.Devices[i] = device.info
		}

		records = append(records, rec)
	}

	return records, nil
}

func enumerateDevices(platform C.cl_platform_id) ([]deviceRecord, error) {
	var count C.cl_uint
	status := C.clGetDeviceIDs(platform, C.CL_DEVICE_TYPE_ALL, 0, nil, &count)
	if status == C.CL_DEVICE_NOT_FOUND {
		return nil, ErrNoDevices
	}
	if status != C.CL_SUCCESS {
		return nil, statusError("clGetDeviceIDs(count)", status)
	}
	if count == 0 {
		return nil, ErrNoDevices
	}

	deviceIDs := make([]C.cl_device_id, int(count))
	status = C.clGetDeviceIDs(platform, C.CL_DEVICE_TYPE_ALL, count, &deviceIDs[0], nil)
	if status != C.CL_SUCCESS {
		return nil, statusError("clGetDeviceIDs(list)", status)
	}

	devices := make([]deviceRecord, 0, int(count))
	for _, id := range deviceIDs {
		info, err := buildDeviceInfo(id)
		if err != nil {
			return nil, err
		}
		devices = append(devices, deviceRecord{
			id:   id,
			info: info,
		})
	}

	return devices, nil
}

func buildDeviceInfo(id C.cl_device_id) (DeviceInfo, error) {
	name, err := getDeviceString(id, C.CL_DEVICE_NAME)
	if err != nil {
		return DeviceInfo{}, err
	}
	vendor, err := getDeviceString(id, C.CL_DEVICE_VENDOR)
	if err != nil {
		return DeviceInfo{}, err
	}
	version, err := getDeviceString(id, C.CL_DEVICE_VERSION)
	if err != nil {
		return DeviceInfo{}, err
	}

	var rawType C.cl_device_type
	status := C.clGetDeviceInfo(id, C.CL_DEVICE_TYPE, C.size_t(unsafe.Sizeof(rawType)), unsafe.Pointer(&rawType), nil)
	if status != C.CL_SUCCESS {
		return DeviceInfo{}, statusError("clGetDeviceInfo(type)", status)
	}

	var computeUnits C.cl_uint
	status = C.clGetDeviceInfo(id, C.CL_DEVICE_MAX_COMPUTE_UNITS, C.size_t(unsafe.Sizeof(computeUnits)), unsafe.Pointer(&computeUnits), nil)
	if status != C.CL_SUCCESS {
		return DeviceInfo{}, statusError("clGetDeviceInfo(computeUnits)", status)
	}

	return DeviceInfo{
		Name:            name,
		Vendor:          vendor,
		Version:         version,
		Type:            mapDeviceType(rawType),
		MaxComputeUnits: uint32(computeUnits),
	}, nil
}

func getPlatformString(id C.cl_platform_id, param C.cl_platform_info) (string, error) {
	var size C.size_t
	status := C.clGetPlatformInfo(id, param, 0, nil, &size)
	if status != C.CL_SUCCESS {
		return "", statusError("clGetPlatformInfo(size)", status)
	}
	if size == 0 {
		return "", nil
	}

	buf := make([]byte, int(size))
	status = C.clGetPlatformInfo(id, param, size, unsafe.Pointer(&buf[0]), nil)
	if status != C.CL_SUCCESS {
		return "", statusError("clGetPlatformInfo(value)", status)
	}

	return trimNull(buf), nil
}

func getDeviceString(id C.cl_device_id, param C.cl_device_info) (string, error) {
	var size C.size_t
	status := C.clGetDeviceInfo(id, param, 0, nil, &size)
	if status != C.CL_SUCCESS {
		return "", statusError("clGetDeviceInfo(size)", status)
	}
	if size == 0 {
		return "", nil
	}

	buf := make([]byte, int(size))
	status = C.clGetDeviceInfo(id, param, size, unsafe.Pointer(&buf[0]), nil)
	if status != C.CL_SUCCESS {
		return "", statusError("clGetDeviceInfo(value)", status)
	}

	return trimNull(buf), nil
}

func trimNull(buf []byte) string {
	if len(buf) == 0 {
		return ""
	}
	if buf[len(buf)-1] == 0 {
		buf = buf[:len(buf)-1]
	}
	return string(buf)
}

func mapDeviceType(dt C.cl_device_type) DeviceType {
	switch {
	case dt&C.CL_DEVICE_TYPE_GPU != 0:
		return DeviceTypeGPU
	case dt&C.CL_DEVICE_TYPE_CPU != 0:
		return DeviceTypeCPU
	case dt&C.CL_DEVICE_TYPE_ACCELERATOR != 0:
		return DeviceTypeAccelerator
	case dt&C.CL_DEVICE_TYPE_DEFAULT != 0:
		return DeviceTypeDefault
	default:
		return DeviceTypeUnknown
	}
}

func statusError(prefix string, status C.cl_int) error {
	return fmt.Errorf("%s: %s (%d)", prefix, C.GoString(C.mayfly_cl_error_string(status)), int(status))
}
