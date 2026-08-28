//go:build gpu

// The tests below drive one OpenCL device through the unexported adapter. They
// stay in package renderer for the fixtures and the adapter, and they stay
// serial because the backend does not advertise safe concurrent evaluation.
//
//nolint:testpackage,paralleltest // shares the unexported adapter and one serial device
package renderer

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/cwbudde/circlefit/internal/fit/gpu"
)

// The OpenCL renderer is a float32 reimplementation of a float64 rasterizer, so
// it is held to a bounded deviation rather than to the byte-exact contract in
// docs/renderer-correctness.md. These are the budgets that contract's GPU
// section quotes, and TestOpenCLDeviationBudget is what measures against them.
const (
	// openCLMaxChannelDeviation is the largest per-channel difference from the
	// CPU render that any scene in this file may produce.
	openCLMaxChannelDeviation = 2
	// openCLMaxRelativeCostError bounds the cost difference on scenes whose
	// cost is large enough for a relative measure to mean anything.
	openCLMaxRelativeCostError = 0.01
	// openCLRelativeCostFloor is that "large enough". Below it a handful of
	// edge pixels dominates the total, so the absolute tolerance governs.
	openCLRelativeCostFloor = 1000.0
)

// TestOpenCLDeviceReportsAPreparedDevice pins what Task 11.10 means by a
// prepared OpenCL runner. Initialization succeeding is not enough: InitOpenCL
// falls back to a CPU device, so a machine with only PoCL installed passes
// every parity test in this file while measuring nothing about a GPU. Under
// CIRCLEFIT_REQUIRE_OPENCL=1 the selected device therefore has to be one.
func TestOpenCLDeviceReportsAPreparedDevice(t *testing.T) {
	ref := patternedReference(image.Rect(0, 0, 8, 8))

	r, release := newOpenCLTestRenderer(t, ref, 1)
	defer release()

	runtime := r.Runtime()
	if runtime == nil {
		t.Fatal("initialised OpenCL renderer reports no runtime")
	}

	device, platform := runtime.Device, runtime.Platform
	t.Logf("platform %q (%s, %s)", platform.Name, platform.Vendor, platform.Version)
	t.Logf("device %q (%s, %s, type %s, %d compute units, reduction local size %d)",
		device.Name, device.Vendor, device.Version, device.Type, device.MaxComputeUnits, r.LocalSize())

	if device.Name == "" {
		t.Error("selected OpenCL device reports no name")
	}

	if device.MaxComputeUnits == 0 {
		t.Error("selected OpenCL device reports no compute units")
	}

	if r.LocalSize() <= 0 {
		t.Errorf("reduction local size = %d, want a positive workgroup size", r.LocalSize())
	}

	if device.Type != gpu.DeviceTypeGPU {
		if requireOpenCLTests() {
			t.Fatalf("device type = %s, want %s; this run is not vendor-GPU validation",
				device.Type, gpu.DeviceTypeGPU)
		}

		t.Logf("device type is %s, not %s: parity holds but this is not vendor-GPU validation",
			device.Type, gpu.DeviceTypeGPU)
	}
}

