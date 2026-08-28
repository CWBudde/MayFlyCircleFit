package renderer

// degradableRenderer reports whether a backend has abandoned its own execution
// path and is answering from a CPU fallback instead. Only OpenCL implements it;
// the CPU renderer has nothing to fall back to.
type degradableRenderer interface {
	Degraded() bool
}

// Degraded reports whether base has permanently fallen back to a CPU path.
//
// It exists for the same reason EvaluationWidth does: callers have to be able
// to report what the renderer actually did rather than what the configuration
// asked for. A degraded OpenCL renderer answers every later Cost and Render
// from its CPU fallback without returning an error -- neither method has an
// error return -- so a run that degrades mid-flight is otherwise
// indistinguishable from one that ran on the device throughout. That matters
// more here than a label usually would, because the two backends do not
// produce comparable costs: the device computes in float32 against a float64
// CPU path, so the objective's scale changes at the moment of degradation.
//
// It reports false for every backend that cannot degrade, including the CPU
// renderer and every backend in a build without the gpu tag.
func Degraded(base Renderer) bool {
	renderer, ok := base.(degradableRenderer)
	if !ok {
		return false
	}

	return renderer.Degraded()
}
