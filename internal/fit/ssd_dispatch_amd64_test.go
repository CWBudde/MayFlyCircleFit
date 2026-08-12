//go:build amd64

package fit

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/cpu"
)

const ssdAVX2DisabledHelper = "MAYFLY_TEST_SSD_AVX2_DISABLED"

// TestSSDAVX2DisabledFallback starts a fresh test process because GODEBUG CPU
// overrides are consumed before package initialization and cannot be tested by
// changing the environment in the current process.
func TestSSDAVX2DisabledFallback(t *testing.T) {
	if os.Getenv(ssdAVX2DisabledHelper) == "1" {
		if cpu.X86.HasAVX2 {
			t.Fatal("cpu.X86.HasAVX2 is true with GODEBUG=cpu.avx2=off")
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

	cmd := exec.Command(os.Args[0], "-test.run=^TestSSDAVX2DisabledFallback$")
	cmd.Env = ssdAVX2DisabledEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("AVX2-disabled subprocess failed: %v\n%s", err, output)
	}
}

func ssdAVX2DisabledEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+2)
	godebug := "cpu.avx2=off"
	for _, entry := range environ {
		if strings.HasPrefix(entry, "GODEBUG=") {
			if value := strings.TrimPrefix(entry, "GODEBUG="); value != "" {
				godebug = value + "," + godebug
			}
			continue
		}
		if strings.HasPrefix(entry, ssdAVX2DisabledHelper+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GODEBUG="+godebug, ssdAVX2DisabledHelper+"=1")
}
