//go:build gpu

package renderer

import (
	"image"
	"image/color"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
)

const (
	openCLCostAbsoluteTolerance = 0.01
	// Float32 compositing and circle-edge coverage may differ slightly from the
	// float64 CPU path, while the rendered channels remain within two bytes.
	openCLCostRelativeTolerance = 0.01
)

func TestOpenCLRendererMatchesCPU(t *testing.T) {
	ref := patternedReference(image.Rect(0, 0, 32, 32))
	params := []float64{
		16, 16, 8, 0.2, 0.4, 0.8, 0.9,
		8, 24, 5, 0.9, 0.1, 0.3, 0.45,
	}

	assertOpenCLParity(t, ref, params)
}

func TestOpenCLReductionBoundaries(t *testing.T) {
	probeRef := patternedReference(image.Rect(0, 0, 1, 1))
	probe := newOpenCLTestRenderer(t, probeRef, 0)
	localSize := probe.localSize
	probe.release()

	pixelCounts := []int{
		1,
		max(1, localSize-1),
		localSize,
		localSize + 1,
		localSize*2 - 1,
		localSize*2 + 1,
	}
	for _, pixelCount := range pixelCounts {
		t.Run("pixels_"+strconv.Itoa(pixelCount), func(t *testing.T) {
			ref := patternedReference(image.Rect(0, 0, pixelCount, 1))
			params := []float64{
				float64(pixelCount) / 2, 0, max(1, float64(pixelCount)/4),
				0.3, 0.7, 0.2, 0.65,
			}
			assertOpenCLParity(t, ref, params)
		})
	}
}

func TestOpenCLReferenceOriginStrideAndAlpha(t *testing.T) {
	parent := image.NewNRGBA(image.Rect(-4, -3, 29, 23))
	refBounds := image.Rect(2, 1, 21, 14)
	ref := parent.SubImage(refBounds).(*image.NRGBA)
	for y := refBounds.Min.Y; y < refBounds.Max.Y; y++ {
		for x := refBounds.Min.X; x < refBounds.Max.X; x++ {
			ref.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*17 + y*3) & 0xff),
				G: uint8((x*5 + y*19) & 0xff),
				B: uint8((x*11 + y*7) & 0xff),
				A: uint8((x*13 + y*23) & 0xff),
			})
		}
	}

	params := []float64{
		9, 6, 7, 1, 0, 0.25, 0.8,
		-4, 2, 6, 0, 1, 0.5, 0.4,
		15, 11, 0, 0.2, 0.3, 1, 0.9,
	}
	assertOpenCLParity(t, ref, params)
}

func TestOpenCLZeroCirclesAndInvalidParams(t *testing.T) {
	ref := patternedReference(image.Rect(0, 0, 17, 9))
	assertOpenCLParity(t, ref, nil)
	empty := newOpenCLTestRenderer(t, image.NewNRGBA(image.Rect(0, 0, 0, 0)), 0)
	if cost := empty.Cost(nil); !math.IsInf(cost, 1) {
		empty.release()
		t.Fatalf("Cost(empty image) = %v, want +Inf", cost)
	}
	if bounds := empty.Render(nil).Bounds(); !bounds.Empty() {
		empty.release()
		t.Fatalf("Render(empty image) bounds = %v, want empty", bounds)
	}
	empty.release()

	r := newOpenCLTestRenderer(t, ref, 1)
	defer r.release()
	short := make([]float64, paramsPerCircle-1)
	if cost := r.Cost(short); !math.IsInf(cost, 1) {
		t.Fatalf("Cost(short params) = %v, want +Inf", cost)
	}
	if r.degraded {
		t.Fatal("invalid parameters permanently degraded the OpenCL backend")
	}
	want := r.fallback.Render(short)
	got := r.Render(short)
	assertNRGBAWithin(t, want, got, 0)
}

