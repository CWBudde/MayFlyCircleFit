//go:build amd64

package renderer

import (
	"fmt"
	"math/rand"
	"testing"

	"golang.org/x/sys/cpu"
)

func TestCircleSpanFloat32AVX2MatchesScalar(t *testing.T) {
	if !cpu.X86.HasAVX2 {
		t.Skip("AVX2 unavailable")
	}

	tests := []struct {
		center, remaining float32
		width             int
	}{
		{center: 16.125, remaining: 0, width: 33},
		{center: 16.125, remaining: 1, width: 33},
		{center: 16.125, remaining: 49, width: 33},
		{center: 16.125, remaining: 64, width: 33},
		{center: 16.125, remaining: 65, width: 33},
		{center: 1.25, remaining: 400, width: 33},
		{center: 31.25, remaining: 400, width: 33},
	}
	for _, test := range tests {
		wantStart, wantEnd := circleSpanFloat32(test.center, test.remaining, test.width)
		gotStart, gotEnd := circleSpanFloat32AVX2(test.center, test.remaining, test.width)
		if gotStart != wantStart || gotEnd != wantEnd {
			t.Fatalf("circleSpanFloat32AVX2(%v, %v, %d) = [%d,%d), want [%d,%d)",
				test.center, test.remaining, test.width, gotStart, gotEnd, wantStart, wantEnd)
		}
	}

	rng := rand.New(rand.NewSource(1013))
	for range 100_000 {
		width := rng.Intn(2048) + 1
		center := rng.Float32() * float32(width-1)
		radius := rng.Float32() * float32(width)
		remaining := radius * radius * rng.Float32()
		wantStart, wantEnd := circleSpanFloat32(center, remaining, width)
		gotStart, gotEnd := circleSpanFloat32AVX2(center, remaining, width)
		if gotStart != wantStart || gotEnd != wantEnd {
			t.Fatalf("random circleSpanFloat32AVX2(%v, %v, %d) = [%d,%d), want [%d,%d)",
				center, remaining, width, gotStart, gotEnd, wantStart, wantEnd)
		}
	}
}

func TestCircleSpanFloat32Backend(t *testing.T) {
	want := "scalar"
	if cpu.X86.HasAVX2 {
		want = "avx2"
	}
	if circleSpanFloat32Backend != want {
		t.Fatalf("float32 geometry backend = %q, want %q", circleSpanFloat32Backend, want)
	}
}

func BenchmarkCircleSpanFloat32AVX2Direct(b *testing.B) {
	if !cpu.X86.HasAVX2 {
		b.Skip("AVX2 unavailable")
	}
	for _, radius := range []float32{5.25, 25.25, 100.25, 256.25} {
		remaining := radius * radius
		b.Run("scalar_R"+benchmarkFloatName(radius), func(b *testing.B) {
			for range b.N {
				xStart, xEnd := circleSpanFloat32(256.125, remaining, 513)
				geometryBenchmarkSink = xEnd - xStart
			}
		})
		b.Run("avx2_R"+benchmarkFloatName(radius), func(b *testing.B) {
			for range b.N {
				xStart, xEnd := circleSpanFloat32AVX2Kernel(256.125, remaining, 256, 513)
				geometryBenchmarkSink = xEnd - xStart
			}
		})
	}
}

func benchmarkFloatName(value float32) string {
	return fmt.Sprintf("%g", value)
}