// TestOpenCLPaintsEveryIntersectingRowsNearestSample pins the rasterization rule
// the kernel used to get wrong, and it is the reason this file exists.
//
// The CPU span search starts at int(centerX+0.5) and walks outward without ever
// testing that pixel, so every row the disc reaches paints its nearest sample --
// including the two tangent rows where dx*dx+dy*dy <= r*r paints nothing. The
// kernel tested only the disc, so it dropped both tangent rows of a small
// circle. That is not a float32 artifact and no tolerance covers it: on the case
// below it moved the cost by a factor of two.
//
// docs/renderer-correctness.md documents the CPU rule and names this exact
// circle; TestSpanSearchAlwaysCoversNearestSample is the CPU-side pin.
func TestOpenCLPaintsEveryIntersectingRowsNearestSample(t *testing.T) {
	ref := solidImage(24, 24, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	params := []float64{10.5, 10, 1, 0, 0, 0, 1}

	cpu := NewCPURenderer(ref, 1)

	gpuRenderer, release := newOpenCLTestRenderer(t, ref, 1)
	defer release()

	want := cpu.Render(params)
	got := gpuRenderer.Render(params)

	// Row 10 is the disc's own row; rows 9 and 11 are the tangent rows that the
	// disc test rejects and the span search paints anyway.
	for _, y := range []int{9, 10, 11} {
		if painted := got.NRGBAAt(11, y); painted != want.NRGBAAt(11, y) {
			t.Errorf("pixel (11,%d) = %#v, CPU renders %#v", y, painted, want.NRGBAAt(11, y))
		}
	}

	assertNRGBAWithin(t, want, got, 0)
	assertCostWithin(t, cpu.Cost(params), gpuRenderer.Cost(params))
}

// TestOpenCLParityAcrossCircleCountsAndSizes crosses circle counts with canvas
// sizes. The sizes are deliberately not all powers of two: a canvas whose pixel
// count is not a multiple of the reduction workgroup size exercises the padded
// tail of the on-device sum, and an odd width exercises the row indexing.
func TestOpenCLParityAcrossCircleCountsAndSizes(t *testing.T) {
	sizes := []image.Point{{X: 1, Y: 1}, {X: 7, Y: 5}, {X: 33, Y: 17}, {X: 64, Y: 64}, {X: 137, Y: 91}}
	counts := []int{1, 2, 8, 32}

	for _, size := range sizes {
		for _, count := range counts {
			name := fmt.Sprintf("%dx%d_k%d", size.X, size.Y, count)
			t.Run(name, func(t *testing.T) {
				ref := patternedReference(image.Rect(0, 0, size.X, size.Y))
				seed := int64(size.X*1000 + size.Y*10 + count)
				//nolint:gosec // a reproducible fixture, not a security context
				params := randomCircles(t, rand.New(rand.NewSource(seed)), count, size)

				assertOpenCLParity(t, ref, params)
			})
		}
	}
}

// TestOpenCLEdgeCaseScenes is the named catalog: every degenerate circle the
// renderer accepts, plus the overlap and clipping cases. The CPU render is the
// golden image, and a mismatch writes both renders and an amplified difference
// map so the failure can be looked at rather than only counted.
func TestOpenCLEdgeCaseScenes(t *testing.T) {
	const w, h = 40, 30

	tests := []struct {
		name   string
		params []float64
	}{
		{"no_circles", nil},
		{"single_centered", []float64{20, 15, 9, 0.2, 0.5, 0.8, 0.75}},
		{"fully_opaque", []float64{20, 15, 9, 0.1, 0.2, 0.3, 1}},
		{"zero_opacity", []float64{20, 15, 9, 0.1, 0.2, 0.3, 0}},
		// The kernel used to skip everything below 0.001, where the CPU skips
		// only exact zero. Both now agree; the circle is invisible either way,
		// but agreeing for the right reason is the point.
		{"opacity_below_former_kernel_epsilon", []float64{20, 15, 9, 0, 0, 0, 0.0005}},
		{"minimum_representable_opacity", []float64{20, 15, 9, 0, 0, 0, 1.0 / 255.0}},
		{"radius_zero_integer_center", []float64{20, 15, 0, 0, 0, 0, 1}},
		{"radius_zero_fractional_center", []float64{20.5, 15.5, 0, 0, 0, 0, 1}},
		{"radius_negative", []float64{20, 15, -5, 0, 0, 0, 1}},
		{"radius_subpixel", []float64{20.5, 15.5, 0.4, 0, 0, 0, 1}},
		{"radius_covers_canvas", []float64{20, 15, 200, 0.9, 0.1, 0.4, 0.6}},
		{"outside_left", []float64{-40, 15, 8, 0, 0, 0, 1}},
		{"outside_right", []float64{80, 15, 8, 0, 0, 0, 1}},
		{"outside_top", []float64{20, -40, 8, 0, 0, 0, 1}},
		{"outside_bottom", []float64{20, 70, 8, 0, 0, 0, 1}},
		{"straddle_left_edge", []float64{-2, 15, 8, 0.3, 0.6, 0.1, 0.9}},
		{"straddle_right_edge", []float64{41, 15, 8, 0.3, 0.6, 0.1, 0.9}},
		{"straddle_top_edge", []float64{20, -2, 8, 0.3, 0.6, 0.1, 0.9}},
		{"straddle_bottom_edge", []float64{20, 31, 8, 0.3, 0.6, 0.1, 0.9}},
		{"straddle_corner", []float64{-3, -3, 10, 0.3, 0.6, 0.1, 0.9}},
		{
			"two_overlapping",
			[]float64{16, 15, 10, 1, 0, 0, 0.5, 24, 15, 10, 0, 0, 1, 0.5},
		},
		{
			"concentric_stack",
			[]float64{
				20, 15, 12, 1, 0, 0, 0.4,
				20, 15, 8, 0, 1, 0, 0.4,
				20, 15, 4, 0, 0, 1, 0.4,
			},
		},
		{
			"coincident_translucent",
			[]float64{
				20, 15, 7, 0.5, 0.5, 0.5, 0.3,
				20, 15, 7, 0.5, 0.5, 0.5, 0.3,
				20, 15, 7, 0.5, 0.5, 0.5, 0.3,
			},
		},
		{
			"subpixel_center_walk",
			[]float64{
				10, 15, 3, 0, 0, 0, 1,
				15.25, 15, 3, 0, 0, 0, 1,
				20.5, 15, 3, 0, 0, 0, 1,
				25.75, 15, 3, 0, 0, 0, 1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := patternedReference(image.Rect(0, 0, w, h))
			assertOpenCLSceneParity(t, tt.name, ref, tt.params)
		})
	}
}

// TestOpenCLDegenerateCanvases covers the shapes whose pixel count breaks the
// reduction's assumptions: a single pixel, a single row, and a single column.
func TestOpenCLDegenerateCanvases(t *testing.T) {
	bounds := []image.Rectangle{
		image.Rect(0, 0, 1, 1),
		image.Rect(0, 0, 1, 64),
		image.Rect(0, 0, 64, 1),
		image.Rect(0, 0, 3, 1),
	}

	for _, rect := range bounds {
		t.Run(fmt.Sprintf("%dx%d", rect.Dx(), rect.Dy()), func(t *testing.T) {
			ref := patternedReference(rect)
			centerX, centerY := float64(rect.Dx())/2, float64(rect.Dy())/2
			params := []float64{centerX, centerY, 2, 0.1, 0.8, 0.3, 0.85}
			assertOpenCLParity(t, ref, params)
		})
	}
}

// TestOpenCLPreservesCompositingOrder checks the property a per-pixel kernel can
// get wrong without any parity test noticing: circles composite in parameter
// order. Two overlapping opaque circles render differently depending on which
// is drawn second, and the GPU has to disagree with itself in the same way the
// CPU does.
func TestOpenCLPreservesCompositingOrder(t *testing.T) {
	ref := solidImage(32, 32, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	red := []float64{13, 16, 9, 1, 0, 0, 1}
	blue := []float64{19, 16, 9, 0, 0, 1, 1}

	redThenBlue := append(append([]float64(nil), red...), blue...)
	blueThenRed := append(append([]float64(nil), blue...), red...)

	cpu := NewCPURenderer(ref, 2)

	gpuRenderer, release := newOpenCLTestRenderer(t, ref, 2)
	defer release()

	wantFirst := cpu.Render(redThenBlue)
	gotFirst := cloneNRGBA(gpuRenderer.Render(redThenBlue))
	assertNRGBAWithin(t, wantFirst, gotFirst, openCLMaxChannelDeviation)

	wantSecond := cpu.Render(blueThenRed)
	gotSecond := gpuRenderer.Render(blueThenRed)
	assertNRGBAWithin(t, wantSecond, gotSecond, openCLMaxChannelDeviation)

	// The overlap is where the order shows. If the kernel composited in some
	// other order the two renders would be identical and both would still match
	// a CPU render that happened to agree there.
	if gotFirst.NRGBAAt(16, 16) == gotSecond.NRGBAAt(16, 16) {
		t.Fatalf("swapping draw order left the overlap unchanged at %#v", gotFirst.NRGBAAt(16, 16))
	}
}

// TestOpenCLDeviationBudget measures what the float32 kernel actually costs
// against the float64 CPU renderer over a randomized catalog, and fails if it
// exceeds the budget documented in docs/renderer-correctness.md. It reports the
// measured worst case either way, so the documented number can be checked
// against a run rather than trusted.
func TestOpenCLDeviationBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("randomized deviation sweep is not a short test")
	}

	sizes := []image.Point{{X: 32, Y: 32}, {X: 129, Y: 77}, {X: 256, Y: 256}}
	counts := []int{1, 4, 24}

	var (
		worstChannel   int
		worstCostDelta float64
		worstRelative  float64
		worstScene     string
	)

	for _, size := range sizes {
		for _, count := range counts {
			ref := patternedReference(image.Rect(0, 0, size.X, size.Y))
			cpu := NewCPURenderer(ref, count)
			gpuRenderer, release := newOpenCLTestRenderer(t, ref, count)

			//nolint:gosec // a reproducible fixture, not a security context
			rng := rand.New(rand.NewSource(int64(size.X*7919 + size.Y*104729 + count)))

			for trial := range 12 {
				params := randomCircles(t, rng, count, size)

				wantCost, gotCost := cpu.Cost(params), gpuRenderer.Cost(params)
				delta := math.Abs(wantCost - gotCost)

				relative := 0.0
				if wantCost >= openCLRelativeCostFloor {
					relative = delta / wantCost
				}

				channel := maxChannelDeviation(cpu.Render(params), gpuRenderer.Render(params))
				if channel > worstChannel || relative > worstRelative {
					worstScene = fmt.Sprintf("%dx%d k=%d trial %d", size.X, size.Y, count, trial)
				}

				worstChannel = max(worstChannel, channel)
				worstCostDelta = math.Max(worstCostDelta, delta)
				worstRelative = math.Max(worstRelative, relative)
			}

			release()
		}
	}

	t.Logf("worst channel deviation %d, worst absolute cost delta %.4f, worst relative cost error %.6f%% (%s)",
		worstChannel, worstCostDelta, 100*worstRelative, worstScene)

	if worstChannel > openCLMaxChannelDeviation {
		t.Errorf("worst channel deviation %d exceeds the documented budget of %d",
			worstChannel, openCLMaxChannelDeviation)
	}

	if worstRelative > openCLMaxRelativeCostError {
		t.Errorf("worst relative cost error %.6f exceeds the documented budget of %.6f",
			worstRelative, openCLMaxRelativeCostError)
	}
}