func TestOpenCLCostDefersImageReadback(t *testing.T) {
	ref := patternedReference(image.Rect(0, 0, 31, 17))
	r := newOpenCLTestRenderer(t, ref, 1)
	defer r.release()

	params := []float64{15, 8, 6, 0.2, 0.6, 0.9, 0.75}
	_ = r.Cost(params)
	if !r.deviceValid {
		t.Fatal("Cost did not leave a valid device result")
	}
	if r.imageValid || r.imageScratch != nil {
		t.Fatal("Cost materialized the output image on the host")
	}

	deviceHash := r.deviceHash
	evaluations := r.evaluations
	_ = r.Render(params)
	if !r.imageValid || r.imageScratch == nil {
		t.Fatal("Render did not materialize the resident device image")
	}
	if r.deviceHash != deviceHash {
		t.Fatal("Render changed the cached device evaluation for identical parameters")
	}
	if r.evaluations != evaluations {
		t.Fatal("Render reran the kernels for identical parameters")
	}

	changed := append([]float64(nil), params...)
	changed[0]++
	_ = r.Cost(changed)
	if !r.deviceValid || r.deviceHash == deviceHash {
		t.Fatal("changed parameters did not replace the cached device evaluation")
	}
	if r.evaluations != evaluations+1 {
		t.Fatal("changed parameters did not run exactly one new device evaluation")
	}
	if r.imageValid {
		t.Fatal("changed parameters did not invalidate the host image cache")
	}
}

func assertOpenCLParity(t *testing.T, ref *image.NRGBA, params []float64) {
	t.Helper()
	circles := len(params) / paramsPerCircle
	cpu := NewCPURenderer(ref, circles)
	gpu := newOpenCLTestRenderer(t, ref, circles)
	defer gpu.release()

	wantCost := cpu.Cost(params)
	gotCost := gpu.Cost(params)
	gpuImage := gpu.Render(params)
	if imageCost := fit.MSECost(gpuImage, ref); math.Abs(imageCost-gotCost) > openCLCostAbsoluteTolerance {
		t.Fatalf("device cost %f does not describe rendered image cost %f", gotCost, imageCost)
	}
	assertCostWithin(t, wantCost, gotCost)
	assertNRGBAWithin(t, cpu.Render(params), gpuImage, 2)
}

func newOpenCLTestRenderer(t *testing.T, ref *image.NRGBA, circles int) *openCLRenderer {
	t.Helper()
	renderer, cleanup, err := NewOpenCLRenderer(ref, circles)
	if err != nil {
		if os.Getenv("MAYFLY_REQUIRE_OPENCL") == "1" {
			t.Fatalf("required GPU backend unavailable: %v", err)
		}
		t.Skipf("GPU backend unavailable: %v", err)
	}
	r, ok := renderer.(*openCLRenderer)
	if !ok {
		cleanup()
		t.Fatalf("NewOpenCLRenderer returned %T, want *openCLRenderer", renderer)
	}
	return r
}

func assertCostWithin(t *testing.T, want, got float64) {
	t.Helper()
	tolerance := openCLCostAbsoluteTolerance + math.Abs(want)*openCLCostRelativeTolerance
	if diff := math.Abs(want - got); diff > tolerance {
		t.Fatalf("cost mismatch (cpu=%f gpu=%f diff=%f tolerance=%f)", want, got, diff, tolerance)
	}
}

func patternedReference(bounds image.Rectangle) *image.NRGBA {
	ref := image.NewNRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ref.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x*31 + y*7) & 0xff),
				G: uint8((x*13 + y*29) & 0xff),
				B: uint8((x*3 + y*37) & 0xff),
				A: uint8((x*17 + y*11) & 0xff),
			})
		}
	}
	return ref
}

func assertNRGBAWithin(t *testing.T, a, b *image.NRGBA, tolerance uint8) {
	t.Helper()

	if a.Bounds().Dx() != b.Bounds().Dx() || a.Bounds().Dy() != b.Bounds().Dy() {
		t.Fatalf("dimensions mismatch: %v vs %v", a.Bounds(), b.Bounds())
	}

	for y := 0; y < a.Bounds().Dy(); y++ {
		for x := 0; x < a.Bounds().Dx(); x++ {
			ai := a.PixOffset(a.Bounds().Min.X+x, a.Bounds().Min.Y+y)
			bi := b.PixOffset(b.Bounds().Min.X+x, b.Bounds().Min.Y+y)
			for c := 0; c < 4; c++ {
				va := a.Pix[ai+c]
				vb := b.Pix[bi+c]
				if diff := absUint8Diff(va, vb); diff > tolerance {
					t.Fatalf("pixel mismatch at (%d,%d) channel %d: %d vs %d", x, y, c, va, vb)
				}
			}
		}
	}
}

func absUint8Diff(a, b uint8) uint8 {
	if a > b {
		return a - b
	}
	return b - a
}
