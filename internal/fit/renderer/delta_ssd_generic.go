//go:build !amd64 && !arm64

package renderer

const deltaSSDBackend = "scalar"

func deltaSSDSpan(candidate, base, reference []byte, pixels int) int64 {
	return deltaSSDSpanScalar(candidate, base, reference, pixels)
}
