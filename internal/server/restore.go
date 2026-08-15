package server

import (
	"errors"
	"log/slog"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/store"
)

// restorePersistedJobs reconstructs terminal jobs from checkpoints so server
// restarts do not make completed work disappear from the UI and API.
func (s *Server) restorePersistedJobs() {
	if s.store == nil && s.projects == nil {
		return
	}

	restored := 0
	for _, slug := range s.projectSlugsForRestore() {
		restored += s.restoreProjectJobs(slug, s.storeForSlug(slug))
	}

	if restored > 0 {
		slog.Info("Restored persisted jobs", "count", restored)
	}
}

// projectSlugsForRestore lists every project to scan. The default project is
// the legacy `<data-root>/jobs` tree, so pre-project installations restore
// exactly as before without any migration.
func (s *Server) projectSlugsForRestore() []string {
	if s.projects == nil {
		return []string{app.DefaultProject}
	}
	return s.projects.Slugs()
}

func (s *Server) restoreProjectJobs(slug string, projectStore store.Store) int {
	if projectStore == nil {
		return 0
	}
	checkpoints, err := projectStore.ListCheckpoints()
	if err != nil {
		slog.Warn("Unable to list persisted jobs", "project", slug, "error", err)
		return 0
	}

	restored := 0
	for _, info := range checkpoints {
		checkpoint, err := projectStore.LoadCheckpoint(info.JobID)
		if err != nil {
			slog.Warn("Unable to restore persisted job", "project", slug, "job_id", info.JobID, "error", err)
			continue
		}

		job := jobFromCheckpoint(checkpoint, slug)
		if artifactStore, ok := projectStore.(store.ArtifactStore); ok {
			if err := restoreJobTrace(job, artifactStore); err != nil && !errors.Is(err, store.ErrNotFound) {
				slog.Warn("Unable to restore persisted job history", "project", slug, "job_id", info.JobID, "error", err)
			}
		}
		if err := s.jobManager.restoreJob(job); err != nil {
			slog.Warn("Unable to register persisted job", "project", slug, "job_id", info.JobID, "error", err)
			continue
		}
		restored++
	}
	return restored
}

func jobFromCheckpoint(checkpoint *store.Checkpoint, project string) *Job {
	state := StateCancelled
	switch checkpoint.Termination {
	case "completed", "target_cost", "stagnation", "stage_convergence", "refill_limit":
		state = StateCompleted
	case "failed":
		state = StateFailed
	case "cancelled", store.TerminationUnknown, store.TerminationLegacy:
		state = StateCancelled
	}

	end := checkpoint.Timestamp
	return &Job{
		ID:           checkpoint.JobID,
		Project:      project,
		State:        state,
		Config:       checkpoint.Config,
		BestParams:   append([]float64(nil), checkpoint.BestParams...),
		BestCost:     checkpoint.BestCost,
		BestRevision: 1,
		InitialCost:  checkpoint.InitialCost,
		Iterations:   checkpoint.Iterations,
		Evaluations:  int(checkpoint.Evaluations),
		Termination:  checkpoint.Termination,
		StartTime:    checkpoint.Timestamp,
		EndTime:      &end,
	}
}

func restoreJobTrace(job *Job, artifactStore store.ArtifactStore) error {
	reader, err := artifactStore.NewTraceReader(job.ID)
	if err != nil {
		return err
	}
	defer func() {
		if err := reader.Close(); err != nil {
			slog.Warn("Unable to close restored job history", "job_id", job.ID, "error", err)
		}
	}()

	entries, err := reader.ReadAll()
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}

	job.MetricHistory = make([]MetricSample, len(entries))
	for i, entry := range entries {
		job.MetricHistory[i] = MetricSample{
			Iteration: entry.Iteration, Cost: entry.Cost, PSNR: cloneFloat(entry.PSNR),
			PSNRInfinite: entry.PSNRInfinite, SSIM: cloneFloat(entry.SSIM), Timestamp: entry.Timestamp,
		}
		job.PSNR = cloneFloat(entry.PSNR)
		job.PSNRInfinite = entry.PSNRInfinite
		if entry.SSIM != nil {
			job.SSIM = cloneFloat(entry.SSIM)
		}
	}
	if !entries[0].Timestamp.IsZero() {
		job.StartTime = entries[0].Timestamp
	}
	if last := entries[len(entries)-1].Timestamp; !last.IsZero() {
		job.EndTime = &last
	}
	return nil
}
