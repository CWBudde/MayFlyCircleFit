//go:build !amd64 && !arm64

package fit

import "testing"

func TestGenericSIMDDispatchUsesScalarBackends(t *testing.T) {
	if Tier() != TierScalar {
		t.Fatalf("tier = %s, want scalar", Tier())
	}
	if ActiveSSDKernel() != TierScalar {
		t.Fatalf("SSD kernel = %s, want scalar", ActiveSSDKernel())
	}
	if ActiveSADKernel() != TierScalar {
		t.Fatalf("SAD kernel = %s, want scalar", ActiveSADKernel())
	}

	a := []uint8{10, 20, 30, 255}
	b := []uint8{12, 18, 33, 0}

	if got, want := fastSSD(a, b, 4, 1, 1), fastSSD_Scalar(a, b, 4, 1, 1); got != want {
		t.Fatalf("scalar SSD dispatch = %v, want %v", got, want)
	}
	if got, want := fastSAD(a, b, 4, 1, 1), fastSAD_Scalar(a, b, 4, 1, 1); got != want {
		t.Fatalf("scalar SAD dispatch = %v, want %v", got, want)
	}
}
