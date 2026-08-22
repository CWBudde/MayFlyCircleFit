package fit

import (
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/app"
)

// app.ParametersPerCircle duplicates this package's paramsPerCircle because
// dependencies flow from fit toward app and cannot be reversed. The duplicate
// is load-bearing: app multiplies it by the circle count to decide whether a
// population is affordable, so the two drifting apart would silently move that
// limit. This test is the reason the duplication is safe.
func TestParametersPerCircleMatchesTheRenderer(t *testing.T) {
	if app.ParametersPerCircle != paramsPerCircle {
		t.Fatalf(
			"app.ParametersPerCircle = %d, fit.paramsPerCircle = %d; "+
				"the population-dimension limit is derived from the first and spent on the second",
			app.ParametersPerCircle, paramsPerCircle,
		)
	}
}
