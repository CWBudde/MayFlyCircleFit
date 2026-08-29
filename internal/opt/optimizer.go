package opt

import "context"

// InequalityConstraint describes a continuous constraint g(params) <= 0.
// Positive values are violations; zero and negative values are feasible.
type InequalityConstraint func([]float64) float64

// Problem describes one bounded minimization problem.
type Problem struct {
	Eval func([]float64) float64
	// Repair canonicalizes a bounded candidate before evaluation and before it
	// is exposed through progress or results. It may be nil.
	Repair       func([]float64)
	Inequalities []InequalityConstraint
	Lower        []float64
	Upper        []float64
	Dim          int
}

// Candidate is a known solution used to seed a continuation run.
type Candidate struct {
	Params []float64
	Cost   float64
}

// Progress is an immutable best-so-far optimizer snapshot.
type Progress struct {
	Diagnostics *SearchDiagnostics
	Iterations  int
	Evaluations int
	BestParams  []float64
	BestCost    float64
}

// SearchDiagnostics records optimizer-specific distribution state at the
// same iteration boundary as Progress. Fields that do not apply to an engine
// remain zero: Mayfly reports PopulationSpread, while CMA-ES reports Sigma,
// ConditionNumber and DistributionExtent. Diagnostics are opt-in because
// observing Mayfly's complete population requires a deep copy of every
// individual.
type SearchDiagnostics struct {
	PopulationSpread float64 `json:"populationSpread,omitempty"`
	Sigma            float64 `json:"sigma,omitempty"`
	ConditionNumber  float64 `json:"conditionNumber,omitempty"`
	// DistributionExtent is sigma * max(D), the largest axis of the CMA-ES
	// sampling ellipse in the normalized search box.
	//
	// It is recorded because Sigma on its own answers no question. CMA-ES
	// identifies only sigma^2 * C, so the split between the scalar step size
	// and the covariance matrix is a gauge freedom, and the pinned library
	// does not renormalize C. Sigma can therefore inflate by many orders of
	// magnitude while the axis lengths deflate by the same factor, leaving the
	// distribution exactly where it was. The twelve-block campaign in
	// docs/cmaes-report.md recorded sigma reaching 1e43 in separable mode
	// while the run was still improving its incumbent, and could not tell the
	// two readings apart because this field did not exist.
	//
	// This is the quantity the library's own TolXUp criterion compares against
	// its initial value, so a run whose extent is flat is a run whose sampling
	// distribution has not moved, whatever its sigma column says.
	DistributionExtent float64 `json:"distributionExtent,omitempty"`
	// Restart is the zero-based index of the independent run this sample was
	// taken in. It is zero for a single run and for every engine without a
	// restart schedule, so it stays absent from the trace unless a schedule
	// actually restarted.
	//
	// It exists because a restart schedule's samples are otherwise
	// indistinguishable: cumulative iteration and evaluation counts run
	// straight through a restart boundary, so a trace cannot say which run
	// produced a sample, and neither the per-run budget nor the evaluations a
	// run spent after its last improvement can be recovered from it. Pairing
	// this with Result.Restarts, which carries why each run ended, is what
	// makes a restart arm measurable rather than merely summarized.
	Restart int `json:"restart,omitempty"`
}

// Observer consumes synchronous best-so-far snapshots.
type Observer func(Progress)

// ProgressMapper converts optimizer-local progress into the caller's complete
// parameter space. Staged renderers use it to prepend already-retained circles
// before progress reaches monitoring and persistence observers.
type ProgressMapper func(Progress) Progress

// EpochBoundary reports a completed restart epoch with cumulative work for the
// current optimizer invocation.
type EpochBoundary struct {
	Epoch       int
	Progress    Progress
	Termination Termination
}

// EpochObserver consumes a synchronous epoch boundary. Returning an error
// aborts the remaining epochs so persistence failures cannot be ignored.
type EpochObserver func(EpochBoundary) error

