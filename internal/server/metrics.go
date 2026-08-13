package server

import (
	"image"
	"log/slog"
	"math"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

func qualitySample(iteration int, cost float64, ssim *float64, timestamp time.Time) MetricSample {
	psnr, infinite := serializablePSNR(cost)
	return MetricSample{
		Iteration: iteration, Cost: cost, PSNR: psnr, PSNRInfinite: infinite,
		SSIM: cloneFloat(ssim), Timestamp: timestamp,
	}
}

func serializablePSNR(mse float64) (*float64, bool) {
	value := fit.PSNR(mse)
	if math.IsInf(value, 1) {
		return nil, true
	}
	if math.IsNaN(value) || math.IsInf(value, -1) {
		return nil, false
	}
	return &value, false
}

func calculateSSIM(current, reference *image.NRGBA, jobID string) *float64 {
	value, err := fit.SSIM(current, reference)
	if err != nil {
		slog.Warn("Failed to calculate SSIM", "job_id", jobID, "error", err)
		return nil
	}
	return &value
}

func shouldSampleSSIM(now, lastSample time.Time, cost, lastSampleCost float64) bool {
	return cost < lastSampleCost && now.Sub(lastSample) >= time.Second
}

func traceEntry(sample MetricSample) store.TraceEntry {
	return store.TraceEntry{
		Iteration: sample.Iteration, Cost: sample.Cost, PSNR: cloneFloat(sample.PSNR),
		PSNRInfinite: sample.PSNRInfinite, SSIM: cloneFloat(sample.SSIM), Timestamp: sample.Timestamp,
	}
}
