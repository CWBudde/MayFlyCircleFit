//go:build arm64

package renderer

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/cpu"
)

const compositeNEONDisabledHelper = "MAYFLY_TEST_COMPOSITE_NEON_DISABLED"

func TestCompositeSpanARM64DispatchMatchesCPUFeatures(t *testing.T) {
	want := "scalar"
	if cpu.ARM64.HasASIMD {
		want = "neon"
	}
	if compositeSpanBackend != want {
		t.Fatalf("composite backend = %s, want %s", compositeSpanBackend, want)
	}
}

func TestCompositeSpanNEONDisabledFallback(t *testing.T) {
	if os.Getenv(compositeNEONDisabledHelper) == "1" {
		if cpu.ARM64.HasASIMD {
			t.Fatal("cpu.ARM64.HasASIMD is true with GODEBUG=cpu.all=off")
		}
		if compositeSpanBackend != "scalar" {
			t.Fatalf("composite backend = %s, want scalar", compositeSpanBackend)
		}
		got := makeOpaqueSpanFixture(256)
		want := append([]byte(nil), got...)
		compositeOpaqueSpan(got, 0, 256, 0.13, 0.57, 0.91, 0.37)
		compositeOpaqueSpanScalar(want, 0, 256, 0.13, 0.57, 0.91, 0.37)
		if !bytes.Equal(got, want) {
			t.Fatal("disabled NEON dispatch differs from scalar")
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestCompositeSpanNEONDisabledFallback$")
	cmd.Env = compositeNEONDisabledEnv(os.Environ())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("NEON-disabled subprocess failed: %v\n%s", err, output)
	}
}

func BenchmarkCompositeOpaqueSpanNEONKernel(b *testing.B) {
	if !cpu.ARM64.HasASIMD {
		b.Skip("NEON unavailable")
	}
	for _, pixels := range []int{8, 16, 64, 256} {
		b.Run(benchmarkSpanSizeName(pixels), func(b *testing.B) {
			pix := makeOpaqueSpanFixture(pixels)
			const r, g, blue, alpha = 0.13, 0.57, 0.91, 0.37
			fgR, fgG, fgB, bgBlend := r*alpha, g*alpha, blue*alpha, 1-alpha
			b.ReportAllocs()
			b.SetBytes(int64(pixels * 4))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				compositeOpaqueSpanNEON(unsafe.Pointer(&pix[0]), pixels, fgR, fgG, fgB, bgBlend)
			}
		})
	}
}

func benchmarkSpanSizeName(pixels int) string {
	switch pixels {
	case 8:
		return "8"
	case 16:
		return "16"
	case 64:
		return "64"
	case 256:
		return "256"
	default:
		return "other"
	}
}

func compositeNEONDisabledEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+2)
	godebug := "cpu.all=off"
	for _, entry := range environ {
		if strings.HasPrefix(entry, "GODEBUG=") {
			if value := strings.TrimPrefix(entry, "GODEBUG="); value != "" {
				godebug = value + "," + godebug
			}
			continue
		}
		if strings.HasPrefix(entry, compositeNEONDisabledHelper+"=") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GODEBUG="+godebug, compositeNEONDisabledHelper+"=1")
}