// RunOptions controls progress reporting and restart-from-best behavior.
type RunOptions struct {
	Observer       Observer
	ProgressMapper ProgressMapper
	EpochObserver  EpochObserver
	Initial        *Candidate
	// AdditionalSeeds supplies alternative known candidates for a mixed
	// continuation population. Initial remains the incumbent that must not be
	// lost; additional seeds broaden exploration around other promising basins.
	AdditionalSeeds []Candidate
	ResumeCount     int
	// SeedOffset varies the run seed without implying a continuation. Restart
	// attempts use it so they stay reproducible for a fixed base seed while
	// remaining a distinct dimension from ResumeCount: if both used the same
	// field, epoch 2 of attempt 1 and epoch 1 of attempt 2 would alias onto
	// one seed. Zero reproduces the seed a run would have had without it.
	SeedOffset int
	// Continuation optionally concentrates a seeded run around its known
	// candidates. Nil preserves the historical half-local, half-global Mayfly
	// population and velocity scale. Active-set polishing uses this to request
	// a genuinely local search without changing fresh/global optimization.
	Continuation *ContinuationProfile
}

// ContinuationProfile controls how a Mayfly continuation population explores
// around Initial and AdditionalSeeds. Values are expressed in the optimizer's
// normalized [0,1] space.
type ContinuationProfile struct {
	// LocalFraction is the fraction of each male/female population supplied as
	// seeded candidates. The remainder is initialized globally at random.
	LocalFraction float64
	// Sigma is the standard deviation of seeded perturbations.
	Sigma float64
	// CoordinateRate is the probability that an individual dimension is
	// perturbed. Sparse perturbations avoid moving every variable at once in a
	// high-dimensional active set.
	CoordinateRate float64
	// MaxVelocity caps Mayfly movement per iteration. Zero retains Mayfly's
	// default 10% of the normalized search range.
	MaxVelocity float64
}

// Termination describes why an optimizer stopped.
type Termination string

const (
	// TerminationCompleted means the optimizer consumed its iteration budget.
	TerminationCompleted Termination = "completed"
	// TerminationCancelled means the run stopped on context cancellation.
	TerminationCancelled Termination = "cancelled"
	// TerminationTargetCost means a configured target cost was reached.
	TerminationTargetCost Termination = "target_cost"
	// TerminationStagnation means the best cost stopped improving within the
	// configured stagnation window.
	TerminationStagnation Termination = "stagnation"
	// TerminationConvergence means the optimizer's own distribution-aware
	// criteria judged the search converged (or numerically exhausted) before
	// the iteration budget was consumed. CMA-ES reports it for TolX, TolFun,
	// TolXUp, an ill-conditioned covariance, and the no-effect axis and
	// coordinate tests; population optimizers without such criteria never
	// return it.
	TerminationConvergence Termination = "convergence"
)

// RestartRun is one independent optimizer run inside a restart schedule.
//
// A schedule reports a single Termination for the whole run, and that reason
// is structurally uninformative: the driver overwrites it with its
// budget-exhausted reason whenever the shared evaluation budget is spent,
// which for a campaign arm sized to consume its budget is always. Every run
// therefore reported "completed" no matter how its individual restarts ended,
// so nothing said whether a restart stopped on a convergence criterion — the
// outcome a restart schedule exists to exploit — or simply ran until the
// budget ran out.
//
// Recording the per-run reasons is what distinguishes a schedule that is
// harvesting converged runs from one spending its budget on runs that stopped
// progressing long before they ended.
type RestartRun struct {
	// Termination is why this individual run stopped, verbatim from the
	// engine, not folded into this package's coarser Termination values. A
	// restart schedule's whole point is the distinction between tol_fun,
	// tol_x, condition_number and the rest, which TerminationConvergence
	// erases.
	Termination string `json:"termination"`
	// Regime names the budget account the run belongs to. IPOP records every
	// run as large; BIPOP alternates. It is empty for engines without regimes.
	Regime string `json:"regime,omitempty"`
	// Stage is the zero-based pipeline stage this run belongs to. Joint mode
	// has one; sequential and batch modes run an independent schedule per
	// circle or per batch, and without this their records would pool into one
	// undifferentiated list.
	Stage int `json:"stage"`
	// Restart is the zero-based index of the run within the invocation that
	// produced it. Zero is the initial run, so a schedule that never restarted
	// holds exactly one record. An optimizer wrapped in epochs or cold
	// attempts runs several schedules, each numbered from zero by the engine;
	// the wrapper renumbers them onto one sequence, so this stays unique
	// within a stage and keeps matching the trace, which the wrapper shifts
	// the same way.
	Restart int `json:"restart"`
	// Resume is the resume count of the invocation that recorded this run. It
	// is zero for a job's first run and rises with every resume, which is what
	// keeps the records a resumed job accumulates distinguishable: a
	// continuation drives a fresh schedule numbered from zero again, and it
	// rewrites the trace rather than extending it, so renumbering the records
	// across a resume instead would leave the two disagreeing.
	Resume int `json:"resume,omitempty"`
	// Population is the run's sampled population size. A restart strategy that
	// grows it — IPOP doubles — makes this the record of what it actually grew
	// to.
	Population int `json:"population"`
	// Iterations and Evaluations are local to this run, not cumulative.
	Iterations  int `json:"iterations"`
	Evaluations int `json:"evaluations"`
	// BestCost is the best cost this run reached on its own, which is not the
	// schedule's best unless this run produced it.
	BestCost float64 `json:"bestCost"`
}

