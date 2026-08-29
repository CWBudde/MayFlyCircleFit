package opt

// restartRecords accumulates the per-run restart records of a wrapper that
// invokes its base optimizer more than once -- an epoch chain, or a set of
// cold attempts -- into one list covering the whole invocation.
//
// Two things have to survive that aggregation. The records themselves: a
// wrapper that keeps only the invocation it settled on returns a restart
// history covering one epoch or one attempt while its iteration and evaluation
// totals cover every one of them, which is not a record of the run that
// happened. And each run's identity: an engine numbers its own restarts from
// zero on every invocation, so an unrenumbered list holds several runs
// claiming the same (Stage, Restart) key and no trace sample can be joined to
// the run that produced it.
//
// Renumbering is applied to the diagnostics the wrapper forwards as well as to
// the records, so the trace and the records agree on what a run is called. It
// is the choice restartOptimizer's epoch boundary counter already makes, for
// the same reason.
type restartRecords struct {
	runs []RestartRun
	// next is the index the following invocation's first run takes, which is
	// the number of runs already recorded: an engine numbers its n runs 0..n-1.
	next int
}

// offset reports what the next invocation's restart indices shift by. It is
// read before that invocation starts, because the progress observer wrapping
// it needs the shift while the invocation is still running.
func (r *restartRecords) offset() int {
	return r.next
}

// record renumbers one invocation's records onto the run-wide sequence and
// appends them.
func (r *restartRecords) record(runs []RestartRun) {
	base := r.next
	for _, run := range runs {
		run.Restart += base
		r.runs = append(r.runs, run)
	}

	r.next = base + len(runs)
}

// shiftRestart moves a progress report's restart index onto the run-wide
// sequence. The diagnostics are copied rather than edited through the pointer,
// because the engine that reported them owns that value and may still hold it.
func shiftRestart(progress Progress, offset int) Progress {
	if offset == 0 || progress.Diagnostics == nil {
		return progress
	}

	diagnostics := *progress.Diagnostics
	diagnostics.Restart += offset
	progress.Diagnostics = &diagnostics

	return progress
}

// AppendContinuedRestartRuns appends a continuation's restart records to the
// records a job already accumulated, stamping the invocation they belong to.
//
// A resumed job keeps its cumulative iteration and evaluation counts, so
// replacing the records instead would leave a job claiming totals for work
// whose restart history had been thrown away, and a second resume would erase
// the termination reasons of every earlier one permanently.
//
// The continuation's own indices are left alone rather than renumbered onto
// the earlier ones. A resume drives a fresh schedule numbered from zero, and
// it rewrites the trace rather than extending it, so the trace numbers that
// schedule's runs from zero too; the resume count is what separates the
// records, and it keeps the two accounts agreeing on what a run is called.
func AppendContinuedRestartRuns(prior, next []RestartRun, resume int) []RestartRun {
	if len(next) == 0 {
		return prior
	}

	combined := make([]RestartRun, 0, len(prior)+len(next))
	combined = append(combined, prior...)

	for _, run := range next {
		run.Resume = resume
		combined = append(combined, run)
	}

	return combined
}
