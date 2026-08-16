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
	restored := 0
	for _, slug := range s.projectSlugsForRestore() {
		projectStore, err := s.storeForSlug(slug)
		if err != nil {
			slog.Error("Unable to resolve project store for restore", "project", string(slug), "error", err)
			continue
		}
		restored += s.restoreProjectJobs(slug, projectStore)
	}

	if restored > 0 {
		slog.Info("Restored persisted jobs", "count", restored)
	}
}

// projectSlugsForRestore lists every project to scan. The default project is
// the legacy `<data-root>/jobs` tree, so pre-project installations restore
// exactly as before without any migration.
//
// The order is Slugs()' sorted order, and that is load-bearing rather than
// incidental: if the same job ID exists under two projects, the first project
// scanned registers it and the second is refused. Sorting makes the winner the
// alphabetically-first project on every run, so a collision produces the same
// outcome and the same diagnostic each time instead of alternating with Go's
// randomized map iteration.
func (s *Server) projectSlugsForRestore() []app.Project {
	return s.projects.Slugs()
}

func (s *Server) restoreProjectJobs(slug app.Project, projectStore store.Store) int {
	if projectStore == nil {
		return 0
	}
	checkpoints, err := projectStore.ListCheckpoints()
	if err != nil {
		slog.Warn("Unable to list persisted jobs", "project", string(slug), "error", err)
		return 0
	}

	restored := 0
	for _, info := range checkpoints {
		checkpoint, err := projectStore.LoadCheckpoint(info.JobID)
		if err != nil {
			slog.Warn("Unable to restore persisted job", "project", string(slug), "job_id", info.JobID, "error", err)
			continue
		}

		job := jobFromCheckpoint(checkpoint, slug)
		if artifactStore, ok := projectStore.(store.ArtifactStore); ok {
			if err := restoreJobTrace(job, artifactStore); err != nil && !errors.Is(err, store.ErrNotFound) {
				slog.Warn("Unable to restore persisted job history", "project", string(slug), "job_id", info.JobID, "error", err)
			}
		}
		if err := s.jobManager.restoreJob(job); err != nil {
			// A duplicate ID is not an ordinary skip: the same job UUID exists
			// under two projects on disk, so this project's copy is dropped from
			// the API and the UI entirely while its files stay where they are.
			// Say so, and name both sides, or the operator sees a job vanish
			// with no way to tell which directory to look at.
			if errors.Is(err, errDuplicateJobID) {
				owner := app.Project("unknown")
				var duplicate *duplicateJobError
				if errors.As(err, &duplicate) {
					owner = duplicate.owner
				}
				slog.Error("Cross-project job ID collision; this project's copy is not registered",
					"job_id", info.JobID,
					"owning_project", string(owner),
					"skipped_project", string(slug),
					"detail", "the same job ID exists under both projects; the alphabetically-first project wins and the skipped copy's artifacts remain on disk")
				continue
			}
			slog.Warn("Unable to register persisted job", "project", string(slug), "job_id", info.JobID, "error", err)
			continue
		}
		restored++
	}
	return restored
}

func jobFromCheckpoint(checkpoint *store.Checkpoint, project app.Project) *Job {
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
