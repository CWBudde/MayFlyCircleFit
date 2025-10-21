package fit

import "image"

// Renderer renders circles to an image and computes cost
type Renderer interface {
	// Render creates an image from parameter vector
	Render(params []float64) *image.NRGBA

	// Cost computes error between params and reference
	Cost(params []float64) float64

	// Dim returns the dimensionality of the parameter space
	Dim() int

	// Bounds returns lower and upper bounds for parameters
	Bounds() (lower, upper []float64)

	// Reference returns the reference image
	Reference() *image.NRGBA
}
