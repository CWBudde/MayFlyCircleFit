package server

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
)

func downloadRequested(r *http.Request) bool {
	return r.URL.Query().Get("download") == "1"
}

func setAttachment(w http.ResponseWriter, filename string) {
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
}

func artifactFilename(jobID, suffix string) string {
	return fmt.Sprintf("job-%s-%s", jobID, suffix)
}

func requestedColormap(r *http.Request) (fit.Colormap, error) {
	requested := r.URL.Query().Get("colormap")
	if requested == "" {
		return fit.ColormapTurbo, nil
	}

	colormap, ok := fit.ParseColormap(requested)
	if !ok {
		return "", errors.New("colormap must be turbo or magma")
	}

	return colormap, nil
}

func pngDataURI(img image.Image) (string, error) {
	var encoded bytes.Buffer

	err := png.Encode(&encoded, img)
	if err != nil {
		return "", fmt.Errorf("encode PNG: %w", err)
	}

	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes()), nil
}

// handleGetReport handles GET /api/v1/jobs/:id/report.html.
func (s *Server) handleGetReport(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	colormap, err := requestedColormap(r)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_colormap", err.Error())
		return
	}

	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	if len(job.BestParams) == 0 {
		writeAPIError(w, http.StatusNotFound, "no_results", "no report available yet")
		return
	}

	reference, err := loadReferenceImage(job.Config.RefPath)
	if err != nil {
		slog.Error("Failed to load report reference", "job_id", jobID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "report_failed", "failed to load report reference")

		return
	}

	best, cleanup, err := renderBestSnapshot(job, reference)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "report_failed", "failed to render report snapshot")
		return
	}
	defer cleanup()

	difference := computeDiffImage(reference, best, colormap)

	referenceURI, err := pngDataURI(reference)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "report_failed", "failed to encode report reference")
		return
	}

	bestURI, err := pngDataURI(best)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "report_failed", "failed to encode report result")
		return
	}

	differenceURI, err := pngDataURI(difference)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "report_failed", "failed to encode report difference")
		return
	}

	circles, err := decodeParameterCircles(job.BestParams)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "report_failed", "stored parameters are invalid")
		return
	}

	parameters := make([]ui.CircleParameter, len(circles))
	for i, circle := range circles {
		parameters[i] = ui.CircleParameter{
			Number: circle.Number, X: circle.X, Y: circle.Y, Radius: circle.Radius,
			Red: circle.Red, Green: circle.Green, Blue: circle.Blue, Opacity: circle.Opacity,
		}
	}

	now := time.Now().UTC()

	elapsed := now.Sub(job.StartTime).Seconds()
	if job.EndTime != nil {
		elapsed = job.EndTime.Sub(job.StartTime).Seconds()
	}

	psnr, infinite := serializablePSNR(job.BestCost)

	psnrText := "Unavailable"
	if infinite {
		psnrText = "∞ dB"
	} else if psnr != nil {
		psnrText = fmt.Sprintf("%.2f dB", *psnr)
	}

	ssimText := "Not enabled"
	if job.Config.EnableSSIM {
		ssimText = "Unavailable"
		if job.SSIM != nil {
			ssimText = fmt.Sprintf("%.4f", *job.SSIM)
		}
	}

	report := ui.JobReport{
		ID: job.ID, State: string(job.State), Mode: string(job.Config.Mode), Colormap: string(colormap),
		RefPath: job.Config.RefPath, Circles: job.Config.Circles, Iterations: job.Iterations, Evaluations: job.Evaluations,
		Cost: job.BestCost, PSNR: psnrText, SSIM: ssimText, StartTime: job.StartTime, EndTime: job.EndTime,
		ElapsedSec: elapsed, GeneratedAt: now, ReferenceDataURI: referenceURI, BestDataURI: bestURI,
		DifferenceDataURI: differenceURI, Parameters: parameters,
	}

	var output bytes.Buffer
	if err := ui.JobReportPage(report).Render(r.Context(), &output); err != nil {
		slog.Error("Failed to render report", "job_id", jobID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "report_failed", "failed to render report")

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	setAttachment(w, artifactFilename(jobID, "report.html"))

	if _, err := w.Write(output.Bytes()); err != nil {
		slog.Error("Failed to write report", "job_id", jobID, "error", err)
	}
}