// randomCircles draws circles that stay in the range the optimizer's bounds
// allow: centers up to half a canvas beyond each edge, and radii from below one
// pixel up to a canvas-covering disc.
func randomCircles(t *testing.T, rng *rand.Rand, count int, size image.Point) []float64 {
	t.Helper()

	params := make([]float64, 0, count*paramsPerCircle)
	maxDimension := float64(max(size.X, size.Y))

	for range count {
		params = append(params,
			rng.Float64()*2*float64(size.X)-float64(size.X)/2,
			rng.Float64()*2*float64(size.Y)-float64(size.Y)/2,
			rng.Float64()*maxDimension*0.6,
			rng.Float64(), rng.Float64(), rng.Float64(),
			rng.Float64(),
		)
	}

	return params
}

// assertOpenCLSceneParity is assertOpenCLParity plus the visual artifact. The
// CPU render is the golden image; on a mismatch both renders and an amplified
// difference map are written where a human can open them, because a coordinate
// and two channel values do not show what went wrong with a rasterizer.
func assertOpenCLSceneParity(t *testing.T, scene string, ref *image.NRGBA, params []float64) {
	t.Helper()

	circles := len(params) / paramsPerCircle
	cpu := NewCPURenderer(ref, circles)

	gpuRenderer, release := newOpenCLTestRenderer(t, ref, circles)
	defer release()

	want := cpu.Render(params)
	got := gpuRenderer.Render(params)

	if deviation := maxChannelDeviation(want, got); deviation > openCLMaxChannelDeviation {
		dir := writeSceneArtifacts(t, scene, want, got)
		t.Fatalf("scene %q deviates by %d channels, budget is %d; renders written to %s",
			scene, deviation, openCLMaxChannelDeviation, dir)
	}

	assertCostWithin(t, cpu.Cost(params), gpuRenderer.Cost(params))
}

