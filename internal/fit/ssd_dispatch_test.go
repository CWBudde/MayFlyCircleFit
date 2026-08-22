package fit

import "testing"

var (
	ssdDispatchBenchmarkFunc = ssdDispatchBenchmarkTarget
	ssdDispatchBenchmarkSink float64
)

//go:noinline
func ssdDispatchBenchmarkTarget(a, b []uint8, stride, width, height int) float64 {
	return float64(len(a) + len(b) + stride + width + height)
}

// BenchmarkSSDDispatchOverhead isolates the one-time-selected function-pointer
// call used by fastSSD. Compare function_pointer against direct; their ns/op
// difference is dispatch overhead, independent of the SSD kernel's work.
func BenchmarkSSDDispatchOverhead(b *testing.B) {
	a := []uint8{1}
	other := []uint8{2}

	b.Run("direct", func(b *testing.B) {
		var result float64
		for b.Loop() {
			result = ssdDispatchBenchmarkTarget(a, other, 4, 1, 1)
		}

		ssdDispatchBenchmarkSink = result
	})

	b.Run("function_pointer", func(b *testing.B) {
		var result float64
		for b.Loop() {
			result = ssdDispatchBenchmarkFunc(a, other, 4, 1, 1)
		}

		ssdDispatchBenchmarkSink = result
	})
}
