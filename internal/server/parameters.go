package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

const parametersPerCircle = 7

// parameterCircle is the readable representation of one circle in a parameter
// vector. Color channels and opacity retain their optimizer-native [0, 1]
// values so an export can recreate the exact result.
type parameterCircle struct {
	Number  int     `json:"number"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Radius  float64 `json:"radius"`
	Red     float64 `json:"red"`
	Green   float64 `json:"green"`
	Blue    float64 `json:"blue"`
	Opacity float64 `json:"opacity"`
}

type parameterExport struct {
	JobID      string            `json:"jobID"`
	Cost       float64           `json:"cost"`
	Iterations int               `json:"iterations"`
	Timestamp  time.Time         `json:"timestamp"`
	Params     []float64         `json:"params"`
	Circles    []parameterCircle `json:"circles"`
}

func decodeParameterCircles(params []float64) ([]parameterCircle, error) {
	if len(params)%parametersPerCircle != 0 {
		return nil, fmt.Errorf("parameter count %d is not divisible by %d", len(params), parametersPerCircle)
	}

	circles := make([]parameterCircle, len(params)/parametersPerCircle)
	for i := range circles {
		offset := i * parametersPerCircle
		circles[i] = parameterCircle{
			Number: i + 1, X: params[offset], Y: params[offset+1], Radius: params[offset+2],
			Red: params[offset+3], Green: params[offset+4], Blue: params[offset+5], Opacity: params[offset+6],
		}
	}

	return circles, nil
}

func newParameterExport(job *Job, timestamp time.Time) (parameterExport, error) {
	circles, err := decodeParameterCircles(job.BestParams)
	if err != nil {
		return parameterExport{}, err
	}

	return parameterExport{
		JobID: job.ID, Cost: job.BestCost, Iterations: job.Iterations,
		Timestamp: timestamp.UTC(), Params: append([]float64(nil), job.BestParams...), Circles: circles,
	}, nil
}

// handleGetParameters handles GET /api/v1/jobs/:id/params.json.
func (s *Server) handleGetParameters(w http.ResponseWriter, r *http.Request, jobID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	job, exists := s.jobManager.GetJob(jobID)
	if !exists {
		writeAPIError(w, http.StatusNotFound, "not_found", "job not found")
		return
	}

	if len(job.BestParams) == 0 {
		writeAPIError(w, http.StatusNotFound, "no_results", "no parameters available yet")
		return
	}

	export, err := newParameterExport(job, time.Now())
	if err != nil {
		slog.Error("Failed to export job parameters", "job_id", jobID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "invalid_parameters", "stored parameters are invalid")

		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setAttachment(w, artifactFilename(jobID, "params.json"))
	w.Header().Set("Cache-Control", "no-store")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(export); err != nil {
		slog.Error("Failed to encode parameter export", "job_id", jobID, "error", err)
	}
}