// Result is the complete, measured outcome of an optimization run.
type Result struct {
	BestParams []float64
	// Restarts records each independent run of a restart schedule. It is nil
	// for an ordinary single run and for every engine that has no restart
	// schedule, so a caller persisting it writes nothing in those cases.
	Restarts    []RestartRun
	BestCost    float64
	Iterations  int
	Evaluations int
	Termination Termination
}

// LifecycleOptimizer supports errors, cooperative cancellation, measured
// progress, and genuine restart-from-best population seeding.
type LifecycleOptimizer interface {
	RunContext(context.Context, Problem, RunOptions) (Result, error)
}

// Optimizer defines an optimization algorithm interface.
type Optimizer interface {
	// Run executes the optimization
	// eval: objective function to minimize
	// lower, upper: parameter bounds
	// dim: dimensionality of parameter space
	// Returns: best parameters and best cost
	Run(eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64)
}

// ResumableOptimizer extends Optimizer with the ability to resume from a checkpoint.
// Implementations should seed the population with variations of the provided solution.
type ResumableOptimizer interface {
	Optimizer

	// RunWithInitial executes optimization starting from an initial solution.
	// The initial solution is used to seed the population with random variations.
	// This allows resuming optimization from a checkpoint.
	//
	// initialParams: starting point for optimization (best params from checkpoint)
	// initialCost: cost of the initial solution (for reference)
	// eval: objective function to minimize
	// lower, upper: parameter bounds
	// dim: dimensionality of parameter space
	//
	// Returns: best parameters and best cost
	//
	// Implementation notes:
	//   - Population can be seeded as: 50% clones of initialParams + 50% random
	//   - Or: initialParams + random variations with decreasing perturbation
	//   - The optimizer should respect the same random seed for reproducibility
	//   - Iteration count may reset or continue (implementation-specific)
	RunWithInitial(initialParams []float64, initialCost float64, eval func([]float64) float64, lower, upper []float64, dim int) ([]float64, float64)
}

// IterationBudgetOptimizer is implemented by optimizers that can say how many
// iterations one invocation may consume, and by every wrapper around one.
type IterationBudgetOptimizer interface {
	IterationBudget() int
}

// StageIterationBudget reports the iterations one optimizer invocation may
// spend, or zero when the optimizer does not declare a cap.
//
// A pipeline that runs an optimizer several times needs this to keep its own
// budget: the iteration count is configured on the optimizer, not passed per
// run, so without asking there is no way to tell what a further invocation
// would cost before paying for it.
func StageIterationBudget(optimizer Optimizer) int {
	reporter, ok := optimizer.(IterationBudgetOptimizer)
	if !ok {
		return 0
	}

	return max(0, reporter.IterationBudget())
}