func writeSceneArtifacts(t *testing.T, scene string, want, got *image.NRGBA) string {
	t.Helper()

	dir := os.Getenv("CIRCLEFIT_GPU_ARTIFACTS")
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "circlefit-gpu-mismatch")
	}
	// The directory is the developer's own choice and the scene names are
	// literals from the table above, so neither is attacker-controlled.
	err := os.MkdirAll(dir, 0o755) //nolint:gosec // developer-supplied debug output path
	if err != nil {
		t.Logf("cannot create artifact directory %s: %v", dir, err)

		return "(unwritten)"
	}

	for name, img := range map[string]*image.NRGBA{
		scene + "-cpu.png":  want,
		scene + "-gpu.png":  got,
		scene + "-diff.png": differenceImage(want, got),
	} {
		writePNG(t, filepath.Join(dir, name), img)
	}

	return dir
}

func writePNG(t *testing.T, path string, img *image.NRGBA) {
	t.Helper()

	file, err := os.Create(path) //nolint:gosec // developer-supplied debug output path
	if err != nil {
		t.Logf("cannot write %s: %v", path, err)

		return
	}
	defer file.Close()

	err = png.Encode(file, img)
	if err != nil {
		t.Logf("cannot encode %s: %v", path, err)
	}
}

// differenceImage amplifies the per-channel deviation so that a difference of
// one or two, which is invisible next to the renders themselves, is legible.
func differenceImage(a, b *image.NRGBA) *image.NRGBA {
	diff := image.NewNRGBA(image.Rect(0, 0, a.Bounds().Dx(), a.Bounds().Dy()))
	for i := 0; i+3 < len(diff.Pix); i += 4 {
		for c := range 3 {
			amplified := int(absUint8Diff(a.Pix[i+c], b.Pix[i+c])) * 64
			diff.Pix[i+c] = uint8(min(amplified, 255))
		}

		diff.Pix[i+3] = 255
	}

	return diff
}

func maxChannelDeviation(a, b *image.NRGBA) int {
	worst := 0
	for i := range a.Pix {
		worst = max(worst, int(absUint8Diff(a.Pix[i], b.Pix[i])))
	}

	return worst
}

func requireOpenCLTests() bool {
	return os.Getenv("CIRCLEFIT_REQUIRE_OPENCL") == "1"
}
