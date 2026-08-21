package app

import (
	"errors"
	"math"
	"testing"
)

// The out-of-memory scenario has no test that survives the condition it names:
// a Go heap exhaustion is a fatal runtime error, not a returned one, so it
// cannot be caught, recovered, or asserted on from inside the process. What can
// be tested is the layer that keeps a request from asking for the allocation in
// the first place, which is what these bounds exist for. They run at the
// trusted-local boundary before any buffer is sized, so an untrusted width or
// circle count cannot turn into a multi-gigabyte allocation.

// TestImageDimensionGuardSurvivesOverflow is the guard against the arithmetic
// that would defeat it. A width*height product is the natural way to write this
// check and the wrong one: on a 64-bit int the product of two large dimensions
// wraps to a small or negative number and passes, which is exactly the input an
// attacker would pick. The implementation divides instead, so these must be
// rejected.
func TestImageDimensionGuardSurvivesOverflow(t *testing.T) {
	overflowing := []struct {
		name          string
		width, height int
	}{
		{name: "product wraps to zero", width: 1 << 32, height: 1 << 32},
		{name: "product wraps negative", width: math.MaxInt32, height: math.MaxInt32},
		{name: "max int square", width: math.MaxInt64, height: math.MaxInt64},
		{name: "one huge side", width: math.MaxInt64, height: 1},
	}
	for _, test := range overflowing {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateImageDimensions(test.width, test.height)
			if err == nil {
				t.Fatalf("ValidateImageDimensions(%d, %d) = nil, want a rejection: this would size an allocation from a wrapped product", test.width, test.height)
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v (%T), want a *ValidationError so the boundary reports it as a bad request", err, err)
			}
		})
	}
}

// TestImageDimensionGuardIsExactAtItsBound pins the boundary itself, so a later
// refactor cannot quietly move the limit by one pixel in either direction.
func TestImageDimensionGuardIsExactAtItsBound(t *testing.T) {
	if err := ValidateImageDimensions(MaxImagePixels, 1); err != nil {
		t.Fatalf("ValidateImageDimensions at exactly the limit = %v, want it accepted", err)
	}
	if err := ValidateImageDimensions(MaxImagePixels+1, 1); err == nil {
		t.Fatal("ValidateImageDimensions one pixel over the limit = nil, want a rejection")
	}
	// 4096*4096 is exactly the limit; 4097*4096 is 16,781,312 and over it.
	if err := ValidateImageDimensions(4096, 4096); err != nil {
		t.Fatalf("ValidateImageDimensions(4096, 4096) = %v, want the exact limit accepted", err)
	}
	if err := ValidateImageDimensions(4097, 4096); err == nil {
		t.Fatal("ValidateImageDimensions(4097, 4096) = nil, want a rejection just over the limit")
	}
}

// TestCircleCountGuardBoundsTheParameterVector ties MaxCircles to the
// allocation it actually bounds. A circle is paramsPerCircle float64 values in
// the optimizer's vector, and the population holds several such vectors, so an
// unbounded circle count is an unbounded allocation.
func TestCircleCountGuardBoundsTheParameterVector(t *testing.T) {
	config := DefaultConfig()
	config.RefPath = "assets/reference.png"
	config.Circles = MaxCircles + 1
	if err := config.Validate(); err == nil {
		t.Fatalf("Validate() accepted %d circles, want a rejection above the %d limit", config.Circles, MaxCircles)
	}

	config.Circles = MaxCircles
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() at exactly %d circles = %v, want it accepted", MaxCircles, err)
	}

	// 7 float64 per circle. The bound has to stay small enough that the
	// parameter vector alone cannot exhaust a modest heap; if MaxCircles is
	// raised again, this is the number to re-justify.
	const bytesPerCircle = 7 * 8
	if vectorBytes := MaxCircles * bytesPerCircle; vectorBytes > 1<<20 {
		t.Fatalf("a full parameter vector is %d bytes, above the 1 MiB this bound was chosen to keep it under", vectorBytes)
	}
}
