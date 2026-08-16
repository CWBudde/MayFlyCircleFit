//go:build arm64

package fit

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/cpu"
)

const ssdNEONDisabledHelper = "MAYFLY_TEST_SSD_NEON_DISABLED"

func TestARM64SIMDDispatchMatchesCPUFeatures(t *testing.T) {
	// The environment opt-out outranks the feature check: ASIMD is mandatory on
	// ARM64 and stays reported even when the scalar fallback was requested.
	wantSSD := SSDBackendScalar
	if cpu.ARM64.HasASIMD && !SIMDDisabledByEnv() {
		wantSSD = SSDBackendNEON
	}
	if ActiveSSDBackend != wantSSD {
		t.Fatalf("SSD backend = %s, want %s", ActiveSSDBackend, wantSSD)
	}

	// SAD does not have an ARM64 assembly kernel.
	if ActiveSADBackend != SADBackendScalar {
		t.Fatalf("SAD backend = %s, want scalar", ActiveSADBackend)
	}

	a := []uint8{10, 20, 30, 255}
	b := []uint8{12, 18, 33, 0}
	if got, want := fastSSD(a, b, 4, 1, 1), fastSSD_Scalar(a, b, 4, 1, 1); got != want {
		t.Fatalf("SSD dispatch = %v, want %v", got, want)
	}
	if got, want := fastSAD(a, b, 4, 1, 1), fastSAD_Scalar(a, b, 4, 1, 1); got != want {
		t.Fatalf("scalar SAD dispatch = %v, want %v", got, want)
	}
}

// TestSSDNEONDisabledFallback starts a fresh process because GODEBUG CPU
// overrides are consumed before package initialization. cpu.all=off is used
// because ASIMD is mandatory on ARM64 and is not exposed as an individual Go
// runtime feature override on every supported toolchain.
func TestSSDNEONDisabledFallback(t *testing.T) {
	if os.Getenv(ssdNEONDisabledHelper) == "1" {
		if cpu.ARM64.HasASIMD {
			t.Fatal("cpu.ARM64.HasASIMD is true with GODEBUG=cpu.all=off")
		}
		if ActiveSSDBackend != SSDBackendScalar {
			t.Fatalf("SSD backend = %s, want scalar", ActiveSSDBackend)
		}

		a := []uint8{0, 10, 20, 255}
		b := []uint8{30, 40, 50, 0}
		if got, want := fastSSD(a, b, 4, 1, 1), fastSSD_Scalar(a, b, 4, 1, 1); got != want {
			t.Fatalf("fallback SSD = %v, scalar = %v", got, want)
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestSSDNEONDisabledFallback$")
	cmd.Env = ssdNEONDisabledEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("NEON-disabled subprocess failed: %v\n%s", err, output)
	}
}

func ssdNEONDisabledEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+2)
	godebug := "cpu.all=off"
	for _, entry := range environ {
		if strings.HasPrefix(entry, "GODEBUG=") {
			if value := strings.TrimPrefix(entry, "GODEBUG="); value != "" {
				godebug = value + "," + godebug
			}
			continue
		}
		if strings.HasPrefix(entry, ssdNEONDisabledHelper+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GODEBUG="+godebug, ssdNEONDisabledHelper+"=1")
}
