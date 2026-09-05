// Command cmaes-measurement submits and collects the evaluation-matched
// campaign described by go-cma-es PLAN.md Phase 11.
//
//nolint:cyclop,embeddedstructfieldcheck,err113,forbidigo,goconst,lll,noinlineerr,wrapcheck // A standalone campaign driver reports contextual CLI errors and Markdown to stdout.
package main

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/opt"
	"github.com/cwbudde/circlefit/internal/store"
)

// The registered design names, so every place that has to name one -- the
// switch in campaignDesign, the artifact defaults, and the tests that
// enumerate them -- cannot drift apart.
const (
	designPhase21     = "phase21"
	designLambda      = "lambda"
	designPilot       = "stagnation-pilot"
	designStag        = "stagnation"
	designSplit       = "budget-split"
	designLadder      = "restart-ladder"
	designHunt        = "deep-hunt"
	designCov         = "covariance"
	designActive      = "active-cma"
	designCovClean    = "covariance-clean"
	designActiveFull  = "active-cma-full"
	designShape       = "restart-shape"
	designExtendWidth = "extend-width"
)

// huntBudget is the deep hunt's per-job evaluation cap. It is 1.94x
// defaultBudget on purpose: every earlier campaign inherited the MayFly-matched
// 6,502,400 and, at that cap, no CMA-ES run at lambda 4096 has ever terminated
// on anything but maximum_evaluations -- 57 of 57 across the two committed
// restart CSVs, at a mean of 585 generations, when lambda 2048 needs 1450 to
// converge. The hunt makes no comparison against a MayFly arm, so it has no
// reason to keep a cap that exists to match MayFly's shape. 3*2^22 divides
// exactly by 1024, 2048 and 4096, so every arm stays evaluation-matched by
// construction.
const huntBudget = 12_582_912

// runsAtHuntBudget names the designs registered above the fixed cap. The deep
// hunt raised the cap because it ran no Mayfly arm and had a reason to let the
// IPOP ladder finish; the covariance campaign keeps the raised cap for a
// sharper reason. The hunt's block arm took its block best from the lambda 8192
// rung in six of eleven blocks, and that rung exists only at this budget, so a
// covariance contract run at defaultBudget would be testing a different
// mechanism from the one that produced the lead. Both arms share the number, so
// the contrast stays evaluation-matched -- it simply cannot be quoted against a
// campaign that ran at defaultBudget, which is what the reports say.
func runsAtHuntBudget(name string) bool {
	return name == designHunt || name == designCov
}

const (
	defaultBudget  = 6_502_400
	defaultProject = "cmaes-phase11"
	defaultPop     = 1024
	// campaignBlocks is the paired-block count the phase21 and lambda designs
	// registered. It is no longer universal: a design carries its own count, so
	// a descriptive pilot can buy three blocks and an inferential campaign
	// twelve, and df follows the design rather than this constant.
	campaignBlocks = 12
	// defaultCircles is the eight-circle batch every campaign before the
	// budget-split screen fitted, and the shape searchDimensions describes.
	defaultCircles = 8
	// searchDimensions is the dimensionality of that default fixture: eight
	// circles of seven parameters, fitted in one batch. It is only read by
	// hansenStagnationWindow, and both designs that call it run the default
	// fixture. A design on another circle count must not reuse it.
	searchDimensions = 56
	// resultColumns is the width of the header writeResults emits.
	resultColumns = 13
	// stagnationSeedBase is shared by the stagnation campaign and the restart
	// ladder on purpose. The ladder registers sep-ipop and sep-ipop-w60 in the
	// configuration the stagnation campaign already ran, so on these seeds both
	// arms have to reproduce that campaign's twelve cells bit for bit. That is
	// the ladder's validity check, the way the lambda screen's replication arms
	// checked Phase 21, and it is what licenses reading the two campaigns'
	// rows against each other. It also puts seed 111018 -- the block that
	// produced the best eight-circle cost this project has recorded, 752.52 --
	// inside the design, so the ladder meets the record on the seed that set it.
	stagnationSeedBase = 111_012
	// legacyResultColumns is the width every result CSV committed under docs/
	// still has, from before the backend provenance column existed. readResults
	// accepts it so a recorded campaign can still be re-analysed, rather than
	// the archive being rewritten to add a column nobody measured at the time.
	legacyResultColumns = 12
)

type arm struct {
	name              string
	optimizer         string
	covariance        string
	restartStrategy   string
	iters             int
	popSize           int
	optimizerRestarts int
	// stopStagnationIters ends an individual run after this many iterations
	// without sufficient progress, which is what lets a restart schedule
	// reclaim the budget of a run that has stopped improving. Zero is the
	// behaviour every campaign before this one measured: no criterion is
	// configured, Stop.enabled() is false, and a dead run holds its budget to
	// the cap.
	stopStagnationIters int
	// optimizerEpochs runs the engine several times in sequence, each epoch
	// re-initialized from the previous incumbent rather than cold. It is the
	// warm counterpart to optimizerRestarts, and it reaches CMA-ES as well as
	// MayFly: the server wraps WithRestarts(WithEpochs(...)) around whatever
	// newStageOptimizer built, and CMAESAdapter implements RunWithInitial.
	optimizerEpochs int
	// stopMinImprovement is the absolute cost reduction that counts as
	// progress. Zero accepts any improvement and is the only setting that can
	// become a shipped default, because a threshold in cost units does not
	// transfer between reference images whose costs differ in scale.
	stopMinImprovement float64
	// initialSigma is the CMA-ES initial step size, normalized: the adapter
	// searches a unit box with its mean at 0.5, so this is the fraction of
	// every coordinate's own range that generation zero sees. Zero leaves the
	// field unsent and the job takes app's DefaultCMAESInitialSigma of 0.3,
	// which is what every campaign before the deep hunt ran -- none of them
	// set this at all.
	initialSigma float64
	// passiveCMA turns negative rank-mu covariance adaptation off. It is
	// spelled negatively so the zero value is the shipped default (active
	// adaptation on) and arm stays comparable; the payload sends
	// activeCMA: false only when this is set.
	passiveCMA bool
	// warmStart seeds the run at recordCircles rather than from the residual,
	// which is the only way an arm can begin at a known solution: every other
	// warm start in the system comes from a checkpoint, and a checkpoint is
	// written by a run rather than by a design. It is deliberately a bool
	// rather than a slice so arm stays comparable -- the tests compare arms
	// with != to assert that two designs registered the same one.
	warmStart bool
	// stages is how many extend stages follow the arm's base stage. Zero is
	// every campaign before extend-width: one from-scratch batch job, posted
	// to /api/v1/jobs. A positive count makes the arm a schedule instead --
	// a seeded base plus this many extend steps, posted to /api/v1/schedules
	// -- because the frozen-prefix continuation an extend performs has no
	// single-job form. iters, popSize and optimizerRestarts then describe one
	// stage, and plannedEvaluations multiplies them up.
	stages int
	// width is additionalCircles on each of those stages, so stages*width is
	// what the arm appends. It is the variable extend-width exists to vary:
	// the same circles and the same evaluation cap, committed in different
	// sized groups.
	width int
	// seeded starts the base stage from recordCircles. It is separate from
	// warmStart because that field seeds the run under test itself, while this
	// seeds a base stage that the run under test then extends and never
	// revisits -- an extend freezes its prefix.
	seeded bool
}

// plannedEvaluations is the nominal cap an arm spends across every stage it
// runs. It is the one place the arithmetic lives, because printPlan and
// evaluationCap disagreeing about a staged arm would make the plan printout and
// the spend column describe different campaigns.
//
// Only CMA-ES spends exactly lambda evaluations per iteration; a MayFly arm
// evaluates its population several times per iteration and is matched to the
// budget by campaign shape instead, so it reports zero and its callers say so.
func (a arm) plannedEvaluations() int {
	if a.optimizer == "mayfly" {
		return 0
	}

	perStage := a.iters * a.popSize * max(a.optimizerEpochs, 1) * restartAttempts(a.optimizerRestarts)

	return perStage * max(a.stages, 1)
}

// recordCircle is one circle of a known solution, in the shape
// app.CircleSpecs accepts once the colour is formatted.
type recordCircle struct {
	x, y, r          float64
	red, green, blue float64
	opacity          float64
}

// recordReference is the fixture recordCircles and recordCost were recorded on.
// A design that reports either has to pin it: the coordinates are bounded by a
// 512x512 canvas and the cost is not comparable across reference images, so
// letting -ref redirect such a design would invalidate the comparison and can
// fail bounds validation on a smaller canvas.
const recordReference = "example/MayFly-512.png"

// recordCircles is the best eight-circle fit this repository has recorded on
// recordReference: cost 726.1984354654948, seed 114007, job
// 65b38e2f-e75f-4d80-8ae9-822e8a28ede6, produced by the deep hunt's blk-ipop
// arm -- block covariance, IPOP, lambda 1024, activeCMA on -- at huntBudget.
//
// It supersedes 752.5220120747884, which stood here until the extend-width
// campaign made the constant load-bearing rather than cosmetic: that design
// seeds its base stage from these circles, so a stale solution would not merely
// misreport a "vs record" column, it would fit the wrong prefix. Replacing it
// re-dates one committed number elsewhere: re-running -action analyze against
// docs/cmaes-deep-hunt-measurement.csv now prints its "vs record" column
// against 726.20, where the committed report printed it against 752.52. The
// report's own text is the record of that, and it asked for this change.
//
// Every value is inside fit.NewBounds for a 512x512 reference -- x and y in
// [-256, 767], r in [1, 512] -- which matters because app refuses an
// out-of-bounds initialCircles rather than clamping it. Two of them sit exactly
// on a bound. The colours go through an 8-bit hex round trip on the way to the
// server, so the run starts a hair off the recorded optimum.
func recordCircles() []recordCircle {
	return []recordCircle{
		{x: -255.9984368618, y: 162.9965102619, r: 453.9864281082, red: 0.3609139228, green: 0.2820853129, blue: 0.0920986151, opacity: 0.7111318709},
		{x: 744.4424245310, y: -26.0369045361, r: 505.2796370210, red: 0.3440046884, green: 0.2823678220, blue: 0.0693873643, opacity: 0.8724086185},
		{x: 265.0455681973, y: -60.1884457624, r: 173.5574152281, red: 0.2274085173, green: 0.1787557007, blue: 0.0083019424, opacity: 0.9996893742},
		{x: 238.4804876773, y: 115.1411171072, r: 123.4125723014, red: 0.0002970486, green: 0.0029787927, blue: 0.0010754562, opacity: 0.3081065940},
		{x: 603.0374244715, y: 704.5071940232, r: 497.7213147548, red: 0.3691050888, green: 0.3294344931, blue: 0.1844828750, opacity: 0.9995349479},
		{x: -130.9847103652, y: 397.7000301004, r: 378.6305876042, red: 0.1005364754, green: 0.0744961555, blue: 0.0001518657, opacity: 0.8537465786},
		{x: 404.5290495013, y: 113.0087727614, r: 35.9797436427, red: 0.9995692756, green: 0.9541192220, blue: 0.7392165928, opacity: 0.9983728139},
		{x: 197.2818260205, y: 149.9331262837, r: 347.0495027937, red: 0.2275988185, green: 0.2439870809, blue: 0.1426256606, opacity: 0.4372276652},
	}
}

// recordCost is the cost recordCircles produces at full precision.
// reportDescriptive prints each arm's minimum against it, which is the whole
// point of a design that reports an order statistic instead of a paired test.
//
// A run seeded from recordCircles does not start here. initialCircles names a
// colour in eight bits per channel, so the three colour coordinates are
// quantized on the way to the server while x, y, r and opacity pass through as
// float64. Scored back through `circlefit score`, the quantized arrangement
// costs 728.382406870524 -- 2.184 above this constant. That gap is identical
// for every arm seeded from it, so it cancels in a paired contrast between two
// such arms and does not cancel against a cold arm.
const recordCost = 726.1984354654948

// recordQuantizedCost is what recordCircles actually costs once initialCircles
// has rounded its colours, measured with
// `circlefit score --ref example/MayFly-512.png --circles <specs>`. A design
// that seeds a base stage from the record asserts its base against this, not
// against recordCost.
const recordQuantizedCost = 728.382406870524

// recordCostAtSharedCap is the best eight-circle cost recorded at
// defaultBudget, from the stagnation campaign's sep-ipop arm and returned bit
// for bit by four of the restart ladder's lambda-1024 schedules on that seed.
//
// It is not the standing record and is not meant to be. recordCost was bought
// at huntBudget, 1.94x this cap, so an arm held to defaultBudget beating it
// would be remarkable while failing to is no information at all. A design
// reports against whichever of the two shares its cap; recordReference pins
// both to the same fixture.
const recordCostAtSharedCap = 752.5220120747884

// coldRestartStrategy is the restartStrategy value an arm carries when it uses
// the engine-agnostic cold-restart wrapper rather than one of CMA-ES's own
// shared-budget schedules. It moved out of the test file when printPlan needed
// it: an arm whose only schedule is the wrapper has no engine strategy to name.
const coldRestartStrategy = "none"

// plannedContrast is one paired comparison a design registers before it runs.
// Naming them is what bounds multiplicity: the lambda screen crossed two
// factors and thereby manufactured thirteen contrasts out of eight arms, and
// the resulting Holm threshold retained a p of 0.0056. A design that declares
// the comparisons it intends pays for those and no others.
type plannedContrast struct {
	control   string
	candidate string
	// primary marks the single comparison the campaign exists to make. A
	// design may register at most one.
	primary bool
}

// plannedInteraction is a difference-in-differences: the paired per-block
// difference between two contrasts' gains.
//
// It exists because comparing two contrasts' verdicts is not a test of the
// difference between them. A design that reads "large and significant here,
// small and null there" as evidence that the two differ has committed the
// difference-in-significance error, and a 2x2 whose conclusion rests on that
// reading has to test the interaction directly instead. The reading is
// outer - inner, block by block, on the same seeds, so it carries its own
// standard error and its own degrees of freedom.
//
// A design that registers one pays multiplicity for it like any other member
// of the family, which is the honest price: the interaction asks a question
// neither contrast asks, so it is not free.
type plannedInteraction struct {
	// outer is the contrast whose gain the campaign wants to explain; inner is
	// the contrast it is explained against.
	outer plannedContrast
	inner plannedContrast
}

// describe names the interaction the way the report prints it.
func (i plannedInteraction) describe() string {
	return fmt.Sprintf("(`%s` vs `%s`) - (`%s` vs `%s`)",
		i.outer.candidate, i.outer.control, i.inner.candidate, i.inner.control)
}

// design is one registered campaign: a fixed arm set, the arm every paired
// comparison is taken against, and an optional second control reported
// underneath. Designs are named and enumerated rather than assembled from
// flags so a campaign cannot silently differ from the one that was registered.
type design struct {
	name             string
	baseline         string
	secondaryControl string
	// blocks is the design's paired-block count and the source of df. It is
	// per design rather than global so a descriptive pilot and an inferential
	// campaign can live in the same driver without either borrowing the
	// other's degrees of freedom.
	blocks int
	// seedBase is the design's first block seed prefix. It belongs to the
	// design, not to a flag, so two campaigns cannot silently share seeds and
	// a replication cannot silently fail to.
	seedBase int64
	arms     []arm
	// contrasts is the family every paired test is corrected over. phase21 and
	// lambda derive theirs from the two controls, which is how their committed
	// reports were produced and must stay reproducible; a design that fills
	// this in itself is corrected over exactly the comparisons it names.
	contrasts []plannedContrast
	// interaction is an optional difference-in-differences over two of those
	// contrasts. It joins the same Holm family, so registering one costs the
	// rest of the family a step; a design that only wants to describe the gap
	// between two contrasts should leave it nil and say so.
	interaction *plannedInteraction
	// reference and circles are the fixture. They belong to the design because
	// a campaign run on a different image is not comparable to one run on
	// example/MayFly-512.png, and a flag that silently changed either would
	// make two campaigns' rows look poolable when they are not. A design that
	// leaves circles zero runs defaultCircles; one that leaves reference empty
	// takes whatever -ref supplies, which defaults to the image every earlier
	// campaign used but is not pinned to it.
	reference string
	circles   int
	// descriptive marks a design that buys mechanism rather than inference.
	// It has too few blocks for a paired test, so the report prints arm
	// summaries and refuses to print a statistic.
	descriptive bool
	// record is the best cost previously recorded on this design's fixture.
	// When it is set the descriptive report prints each arm's minimum against
	// it, because a design whose goal is to beat a number has to say plainly
	// whether it did. Zero prints no such column.
	record float64
}

// fixture reports the reference image and circle count the design runs. A
// design that names neither falls back to defaultCircles and to the caller's
// -ref, so only a design that pins its reference is guaranteed the image it
// was registered on.
func (d design) fixture(fallback string) (string, int) {
	reference, circles := d.reference, d.circles
	if reference == "" {
		reference = fallback
	}

	if circles == 0 {
		circles = defaultCircles
	}

	return reference, circles
}

// primaryContrast returns the design's registered primary comparison.
func (d design) primaryContrast() (plannedContrast, bool) {
	for _, current := range d.contrasts {
		if current.primary {
			return current, true
		}
	}

	return plannedContrast{}, false
}

type manifestRow struct {
	Arm   string
	Block int
	Seed  int64
	// ScheduleID is set for a staged arm and empty for a single-job one. A
	// staged arm has no job ID at submit time -- the executor starts its stages
	// one at a time -- so the manifest records the campaign and collect
	// resolves the final stage's job from it.
	ScheduleID string
	// JobID is the job whose cost is this row's answer. For a staged arm that
	// is the last extend stage, resolved at collect time and empty in the
	// manifest on disk.
	JobID string
	// Stages is every job the row's evaluations were spent in, in order, each
	// with the share of the cap it was held to. It is populated at collect time
	// for a staged arm and nil for a single-job one, where jobs() supplies the
	// one-element equivalent.
	Stages []stageJob
}

// stageJob is one job a manifest row resolved to, and the budget that job alone
// was held to. A staged arm splits the campaign cap evenly across its extend
// stages, so a per-stage reading -- scoring, or the trajectory downsampler's
// buckets -- has to use the stage's share and not the campaign's.
type stageJob struct {
	Stage int
	JobID string
	// Project is the slug the job's directory lives under. It is not always
	// the campaign's: the schedule executor creates every stage under
	// app.DefaultProject, because a schedule document has no project field --
	// project is not part of JobConfig, and the base stanza is a JobConfig.
	// A reader that assumed config.project would look in an empty directory.
	Project string
	Budget  int
}

// jobs is every job this row spent evaluations in. A row from a campaign
// without stages is one job holding the whole budget, which is what every
// design before extend-width submitted.
func (r manifestRow) jobs(project string, budget int) []stageJob {
	if len(r.Stages) > 0 {
		return r.Stages
	}

	return []stageJob{{Stage: 0, JobID: r.JobID, Project: project, Budget: budget}}
}

type jobStatus struct {
	ID          string  `json:"id"`
	State       string  `json:"state"`
	Termination string  `json:"termination"`
	BestCost    float64 `json:"bestCost"`
	Elapsed     float64 `json:"elapsed"`
	Iterations  int     `json:"iterations"`
	Evaluations int     `json:"evaluations"`
	// EffectiveBackend and BackendDegraded are what actually ran, as opposed to
	// what submitJob asked for. The server has always sent them and this driver
	// used to drop them, which left the CSV unable to distinguish a CPU run from
	// an OpenCL run that fell back -- and a fallback changes the arithmetic from
	// float64 to float32 and back mid-run. Both are now written to the
	// checkpoint and restored from it, so a restarted server still reports them;
	// they are empty only for a checkpoint written before the fields existed, or
	// for a run whose process never built a renderer. The checkpoint is the
	// fallback either way, which is what collectPreliminary reads.
	EffectiveBackend string `json:"effectiveBackend"`
	BackendDegraded  bool   `json:"backendDegraded"`
}

type resultRow struct {
	manifestRow
	State             string
	Termination       string
	OptimizerVersion  string
	Score             float64
	ScoredEvaluations int
	FinalEvaluations  int
	Iterations        int
	ElapsedSeconds    float64
	// Backend is the backend that actually produced this row's costs, with a
	// "(degraded)" suffix when the device was lost mid-run. It is empty for a
	// row read back from a campaign recorded before the column existed, which is
	// every CSV committed under docs/ -- those are all CPU runs, because
	// submitJob has always named cpu explicitly and a named backend always beats
	// the server default, but they say so nowhere in the file and this driver
	// does not put words in their mouth.
	Backend string
	// Restarts is the collected checkpoint's per-run record. It is populated
	// only by collectJob, which reads the checkpoint; a row parsed back out of
	// the result CSV by readResults leaves it empty, because the result CSV is
	// one row per job and these are one row per run.
	Restarts []opt.RestartRun
}

type checkpoint struct {
	OptimizerVersion string           `json:"optimizerVersion"`
	Restarts         []opt.RestartRun `json:"restarts"`
	// EffectiveBackend and BackendDegraded mirror the server's own fields. The
	// checkpoint is the only place -action preliminary can learn them: it
	// synthesizes its jobStatus from checkpoint-info.json, which carries no
	// provenance, so without these the CSV would report unknown for every row
	// of a campaign still in flight.
	EffectiveBackend string `json:"effectiveBackend"`
	BackendDegraded  bool   `json:"backendDegraded"`
}

type checkpointInfo struct {
	Termination      string `json:"termination"`
	OptimizerVersion string `json:"optimizerVersion"`
	Iteration        int    `json:"iteration"`
	Evaluations      int    `json:"evaluations"`
}

type settings struct {
	server       string
	dataRoot     string
	reference    string
	manifestPath string
	resultsPath  string
	trajectory   string
	restartsPath string
	project      string
	action       string
	design       string
	blocks       int
	budget       int
	workers      int
	seedBase     int64
}

// designBudget resolves the per-job evaluation budget a design runs at. It is
// resolved once, in main, because -budget is two things at the same time: the
// input the arm builders size iters from, and the cap collectJob scores a
// trace against. A campaign submitted at one and collected at the other would
// silently truncate every job's score at the smaller number, which is a whole
// day of compute reported as somebody else's result. So the design owns the
// value and the flag can only assert it, exactly as -blocks and -seed-base
// already work.
func designBudget(name string, requested int) (int, error) {
	registered := defaultBudget
	if runsAtHuntBudget(name) {
		registered = huntBudget
	}

	if requested != 0 && requested != registered {
		return 0, fmt.Errorf("design %s registers a %d-evaluation budget, got -budget %d",
			name, registered, requested)
	}

	return registered, nil
}

func main() {
	config := parseFlags()

	budget, budgetErr := designBudget(config.design, config.budget)
	if budgetErr != nil {
		fmt.Fprintln(os.Stderr, budgetErr)
		os.Exit(1)
	}

	config.budget = budget

	var err error

	switch config.action {
	case "submit":
		err = submit(config)
	case "collect":
		err = collect(config)
	case "preliminary":
		err = collectPreliminary(config)
	case "analyze":
		err = analyzeDesign(config)
	case "plan":
		err = printPlan(config)
	default:
		err = fmt.Errorf("unknown action %q (want plan, submit, collect, preliminary, or analyze)", config.action)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() settings {
	var config settings
	flag.StringVar(&config.action, "action", "collect", "plan, submit, collect, preliminary, or analyze")
	flag.StringVar(&config.design, "design", "phase21", "registered campaign design: phase21, lambda, stagnation-pilot, "+
		"stagnation, budget-split, restart-ladder, deep-hunt, covariance, active-cma, covariance-clean, "+
		"active-cma-full or restart-shape")
	flag.StringVar(&config.server, "server", "http://localhost:8085", "serve base URL")
	flag.StringVar(&config.dataRoot, "data-root", "./data/cmaes-phase11", "serve data root")
	flag.StringVar(&config.reference, "ref", "example/MayFly-512.png", "reference image")
	flag.StringVar(&config.manifestPath, "manifest", "", "job manifest (default: the design's own)")
	flag.StringVar(&config.resultsPath, "results", "", "collected result CSV (default: the design's own)")
	flag.StringVar(&config.trajectory, "trajectories", "", "diagnostic trajectory CSV (default: the design's own)")
	flag.StringVar(&config.restartsPath, "restarts", "", "per-restart outcome CSV (default: the design's own)")
	flag.StringVar(&config.project, "project", defaultProject, "server project")
	flag.IntVar(&config.blocks, "blocks", 0, "assert the design's paired block count (0 uses it)")
	flag.IntVar(&config.budget, "budget", 0, "optimizer evaluation cap; 0 takes the design's own")
	flag.IntVar(&config.workers, "workers", 8, "parallel evaluation workers")
	flag.Int64Var(&config.seedBase, "seed-base", 0, "assert the design's first block seed prefix (0 uses it)")
	flag.Parse()

	return withDesignArtifacts(config)
}

// withDesignArtifacts fills every artifact path the caller left unset with one
// belonging to the selected design. Without it a second campaign collected
// with only -design would write over the first one's committed data: the
// Phase 21 paths are not a neutral default, they are one campaign's record.
// Submission already refuses to overwrite an existing manifest; this extends
// that refusal to the CSVs collection writes.
func withDesignArtifacts(config settings) settings {
	prefix := "docs/cmaes-"
	manifest := "manifest.csv"

	if config.design != "" && config.design != designPhase21 {
		prefix += config.design + "-"
		manifest = "manifest-" + config.design + ".csv"
	}

	if config.manifestPath == "" {
		config.manifestPath = filepath.Join(config.dataRoot, manifest)
	}

	if config.resultsPath == "" {
		config.resultsPath = prefix + "measurement.csv"
	}

	if config.trajectory == "" {
		config.trajectory = prefix + "trajectories.csv"
	}

	if config.restartsPath == "" {
		config.restartsPath = prefix + "restarts.csv"
	}

	return config
}

// lambdaLevels are the initial CMA-ES population sizes the lambda screen
// visits. 1024 is what every earlier measurement used; 20 is the smallest
// population app.MinPopulation permits and the closest this repository can get
// to Hansen's default of 4+floor(3*ln(n)) = 16 for the 56-dimension search; 64
// sits between them. Every level has to divide the budget exactly so all arms
// are evaluation-matched by construction rather than by post-hoc truncation.
func lambdaLevels() []int { return []int{1024, 64, 20} }

func campaignDesign(name string, budget int) (design, error) {
	// Every design registered before the deep hunt was built against the fixed
	// MayFly-matched cap, and campaignArms refuses a larger one so its arms
	// cannot silently stop being evaluation-matched. That refusal used to sit
	// in a call at the top of this function, which meant no design could ever
	// run above the cap; it belongs to the designs that inherit the cap, not
	// to the switch.
	if !runsAtHuntBudget(name) && budget > defaultBudget {
		return design{}, fmt.Errorf(
			"budget %d exceeds the fixed campaign budget %d; only %s and %s are registered above it",
			budget, defaultBudget, designHunt, designCov)
	}

	switch name {
	case designPhase21:
		arms, err := campaignArms(budget)
		if err != nil {
			return design{}, err
		}

		return withDerivedContrasts(design{
			name: name, baseline: "mayfly-single", secondaryControl: "mayfly-r16",
			blocks: campaignBlocks, seedBase: 111_000, arms: arms,
		}), nil
	case designLambda:
		screen, screenErr := lambdaScreenArms(budget)
		if screenErr != nil {
			return design{}, screenErr
		}

		return withDerivedContrasts(design{
			name: name, baseline: "cmaes-single", secondaryControl: "cmaes-ipop",
			blocks: campaignBlocks, seedBase: 111_000, arms: screen,
		}), nil
	case designPilot:
		pilot, pilotErr := stagnationPilotArms(budget)
		if pilotErr != nil {
			return design{}, pilotErr
		}

		return design{
			name: name, baseline: "sep-ipop-l20",
			blocks: stagnationPilotBlocks, seedBase: 112_000, arms: pilot,
			descriptive: true,
		}, nil
	case designStag:
		campaign, campaignErr := stagnationArms(budget)
		if campaignErr != nil {
			return design{}, campaignErr
		}

		return design{
			name: name, baseline: "sep-ipop-l20", secondaryControl: "sep-ipop",
			blocks: campaignBlocks, seedBase: stagnationSeedBase, arms: campaign,
			contrasts: []plannedContrast{
				{control: "sep-ipop-l20", candidate: "sep-ipop-l20-w102", primary: true},
				{control: "sep-ipop", candidate: "sep-ipop-w60"},
			},
		}, nil
	case designSplit:
		split, splitErr := budgetSplitArms(budget)
		if splitErr != nil {
			return design{}, splitErr
		}

		return design{
			name: name, baseline: "mayfly-r16", secondaryControl: "sep-r5",
			blocks: campaignBlocks, seedBase: 113_000, arms: split,
			reference: "example/Ref-512.png", circles: 12,
			contrasts: []plannedContrast{
				{control: "mayfly-r16", candidate: "sep-ipop", primary: true},
				{control: "sep-r5", candidate: "sep-e5"},
			},
		}, nil
	case designLadder:
		ladder, ladderErr := restartLadderArms(budget)
		if ladderErr != nil {
			return design{}, ladderErr
		}

		return design{
			name: name, baseline: "sep-ipop", secondaryControl: "sep-ipop-w60",
			blocks: campaignBlocks, seedBase: stagnationSeedBase, arms: ladder,
			reference: recordReference, circles: defaultCircles,
			record: recordCostAtSharedCap,
			contrasts: []plannedContrast{
				{control: "sep-ipop", candidate: "sep-r32-l64", primary: true},
				{control: "sep-ipop-w60", candidate: "sep-bipop-w60"},
			},
		}, nil
	case designHunt:
		hunt, huntErr := deepHuntArms(budget)
		if huntErr != nil {
			return design{}, huntErr
		}

		// No contrasts and no secondary control on purpose. The hunt reports a
		// minimum, and a minimum is an order statistic rather than a paired
		// mean, so there is nothing here for Holm to correct and nothing that
		// a t on twelve blocks would be measuring. descriptive routes the
		// report past every statistic in this driver.
		return design{
			name: name, baseline: "sep-ipop",
			blocks: huntBlocks, seedBase: 114_000, arms: hunt,
			reference: recordReference, circles: defaultCircles,
			descriptive: true, record: recordCost,
		}, nil
	case designCov:
		covariance, covErr := covarianceArms(budget)
		if covErr != nil {
			return design{}, covErr
		}

		// Two contrasts, named before the campaign runs, and both single-factor
		// against the same control. That is the whole family Holm corrects
		// here: the lambda screen manufactured thirteen contrasts out of eight
		// arms by crossing two factors and paid for it with a threshold that
		// retained a p of 0.0056, and this campaign exists to answer one
		// question sharply rather than four vaguely.
		//
		// The fixture is pinned for the same reason the hunt and the ladder pin
		// theirs: a cost on another image is not comparable, and the whole
		// point is to test a difference measured on this one.
		return design{
			name: name, baseline: "sep-ipop",
			blocks: covarianceBlocks, seedBase: covarianceSeedBase, arms: covariance,
			reference: recordReference, circles: defaultCircles,
			contrasts: []plannedContrast{
				{control: "sep-ipop", candidate: "blk-ipop", primary: true},
				{control: "sep-ipop", candidate: "sep-ipop-passive"},
			},
		}, nil
	case designActive:
		active, activeErr := activeCMAArms(budget)
		if activeErr != nil {
			return design{}, activeErr
		}

		// One contrast, and it is the whole family. Holm's first gate is
		// therefore the unadjusted 0.05: the campaign asks a single question,
		// and the lambda screen already paid for the alternative -- crossing
		// two factors turned eight arms into thirteen contrasts and retained a
		// p of 0.0056.
		//
		// The seed base is the stagnation campaign's and the restart ladder's,
		// which needs its own justification because this design repeats no arm
		// and so gets no bit-for-bit validity check from it. What it gets is a
		// by-product: blk-r32-l64 runs the identical blocks as the ladder's
		// committed sep-r32-l64 cells, at the identical rung and budget, so
		// the two can be read as a paired block-against-separable comparison
		// at a rung where *both* modes are clean. The covariance campaign
		// could not make that comparison -- its separable arm was clamped dead
		// -- so this is the one place the corpus can ask whether block's win
		// survives when separable is allowed to work. It is cross-campaign and
		// unregistered, so it is a lead, never a finding.
		return design{
			name: name, baseline: active[0].name,
			blocks: campaignBlocks, seedBase: stagnationSeedBase, arms: active,
			reference: recordReference, circles: defaultCircles,
			contrasts: []plannedContrast{
				{control: active[0].name, candidate: active[1].name, primary: true},
			},
		}, nil
	case designCovClean:
		clean, cleanErr := covarianceCleanArms(budget)
		if cleanErr != nil {
			return design{}, cleanErr
		}

		// Two single-factor contrasts, each against its own rung's separable
		// control, plus the interaction between them. The primary is the
		// question AGENTS.md blocks a covariance default on; the secondary is
		// the clamped rung the covariance report registered its +39.12 on.
		//
		// The interaction is registered rather than read off the other two.
		// The campaign's explanatory claim is that the covariance win is the
		// clamp, and that claim is about the *difference* between the two
		// rungs' effects -- which a small p at one rung beside a large p at
		// the other does not establish, however tempting the pattern looks. It
		// is a third member of the family and Holm corrects over all three.
		clamped := plannedContrast{control: clean[2].name, candidate: clean[3].name}
		cleanRung := plannedContrast{control: clean[0].name, candidate: clean[1].name, primary: true}

		return design{
			name: name, baseline: clean[0].name,
			blocks: covarianceCleanBlocks, seedBase: covarianceCleanSeedBase, arms: clean,
			reference: recordReference, circles: defaultCircles,
			contrasts:   []plannedContrast{cleanRung, clamped},
			interaction: &plannedInteraction{outer: clamped, inner: cleanRung},
		}, nil
	case designShape:
		shape, shapeErr := restartShapeArms(budget)
		if shapeErr != nil {
			return design{}, shapeErr
		}

		// Two contrasts, both against a control that spends its cap. The primary
		// is the head-to-head a default has to choose between; the secondary
		// measures the filling shape against the fixed count it replaces, which
		// is the question the restart ladder could not ask.
		//
		// A BIPOP arm was registered here and then removed before the design
		// ran; see restartShapeArms for why it could not be made to measure
		// anything without two further arms.
		return design{
			name: name, baseline: shape[0].name, secondaryControl: shape[1].name,
			blocks: restartShapeBlocks, seedBase: restartShapeSeedBase, arms: shape,
			reference: recordReference, circles: defaultCircles,
			record: recordCostAtSharedCap,
			contrasts: []plannedContrast{
				{control: shape[0].name, candidate: shape[2].name, primary: true},
				{control: shape[1].name, candidate: shape[2].name},
			},
		}, nil
	case designExtendWidth:
		widths, widthErr := extendWidthArms(budget)
		if widthErr != nil {
			return design{}, widthErr
		}

		// Four contrasts in one Holm family. The primary re-asks the +1 versus
		// +8 question on the current pin and with the current engine; the two
		// intermediate widths are registered rather than derived so the family
		// is exactly what the campaign intends to read, the way the lambda
		// screen's thirteen derived contrasts showed is worth avoiding.
		//
		// The secondary runs the other way round on purpose. Its control is the
		// cold arm, so a positive gain means the seeded prefix helped, which is
		// the direction the question is asked in: is the record an asset or a
		// cage? It is the one contrast that can invalidate the campaign's
		// premise rather than answer its primary.
		//
		// No record column. A sixteen-circle cost on this fixture is comparable
		// to nothing recorded so far, and recordCostAtSharedCap is an
		// eight-circle number; printing either would invite exactly the
		// comparison AGENTS.md forbids. The reference point this campaign has
		// is recordQuantizedCost, the cost its own base stage starts from, and
		// that belongs in the report beside every arm rather than in a BEAT
		// line.
		return design{
			name: name, baseline: "ext-w8", secondaryControl: "cold-w16",
			blocks: extendWidthBlocks, seedBase: extendWidthSeedBase, arms: widths,
			reference: recordReference, circles: extendWidthCircles,
			contrasts: []plannedContrast{
				{control: "ext-w8", candidate: "ext-w1", primary: true},
				{control: "ext-w8", candidate: "ext-w4"},
				{control: "ext-w8", candidate: "ext-w2"},
				{control: "cold-w16", candidate: "ext-w8"},
			},
		}, nil
	case designActiveFull:
		full, fullErr := activeCMAFullArms(budget)
		if fullErr != nil {
			return design{}, fullErr
		}

		// One contrast, the whole family, Holm's first gate the unadjusted
		// 0.05 -- the active-CMA campaign's reasoning, unchanged, because this
		// design asks the identical single question in a different covariance
		// mode.
		return design{
			name: name, baseline: full[0].name,
			blocks: activeCMAFullBlocks, seedBase: activeCMAFullSeedBase, arms: full,
			reference: recordReference, circles: defaultCircles,
			contrasts: []plannedContrast{
				{control: full[0].name, candidate: full[1].name, primary: true},
			},
		}, nil
	default:
		return design{}, fmt.Errorf(
			"unknown design %q (want phase21, lambda, stagnation-pilot, stagnation, budget-split, "+
				"restart-ladder, deep-hunt, covariance, active-cma, covariance-clean, active-cma-full "+
				"or restart-shape)",
			name)
	}
}

// withDerivedContrasts fills in the family a design gets when it does not name
// one: every arm against each control, controls outer. It reproduces exactly
// what buildContrasts used to compute inline, so the phase21 and lambda
// reports stay reproducible from their committed CSVs.
func withDerivedContrasts(plan design) design {
	controls := []string{plan.baseline, plan.secondaryControl}

	plan.contrasts = make([]plannedContrast, 0, len(controls)*(len(plan.arms)-1))
	for _, control := range controls {
		for _, current := range plan.arms {
			if current.name == control || current.name == plan.baseline {
				continue
			}

			plan.contrasts = append(plan.contrasts, plannedContrast{control: control, candidate: current.name})
		}
	}

	return plan
}

// stagnationPilotBlocks is deliberately far too few blocks for a paired test.
// The pilot exists to measure a mechanism -- how many restarts a window buys
// and how much budget it reclaims -- and to select the window the registered
// campaign will then test on cost. Selecting it on cost and afterwards testing
// cost would be selecting on the outcome.
const stagnationPilotBlocks = 3

// hansenStagnationWindow is Hansen's stagnation length, 120 + 30*n/lambda, for
// the dimensionality this campaign searches. It is an a-priori anchor for the
// window and not a claim of fidelity: go-cma-es stops a run after N iterations
// without sufficient progress, while Hansen's criterion tests a median of
// fitness histories across that span.
func hansenStagnationWindow(lambda int) int { return 120 + 30*searchDimensions/lambda }

// stagnationPilotArms measures what a stagnation criterion does to a restart
// schedule, at the two population sizes the registered campaign will use. Both
// are separable IPOP, the shape the lambda screen left standing; lambda 20 is
// where the measured waste is worst (56.7%) and where an IPOP ladder stays
// inside the population range that screen found unremarkable, and lambda 1024
// is the shape Phase 21 actually ran.
//
// Each level contributes its own no-criterion baseline, so reclaimed budget is
// read within the pilot's own three blocks rather than against the lambda
// screen's different seeds. Windows are half, one and four times the Hansen
// anchor. One extra cell raises stopMinImprovement off zero at lambda 20's
// anchor: the committed lambda traces show 30.9% of that arm's recorded
// improvements are smaller than 0.1 cost units and the smallest is 2.7e-05, so
// whether trivial improvements keep resetting the counter is worth one arm --
// but it stays exploratory, because an absolute cost threshold cannot become a
// default that holds on a reference image of a different scale.
func stagnationPilotArms(budget int) ([]arm, error) {
	levels := []int{20, 1024}

	arms := make([]arm, 0, len(levels)*4+1)
	for _, lambda := range levels {
		if budget%lambda != 0 {
			return nil, fmt.Errorf("budget %d is not divisible by lambda %d; the arms would not be evaluation-matched", budget, lambda)
		}

		base := stagnationArm(stagnationArmName(lambda, 0), lambda, budget, 0, 0)
		arms = append(arms, base)

		anchor := hansenStagnationWindow(lambda)
		for _, window := range []int{anchor / 2, anchor, anchor * 4} {
			if err := checkStagnationWindow(lambda, budget, window); err != nil {
				return nil, err
			}

			arms = append(arms, stagnationArm(stagnationArmName(lambda, window), lambda, budget, window, 0))
		}

		if lambda == 20 {
			arms = append(arms, stagnationArm(
				stagnationArmName(lambda, anchor)+"-min01", lambda, budget, anchor, 0.1))
		}
	}

	return arms, nil
}

// checkStagnationWindow rejects a window a run at this level could never
// reach. app.JobConfig.Validate refuses a stopStagnationIters larger than
// iters, so a budget too small for a level's window fails at submit -- after
// the preceding arms are already queued, and the manifest is written with
// O_EXCL, so the campaign cannot simply be resubmitted. Catch it here, while
// the design is still being built and nothing has been queued.
func checkStagnationWindow(lambda, budget, window int) error {
	iters := budget / lambda
	if window > iters {
		return fmt.Errorf(
			"budget %d leaves lambda %d only %d iterations, shorter than its %d-generation stagnation window",
			budget, lambda, iters, window)
	}

	return nil
}

// stagnationArms is the registered campaign the pilot selected. Two pairs, one
// per population size: each level's no-criterion baseline against the same
// level under a stagnation window of half the Hansen anchor -- 102 generations
// at lambda 20 and 60 at lambda 1024.
//
// The window is not chosen here. The pilot's rule was fixed before its data
// existed -- take the window that reclaims the most budget while still
// completing at least two restarts, ties toward the anchor -- and the pilot
// answered it at both levels with the half-anchor: it reclaimed 19.7 and 25.6
// percentage points of budget spent after the last improvement, where the
// anchor itself reclaimed nothing at lambda 20 and four times the anchor never
// fired at all, returning its baseline's cost to the last digit in all three
// blocks. Cost did not select it and cost is what this campaign tests.
//
// Four arms and exactly two registered contrasts, so Holm corrects over two
// rather than over however many arms the design happens to carry. lambda 20 is
// primary: it is the level where the pilot's criterion bought another restart
// rather than merely a longer final run, the ladder at lambda 1024 being
// capped at three runs by the evaluation budget however it terminates.
func stagnationArms(budget int) ([]arm, error) {
	arms := make([]arm, 0, 4)

	for _, lambda := range []int{20, defaultPop} {
		if budget%lambda != 0 {
			return nil, fmt.Errorf(
				"budget %d is not divisible by lambda %d; the arms would not be evaluation-matched", budget, lambda)
		}

		window := hansenStagnationWindow(lambda) / 2
		if err := checkStagnationWindow(lambda, budget, window); err != nil {
			return nil, err
		}

		arms = append(arms,
			stagnationArm(stagnationArmName(lambda, 0), lambda, budget, 0, 0),
			stagnationArm(stagnationArmName(lambda, window), lambda, budget, window, 0))
	}

	return arms, nil
}

// stagnationArmName keeps the lambda level and the window in the arm name, so
// a result CSV row says what it ran without a lookup. Window zero is the
// no-criterion baseline.
func stagnationArmName(lambda, window int) string {
	name := "sep-ipop"
	if lambda != defaultPop {
		name = fmt.Sprintf("%s-l%d", name, lambda)
	}

	if window > 0 {
		name = fmt.Sprintf("%s-w%d", name, window)
	}

	return name
}

func stagnationArm(name string, lambda, budget, window int, minImprovement float64) arm {
	return arm{
		name: name, optimizer: "cmaes", covariance: "separable", restartStrategy: "ipop",
		iters: budget / lambda, popSize: lambda, optimizerRestarts: 1,
		stopStagnationIters: window, stopMinImprovement: minImprovement,
	}
}

// ladderWork is the product of population and cold restarts every rung of the
// restart ladder holds fixed. Because lambda * restarts is constant and each
// run gets budget/ladderWork generations, every rung spends the cap exactly
// while trading sampling breadth per generation against the number of
// independent searches. 2048 is the largest product that keeps the whole legal
// width of the ladder reachable: at lambda 32 it needs 64 cold restarts, which
// is exactly app.MaxOptimizerRestarts, and 32 is above app.MinPopulation of 20.
const ladderWork = 2048

// ladderLambdas are the rungs the campaign actually runs. The product above
// admits every power of two from 1024 down to 32, but a twelve-block campaign
// costs about 1,740 job-seconds an arm on this fixture and the run had a fixed
// deadline, so the design spends its arms on span rather than on resolution:
// three rungs a factor of four apart plus the extreme one, which is where the
// restart count reaches app.MaxOptimizerRestarts. The two dropped rungs, 512
// and 128, are interior points of the same trend and neither carries a
// registered contrast.
func ladderLambdas() []int { return []int{defaultPop, 256, 64, 32} }

// restartLadderArms asks how many independent basins a fixed budget can buy,
// and whether buying more of them beats spending the budget on the shape that
// currently holds the record.
//
// The record it is aimed at is 752.52, set by the stagnation campaign's
// sep-ipop arm at seed 111018. Two things about that number motivate the whole
// design. It was reached at 1,224,704 evaluations, 19% of the cap, so the
// remaining four fifths of that run bought nothing; across the arm the best
// arrives at 57% of budget on average. And IPOP doubles lambda each rung, so a
// block affords two or three runs and twelve blocks are about thirty converged
// searches in total. The record is therefore the minimum of roughly thirty
// draws from the basin distribution, not the product of a deep search, and the
// obvious way to beat it is to take more draws rather than longer ones.
//
// The ladder holds lambda * restarts at ladderWork so every rung is
// evaluation-matched by construction, and holds generations per run constant
// at budget/ladderWork so a rung differs in how its runs are shaped rather
// than in how far each is allowed to get. lambda and the restart count
// necessarily move together, so a rung effect belongs to the pair; what makes
// it readable as the restart count is the lambda screen's existing null, which
// found lambda at 20, 64 and 1024 indistinguishable on the mean.
//
// Three restart-strategy arms run beside it at the shape Phase 21 ran.
// sep-ipop and sep-ipop-w60 repeat the stagnation campaign exactly, on its own
// seeds, as the incumbent and as the control that separates BIPOP from the
// criterion it needs. sep-bipop-w60 is the arm nothing has measured: BIPOP
// alternates large runs with small ones at randomized budgets and randomized-
// down sigma, which is a mechanism for leaving a basin rather than refining
// one.
//
// The criterion on the BIPOP arm is structural, not a re-run of the stagnation
// campaign's null. go-cma-es gives the first large run a budget equal to the
// entire schedule, and BIPOP reaches its small regime only after a large run
// has finished, so an unarmed bipop job is IPOP under another name. The window
// is the same half-anchor the stagnation campaign selected on mechanism and
// then measured, so nothing about it is chosen here.
//
// The ladder arms deliberately carry no criterion. Their restart count is
// fixed, so ending a dead run early cannot buy another one; it would only
// leave the budget unspent.
//
// Two contrasts are registered, so Holm corrects over two questions rather
// than the twenty-one that seven arms would otherwise produce. sep-r32-l64 is
// named as the primary candidate in advance rather than chosen from the
// ladder afterwards: lambda 64 is four times Hansen's default at this
// dimensionality, so covariance still adapts, while 32 restarts is the most
// independent draws available at a lambda that adapts reliably.
func restartLadderArms(budget int) ([]arm, error) {
	if budget <= 0 || budget%ladderWork != 0 {
		return nil, fmt.Errorf(
			"budget %d must be positive and divisible by %d; the ladder rungs would not be evaluation-matched",
			budget, ladderWork)
	}

	if budget%defaultPop != 0 {
		return nil, fmt.Errorf(
			"budget %d is not divisible by lambda %d; the restart-strategy arms would not be evaluation-matched",
			budget, defaultPop)
	}

	generations := budget / ladderWork

	rungs := ladderLambdas()

	arms := make([]arm, 0, len(rungs)+3)
	for _, lambda := range rungs {
		if ladderWork%lambda != 0 {
			return nil, fmt.Errorf("ladder rung lambda %d does not divide the ladder product %d", lambda, ladderWork)
		}

		restarts := ladderWork / lambda
		if lambda < app.MinPopulation {
			return nil, fmt.Errorf(
				"ladder rung lambda %d is below app.MinPopulation %d and would be refused at submit",
				lambda, app.MinPopulation)
		}

		if restarts > app.MaxOptimizerRestarts {
			return nil, fmt.Errorf(
				"ladder rung lambda %d needs %d cold restarts, above app.MaxOptimizerRestarts %d",
				lambda, restarts, app.MaxOptimizerRestarts)
		}

		arms = append(arms, arm{
			name:      fmt.Sprintf("sep-r%d-l%d", restarts, lambda),
			optimizer: "cmaes", covariance: "separable", restartStrategy: "none",
			iters: generations, popSize: lambda, optimizerRestarts: restarts,
		})
	}

	window := hansenStagnationWindow(defaultPop) / 2
	if err := checkStagnationWindow(defaultPop, budget, window); err != nil {
		return nil, err
	}

	strategyArm := func(name, strategy string, stagnation int) arm {
		return arm{
			name: name, optimizer: "cmaes", covariance: "separable", restartStrategy: strategy,
			iters: budget / defaultPop, popSize: defaultPop, optimizerRestarts: 1,
			stopStagnationIters: stagnation,
		}
	}

	// The two IPOP arms take their names from stagnationArmName, so they cannot
	// drift from the campaign whose cells they have to reproduce.
	return append(arms,
		strategyArm(stagnationArmName(defaultPop, 0), "ipop", 0),
		strategyArm(stagnationArmName(defaultPop, window), "ipop", window),
		strategyArm(fmt.Sprintf("sep-bipop-w%d", window), "bipop", window),
	), nil
}

// budgetSplitArms asks two questions on a fixture nothing has been measured on.
//
// The first is whether CMA-ES should be the default engine. Phase 21 found
// separable IPOP beating MayFly's long run in 12/12 blocks and its r16 arm in
// 11/12, and the three objections recorded against reading that as a default
// change have since been answered and none survived: lambda is a null at 20,
// 64 and 1024, separable covariance alone is a null, and arming a stagnation
// criterion is a null, so the wasted budget it was spending was never a
// recoverable gain. What remains is that every one of those numbers was taken
// on eight circles of example/MayFly-512.png. This design repeats the decisive
// contrast on a photographic reference at twelve circles, which is the open
// box of Task 10.
//
// The second is Task 3's, asked of CMA-ES rather than of MayFly. A stage's
// budget can be split three ways and every campaign so far varied only the
// third: warm epochs re-initialize from the incumbent, cold restarts are
// independent attempts scored best-of, and IPOP is the adapter's own ladder
// doubling lambda. sep-e5 and sep-r5 hold the budget fixed and spend it as five
// warm epochs and five cold attempts, and the registered contrast is one
// against the other -- that pair, not either against sep-ipop, is the epoch-
// versus-cold-restart question Task 3 asks. sep-ipop and the unsplit sep-single
// are run alongside as the reference shapes the two split arms are read
// against, but they carry no test of their own.
//
// Only two contrasts are registered, so Holm corrects over the two questions
// rather than the fifteen that six arms would otherwise produce.
func budgetSplitArms(budget int) ([]arm, error) {
	if budget <= 0 || budget%defaultPop != 0 {
		return nil, fmt.Errorf(
			"budget %d must be positive and divisible by lambda %d; the arms would not be evaluation-matched",
			budget, defaultPop)
	}
	// The two Mayfly controls keep Phase 21's fixed shape, whose cost is
	// defaultBudget evaluations, while only the CMA-ES arms derive their length
	// from the budget. A larger one would fund those past anything the Mayfly
	// arms reach and the collector would still print the engine comparison as
	// evaluation-matched. Reject that instead of reporting it.
	if budget > defaultBudget {
		return nil, fmt.Errorf(
			"budget %d exceeds the fixed Mayfly campaign budget %d; the arms would no longer be evaluation-matched",
			budget, defaultBudget)
	}

	iters := budget / defaultPop
	// The split count has to divide the generation count exactly or the split
	// arms would spend a different budget from the unsplit ones and the
	// comparison would be confounded by it. 6350 = 2 * 5^2 * 127, so four -- the
	// ladder's smallest interesting step in docs/restart-vs-budget-report.md --
	// does not divide it and five does.
	const splits = 5
	if iters%splits != 0 {
		return nil, fmt.Errorf(
			"%d generations do not divide into %d equal parts; the split arms would not be evaluation-matched",
			iters, splits)
	}

	cmaes := func(name string, epochs, restarts, perRun int) arm {
		return arm{
			name: name, optimizer: "cmaes", covariance: "separable", restartStrategy: "none",
			iters: perRun, popSize: defaultPop, optimizerRestarts: restarts, optimizerEpochs: epochs,
		}
	}

	return []arm{
		// The MayFly controls keep the shapes Phase 21 ran, so the engine
		// question is asked the same way on the new fixture.
		{name: "mayfly-single", optimizer: "mayfly", iters: 2048, popSize: defaultPop, optimizerRestarts: 1},
		{name: "mayfly-r16", optimizer: "mayfly", iters: 128, popSize: defaultPop, optimizerRestarts: 16},

		cmaes("sep-single", 1, 1, iters),
		// Five warm epochs and five cold attempts each get a fifth of the
		// generations, so all four CMA-ES arms spend the same 6,502,400.
		cmaes("sep-e5", splits, 1, iters/splits),
		cmaes("sep-r5", 1, splits, iters/splits),
		{
			name: "sep-ipop", optimizer: "cmaes", covariance: "separable", restartStrategy: "ipop",
			iters: iters, popSize: defaultPop, optimizerRestarts: 1,
		},
	}, nil
}

// lambdaScreenArms crosses the two covariance modes with four restart-and-
// population shapes. Three of its cells -- cmaes-single, cmaes-ipop and
// sep-cmaes-ipop -- repeat Phase 21 exactly, at the same seeds, so a
// cross-campaign difference shows up as a measured discrepancy instead of
// being assumed away; sep-cmaes-single is the cell Phase 21 never ran, and it
// is the one that separates covariance mode from restart strategy.
func lambdaScreenArms(budget int) ([]arm, error) {
	modes := []struct{ prefix, covariance string }{{"", "full"}, {"sep-", "separable"}}
	levels := lambdaLevels()

	arms := make([]arm, 0, len(modes)*(1+len(levels)))
	for _, mode := range modes {
		arms = append(arms, arm{
			name: mode.prefix + "cmaes-single", optimizer: "cmaes", covariance: mode.covariance,
			restartStrategy: "none", iters: budget / defaultPop, popSize: defaultPop, optimizerRestarts: 1,
		})
		for _, lambda := range levels {
			if budget%lambda != 0 {
				return nil, fmt.Errorf("budget %d is not divisible by lambda %d; the arms would not be evaluation-matched", budget, lambda)
			}

			name := mode.prefix + "cmaes-ipop"
			if lambda != defaultPop {
				name = fmt.Sprintf("%s-l%d", name, lambda)
			}

			arms = append(arms, arm{
				name: name, optimizer: "cmaes", covariance: mode.covariance,
				restartStrategy: "ipop", iters: budget / lambda, popSize: lambda, optimizerRestarts: 1,
			})
		}
	}

	return arms, nil
}

// huntBlocks is the deep hunt's block count. Blocks are independent seeds
// here rather than pairs: a best-of-N hunt gets its power from how many
// distinct initial populations it draws, not from df, so eleven is a compute
// budget rather than a statistical one. Nothing in this design reads it as
// degrees of freedom, because nothing in this design computes a t.
const huntBlocks = 11

const (
	// huntLargePop is app.MaxPopulation: the largest population the job config
	// will accept, and the rung at which no run in this repository has ever
	// been allowed to converge.
	huntLargePop = app.MaxPopulation
	// huntEpochs splits an arm's budget into warm restarts. Eight sits just
	// above the 1126-generation mean at which a lambda-1024 run trips TolFun,
	// so each epoch gets 1536 generations: long enough to converge, short
	// enough that the arm does not spend most of its cap on a dead run.
	huntEpochs = 8
)

// huntRow is one registered row of the deep hunt. It exists so the arm table
// can be read as a table -- one line per arm, one column per knob -- rather
// than as nine constructor calls in which a changed field is invisible.
type huntRow struct {
	name         string
	covariance   string
	strategy     string
	lambda       int
	epochs       int
	initialSigma float64
	passiveCMA   bool
	warmStart    bool
}

// deepHuntRows is the registered arm table. sep-ipop is the control -- the
// configuration that holds the record -- and the rows divide into two kinds
// that have to be read differently.
//
// Four are true single-factor rows against it, so an arm that wins names its
// own cause: blk-ipop moves covariance alone, sep-ipop-s015 and sep-ipop-s050
// move initialSigma alone, and sep-ipop-passive moves activeCMA alone.
//
// The other four are compound and cannot identify a cause. sep-l4096 drops
// IPOP and raises lambda; blk-l4096 does both and changes covariance on top;
// sep-e8 drops IPOP and splits the budget into epochs; sep-warm-e8 does that
// and also sets initialSigma and warm-starts from the record. They are here
// because the budget buys the whole table and each is worth a look, but a win
// by one of them is an exploratory lead for a registered campaign, never a
// finding about the knob its name happens to mention.
//
// The three knobs nobody has turned are covarianceMode: block -- eight 7x7
// blocks, one per circle, because app pins blockSize to ParametersPerCircle --
// initialSigma, and activeCMA. None of them has been set by any campaign in
// this repository; every earlier one varied lambda, the restart strategy and a
// stagnation window, and all three of those are now measured nulls.
func deepHuntRows() []huntRow {
	return []huntRow{
		{name: "sep-ipop", covariance: "separable", strategy: "ipop", lambda: defaultPop, epochs: 1},
		{name: "blk-ipop", covariance: "block", strategy: "ipop", lambda: defaultPop, epochs: 1},
		{name: "blk-l4096", covariance: "block", strategy: "none", lambda: huntLargePop, epochs: 1},
		{name: "sep-l4096", covariance: "separable", strategy: "none", lambda: huntLargePop, epochs: 1},
		{name: "sep-ipop-s015", covariance: "separable", strategy: "ipop", lambda: defaultPop, epochs: 1, initialSigma: 0.15},
		{name: "sep-ipop-s050", covariance: "separable", strategy: "ipop", lambda: defaultPop, epochs: 1, initialSigma: 0.50},
		{name: "sep-ipop-passive", covariance: "separable", strategy: "ipop", lambda: defaultPop, epochs: 1, passiveCMA: true},
		{name: "sep-e8", covariance: "separable", strategy: "none", lambda: defaultPop, epochs: huntEpochs},
		{name: "sep-warm-e8", covariance: "separable", strategy: "none", lambda: defaultPop, epochs: huntEpochs, initialSigma: 0.05, warmStart: true},
	}
}

// deepHuntArms sizes the registered table against the budget. Every arm spends
// iters*lambda*epochs evaluations and every one of those products has to equal
// the budget exactly, so the table is evaluation-matched by construction rather
// than by truncating a longer arm afterwards -- even though a descriptive
// design would survive an unmatched one, because an arm that quietly ran on
// more evaluations than its neighbours would make the minimum column
// unreadable.
func deepHuntArms(budget int) ([]arm, error) {
	return sizeHuntRows(deepHuntRows(), budget)
}

// sizeHuntRows turns a registered row table into arms at the given budget,
// refusing any table it cannot keep evaluation-matched. It is shared by the
// deep hunt and the covariance campaign so the two cannot drift apart in how
// they size an arm: the campaign's sep-ipop and blk-ipop rows have to be the
// configurations the hunt actually ran, or its confirmation would be of
// something else.
func sizeHuntRows(rows []huntRow, budget int) ([]arm, error) {
	if budget <= 0 {
		return nil, fmt.Errorf("budget %d must be positive", budget)
	}

	arms := make([]arm, 0, len(rows))

	for _, row := range rows {
		work := row.lambda * row.epochs
		if budget%work != 0 {
			return nil, fmt.Errorf(
				"budget %d is not divisible by arm %s's lambda %d times %d epochs; "+
					"the arms would not be evaluation-matched",
				budget, row.name, row.lambda, row.epochs)
		}

		iters := budget / work

		if row.lambda < app.MinPopulation || row.lambda > app.MaxPopulation {
			return nil, fmt.Errorf("arm %s wants lambda %d, outside app's %d..%d population range",
				row.name, row.lambda, app.MinPopulation, app.MaxPopulation)
		}

		if row.epochs > app.MaxOptimizerEpochs {
			return nil, fmt.Errorf("arm %s wants %d epochs, above app's maximum of %d",
				row.name, row.epochs, app.MaxOptimizerEpochs)
		}

		if iters > app.MaxIterations {
			return nil, fmt.Errorf("arm %s wants %d iterations, above app's maximum of %d",
				row.name, iters, app.MaxIterations)
		}

		arms = append(arms, arm{
			name: row.name, optimizer: "cmaes",
			covariance: row.covariance, restartStrategy: row.strategy,
			iters: iters, popSize: row.lambda, optimizerRestarts: 1,
			optimizerEpochs: row.epochs, initialSigma: row.initialSigma,
			passiveCMA: row.passiveCMA, warmStart: row.warmStart,
		})
	}

	return arms, nil
}

// covarianceBlocks is the paired-block count every registered campaign in this
// driver has used. The covariance campaign has no reason to depart from it: the
// lead it tests is large -- 77.24 cost points, 11 blocks of 11 in the deep hunt
// -- so twelve blocks is not the marginal case that would argue for more, and
// keeping the count makes its power directly comparable to the campaigns whose
// nulls it is read against.
const covarianceBlocks = campaignBlocks

// covarianceSeedBase is deliberately *not* the deep hunt's 114_000, and the
// reason is the opposite of the restart ladder's. The ladder shared the
// stagnation campaign's seeds so its repeated arms would reproduce that
// campaign bit for bit, which is a validity check worth having when the new
// arms are the new evidence. Here the arms are the same arms, so reusing
// 114_000 would re-report the eleven blocks that produced the lead rather than
// test it. Fresh seeds are the whole point: an unregistered 11-of-11 has to be
// confirmed on draws it did not come from. 115_001-115_003 are avoided too --
// they belong to the deep hunt's warm-sigma probe, which is not a registered
// design and so is invisible to the disjointness test.
const covarianceSeedBase = 116_000

// covarianceRows is the registered arm table, and every row is a single-factor
// move against sep-ipop, the configuration that held the record before the deep
// hunt and the control every CMA-ES campaign here has used.
//
// blk-ipop moves covarianceMode alone, from separable to block -- eight 7x7
// blocks, one per circle, because app pins blockSize to ParametersPerCircle. It
// is the primary contrast, and it exists because the deep hunt found it better
// in 11 blocks of 11 by a mean of 77.24 while registering no contrast at all,
// so the difference is an observed one that has never been tested.
//
// sep-ipop-passive moves activeCMA alone. The deep hunt registered exactly this
// arm and could not read it: ten of its eleven jobs were cancelled while queued
// to free workers, leaving n = 1. It is the secondary contrast because the
// campaign owes the knob a measurement, not because anything suggests it wins.
//
// There is no compound row. The deep hunt bought four of them with the budget
// that was going spare and none identified a cause; this campaign is an
// inferential one, and every arm it adds is a contrast Holm has to pay for.
func covarianceRows() []huntRow {
	return []huntRow{
		{name: "sep-ipop", covariance: "separable", strategy: "ipop", lambda: defaultPop, epochs: 1},
		{name: "blk-ipop", covariance: "block", strategy: "ipop", lambda: defaultPop, epochs: 1},
		{name: "sep-ipop-passive", covariance: "separable", strategy: "ipop", lambda: defaultPop, epochs: 1, passiveCMA: true},
	}
}

// covarianceArms sizes that table against the budget. It shares deepHuntArms'
// checker because the rows are the same shape and the matching requirement is
// the same one: every arm spends iters*lambda*epochs evaluations and all three
// products have to equal the budget exactly, or a paired difference would be
// reading an unmatched pair.
func covarianceArms(budget int) ([]arm, error) {
	return sizeHuntRows(covarianceRows(), budget)
}

// activeCMALambda is the population every arm of the active-CMA campaign
// holds, and it is chosen for the size of the treatment rather than for
// realism. Negative rank-mu adaptation is not a knob whose effect is constant:
// its whole magnitude is the negativeMass that active.go scales the negative
// weights by, and that mass shrinks steeply with lambda long before the clamp
// binds. At 56 dimensions in block mode it is 0.281 at lambda 64, 0.0554 at
// 256 and 0.00155 at the shipped popSize of 1024 -- so a campaign run at the
// default population would apply a treatment three orders of magnitude smaller
// than this one and return a null that says nothing about the knob. That is
// the covariance campaign's void reached by a second route, and
// activeCMANegativeMass exists to refuse it.
//
// lambda 64 is also four times Hansen's default at this dimensionality, so
// covariance still adapts, and it is the restart ladder's own primary rung.
const activeCMALambda = 64

// activeCMAMinNegativeMass is the smallest treatment this design will register,
// and it is set to exclude two distinct failures rather than one.
//
// The clamped regime is the failure the covariance campaign hit. Where the
// rank-mu correction drives cmu to its 1-c1 clamp, Hansen's
// positive-definiteness guard collapses to the difference of two quantities
// that are equal to within summation rounding, and the mass that survives is
// about 1e-17 -- sixteen orders of magnitude below a live one. That is a near
// cancellation rather than the exact zero docs/cmaes-covariance-report.md
// describes, and the distinction is arithmetic trivia: at 1e-17 the arm was
// measured bit-identical to its control in all twelve blocks. A floor at any
// scale a person would write catches it.
//
// The near-inert regime is the subtler one, and it is why the floor is not
// simply a guard against rounding. Block covariance at the shipped popSize of
// 1024 is genuinely unclamped, and its mass is still only 0.00155: a campaign
// there would apply a treatment 180x smaller than this one and return a null
// that says nothing about the knob. 0.01 excludes both, and the registered rung
// clears it by a factor of 28.
const activeCMAMinNegativeMass = 0.01

// activeCMAArms registers the campaign that finally measures activeCMA. Two
// campaigns have now failed to: the deep hunt lost its arm to ten cancelled
// jobs, and the covariance campaign lost its secondary contrast to the library
// -- sep-ipop-passive returned costs bit-identical to its control in all twelve
// blocks because go-cma-es v0.1.0's rank-mu clamp makes the knob arithmetically
// inert in separable mode at lambda 1024. See docs/cmaes-covariance-report.md.
//
// Three properties of this design are the direct consequence of that failure,
// and none of them is a preference:
//
// Block covariance, because it is the mode the covariance campaign established
// as the winner and therefore the mode a default would name, and because it is
// clean at every lambda this design could reach.
//
// Cold restarts rather than IPOP, because an IPOP ladder doubles its
// population: blk-ipop against blk-ipop-passive would differ on its first rung
// alone and be inert on 2048, 4096 and 8192, which is the same dilution to
// nothing in a new costume. optimizerRestarts holds lambda fixed, so every one
// of the 32 runs applies the identical treatment.
//
// And lambda 64, for the reason activeCMALambda gives.
//
// The rung is the restart ladder's ladderWork product, so both arms are sized
// against the cap identically: 64 * 32 = 2048 and each run gets budget/2048
// generations.
//
// That makes the design cap-matched and NOT spend-matched, and it is registered
// that way rather than described as evaluation-matched. WithRestarts runs a
// fixed count of attempts and consults no evaluation budget, so a run that
// trips TolFun early returns its remainder to nobody -- the very hole
// PLAN.md's restart-ladder box records, and closing it is a change to the
// restart wrapper rather than a choice available to a campaign. The ladder
// measured the identical lambda 64 schedule at a mean of 2,387,822
// evaluations, 36.7% of the cap, ranging 34.4-39.9% across its twelve blocks.
// Expect this campaign to land there too.
//
// Two consequences the report has to carry rather than assume away. The arms
// may not be spend-matched to each other, because active and passive
// adaptation can reach TolFun after different numbers of evaluations, so
// finalEvaluations must be read per arm and not taken as the cap. And the
// ladder's own five-point spread is the yardstick for that reading: a spend
// asymmetry inside it is seed noise, while a much larger one is a finding
// about the knob and has to be reported as part of the result rather than
// beside it.
func activeCMAArms(budget int) ([]arm, error) {
	if budget <= 0 || budget%ladderWork != 0 {
		return nil, fmt.Errorf(
			"budget %d must be positive and divisible by %d; the two arms would not be evaluation-matched",
			budget, ladderWork)
	}

	restarts := ladderWork / activeCMALambda

	if activeCMALambda < app.MinPopulation {
		return nil, fmt.Errorf("lambda %d is below app.MinPopulation %d and would be refused at submit",
			activeCMALambda, app.MinPopulation)
	}

	if restarts > app.MaxOptimizerRestarts {
		return nil, fmt.Errorf("lambda %d needs %d cold restarts, above app.MaxOptimizerRestarts %d",
			activeCMALambda, restarts, app.MaxOptimizerRestarts)
	}

	// The design's whole validity rests on the treatment being large enough to
	// read, and that is arithmetic this driver can check rather than assert.
	// Refusing here means a future edit to the rung cannot quietly reproduce
	// the void that cost the covariance campaign its secondary contrast.
	mass := activeCMANegativeMass(searchDimensions, activeCMALambda, app.ParametersPerCircle)
	if mass < activeCMAMinNegativeMass {
		return nil, fmt.Errorf(
			"block covariance at lambda %d in %d dimensions gives active adaptation a negative mass of %g, "+
				"below the %g this design requires; the contrast would measure nothing",
			activeCMALambda, searchDimensions, mass, activeCMAMinNegativeMass)
	}

	control := arm{
		name:      fmt.Sprintf("blk-r%d-l%d", restarts, activeCMALambda),
		optimizer: "cmaes", covariance: "block", restartStrategy: "none",
		iters: budget / ladderWork, popSize: activeCMALambda, optimizerRestarts: restarts,
	}

	passive := control
	passive.name = control.name + "-passive"
	passive.passiveCMA = true

	return []arm{control, passive}, nil
}

// hansenWeights returns go-cma-es v0.1.0's normalized positive recombination
// weights, the unnormalized log base they are built from, and the variance
// effective selection mass they imply. It is split out of
// activeCMANegativeMass so the clamp predicate below can reach muEff without
// duplicating the arithmetic; the operations and their order are unchanged, so
// every value it returns is bit-identical to what that function computed
// inline.
func hansenWeights(lambda int) ([]float64, float64, float64) {
	mu := lambda / 2

	weightBase := math.Log(math.Max(float64(mu), float64(lambda)/2) + 0.5)

	weights := make([]float64, mu)
	weightSum := 0.0

	for i := range weights {
		weights[i] = weightBase - math.Log(float64(i+1))
		weightSum += weights[i]
	}

	squareSum := 0.0

	for i := range weights {
		weights[i] /= weightSum
		squareSum += weights[i] * weights[i]
	}

	return weights, weightBase, 1 / squareSum
}

// hansenLearningRates returns the rank-one and rank-mu covariance learning
// rates go-cma-es v0.1.0 derives, and whether the rank-mu rate arrived at its
// 1-c1 clamp.
//
// blockDimension is what selects the covariance mode: 1 for separable, the
// per-circle parameter count for block, and anything at or above dimension for
// full, which takes no correction at all. The correction divides by
// blockDimension+2, so the smaller the block the larger the multiplier and the
// sooner the clamp binds -- which is why separable degenerates first.
//
// The clamp is the defect docs/cmaes-covariance-report.md measured. Once cmu is
// the clamp the covariance decay 1-c1-cmu is exactly zero, so the matrix is
// rebuilt from each generation and remembers nothing, and Hansen's
// positive-definiteness guard collapses to the difference of two equal
// quantities, so activeCMA is inert. Both consequences follow from this one
// boolean, which is why a design may test it directly rather than inferring it
// from a measured mass.
//
// The parameter and local names are Hansen's notation and go-cma-es's own field
// names. Renaming them breaks the correspondence these functions exist to
// preserve, which is the only thing that makes them reviewable.
//
//nolint:varnamelen // c1 and cmu are Hansen's notation and go-cma-es's own
func hansenLearningRates(dimension int, muEff float64, blockDimension int) (float64, float64, bool) {
	n := float64(dimension)

	c1 := 2 / ((n+1.3)*(n+1.3) + muEff)
	cmu := math.Min(1-c1, 2*(muEff-2+1/muEff)/((n+2)*(n+2)+muEff))

	if blockDimension < dimension {
		cmu = math.Min(1-c1, cmu*(n+2)/float64(blockDimension+2))
	}

	return c1, cmu, cmu >= 1-c1
}

// rankMuClamped reports whether a covariance mode is degenerate at a
// population, in the dimensionality a design searches. It is the predicate a
// design uses to state, and have checked, which of its cells the library's
// defect applies to.
func rankMuClamped(dimension, lambda, blockDimension int) bool {
	if lambda/2 < 1 || dimension < 1 {
		return false
	}

	_, _, muEff := hansenWeights(lambda)
	_, _, clamped := hansenLearningRates(dimension, muEff, blockDimension)

	return clamped
}

// separableBlockDimension is the blockDimension separable covariance presents
// to the arithmetic above: it adapts one variance per coordinate. Block mode
// passes app.ParametersPerCircle, one dense matrix per circle, and full mode
// passes the search dimensionality itself, which takes no correction at all.
const separableBlockDimension = 1

// activeCMANegativeMass returns the mass go-cma-es v0.1.0 scales its negative
// rank-mu weights by, for a configuration this driver is about to register. It
// replicates deriveStrategyParameters and deriveNegativeWeights rather than
// calling them, because both are unexported; the replication is pinned against
// the three cmu values docs/cmaes-covariance-report.md read out of the library
// itself, so a divergence fails a test rather than passing silently.
//
// A vanishing mass is the failure this whole campaign exists because of: when
// the separable or block correction drives cmu to its 1-c1 clamp, Hansen's
// positive-definiteness guard collapses to the difference of two quantities
// that are equal to within summation rounding, every negative weight is scaled
// to about 1e-17 of itself, and activeCMA is inert however the job is
// configured. It is a near cancellation rather than the exact zero
// docs/cmaes-covariance-report.md describes -- which is why callers must test
// the result against activeCMAMinNegativeMass and not against zero.
func activeCMANegativeMass(dimension, lambda, blockDimension int) float64 {
	mu := lambda / 2
	if mu < 1 || dimension < 1 {
		return 0
	}

	weights, weightBase, muEff := hansenWeights(lambda)
	//nolint:varnamelen // c1 and cmu are Hansen's notation and go-cma-es's own
	// field names, kept here for the same reason hansenLearningRates keeps them.
	c1, cmu, _ := hansenLearningRates(dimension, muEff, blockDimension)

	n := float64(dimension)

	if cmu <= 0 || lambda-mu <= 0 {
		return 0
	}

	negativeSum := 0.0
	negativeSquareSum := 0.0

	for rank := mu + 1; rank <= lambda; rank++ {
		weight := math.Min(0, weightBase-math.Log(float64(rank)))
		negativeSum -= weight
		negativeSquareSum += weight * weight
	}

	if negativeSum == 0 || negativeSquareSum == 0 {
		return 0
	}

	muEffMinus := negativeSum * negativeSum / negativeSquareSum
	mass := math.Min(1+c1/cmu, 1+2*muEffMinus/(muEff+2))

	// Positive weights are normalized to sum 1 above, so this is exactly the
	// guard active.go applies -- and the expression that all but cancels once
	// cmu has been assigned the clamp, leaving the rounding residue rather
	// than a true zero.
	positiveDefinite := (1 - c1 - cmu*sumOf(weights)) / (n * cmu)

	return math.Min(mass, positiveDefinite)
}

func sumOf(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}

	return total
}

// covarianceCleanSeedBase is a fresh range, and choosing one is the design's
// central methodological decision rather than housekeeping. The lead this
// campaign re-asks was read off the restart ladder's and the active-CMA
// campaign's shared blocks at seeds 111013-111024; re-running that contrast on
// those seeds would re-read the data that raised it rather than test it. The
// price is the bit-for-bit replication check a shared range buys, and this
// design pays it: it repeats no committed cell.
const covarianceCleanSeedBase = 117_000

// covarianceCleanBlocks is twice the count every inferential campaign in this
// repository has run, bought with the wall clock the fixed cap frees relative
// to the two campaigns that ran at huntBudget.
//
// It is a direct response to the active-CMA report, which put the cost of a
// twelve-block null plainly: at a paired sd of 48.43 an effect of the size it
// observed needs roughly four times the blocks. Twenty-four is not four times
// and this design does not pretend it is -- it is the largest count the
// campaign window affords, it narrows the paired interval by about 29%, and a
// null here still has to be reported as absence of evidence.
const covarianceCleanBlocks = 24

// The two rungs the design crosses covariance mode against. Each is chosen for
// what go-cma-es v0.1.0 does at it, not for realism.
//
// covarianceCleanRung is where both modes are clean, so the contrast there
// measures the covariance model and nothing else. It is the restart ladder's
// primary rung and the active-CMA campaign's, which is what makes this
// campaign's reading comparable to the lead it tests.
//
// covarianceShippedRung is app's default popSize and the rung
// docs/cmaes-covariance-report.md registered its +39.12 on. Separable clamps
// there and block does not, so the contrast measures the covariance model
// confounded with a dead control -- deliberately, because that confound is the
// thing under test.
const (
	covarianceCleanRung   = 64
	covarianceShippedRung = defaultPop
)

// covarianceCleanArms registers the campaign that asks whether block
// covariance's registered win survives when separable is allowed to work.
//
// docs/cmaes-covariance-report.md measured block against separable at
// +39.12 (t = +2.72, 11/12) and it rejects under Holm, but its separable
// control ran at lambda 1024, where the rank-mu clamp makes separable
// memoryless -- so the registered result cannot distinguish a better
// covariance model from a broken comparison. The active-CMA campaign read the
// same contrast at lambda 64, where both modes are clean, and got +7.27
// (t = 0.54, 7/12). That reading is cross-campaign and unregistered, so the
// corpus currently holds a registered result and an unregistered lead pointing
// opposite ways, and AGENTS.md blocks a covariance default on resolving it.
//
// The design is a 2x2 -- covariance mode crossed with rung -- and it is a 2x2
// for a reason that a single clean-rung contrast would miss. A clean-rung null
// on its own cannot separate "block is no better anywhere" from "this campaign
// lacked the power to see it", because the effect it is chasing is small. The
// clamped rung is run in the same campaign, on the same seeds and the same
// cap, so the two contrasts can be subtracted block by block: if block's lead
// is larger at 1024 than at 64, the campaign has shown that the covariance win
// is the clamp, which is a positive finding rather than a failure to reject.
//
// That subtraction is registered as an interaction rather than eyeballed off
// the two contrasts' verdicts. Reading "significant at 1024, null at 64" as a
// difference between the rungs would be the difference-in-significance error:
// the two tests can land either side of a threshold while the effects they
// estimate are indistinguishable. So the family is three, Holm corrects over
// three, and the explanatory claim is the one that carries its own p.
//
// Cold restarts at a fixed lambda rather than IPOP, for the reason the
// active-CMA design gives: an IPOP ladder doubles its population, so it would
// walk each arm across the clamp boundary mid-run and the rung would stop
// naming a condition. optimizerRestarts holds lambda fixed, so every run in an
// arm sits on the same side of it.
//
// The cap is defaultBudget, not huntBudget. That costs comparability with the
// covariance campaign's absolute figures and buys it with the restart ladder
// and the active-CMA campaign, which is the trade this design wants: those two
// are where the lead came from, and every contrast that matters here is
// within-campaign anyway.
//
// Like the active-CMA campaign this is cap-matched and NOT spend-matched.
// WithRestarts consults no evaluation budget, so an arm whose runs trip TolFun
// early returns the remainder to nobody. Expect the lambda 64 arms near the
// ladder's measured 36.7% of cap and the lambda 1024 arms well above it, and
// read finalEvaluations per arm rather than assuming the cap.
func covarianceCleanArms(budget int) ([]arm, error) {
	if budget <= 0 || budget%ladderWork != 0 {
		return nil, fmt.Errorf(
			"budget %d must be positive and divisible by %d; the arms would not be evaluation-matched",
			budget, ladderWork)
	}

	modes := []struct {
		name       string
		short      string
		blockDimen int
	}{
		{name: "separable", short: "sep", blockDimen: separableBlockDimension},
		{name: "block", short: "blk", blockDimen: app.ParametersPerCircle},
	}

	// Which cell the library's defect applies to is the design's whole claim,
	// so it is checked here rather than asserted in a comment. A future edit to
	// either rung that moved a cell across the clamp boundary would change what
	// the campaign measures without changing anything a reader would notice.
	wantClamped := map[string]bool{
		"separable-64": false, "block-64": false,
		"separable-1024": true, "block-1024": false,
	}

	arms := make([]arm, 0, 4)

	for _, rung := range []int{covarianceCleanRung, covarianceShippedRung} {
		restarts := ladderWork / rung

		if rung < app.MinPopulation {
			return nil, fmt.Errorf("lambda %d is below app.MinPopulation %d and would be refused at submit",
				rung, app.MinPopulation)
		}

		if restarts > app.MaxOptimizerRestarts {
			return nil, fmt.Errorf("lambda %d needs %d cold restarts, above app.MaxOptimizerRestarts %d",
				rung, restarts, app.MaxOptimizerRestarts)
		}

		for _, mode := range modes {
			clamped := rankMuClamped(searchDimensions, rung, mode.blockDimen)

			key := fmt.Sprintf("%s-%d", mode.name, rung)
			if want, known := wantClamped[key]; !known || want != clamped {
				return nil, fmt.Errorf(
					"%s covariance at lambda %d in %d dimensions clamps=%t, but the design is registered "+
						"for clamps=%t; the 2x2 would not measure the condition it names",
					mode.name, rung, searchDimensions, clamped, want)
			}

			arms = append(arms, arm{
				name:      fmt.Sprintf("%s-r%d-l%d", mode.short, restarts, rung),
				optimizer: "cmaes", covariance: mode.name, restartStrategy: "none",
				iters: budget / ladderWork, popSize: rung, optimizerRestarts: restarts,
			})
		}
	}

	return arms, nil
}

// activeCMAFullBlocks is this design's own count, not a reference to the
// covariance-clean campaign's, even though the two are equal today.
//
// They are equal for the same external reason -- twenty-four is what a campaign
// window affords at the fixed cap -- and for different internal ones, so a
// shared constant would make one design's power argument silently govern the
// other's. This one's argument is its own: docs/cmaes-active-cma-report.md put
// the cost of its twelve-block null at roughly four times the blocks, which is
// forty-eight, and this campaign runs half that while applying a treatment 3.8x
// larger at the same rung. Whether the larger treatment makes up the difference
// is exactly what it measures, so the count is a bet the report has to state
// rather than a number inherited from elsewhere.
const activeCMAFullBlocks = 24

// activeCMAFullSeedBase is a fresh range. The full-mode campaign repeats no
// committed cell, so it gets no bit-for-bit replication check, and it asks its
// question of blocks no earlier design has seen.
const activeCMAFullSeedBase = 118_000

// activeCMAFullArms registers activeCMA measured in full covariance mode, which
// is the gap docs/cmaes-active-cma-report.md names in its own conclusion: the
// knob "stays unmeasured in full covariance mode, which never clamps and is the
// other clean place to ask".
//
// The block campaign that preceded it retained its null at t = -1.70 and read
// as absence of evidence rather than a zero. Full mode is the better place to
// re-ask it for a reason that is arithmetic rather than preference: the whole
// magnitude of the treatment is the mass active.go scales the negative weights
// by, and at lambda 64 in 56 dimensions that mass is 1.065 in full mode against
// 0.281 in block. So this campaign applies a treatment 3.8x larger than the one
// that returned the null, at the same rung, the same cap and the same restart
// schedule -- which is what makes it a sharper instrument and not merely
// another arm.
//
// Full mode also never clamps at any lambda, at any dimensionality this project
// searches, so the rung carries no risk of reproducing the covariance
// campaign's void. That is asserted below rather than assumed.
//
// Everything else is held at the block campaign's settings on purpose. Same
// rung, same ladderWork product, same cold restarts, same cap. The two
// campaigns then differ in covariance mode alone, so their nulls and their
// intervals can be read against each other even though the comparison is
// across campaigns and therefore a lead rather than a finding.
func activeCMAFullArms(budget int) ([]arm, error) {
	if budget <= 0 || budget%ladderWork != 0 {
		return nil, fmt.Errorf(
			"budget %d must be positive and divisible by %d; the two arms would not be evaluation-matched",
			budget, ladderWork)
	}

	restarts := ladderWork / activeCMALambda

	if restarts > app.MaxOptimizerRestarts {
		return nil, fmt.Errorf("lambda %d needs %d cold restarts, above app.MaxOptimizerRestarts %d",
			activeCMALambda, restarts, app.MaxOptimizerRestarts)
	}

	if searchDimensions > app.MaxCMAESFullDimensions {
		return nil, fmt.Errorf("full covariance is refused above %d dimensions and this design searches %d",
			app.MaxCMAESFullDimensions, searchDimensions)
	}

	// Full mode takes no block correction, so the clamp cannot bind. Checked
	// rather than asserted, because the whole point of running here is that the
	// treatment survives.
	if rankMuClamped(searchDimensions, activeCMALambda, searchDimensions) {
		return nil, fmt.Errorf("full covariance at lambda %d in %d dimensions clamps; the contrast would be void",
			activeCMALambda, searchDimensions)
	}

	mass := activeCMANegativeMass(searchDimensions, activeCMALambda, searchDimensions)
	if mass < activeCMAMinNegativeMass {
		return nil, fmt.Errorf(
			"full covariance at lambda %d in %d dimensions gives active adaptation a negative mass of %g, "+
				"below the %g this design requires; the contrast would measure nothing",
			activeCMALambda, searchDimensions, mass, activeCMAMinNegativeMass)
	}

	control := arm{
		name:      fmt.Sprintf("full-r%d-l%d", restarts, activeCMALambda),
		optimizer: "cmaes", covariance: "full", restartStrategy: "none",
		iters: budget / ladderWork, popSize: activeCMALambda, optimizerRestarts: restarts,
	}

	passive := control
	passive.name = control.name + "-passive"
	passive.passiveCMA = true

	return []arm{control, passive}, nil
}

func campaignArms(budget int) ([]arm, error) {
	if budget <= 0 || budget%defaultPop != 0 {
		return nil, fmt.Errorf("budget %d must be positive and divisible by population %d", budget, defaultPop)
	}
	// The two Mayfly arms have a fixed campaign shape -- 2048 iterations,
	// however they are split across restarts -- whose cost is defaultBudget
	// evaluations. Only the CMA-ES arms derive their length from the budget,
	// so a larger one would fund them past anything the Mayfly arms ever
	// reach and the collector would still print the comparison as
	// evaluation-matched. Reject that instead of reporting it.
	if budget > defaultBudget {
		return nil, fmt.Errorf(
			"budget %d exceeds the fixed Mayfly campaign budget %d; the arms would no longer be evaluation-matched",
			budget, defaultBudget,
		)
	}

	return []arm{
		{name: "mayfly-single", optimizer: "mayfly", iters: 2048, popSize: defaultPop, optimizerRestarts: 1},
		{name: "mayfly-r16", optimizer: "mayfly", iters: 128, popSize: defaultPop, optimizerRestarts: 16},
		{name: "cmaes-single", optimizer: "cmaes", covariance: "full", restartStrategy: "none", iters: budget / defaultPop, popSize: defaultPop, optimizerRestarts: 1},
		{name: "cmaes-ipop", optimizer: "cmaes", covariance: "full", restartStrategy: "ipop", iters: budget / defaultPop, popSize: defaultPop, optimizerRestarts: 1},
		{name: "sep-cmaes-ipop", optimizer: "cmaes", covariance: "separable", restartStrategy: "ipop", iters: budget / defaultPop, popSize: defaultPop, optimizerRestarts: 1},
	}, nil
}

// restartShapeSeedBase is a fresh range, and it has to be.
//
// The question this campaign asks was raised on the restart ladder's seeds --
// its primary contrast is that campaign's, re-asked spend-matched -- so
// re-running it there would re-read the data that raised it rather than test
// it. The price is the bit-for-bit replication check a shared range buys, and
// this design pays it: it repeats no committed cell. It is the same trade
// covarianceCleanSeedBase makes, for the same reason.
const restartShapeSeedBase = 119_000

// restartShapeBlocks matches the covariance-clean campaign's count, and again
// for an external reason rather than an inherited argument: twenty-four is what
// the window affords at the fixed cap. Its own justification is that the
// restart ladder returned t = -0.26 on this contrast at twelve blocks with the
// arms spending 29-44% of what they were given, so the comparison has never
// been made at full spend at any block count. Doubling the blocks and fixing
// the spend are the two things that could move it, and this design does both.
const restartShapeBlocks = 24

// restartShapeLambda is where the cold-restart arms sit and where the two
// ladders start.
//
// It is the restart ladder's own primary rung, four times Hansen's default at
// this dimensionality, and the rung the active-CMA and covariance-clean
// campaigns both ran -- so it is the one population this corpus knows most
// about. Starting the ladders there too is what makes the primary contrast a
// comparison of restart *mechanisms* rather than of populations: every arm
// draws its first generation from the same lambda, and they differ in what
// happens after a run converges.
const restartShapeLambda = 64

// restartShapeRungs is every population an IPOP or BIPOP ladder could double
// its way to from this design's starting lambda, out to three rungs past the
// shipped popSize.
//
// It is a function rather than two hand-written lists because the runtime guard
// and the test that pins it have to walk the same rungs. A guard checking fewer
// than the test does is the drift that lets a mode look safe here and clamp on
// the box, which is precisely the failure the mode is pinned to avoid.
func restartShapeRungs() []int {
	rungs := make([]int, 0, 8)
	for lambda := restartShapeLambda; lambda <= 8*defaultPop; lambda *= 2 {
		rungs = append(rungs, lambda)
	}

	return rungs
}

// restartShapeArms asks which restart shape a CMA-ES default would name, with
// every arm spending the cap it was given.
//
// This is the box docs/cmaes-restart-ladder-report.md left open, and it left it
// open for a reason it discovered rather than assumed: its arms spent only
// 29-44% of the cap, because each cold restart trips TolFun early and a fixed
// optimizerRestarts count cannot express "restart until the budget is gone". So
// its primary contrast was cap-matched and not spend-matched, and a null there
// could not distinguish a shape that does not help from a shape that was never
// allowed to run. The filling shape now expresses exactly that, so the contrast
// is worth making again -- and this time the losing arm is losing on merit
// rather than on unspent budget.
//
// **Every arm runs full covariance**, and that is a constraint rather than a
// preference. go-cma-es v0.1.0's rank-mu clamp binds by lambda and mode, and an
// IPOP ladder doubles its population, so a separable ladder from lambda 64
// crosses the boundary on its fourth rung and a block one on its fifth. A
// design cannot pin the top rung a ladder reaches -- that is decided at run
// time -- so the only way to guarantee no arm walks into the clamp mid-run is a
// mode that never clamps at any lambda. Full is that mode. Holding it constant
// across arms is required anyway; full is the value that is also safe. The
// covariance-clean campaign measured block and separable indistinguishable at
// this rung, so nothing is known to be given up by the choice.
//
// The four arms and what separates them:
//
//   - full-ipop-l64 is the control. An IPOP ladder doubles lambda after each
//     converged run and consumes the shared evaluation cap, so it spends what
//     it is given by construction.
//   - full-r32-l64 is the fixed count the ladder ran: 32 independent cold
//     attempts at one lambda, each free to trip TolFun and hand the remainder
//     back to nobody. It is the secondary control, and it is in the design to
//     be the underspender.
//   - full-fill-l64 asks for the same cap as that arm and fills it, starting a
//     further cold attempt whenever a whole one still fits. It is the candidate
//     in two of the three contrasts.
//
// **There is deliberately no BIPOP arm**, and removing one is what review
// caught. go-cma-es gives a BIPOP schedule's first large run a budget equal to
// the whole schedule and reaches the small regime only after a large run has
// finished, so an unarmed bipop job is IPOP under another name -- which this
// driver documents at restartLadderArms and enforces for that design. A bipop
// arm here would therefore have spent 24 blocks comparing two spellings of the
// same search, the way docs/cmaes-covariance-report.md's secondary contrast
// did.
//
// Arming it is not a one-line repair. The criterion has to be matched on both
// sides, and the primary's control must stay unarmed to match the cold arms
// that cannot use a criterion at all, so an honest BIPOP question needs an
// armed IPOP *and* an armed BIPOP on top of these three. That is 48 more jobs
// and a third contrast Holm would charge the primary for, to re-ask something
// the restart ladder already asked. This campaign exists for the primary.
//
// The primary contrast pairs the two shapes that spend their cap, which is the
// choice a default actually faces. It moves more than one thing -- a fixed
// lambda with independent draws against a doubling ladder -- and that is the
// question rather than a confound: nobody proposes tuning the two halves
// separately. The secondary isolates the one factor that is separable, spending
// against bounding at one lambda, and it is the direct measurement of what the
// filling shape buys.
//
// The record it reports against is recordCostAtSharedCap, 752.52, and that is
// the right one here rather than the standing 726.1984354654948: 752.52 was set
// at this cap, while the record was bought with 1.94x of it. A fixed-cap arm
// beating the hunt's number would be remarkable; failing to is not information.
//
// Cap-matched by construction, and spend-matched only as an intent: the fill
// arm is expected near the 97% a scratch reading measured and the fixed arm
// near the ladder's 37%, and finalEvaluations per arm is what says whether that
// happened. Unlike every earlier campaign here, the per-attempt records now
// survive for a fixed count as well, so how many attempts each arm ran and how
// each of them ended is recoverable rather than inferred.
func restartShapeArms(budget int) ([]arm, error) {
	if budget <= 0 || budget%ladderWork != 0 {
		return nil, fmt.Errorf(
			"budget %d must be positive and divisible by %d; the arms would not be evaluation-matched",
			budget, ladderWork)
	}

	if budget%restartShapeLambda != 0 {
		return nil, fmt.Errorf(
			"budget %d is not divisible by lambda %d; the ladder arms would not share the cap",
			budget, restartShapeLambda)
	}

	attempts := ladderWork / restartShapeLambda
	if attempts > app.MaxOptimizerRestarts {
		return nil, fmt.Errorf("the cold-restart arms need %d attempts, above app.MaxOptimizerRestarts %d",
			attempts, app.MaxOptimizerRestarts)
	}

	if restartShapeLambda < app.MinPopulation {
		return nil, fmt.Errorf("lambda %d is below app.MinPopulation %d and would be refused at submit",
			restartShapeLambda, app.MinPopulation)
	}

	// Full never clamps, at any lambda in any dimensionality this project
	// searches. Asserted rather than trusted, because the whole reason the mode
	// is pinned is that a ladder's top rung cannot be known in advance.
	for _, lambda := range restartShapeRungs() {
		if rankMuClamped(searchDimensions, lambda, searchDimensions) {
			return nil, fmt.Errorf("full covariance clamps at lambda %d in %d dimensions; a ladder could reach it",
				lambda, searchDimensions)
		}
	}

	cold := func(name string, restarts int) arm {
		return arm{
			name: name, optimizer: "cmaes", covariance: "full", restartStrategy: "none",
			iters: budget / ladderWork, popSize: restartShapeLambda, optimizerRestarts: restarts,
		}
	}

	ladder := func(name, strategy string) arm {
		return arm{
			name: name, optimizer: "cmaes", covariance: "full", restartStrategy: strategy,
			iters: budget / restartShapeLambda, popSize: restartShapeLambda, optimizerRestarts: 1,
		}
	}

	return []arm{
		ladder(fmt.Sprintf("full-ipop-l%d", restartShapeLambda), "ipop"),
		cold(fmt.Sprintf("full-r%d-l%d", attempts, restartShapeLambda), attempts),
		cold(fmt.Sprintf("full-fill-l%d", restartShapeLambda), -attempts),
	}, nil
}

// assertDesignShape refuses a -blocks or -seed-base that contradicts the
// registered design. Both are the design's to choose, so the flags are an
// assertion a caller can make rather than a way to alter a campaign: a
// mistyped seed base would silently reuse another campaign's seeds, and a
// mistyped block count would change df after the fact.
// The extend-width campaign's fixed shape. Every one of these is a design input
// rather than a flag, for the reason design.seedBase and design.reference are:
// a campaign that could silently differ from the registered one is not a
// registered campaign.
const (
	// extendWidthSeedBase follows restartShapeSeedBase's block. Seeds belong to
	// a design so two campaigns cannot silently share them, and so a
	// replication cannot silently fail to.
	extendWidthSeedBase = 120_000
	extendWidthBlocks   = 12
	// extendWidthLambda is the population every stage runs, and the rung the
	// restart-shape campaign measured its winning shape at.
	extendWidthLambda = 64
	// extendWidthAttemptIters is one cold attempt's generation count, pinned
	// across arms so grouping width is not confounded with attempt length. It
	// is the value full-fill-l64 ran, so an attempt here is the same object the
	// restart-shape campaign measured.
	extendWidthAttemptIters = 3175
	// extendWidthBaseCircles is the seeded prefix and extendWidthCircles the
	// count every arm finishes at. The difference is what the campaign spends
	// its budget appending.
	extendWidthBaseCircles = 8
	extendWidthCircles     = 16
	// extendWidthBasePop and extendWidthBaseIters make the base stage as close
	// to a no-op as the server allows: app.MinPopulation individuals for one
	// generation. The base exists to install recordCircles and write the
	// checkpoint the first extend continues from, not to search. Its cost is
	// asserted against recordQuantizedCost before the campaign runs, and it is
	// identical across the four seeded arms, so it cancels in every contrast
	// among them.
	extendWidthBasePop   = 20
	extendWidthBaseIters = 1
)

// The extend-width campaign: how to spend a budget on circles 9 to 16.
//
// Every campaign in this driver before this one is single-stage and cold --
// eight circles fitted from scratch in one batch, 56 dimensions. This one holds
// a fitted eight-circle prefix and asks how the next eight should be committed:
// all at once, in two fours, four twos, or one at a time. PLAN Task 3 names the
// gap ("measure whether extend and polish stages benefit"; the collapse
// dynamics from a fitted vector are unmeasured) and the extend half became
// expressible when steps[].restarts landed.
//
// The two priors point opposite ways and neither is on the current pin.
// docs/schedule-format.md measured +1 beating +4 by 4.05 cost units at 312
// circles, and says in the same breath that the figure is a pre-v0.7.0 MayFly
// number that must not be compared against a run made today.
// docs/seed-variance-and-population-report.md argues the other direction: an
// extend freezes its prefix, so every stage is a commitment, and it names
// larger extends as the untried lever against exactly that. Re-asking the
// question on the current pin is the campaign.
//
// What is held constant, and why each choice is forced rather than picked:
//
//   - Full covariance. It is the only mode that never clamps the rank-mu rate
//     in go-cma-es v0.1.0, and these arms search four different dimensions
//     (7, 14, 28, 56) where separable and block each cross their boundary in a
//     different place. Holding covariance constant is required anyway; full is
//     the value that is also safe at every width. docs/cmaes-covariance-clean-report.md
//     measured block and separable indistinguishable on clean rungs, so the
//     choice gives up nothing that has been measured.
//   - lambda 64 with budget-filling cold restarts, the shape
//     docs/cmaes-restart-shape-report.md established with the only rejected
//     restart contrast in the corpus (+6.89, p = 0.0083).
//   - Attempt length pinned at extendWidthAttemptIters for every arm. Without
//     this, a narrow arm would run both more stages and shorter attempts, and
//     the campaign would confound grouping width with restart length -- which
//     the restart-shape campaign has already shown is worth several cost units
//     on its own.
//
// So only two things vary with an arm: how many stages it runs, and how many
// cold attempts each stage gets. Their product is constant, which is what makes
// the arms evaluation-matched by construction:
//
//	stages x attemptsPerStage x extendWidthAttemptIters x lambda = budget
//
// Evaluation-matched is not wall-clock-matched here, and the gap runs along the
// variable under test: a narrow stage renders fewer circles per candidate over
// a retained canvas and can take the incremental dirty-span path that joint
// sessions never take, while a small-population run pays per-iteration overhead
// the restart-shape campaign measured at 37% of wall clock. The campaign
// registers on evaluations, as every earlier one does, and reports elapsed as a
// by-product.
//
// cold-w16 is the control that can invalidate the premise rather than answer
// the primary. It fits all sixteen circles from scratch, jointly, at the same
// cap and never sees the record.
// docs/seed-variance-and-population-report.md found the best eight-circle base
// of four finishing last at every rung from 32 circles on, and the deep hunt's
// warm starts settled in a 742.55-744.37 band in 11 of 11 blocks without ever
// approaching the cold-found 726.20. If building on the record is worth
// nothing, this arm is how the campaign finds out instead of assuming.
//
// The seeded arms do not start at recordCost. initialCircles names colours in
// eight bits, so the base costs recordQuantizedCost -- 2.184 above the record.
// That offset is identical for all four seeded arms and cancels in every
// contrast among them; it does not cancel against cold-w16, which it
// handicaps by that much.
func extendWidthArms(budget int) ([]arm, error) {
	const attemptWork = extendWidthAttemptIters * extendWidthLambda

	if budget <= 0 || budget%attemptWork != 0 {
		return nil, fmt.Errorf(
			"budget %d must be positive and divisible by one attempt of %d evaluations; "+
				"the arms would not be evaluation-matched",
			budget, attemptWork)
	}

	attempts := budget / attemptWork
	appended := extendWidthCircles - extendWidthBaseCircles

	arms := make([]arm, 0, 5)

	for _, spec := range []struct {
		name   string
		stages int
	}{
		{"ext-w8", 1},
		{"ext-w4", 2},
		{"ext-w2", 4},
		{"ext-w1", 8},
	} {
		built, err := extendWidthStagedArm(spec.name, spec.stages, attempts, appended)
		if err != nil {
			return nil, err
		}

		arms = append(arms, built)
	}

	coldDimensions := extendWidthCircles * app.ParametersPerCircle
	if err := assertExtendWidthRung("cold-w16", coldDimensions); err != nil {
		return nil, err
	}

	arms = append(arms, arm{
		name: "cold-w16", optimizer: "cmaes",
		covariance: "full", restartStrategy: coldRestartStrategy,
		iters: extendWidthAttemptIters, popSize: extendWidthLambda,
		optimizerRestarts: -attempts,
	})

	for _, built := range arms {
		if spend := built.plannedEvaluations(); spend != budget {
			return nil, fmt.Errorf("%s plans %d evaluations, want %d", built.name, spend, budget)
		}
	}

	return arms, nil
}

// extendWidthStagedArm builds one seeded arm: a prefix, then stages extends of
// equal width, each holding an equal share of the campaign's cold attempts.
// Every refusal here is a design-time one, because an arm that cannot express
// its share of the cap is an arm that would silently not be
// evaluation-matched.
func extendWidthStagedArm(name string, stages, attempts, appended int) (arm, error) {
	if appended%stages != 0 {
		return arm{}, fmt.Errorf("%s: %d stages cannot append %d circles evenly", name, stages, appended)
	}

	if attempts%stages != 0 {
		return arm{}, fmt.Errorf(
			"%s: %d cold attempts cannot be split evenly across %d stages, so the arm would not be "+
				"evaluation-matched against the others", name, attempts, stages)
	}

	width := appended / stages
	perStage := attempts / stages

	if width > app.MaxBatchSize {
		return arm{}, fmt.Errorf("%s: append width %d exceeds app.MaxBatchSize %d", name, width, app.MaxBatchSize)
	}

	// A filling shape asks for a cap of perStage x iters and then spends it,
	// starting a further attempt whenever a whole one still fits, so the
	// attempt count can exceed perStage and is bounded separately by
	// app.MaxOptimizerRestarts. The narrow arms are where that ceiling binds:
	// docs/cmaes-restart-shape-report.md measured its filling arm averaging 61
	// attempts of the 64 available at 56 dimensions, and these stages search as
	// few as 7, where an attempt trips TolFun far sooner. This guard only
	// refuses a cap that cannot be expressed; whether the ceiling binds in
	// practice is a measurement, and the campaign probes it before it runs.
	if perStage > app.MaxOptimizerRestarts {
		return arm{}, fmt.Errorf(
			"%s: %d cold attempts per stage exceeds app.MaxOptimizerRestarts %d",
			name, perStage, app.MaxOptimizerRestarts)
	}

	dimensions := width * app.ParametersPerCircle
	if err := assertExtendWidthRung(name, dimensions); err != nil {
		return arm{}, err
	}

	return arm{
		name: name, optimizer: "cmaes",
		covariance: "full", restartStrategy: coldRestartStrategy,
		iters: extendWidthAttemptIters, popSize: extendWidthLambda,
		optimizerRestarts: -perStage,
		stages:            stages, width: width, seeded: true,
	}, nil
}

// assertExtendWidthRung refuses a stage whose search the pinned library cannot
// run cleanly. It is a design-time refusal for the same reason
// covarianceCleanArms and restartShapeArms carry one: a clamped rank-mu rate is
// silent at run time, and a campaign that walked into it would report a
// covariance model that had stopped remembering anything.
func assertExtendWidthRung(name string, dimensions int) error {
	if dimensions < 1 {
		return fmt.Errorf("%s: a stage must search at least one dimension", name)
	}

	// blockDimension at or above the dimension count is full mode, which never
	// applies the separable correction. Asserting it rather than trusting it
	// keeps the guard true if the arms ever move off full.
	if rankMuClamped(dimensions, extendWidthLambda, dimensions) {
		return fmt.Errorf(
			"%s: lambda %d clamps the rank-mu rate at %d dimensions, so covariance would decay to nothing",
			name, extendWidthLambda, dimensions)
	}

	if product := extendWidthLambda * dimensions; product > app.MaxPopulationDimensions {
		return fmt.Errorf(
			"%s: lambda %d over %d dimensions is %d, above app.MaxPopulationDimensions %d",
			name, extendWidthLambda, dimensions, product, app.MaxPopulationDimensions)
	}

	return nil
}

func assertDesignShape(config settings, plan design) error {
	if config.blocks != 0 && config.blocks != plan.blocks {
		return fmt.Errorf("design %s registers %d paired blocks, got -blocks %d",
			plan.name, plan.blocks, config.blocks)
	}

	if config.seedBase != 0 && config.seedBase != plan.seedBase {
		return fmt.Errorf("design %s registers seed base %d, got -seed-base %d",
			plan.name, plan.seedBase, config.seedBase)
	}

	return nil
}

func submit(config settings) error {
	plan, err := campaignDesign(config.design, config.budget)
	if err != nil {
		return err
	}

	arms := plan.arms

	if err := assertDesignShape(config, plan); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(config.manifestPath), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}

	file, err := os.OpenFile(config.manifestPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create fresh manifest (refusing to duplicate a campaign): %w", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"arm", "block", "seed", "scheduleId", "jobId"}); err != nil {
		return err
	}

	writer.Flush()

	client := &http.Client{Timeout: 30 * time.Second}

	for block := 1; block <= plan.blocks; block++ {
		seed := plan.seedBase + int64(block)
		for _, current := range arms {
			// A staged arm is a campaign document, not a job: an extend
			// continues a frozen prefix from its parent's checkpoint and has no
			// single-job form. Both paths return one identifier, and which
			// column it lands in is what tells collect how to resolve it.
			var scheduleID, jobID string

			var submitErr error

			if current.stages > 0 {
				scheduleID, submitErr = submitSchedule(client, config, plan, current, seed)
			} else {
				jobID, submitErr = submitJob(client, config, plan, current, seed)
			}

			if submitErr != nil {
				return fmt.Errorf("submit %s block %d: %w", current.name, block, submitErr)
			}

			record := []string{
				current.name, strconv.Itoa(block), strconv.FormatInt(seed, 10), scheduleID, jobID,
			}
			if err := writer.Write(record); err != nil {
				return fmt.Errorf("record submitted job: %w", err)
			}

			writer.Flush()

			if err := writer.Error(); err != nil {
				return fmt.Errorf("flush manifest: %w", err)
			}

			fmt.Printf("submitted block %02d %-16s %s%s\n", block, current.name, scheduleID, jobID)
		}
	}

	return nil
}

func submitJob(client *http.Client, config settings, plan design, current arm, seed int64) (string, error) {
	body, err := json.Marshal(jobPayload(config, plan, current, seed))
	if err != nil {
		return "", err
	}

	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost, config.server+"/api/v1/jobs", bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("build create request: %w", err)
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", err
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("server returned %s: %s", response.Status, responseBody)
	}

	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(responseBody, &created); err != nil {
		return "", err
	}

	if created.ID == "" {
		return "", errors.New("create response has no job id")
	}

	return created.ID, nil
}

// warmStartSpecs renders recordCircles in the shape app.CircleSpecs decodes.
// The colour is quantized to eight bits per channel on the way through, which
// is the format's doing rather than a choice: initialCircles is the only
// operator-authored warm start in the system and it names colours as hex. The
// run therefore starts a hair off the recorded optimum, which is harmless for
// a neighbourhood search and is stated in the report rather than hidden.
func warmStartSpecs() []map[string]any {
	circles := recordCircles()
	specs := make([]map[string]any, 0, len(circles))

	channel := func(value float64) int {
		return int(math.Round(math.Min(math.Max(value, 0), 1) * 255))
	}

	for _, circle := range circles {
		specs = append(specs, map[string]any{
			"x": circle.x, "y": circle.y, "r": circle.r,
			"color": fmt.Sprintf("#%02x%02x%02x",
				channel(circle.red), channel(circle.green), channel(circle.blue)),
			"opacity": circle.opacity,
		})
	}

	return specs
}

// jobPayload is the create request one arm of one block submits. It is separate
// from the transport so the registered shape of a job -- fixture, budget, and
// the engine-specific fields -- can be read in one place.
func jobPayload(config settings, plan design, current arm, seed int64) map[string]any {
	reference, circles := plan.fixture(config.reference)
	epochs := max(current.optimizerEpochs, 1)

	payload := map[string]any{
		"project": config.project, "refPath": reference,
		"mode": "batch", "backend": "cpu", "optimizer": current.optimizer,
		"circles": circles, "batchSize": circles, "iters": current.iters, "popSize": current.popSize,
		"optimizerEpochs": epochs, "optimizerRestarts": current.optimizerRestarts,
		"seed": seed, "threads": 1, "parallelEvaluation": true,
		"evaluationWorkers": config.workers, "disableConvergence": true,
		"enableTrace": true, "enableOptimizerDiagnostics": true,
	}
	if current.optimizer == "mayfly" {
		payload["variant"] = "standard"
	} else {
		payload["covarianceMode"] = current.covariance
		payload["restartStrategy"] = current.restartStrategy

		// Only send the knobs an arm actually turns. app refuses every one of
		// these on a MayFly job rather than ignoring it, and an arm that names
		// none of them has to reach the server as the untouched configuration
		// every campaign before the deep hunt ran.

		if current.initialSigma > 0 {
			payload["initialSigma"] = current.initialSigma
		}

		if current.passiveCMA {
			payload["activeCMA"] = false
		}

		if current.warmStart {
			payload["initialCircles"] = warmStartSpecs()
		}
	}
	// Only send the stop fields when a window is configured. app.JobConfig
	// rejects stopMinImprovement without stopStagnationIters, and an arm that
	// sets neither has to reach the server as the untouched configuration every
	// earlier campaign ran.
	if current.stopStagnationIters > 0 {
		payload["stopStagnationIters"] = current.stopStagnationIters
		if current.stopMinImprovement > 0 {
			payload["stopMinImprovement"] = current.stopMinImprovement
		}
	}

	return payload
}

// schedulePayload is the campaign document a staged arm submits. It is the
// schedule-format.md document, built here rather than authored on disk so the
// registered design and the thing that runs cannot drift apart.
//
// The base stage installs recordCircles and nothing else. app requires
// initialCircles on a batch stage with exactly circles entries and a batchSize
// covering all of them, and it refuses the field on every continuation -- an
// extend is seeded from its parent checkpoint, always -- so the seed can only
// enter here. One generation of app.MinPopulation individuals is the smallest
// search the server will accept; the stage exists to write the checkpoint the
// first extend continues from.
//
// The steps are one stanza. repeat is the generator form, so eight single
// circle extends are one step with repeat 8 rather than eight copies of it,
// which is also what keeps the document inside the 128 KiB the API allows.
// restarts is negative: the filling shape asks for a cap of abs(N) x epochs x
// iters and spends it, starting a further attempt whenever a whole one fits.
func schedulePayload(config settings, plan design, current arm, seed int64) map[string]any {
	reference, circles := plan.fixture(config.reference)
	base := circles - current.stages*current.width

	document := map[string]any{
		"schemaVersion": 1,
		"name":          fmt.Sprintf("%s %s block seed %d", plan.name, current.name, seed),
		"seed":          seed,
		"base": map[string]any{
			"refPath": reference,
			"mode":    "batch", "backend": "cpu", "optimizer": current.optimizer,
			"circles": base, "batchSize": base,
			"iters": extendWidthBaseIters, "popSize": extendWidthBasePop,
			"optimizerEpochs": 1, "optimizerRestarts": 1,
			"seed": seed, "threads": 1, "parallelEvaluation": true,
			"evaluationWorkers": config.workers, "disableConvergence": true,
			"enableTrace": true, "enableOptimizerDiagnostics": true,
			"covarianceMode": current.covariance, "restartStrategy": current.restartStrategy,
			"initialCircles": warmStartSpecs(),
		},
		"steps": []map[string]any{{
			"type":              "extend",
			"repeat":            current.stages,
			"additionalCircles": current.width,
			"batchSize":         current.width,
			"epochs":            max(current.optimizerEpochs, 1),
			"iters":             current.iters,
			"popSize":           current.popSize,
			"restarts":          current.optimizerRestarts,
		}},
	}

	return document
}

// submitSchedule posts a staged arm's campaign document and returns the
// schedule ID. A staged arm has no job ID at submit time: its stages are
// started one at a time by the executor, so the manifest records the schedule
// and collect resolves the final stage's job from it.
func submitSchedule(client *http.Client, config settings, plan design, current arm, seed int64) (string, error) {
	body, err := json.Marshal(schedulePayload(config, plan, current, seed))
	if err != nil {
		return "", err
	}

	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost, config.server+"/api/v1/schedules", bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("build schedule request: %w", err)
	}

	request.Header.Set("Content-Type", "application/json")

	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("post schedule: %w", err)
	}
	defer response.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("read schedule response: %w", err)
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("schedule create returned %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}

	var created struct {
		ScheduleID string `json:"scheduleId"`
	}

	if err := json.Unmarshal(payload, &created); err != nil {
		return "", fmt.Errorf("decode schedule response: %w", err)
	}

	if created.ScheduleID == "" {
		return "", fmt.Errorf("schedule create returned no scheduleId: %s", strings.TrimSpace(string(payload)))
	}

	return created.ScheduleID, nil
}

// scheduleStage is the part of a stage listing this driver reads.
type scheduleStage struct {
	Index        int     `json:"index"`
	Kind         string  `json:"kind"`
	State        string  `json:"state"`
	Circles      int     `json:"circles"`
	BestCost     float64 `json:"bestCost"`
	JobID        string  `json:"jobId"`
	ElapsedNanos *int64  `json:"elapsedNanos"`
	Error        string  `json:"error"`
}

// scheduleDetail is the campaign listing GET /api/v1/schedules/:id returns.
type scheduleDetail struct {
	ScheduleID  string          `json:"scheduleId"`
	State       string          `json:"state"`
	TotalStages int             `json:"totalStages"`
	Error       string          `json:"error"`
	Stages      []scheduleStage `json:"stages"`
}

// fetchSchedule reads one campaign's stage listing.
func fetchSchedule(client *http.Client, server, scheduleID string) (scheduleDetail, error) {
	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, server+"/api/v1/schedules/"+scheduleID, nil,
	)
	if err != nil {
		return scheduleDetail{}, err
	}

	response, err := client.Do(request)
	if err != nil {
		return scheduleDetail{}, err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return scheduleDetail{}, err
	}

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return scheduleDetail{}, fmt.Errorf("schedule %s returned %s", scheduleID, response.Status)
	}

	var detail scheduleDetail
	if err := json.Unmarshal(body, &detail); err != nil {
		return scheduleDetail{}, fmt.Errorf("decode schedule %s: %w", scheduleID, err)
	}

	return detail, nil
}

// extendStages returns the campaign's extend stages in index order, which is
// draw order: the last of them is the run whose cost is the campaign's answer.
func (d scheduleDetail) extendStages() []scheduleStage {
	stages := make([]scheduleStage, 0, len(d.Stages))

	for _, stage := range d.Stages {
		if stage.Kind == "extend" {
			stages = append(stages, stage)
		}
	}

	slices.SortFunc(stages, func(a, b scheduleStage) int { return a.Index - b.Index })

	return stages
}

// collectSchedule scores one staged arm's campaign.
//
// The answer is the last extend stage's cost, because an extend freezes the
// prefix it inherits: the final stage's incumbent is the whole sixteen-circle
// vector, and every earlier stage's cost is a prefix of it. The spend columns
// are sums across stages instead, which is the only reading that compares
// against a single-job arm's -- an arm that ran eight stages spent its cap in
// eight places.
//
// The base stage is deliberately not in either total. It is one generation of
// app.MinPopulation individuals whose only job is to install recordCircles, it
// is identical across the four seeded arms, and counting it would put a
// constant into a column whose whole purpose is to show whether the arms spent
// the same amount searching.
//
// Returned state is the campaign's, not a stage's: a schedule that failed at
// stage three is not a completed campaign with a usable final cost, and this
// reports it as whatever the executor called it so the tally in collect names
// the real state.
func collectSchedule(config settings, client *http.Client, record manifestRow) (resultRow, string, error) {
	detail, err := fetchSchedule(client, config.server, record.ScheduleID)
	if err != nil {
		return resultRow{}, "", fmt.Errorf("schedule %s: %w", record.ScheduleID, err)
	}

	if detail.State != "completed" {
		return resultRow{}, detail.State, nil
	}

	stages := detail.extendStages()
	if len(stages) == 0 {
		return resultRow{}, detail.State, fmt.Errorf("schedule %s completed with no extend stage", record.ScheduleID)
	}

	resolved := record
	resolved.Stages = make([]stageJob, 0, len(stages))

	for ordinal, stage := range stages {
		if stage.JobID == "" {
			return resultRow{}, detail.State, fmt.Errorf(
				"schedule %s stage %d completed without a job", record.ScheduleID, stage.Index)
		}

		resolved.Stages = append(resolved.Stages, stageJob{
			Stage: ordinal, JobID: stage.JobID, Project: string(app.DefaultProject), Budget: config.budget,
		})
	}

	resolved.JobID = resolved.Stages[len(resolved.Stages)-1].JobID

	final, err := fetchStatus(client, config.server, resolved.JobID)
	if err != nil {
		return resultRow{}, detail.State, fmt.Errorf("status %s: %w", resolved.JobID, err)
	}

	// Scored against the campaign budget, not the stage's share, because a
	// continuation's counters are cumulative: an extend inherits its parent's
	// evaluation total and counts on from it. The last stage of an eight-stage
	// arm carries samples numbered near the whole cap, so capping its trace at
	// a stage's share would reject every one of them.
	row, err := collectJob(config, string(app.DefaultProject), resolved, final, config.budget)
	if err != nil {
		return resultRow{}, detail.State, err
	}

	// For the same reason the spend columns are the final stage's and not a sum
	// over stages: the counter already accumulated, so adding the stages
	// together would count the first stage's evaluations eight times. Measured
	// on an eight-stage probe -- stage 1 reported 1,625 evaluations and stage 8
	// reported 12,839, each stage adding about 1,602 -- so the final figure is
	// the campaign total and the sum is 4.5x it.
	//
	// Wall clock does not accumulate that way and is summed from the stage
	// listing, where each entry is its own completion minus its own start.
	row.ElapsedSeconds = 0

	for _, stage := range stages {
		if stage.ElapsedNanos != nil {
			row.ElapsedSeconds += time.Duration(*stage.ElapsedNanos).Seconds()
		}
	}

	// Per-restart records are per stage rather than cumulative -- a stage's
	// checkpoint holds only the attempts that stage ran -- so these are
	// concatenated. RestartRun.Stage is the pipeline stage within one job,
	// always zero here because an extend's batch covers exactly the circles it
	// appends; renumbering onto the campaign's stage ordinal is what keeps the
	// restarts CSV readable for an eight-stage arm.
	row.Restarts, err = stageRestartRuns(config, resolved.Stages)
	if err != nil {
		return resultRow{}, detail.State, err
	}

	row.manifestRow = resolved

	return row, detail.State, nil
}

// stageRestartRuns concatenates a campaign's per-attempt records, renumbering
// each onto the campaign stage it came from.
func stageRestartRuns(config settings, stages []stageJob) ([]opt.RestartRun, error) {
	var runs []opt.RestartRun

	for _, job := range stages {
		var saved checkpoint

		body, err := os.ReadFile(filepath.Join(
			jobDirectory(config.dataRoot, job.Project, job.JobID), "checkpoint.json"))
		if err != nil {
			return nil, fmt.Errorf("read %s checkpoint: %w", job.JobID, err)
		}

		if err := json.Unmarshal(body, &saved); err != nil {
			return nil, fmt.Errorf("decode %s checkpoint: %w", job.JobID, err)
		}

		for _, run := range saved.Restarts {
			run.Stage = job.Stage
			runs = append(runs, run)
		}
	}

	return runs, nil
}

func collect(config settings) error {
	manifest, err := readManifest(config.manifestPath)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	results := make([]resultRow, 0, len(manifest))
	resolved := make([]manifestRow, 0, len(manifest))
	counts := make(map[string]int)

	for _, record := range manifest {
		// A staged row names a campaign and a single-job row names a job. Both
		// resolve to one scored result; only the staged path has to find the
		// job first, and only it can report a state that is not a job's.
		if record.ScheduleID != "" {
			result, state, scheduleErr := collectSchedule(config, client, record)
			if scheduleErr != nil {
				return scheduleErr
			}

			counts[state]++

			if state == "completed" {
				results = append(results, result)
				resolved = append(resolved, result.manifestRow)
			}

			continue
		}

		status, statusErr := fetchStatus(client, config.server, record.JobID)
		if statusErr != nil {
			return fmt.Errorf("status %s: %w", record.JobID, statusErr)
		}

		counts[status.State]++
		if status.State != "completed" {
			continue
		}

		result, collectErr := collectJob(config, config.project, record, status, config.budget)
		if collectErr != nil {
			return collectErr
		}

		results = append(results, result)
		resolved = append(resolved, record)
	}

	fmt.Printf("campaign status:")

	for _, state := range []string{"pending", "running", "completed", "failed", "cancelled"} {
		if counts[state] > 0 {
			fmt.Printf(" %s=%d", state, counts[state])
		}
	}

	fmt.Println()

	if len(results) != len(manifest) {
		return fmt.Errorf("campaign is not complete: collected %d of %d jobs", len(results), len(manifest))
	}

	if err := writeResults(config.resultsPath, results); err != nil {
		return err
	}

	if err := writeTrajectories(config, resolved); err != nil {
		return err
	}

	if err := writeRestarts(config, results); err != nil {
		return err
	}

	return analyzeDesign(config)
}

// printPlan reports exactly what submit would create, without creating it.
// A campaign is expensive and a manifest may only be written once, so the arm
// shapes are worth reading before 12 blocks of them are queued.
func printPlan(config settings) error {
	plan, err := campaignDesign(config.design, config.budget)
	if err != nil {
		return err
	}

	if err := assertDesignShape(config, plan); err != nil {
		return err
	}

	reference, circles := plan.fixture(config.reference)

	fmt.Printf("design %s: %d arms x %d blocks = %d jobs, budget %d evaluations, seeds %d..%d\n",
		plan.name, len(plan.arms), plan.blocks, len(plan.arms)*plan.blocks, config.budget,
		plan.seedBase+1, plan.seedBase+int64(plan.blocks))
	fmt.Printf("fixture %s, %d circles\n", reference, circles)
	fmt.Println("| arm | optimizer | shape | covariance | restarts | epochs | popSize (lambda) | iters | " +
		"sigma | active | start | stagnation | evaluations |")
	fmt.Println("| --- | --- | --- | --- | --- | ---: | ---: | ---: | --- | --- | --- | --- | ---: |")

	for _, current := range plan.arms {
		covariance, restarts := current.covariance, current.restartStrategy
		// Only CMA-ES spends exactly lambda evaluations per iteration, so only
		// there is iters*popSize the evaluation count. Mayfly evaluates its
		// population several times per iteration, and its arms are matched to
		// the budget by campaign shape rather than by that product; printing it
		// as an evaluation count would invite the comparison the campaign
		// deliberately makes post-hoc from the trace.
		// Epochs and cold restarts each multiply the generation count, so an
		// arm's budget is the product of all three. Printing one run's share
		// would make a split arm look like it spends a fifth of the cap.
		evaluations := strconv.Itoa(current.plannedEvaluations())
		if current.optimizer == "mayfly" {
			covariance = "-"
			restarts = fmt.Sprintf("%d cold run(s)", current.optimizerRestarts)
			evaluations = "scored from trace"
		} else if attempts := restartAttempts(current.optimizerRestarts); attempts > 1 {
			// A CMA-ES arm can carry both the adapter's own ladder and the
			// generic cold-restart wrapper; naming only the strategy would hide
			// the second. The shape is in the sign, and the two are not the
			// same experiment, so the table says which one an arm asked for.
			shape := fmt.Sprintf("%d cold run(s)", attempts)
			if current.optimizerRestarts < 0 {
				shape = fmt.Sprintf("cold runs filling %d x iters", attempts)
			}

			// An arm carrying only the wrapper has no engine strategy to name,
			// and "none + 32 cold run(s)" reads as though something had been
			// switched off. Prepend the strategy only where there is one.
			restarts = shape
			if current.restartStrategy != coldRestartStrategy {
				restarts = fmt.Sprintf("%s + %s", current.restartStrategy, shape)
			}
		}

		sigma, active, start := "default", "-", "residual"
		if current.optimizer != "mayfly" {
			active = "on"

			if current.initialSigma > 0 {
				sigma = strconv.FormatFloat(current.initialSigma, 'g', -1, 64)
			}

			if current.passiveCMA {
				active = "off"
			}

			if current.warmStart || current.seeded {
				start = "record"
			}
		}

		fmt.Printf("| `%s` | %s | %s | %s | %s | %d | %d | %d | %s | %s | %s | %s | %s |\n",
			current.name, current.optimizer, describeShape(current, circles), covariance, restarts,
			max(current.optimizerEpochs, 1),
			current.popSize, current.iters, sigma, active, start,
			describeStagnation(current), evaluations)
	}

	if plan.secondaryControl != "" {
		fmt.Printf("\nbaseline `%s`, secondary control `%s`\n", plan.baseline, plan.secondaryControl)
	} else {
		fmt.Printf("\nbaseline `%s`\n", plan.baseline)
	}

	if plan.descriptive {
		fmt.Println("descriptive design: no inferential statistic is computed or reported")
	}

	if plan.record > 0 {
		fmt.Printf("standing record on this fixture: %.10f\n", plan.record)
	}

	for _, current := range plan.contrasts {
		role := "registered"
		if current.primary {
			role = "PRIMARY"
		}

		fmt.Printf("%s contrast: `%s` against `%s`\n", role, current.candidate, current.control)
	}

	return nil
}

// describeStagnation renders an arm's stopping configuration for the plan
// table. "none" is the configuration every campaign before this one ran, and
// naming it explicitly is the point of the column.
// describeShape names how an arm reaches the fixture's circle count: one cold
// batch, or a seeded prefix plus a run of extend stages. The restarts and iters
// columns describe one stage, so without this an arm that runs eight of them
// reads as though it spent an eighth of the cap.
func describeShape(current arm, circles int) string {
	if current.stages == 0 {
		return fmt.Sprintf("cold %d, one batch", circles)
	}

	return fmt.Sprintf("%d seeded + %d x +%d", circles-current.stages*current.width,
		current.stages, current.width)
}

func describeStagnation(current arm) string {
	if current.stopStagnationIters == 0 {
		return "none"
	}

	if current.stopMinImprovement > 0 {
		return fmt.Sprintf("%d iters, min %.3g", current.stopStagnationIters, current.stopMinImprovement)
	}

	return fmt.Sprintf("%d iters", current.stopStagnationIters)
}

// analyzeDesign resolves the named design before reporting, so the arm set,
// the controls and the expected job count all come from one place.
func analyzeDesign(config settings) error {
	plan, err := campaignDesign(config.design, config.budget)
	if err != nil {
		return err
	}

	return analyze(config.resultsPath, plan)
}

func collectPreliminary(config settings) error {
	manifest, err := readManifest(config.manifestPath)
	if err != nil {
		return err
	}

	results := make([]resultRow, 0, len(manifest))

	available := make([]manifestRow, 0, len(manifest))
	for _, record := range manifest {
		jobDir := jobDirectory(config.dataRoot, config.project, record.JobID)

		body, readErr := os.ReadFile(filepath.Join(jobDir, "checkpoint-info.json"))
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}

		if readErr != nil {
			return fmt.Errorf("read %s checkpoint info: %w", record.JobID, readErr)
		}

		var saved checkpointInfo
		if err := json.Unmarshal(body, &saved); err != nil {
			return fmt.Errorf("decode %s checkpoint info: %w", record.JobID, err)
		}

		state := "interrupted"
		if saved.Termination == "completed" {
			state = "completed"
		}

		status := jobStatus{
			State: state, Termination: saved.Termination,
			Iterations: saved.Iteration, Evaluations: saved.Evaluations,
		}

		result, collectErr := collectJob(config, config.project, record, status, config.budget)
		if collectErr != nil {
			return collectErr
		}

		result.OptimizerVersion = saved.OptimizerVersion
		results = append(results, result)
		available = append(available, record)
	}

	if len(results) == 0 {
		return errors.New("campaign has no persisted job results")
	}

	if err := writeResults(config.resultsPath, results); err != nil {
		return err
	}

	if err := writeTrajectories(config, available); err != nil {
		return err
	}

	fmt.Printf("wrote %d preliminary results from %d planned jobs; no inferential statistics were calculated\n", len(results), len(manifest))

	return nil
}

func fetchStatus(client *http.Client, server, jobID string) (jobStatus, error) {
	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, server+"/api/v1/jobs/"+jobID+"/status", nil,
	)
	if err != nil {
		return jobStatus{}, fmt.Errorf("build status request: %w", err)
	}

	response, err := client.Do(request)
	if err != nil {
		return jobStatus{}, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return jobStatus{}, fmt.Errorf("server returned %s: %s", response.Status, body)
	}

	var status jobStatus
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&status); err != nil {
		return jobStatus{}, err
	}

	return status, nil
}

// collectJob scores one job. budget is the cap that job alone was held to,
// which is the campaign budget for a single-job arm and the stage's share of
// it for one stage of a staged arm.
// jobDirectory resolves where a job's artifacts live, which is not one rule.
// A named project is an FSStore rooted at <data-root>/projects/<slug>, but the
// default project is the legacy tree at <data-root>/jobs and has no slug
// segment at all -- internal/server/projects.go states it: "The legacy layout
// keeps its jobs directly under `<data-root>/jobs`, so the two never collide."
//
// Every campaign before extend-width read only its own named project and never
// met the other case. A staged one does: the schedule executor creates every
// stage under app.DefaultProject, because project is not a JobConfig field and
// a document's base stanza is a JobConfig, so a campaign's stages land in the
// legacy tree however the driver names its project.
func jobDirectory(dataRoot, project, jobID string) string {
	if project == "" || app.Project(project) == app.DefaultProject {
		return filepath.Join(dataRoot, "jobs", jobID)
	}

	return filepath.Join(dataRoot, "projects", project, "jobs", jobID)
}

func collectJob(config settings, project string, record manifestRow, status jobStatus, budget int) (resultRow, error) {
	jobDir := jobDirectory(config.dataRoot, project, record.JobID)

	trace, err := readTrace(filepath.Join(jobDir, "trace.jsonl"))
	if err != nil {
		return resultRow{}, fmt.Errorf("read %s trace: %w", record.JobID, err)
	}

	score := math.Inf(1)
	scoredEvaluations := 0

	for _, entry := range trace {
		if entry.Evaluations <= budget && entry.Cost < score {
			score = entry.Cost
			scoredEvaluations = entry.Evaluations
		}
	}

	if math.IsInf(score, 1) {
		return resultRow{}, fmt.Errorf("job %s has no trace sample within budget", record.JobID)
	}

	if status.Elapsed == 0 && len(trace) > 1 {
		status.Elapsed = trace[len(trace)-1].Timestamp.Sub(trace[0].Timestamp).Seconds()
	}

	var saved checkpoint

	checkpointBody, err := os.ReadFile(filepath.Join(jobDir, "checkpoint.json"))
	if err != nil {
		return resultRow{}, fmt.Errorf("read %s checkpoint: %w", record.JobID, err)
	}

	if err := json.Unmarshal(checkpointBody, &saved); err != nil {
		return resultRow{}, fmt.Errorf("decode %s checkpoint: %w", record.JobID, err)
	}

	return resultRow{
		manifestRow: record, State: status.State, Termination: status.Termination,
		OptimizerVersion: saved.OptimizerVersion, Score: score,
		ScoredEvaluations: scoredEvaluations, FinalEvaluations: status.Evaluations,
		Iterations: status.Iterations, ElapsedSeconds: status.Elapsed,
		Backend:  backendProvenance(status, saved),
		Restarts: saved.Restarts,
	}, nil
}

// backendProvenance renders what ran into one CSV cell, preferring the live
// status and falling back to what the checkpoint persisted. The fallback is not
// belt and braces: -action preliminary builds its jobStatus from
// checkpoint-info.json, which has no provenance at all, so the checkpoint is
// the only record there.
//
// An empty effective backend in both is still reported as unknown rather than
// assumed to be cpu. It means nothing recorded what ran -- a checkpoint written
// before the fields existed, or a process that never built a renderer -- and a
// campaign that silently substitutes a guess there is exactly the failure this
// column exists to prevent.
func backendProvenance(status jobStatus, saved checkpoint) string {
	backend, degraded := status.EffectiveBackend, status.BackendDegraded
	if backend == "" {
		backend, degraded = saved.EffectiveBackend, saved.BackendDegraded
	}

	if backend == "" {
		return "unknown"
	}

	if degraded {
		return backend + "(degraded)"
	}

	return backend
}

func readTrace(path string) ([]store.TraceEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var entries []store.TraceEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4<<20)

	for scanner.Scan() {
		var entry store.TraceEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			return nil, err
		}

		entries = append(entries, entry)
	}

	return entries, scanner.Err()
}

func readManifest(path string) ([]manifestRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)

	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	// The scheduleId column arrived with the first staged design. Manifests
	// written before it are still readable, and every row in one is a job, the
	// way every campaign before extend-width was.
	staged := slices.Equal(records[0], []string{"arm", "block", "seed", "scheduleId", "jobId"})
	legacy := slices.Equal(records[0], []string{"arm", "block", "seed", "jobId"})

	if len(records) < 2 || (!staged && !legacy) {
		return nil, errors.New("manifest has an unexpected header or no jobs")
	}

	columns := 4
	if staged {
		columns = 5
	}

	rows := make([]manifestRow, 0, len(records)-1)
	for _, record := range records[1:] {
		if len(record) != columns {
			return nil, fmt.Errorf("invalid manifest row %q", record)
		}

		block, blockErr := strconv.Atoi(record[1])

		seed, seedErr := strconv.ParseInt(record[2], 10, 64)
		if blockErr != nil || seedErr != nil {
			return nil, fmt.Errorf("invalid manifest row %q", record)
		}

		row := manifestRow{Arm: record[0], Block: block, Seed: seed, JobID: record[3]}
		if staged {
			row.ScheduleID, row.JobID = record[3], record[4]
		}

		rows = append(rows, row)
	}

	return rows, nil
}

func writeResults(path string, results []resultRow) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)

	header := []string{"arm", "block", "seed", "jobId", "state", "termination", "optimizerVersion", "bestCost", "scoredEvaluations", "finalEvaluations", "iterations", "elapsedSeconds", "backend"}
	if err := writer.Write(header); err != nil {
		return err
	}

	for _, result := range results {
		record := []string{
			result.Arm, strconv.Itoa(result.Block), strconv.FormatInt(result.Seed, 10), result.JobID,
			result.State, result.Termination, result.OptimizerVersion,
			strconv.FormatFloat(result.Score, 'g', 17, 64), strconv.Itoa(result.ScoredEvaluations),
			strconv.Itoa(result.FinalEvaluations), strconv.Itoa(result.Iterations),
			strconv.FormatFloat(result.ElapsedSeconds, 'f', 6, 64), result.Backend,
		}
		if err := writer.Write(record); err != nil {
			return err
		}
	}

	writer.Flush()

	return writer.Error()
}

func writeTrajectories(config settings, manifest []manifestRow) error {
	if err := os.MkdirAll(filepath.Dir(config.trajectory), 0o755); err != nil {
		return err
	}

	file, err := os.Create(config.trajectory)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	// stage is the campaign stage the sample came from, zero for a single-job
	// arm. An eight-stage arm restarts its iteration and evaluation counters in
	// every stage, so without this column its rows would read as one trajectory
	// that jumped backwards eight times.
	if err := writer.Write([]string{"arm", "block", "seed", "stage", "iteration", "evaluations", "bestCost", "populationSpread", "sigma", "conditionNumber", "distributionExtent", "restart"}); err != nil {
		return err
	}

	early := map[int]bool{1: true, 5: true, 10: true, 20: true, 40: true, 80: true, 160: true, 255: true}

	for _, row := range manifest {
		// Each stage is downsampled against its own share of the cap, so an
		// eight-stage arm keeps 256 buckets per stage the way a single-job arm
		// keeps 256 for the whole run. Sampling every stage against the
		// campaign budget instead would collapse a short stage to a handful of
		// rows and hide exactly the shape this column exists to show.
		for _, job := range row.jobs(config.project, config.budget) {
			jobDir := jobDirectory(config.dataRoot, job.Project, job.JobID)

			entries, readErr := readTrace(filepath.Join(jobDir, "trace.jsonl"))
			if readErr != nil {
				return readErr
			}

			lastEligible := -1

			for index, entry := range entries {
				if entry.OptimizerDiagnostics != nil && entry.Evaluations <= job.Budget {
					lastEligible = index
				}
			}

			lastBucket := -1

			for index, entry := range entries {
				if entry.OptimizerDiagnostics == nil || entry.Evaluations > job.Budget {
					continue
				}

				bucket := entry.Evaluations * 256 / job.Budget

				isLast := index == lastEligible
				if !isLast && !early[entry.Iteration] && bucket == lastBucket {
					continue
				}

				lastBucket = bucket
				diagnostic := entry.OptimizerDiagnostics
				populationSpread, sigma, conditionNumber, extent := formatDiagnostics(diagnostic)

				record := []string{
					row.Arm, strconv.Itoa(row.Block), strconv.FormatInt(row.Seed, 10),
					strconv.Itoa(job.Stage),
					strconv.Itoa(entry.Iteration), strconv.Itoa(entry.Evaluations),
					strconv.FormatFloat(entry.Cost, 'g', 17, 64),
					populationSpread, sigma, conditionNumber, extent,
					strconv.Itoa(diagnostic.Restart),
				}
				if err := writer.Write(record); err != nil {
					return err
				}
			}
		}
	}

	writer.Flush()

	return writer.Error()
}

// formatDiagnostics splits one trace entry's optimizer diagnostics into the
// Mayfly column and the three CMA-ES columns, leaving the other engine's cells
// empty. distributionExtent stays empty for a trace written before the adapter
// recorded it, which is every trace behind docs/cmaes-report.md; an empty cell
// says "not measured" where a zero would say "the distribution had no width".
// writeRestarts records one row per independent run of every restart schedule
// in the campaign. It is the only place a restart arm's per-run outcome
// survives: the job-level termination a restart schedule reports is its
// budget-exhausted reason whenever the shared budget is spent, which for an
// arm sized to consume its budget is always, so every restart arm records
// "completed" however its individual runs actually ended.
//
// The file is written even when it holds nothing but a header. A campaign
// whose checkpoints predate the adapter recording these produces exactly that,
// and an empty file says "this campaign has no per-run record" where a missing
// one would be indistinguishable from a collection that failed.
func writeRestarts(config settings, results []resultRow) error {
	if err := os.MkdirAll(filepath.Dir(config.restartsPath), 0o755); err != nil {
		return err
	}

	file, err := os.Create(config.restartsPath)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)

	header := []string{
		"arm", "block", "seed", "stage", "restart", "regime",
		"population", "iterations", "evaluations", "bestCost", "termination",
	}
	if err := writer.Write(header); err != nil {
		return err
	}

	for _, result := range results {
		for _, run := range result.Restarts {
			record := []string{
				result.Arm, strconv.Itoa(result.Block), strconv.FormatInt(result.Seed, 10),
				strconv.Itoa(run.Stage), strconv.Itoa(run.Restart), run.Regime,
				strconv.Itoa(run.Population), strconv.Itoa(run.Iterations),
				strconv.Itoa(run.Evaluations),
				strconv.FormatFloat(run.BestCost, 'g', 17, 64),
				run.Termination,
			}
			if err := writer.Write(record); err != nil {
				return err
			}
		}
	}

	writer.Flush()

	return writer.Error()
}

func formatDiagnostics(diagnostic *opt.SearchDiagnostics) (string, string, string, string) {
	if diagnostic == nil {
		return "", "", "", ""
	}

	if diagnostic.Sigma == 0 && diagnostic.ConditionNumber == 0 {
		return strconv.FormatFloat(diagnostic.PopulationSpread, 'g', 17, 64), "", "", ""
	}

	extent := ""
	if diagnostic.DistributionExtent != 0 {
		extent = strconv.FormatFloat(diagnostic.DistributionExtent, 'g', 17, 64)
	}

	return "",
		strconv.FormatFloat(diagnostic.Sigma, 'g', 17, 64),
		strconv.FormatFloat(diagnostic.ConditionNumber, 'g', 17, 64),
		extent
}

// familyAlpha is the family-wise error rate a design's paired contrasts are
// held to together. Holm's step-down procedure spends it across
// the whole family, so a contrast that clears the uncorrected 0.05 can still
// retain its null here.
const familyAlpha = 0.05

// contrast is one paired comparison of a candidate arm against a control arm,
// carried far enough to be corrected for multiplicity before it is printed.
type contrast struct {
	control   string
	candidate string
	// label is set only on an interaction, whose gain belongs to no pair of
	// arms and so cannot be found by control and candidate. summarize matches
	// on those two, so leaving them empty keeps an interaction out of the arm
	// tables while Holm still corrects over it.
	label     string
	gain      float64
	statistic float64
	pValue    float64
	// interval is the half-width of the 95% paired confidence interval. It is
	// filled for an interaction, where the size of the difference is the whole
	// point and a bare verdict would under-report what was measured.
	interval float64
	wins     int
	rejected bool
}

func analyze(path string, plan design) error {
	rows, err := readResults(path, len(plan.arms)*plan.blocks)
	if err != nil {
		return err
	}

	byArm := make(map[string][]resultRow)
	for _, row := range rows {
		byArm[row.Arm] = append(byArm[row.Arm], row)
	}

	names := make([]string, 0, len(plan.arms))
	for _, current := range plan.arms {
		rows := byArm[current.name]
		if len(rows) != plan.blocks {
			return fmt.Errorf("arm %s has %d blocks, want %d", current.name, len(rows), plan.blocks)
		}

		slices.SortFunc(rows, func(a, b resultRow) int { return a.Block - b.Block })

		names = append(names, current.name)
	}

	if plan.descriptive {
		return reportDescriptive(plan, names, byArm)
	}

	contrasts, err := buildContrasts(plan, byArm)
	if err != nil {
		return err
	}

	holmReject(contrasts, familyAlpha)

	// A design that registers a record answers a second, non-inferential
	// question alongside its contrasts: whether any arm's minimum beat the
	// standing cost. It is an order statistic and carries no p-value, so it
	// gets a column and a verdict line rather than a row in the family.
	record, divider := "", ""
	if plan.record > 0 {
		record, divider = " vs record |", " ---: |"
	}

	fmt.Printf("| arm | mean | sd | median | best |%s gain vs %s | t (df=%d) | p | Holm | blocks won |\n",
		record, plan.baseline, plan.blocks-1)
	fmt.Printf("| --- | ---: | ---: | ---: | ---: |%s ---: | ---: | ---: | --- | ---: |\n", divider)

	for _, name := range names {
		values := costs(byArm[name])
		mean, sd := meanSD(values)

		summary := "control | control | control | control | control"
		if name != plan.baseline {
			summary = summarize(contrasts, plan.baseline, name, plan.blocks)
		}

		best := slices.Min(values)

		against := ""
		if plan.record > 0 {
			against = fmt.Sprintf(" %+.2f |", best-plan.record)
		}

		fmt.Printf("| `%s` | %.2f | %.2f | %.2f | %.2f |%s %s |\n",
			name, mean, sd, median(values), best, against, summary)
	}

	reportRecord(plan, names, byArm)

	// A design that names no secondary control has no contrasts against one,
	// so printing the section anyway produced an "Against :" table whose every
	// row was n/a -- output that reads like a failed lookup in a report meant
	// to be read as the record of a registered experiment. The covariance
	// design is the first inferential one to leave the field empty; every
	// earlier one set it, which is why this went unnoticed until now.
	if plan.secondaryControl != "" {
		fmt.Printf("\nAgainst %s:\n", plan.secondaryControl)
		fmt.Printf("| arm | gain vs %s | t (df=%d) | p | Holm | blocks won |\n",
			plan.secondaryControl, plan.blocks-1)
		fmt.Println("| --- | ---: | ---: | ---: | --- | ---: |")

		for _, name := range names {
			if name == plan.baseline || name == plan.secondaryControl {
				continue
			}

			fmt.Printf("| `%s` | %s |\n",
				name, summarize(contrasts, plan.secondaryControl, name, plan.blocks))
		}
	}

	reportUnprintedContrasts(plan, contrasts)
	reportInteraction(plan, contrasts)

	fmt.Printf("\nHolm step-down over all %d paired contrasts at a family-wise alpha of %.2f;\n",
		len(contrasts), familyAlpha)
	fmt.Printf("the uncorrected two-sided threshold at df=%d is t=%.2f and the Bonferroni one is t=%.2f.\n",
		plan.blocks-1, studentTCritical(familyAlpha, plan.blocks-1),
		studentTCritical(familyAlpha/float64(len(contrasts)), plan.blocks-1))

	if primary, ok := plan.primaryContrast(); ok {
		fmt.Printf("the registered primary contrast is `%s` against `%s`.\n",
			primary.candidate, primary.control)
	}

	return nil
}

// reportDescriptive is the output for a design that buys mechanism instead of
// inference. It prints what each arm cost and what its budget did, and no
// statistic at all: the stagnation pilot has too few blocks to support a paired
// test, and the deep hunt reports a minimum, which is an order statistic rather
// than a mean. Printing a t on either would invite exactly the reading the
// designs were built to avoid.
func reportDescriptive(plan design, names []string, byArm map[string][]resultRow) error {
	fmt.Printf("design %s: descriptive only, %d blocks, no inferential statistics\n\n",
		plan.name, plan.blocks)

	record := ""
	divider := ""

	if plan.record > 0 {
		record = " vs record |"
		divider = " ---: |"
	}

	fmt.Printf("| arm | mean | best | worst | mean evaluations | %% of cap | %% spent after last improvement |%s\n", record)
	fmt.Printf("| --- | ---: | ---: | ---: | ---: | ---: | ---: |%s\n", divider)

	capacity := plan.evaluationCap()

	for _, name := range names {
		rows := byArm[name]
		values := costs(rows)
		mean, _ := meanSD(values)

		// Both shares are ratios of totals rather than means of per-job
		// ratios: the jobs of one arm do not all spend the same number of
		// evaluations, so averaging their fractions would weight a short run
		// exactly like a long one and misstate the budget share the pilot
		// exists to measure.
		var final, wasted float64
		for _, row := range rows {
			final += float64(row.FinalEvaluations)
			wasted += float64(row.FinalEvaluations - row.ScoredEvaluations)
		}

		var share float64
		if final > 0 {
			share = wasted / final * 100
		}

		final /= float64(len(rows))

		best := slices.Min(values)

		against := ""
		if plan.record > 0 {
			against = fmt.Sprintf(" %+.2f |", best-plan.record)
		}

		fmt.Printf("| `%s` | %.2f | %.2f | %.2f | %.0f | %.1f%% | %.1f%% |%s\n",
			name, mean, best, slices.Max(values),
			final, final/float64(capacity)*100, share, against)
	}

	reportRecord(plan, names, byArm)

	fmt.Println("\nRestart counts and per-run termination reasons are in the -restarts CSV;")
	fmt.Println("they, not the costs above, are what selects the window for the registered campaign.")

	return nil
}

// reportRecord prints the campaign best against the standing record. Both
// reports call it, because registering a record is independent of whether the
// design also registers contrasts: the ladder asks its record question next to
// a paired test, the hunt asks it instead of one. It is a no-op for a design
// that registers no record.
func reportRecord(plan design, names []string, byArm map[string][]resultRow) {
	if plan.record <= 0 {
		return
	}

	campaign := math.Inf(1)
	for _, name := range names {
		campaign = math.Min(campaign, slices.Min(costs(byArm[name])))
	}

	verdict := "did not beat"
	if campaign < plan.record {
		verdict = "BEAT"
	}

	fmt.Printf("\ncampaign best %.10f against the standing record %.10f: %s it by %+.10f\n",
		campaign, plan.record, verdict, campaign-plan.record)
}

// evaluationCap is the per-job evaluation budget this design's arms were built
// from, read back off the arms rather than assumed from defaultBudget: a
// campaign submitted with a smaller -budget would otherwise report its spend
// as a share of a cap it never had. Only CMA-ES spends exactly lambda
// evaluations per iteration, so only there is the product the cap; a Mayfly
// arm is matched to the fixed campaign budget by shape instead.
//
// The product is iters*lambda*epochs*restarts, the same one printPlan reports,
// because epochs and cold restarts each multiply the generation count. Reading
// iters*lambda alone would take one run of a split arm for the whole arm and
// report sep-e8's spend as eight times its cap.
// restartAttempts is how many whole attempts a restart count schedules, which
// is its magnitude rather than its value.
//
// A negative count asks for the same cap as its positive twin -- abs(N) * iters
// iterations -- and spends it instead of bounding it, so the two shapes share a
// cap and differ in what they do with the remainder. Reading the value with
// max(N, 1) would take a filling arm for an unsplit one and under-report its cap
// by a factor of its magnitude, which would put a plan table and a cap guard
// out by 32x on the very arms a spend-matched design exists to run.
func restartAttempts(restarts int) int {
	if restarts < 0 {
		restarts = -restarts
	}

	return max(restarts, 1)
}

func (d design) evaluationCap() int {
	highest := 0

	for _, current := range d.arms {
		spend := current.plannedEvaluations()
		if spend == 0 {
			spend = defaultBudget
		}

		if spend > highest {
			highest = spend
		}
	}

	if highest == 0 {
		return defaultBudget
	}

	return highest
}

// buildContrasts evaluates the family the design registered. All of them are
// one family and are corrected together, so the number of contrasts a design
// declares is the multiplicity it pays for -- which is why declaring them is
// now the design's job rather than a consequence of how many arms it happens
// to run.
func buildContrasts(plan design, byArm map[string][]resultRow) ([]contrast, error) {
	contrasts := make([]contrast, 0, len(plan.contrasts)+1)
	for _, planned := range plan.contrasts {
		gain, statistic, wins := pairedImprovement(byArm[planned.control], byArm[planned.candidate])
		contrasts = append(contrasts, contrast{
			control:   planned.control,
			candidate: planned.candidate,
			gain:      gain,
			statistic: statistic,
			pValue:    studentTTwoSided(statistic, plan.blocks-1),
			wins:      wins,
		})
	}

	if plan.interaction == nil {
		return contrasts, nil
	}

	differences, err := interactionDifferences(byArm, *plan.interaction)
	if err != nil {
		return nil, err
	}

	gain, deviation, statistic, wins := pairedStatistics(differences)
	contrasts = append(contrasts, contrast{
		label:     plan.interaction.describe(),
		gain:      gain,
		statistic: statistic,
		pValue:    studentTTwoSided(statistic, plan.blocks-1),
		interval: studentTCritical(familyAlpha, plan.blocks-1) *
			deviation / math.Sqrt(float64(len(differences))),
		wins: wins,
	})

	return contrasts, nil
}

// interactionDifferences subtracts one contrast's per-block gain from the
// other's. Both contrasts run the same blocks by construction -- a design's
// arms all run its whole block set -- so a mismatch is a corrupt result file
// rather than something to paper over with a partial pairing.
func interactionDifferences(byArm map[string][]resultRow, planned plannedInteraction) ([]float64, error) {
	outerBlocks, outer := pairedDifferences(byArm[planned.outer.control], byArm[planned.outer.candidate])
	innerBlocks, inner := pairedDifferences(byArm[planned.inner.control], byArm[planned.inner.candidate])

	if !slices.Equal(outerBlocks, innerBlocks) {
		return nil, fmt.Errorf("interaction %s pairs %v against %v; the blocks do not line up",
			planned.describe(), outerBlocks, innerBlocks)
	}

	differences := make([]float64, len(outer))
	for index := range outer {
		differences[index] = outer[index] - inner[index]
	}

	return differences, nil
}

// summarize renders one contrast's cells for the Markdown tables above.
func summarize(contrasts []contrast, control, candidate string, blocks int) string {
	for _, current := range contrasts {
		if current.control != control || current.candidate != candidate {
			continue
		}

		decision := "retain"
		if current.rejected {
			decision = "reject"
		}

		return fmt.Sprintf("%+.2f | %+.2f | %.5f | %s | %d/%d",
			current.gain, current.statistic, current.pValue, decision, current.wins, blocks)
	}

	return "n/a | n/a | n/a | n/a | n/a"
}

// reportUnprintedContrasts prints every registered contrast the two tables
// above have no column for.
//
// Those tables are organized around the controls: one column against the
// baseline and, where a design names one, a table against the secondary
// control. A design whose contrasts all point at the baseline is fully
// reported by them, which every design before covariance-clean was -- so a
// contrast between two arms that are neither control simply vanished, and the
// row that should have carried it printed "n/a" as though the lookup had
// failed. It had not; there was nowhere to print it.
//
// Registering a comparison and then not reporting it is the worse half of that
// bug: Holm still corrects the family for a contrast the reader never sees.
func reportUnprintedContrasts(plan design, contrasts []contrast) {
	printed := func(control, candidate string) bool {
		if control == plan.baseline {
			return candidate != plan.baseline
		}

		if control == plan.secondaryControl && plan.secondaryControl != "" {
			return candidate != plan.baseline && candidate != plan.secondaryControl
		}

		return false
	}

	remaining := make([]contrast, 0, len(contrasts))

	for _, current := range contrasts {
		if current.label == "" && !printed(current.control, current.candidate) {
			remaining = append(remaining, current)
		}
	}

	if len(remaining) == 0 {
		return
	}

	fmt.Println("\nFurther registered contrasts:")
	fmt.Printf("| contrast | gain | t (df=%d) | p | Holm | blocks won |\n", plan.blocks-1)
	fmt.Println("| --- | ---: | ---: | ---: | --- | ---: |")

	for _, current := range remaining {
		fmt.Printf("| `%s` vs `%s` | %s |\n", current.candidate, current.control,
			summarize(contrasts, current.control, current.candidate, plan.blocks))
	}
}

// reportInteraction prints the design's difference-in-differences, if it
// registered one. It carries the interval as well as the verdict because the
// interaction exists to say how much the two contrasts differ, and a design
// that only printed reject-or-retain would leave the reader to infer the size
// from two other rows -- which is the reading the interaction replaces.
func reportInteraction(plan design, contrasts []contrast) {
	if plan.interaction == nil {
		return
	}

	for _, current := range contrasts {
		if current.label == "" {
			continue
		}

		decision := "retain"
		if current.rejected {
			decision = "reject"
		}

		fmt.Printf("\nRegistered interaction %s:\n", current.label)
		fmt.Printf("| difference of differences | 95%% interval | t (df=%d) | p | Holm | blocks won |\n",
			plan.blocks-1)
		fmt.Println("| ---: | ---: | ---: | ---: | --- | ---: |")
		fmt.Printf("| %+.2f | %+.2f to %+.2f | %+.2f | %.5f | %s | %d/%d |\n",
			current.gain, current.gain-current.interval, current.gain+current.interval,
			current.statistic, current.pValue, decision, current.wins, plan.blocks)

		return
	}
}

// holmReject marks the contrasts whose null hypotheses Holm's step-down
// procedure rejects at the given family-wise alpha. It stops at the first
// contrast that fails its threshold, so every larger p-value retains too.
func holmReject(contrasts []contrast, alpha float64) {
	order := make([]int, len(contrasts))
	for index := range order {
		order[index] = index
	}

	slices.SortFunc(order, func(a, b int) int {
		return cmp.Compare(contrasts[a].pValue, contrasts[b].pValue)
	})

	for rank, index := range order {
		if contrasts[index].pValue >= alpha/float64(len(contrasts)-rank) {
			return
		}

		contrasts[index].rejected = true
	}
}

// studentTTwoSided returns the two-sided p-value of a t statistic on the given
// degrees of freedom. An infinite statistic comes from a zero-variance paired
// difference and is reported as p=0.
func studentTTwoSided(statistic float64, degrees int) float64 {
	if math.IsInf(statistic, 0) {
		return 0
	}

	freedom := float64(degrees)

	return regularizedIncompleteBeta(freedom/(freedom+statistic*statistic), freedom/2, 0.5)
}

// studentTCritical inverts studentTTwoSided by bisection, returning the
// two-sided critical t for the given alpha and degrees of freedom.
func studentTCritical(alpha float64, degrees int) float64 {
	low, high := 0.0, 1e3
	for range 200 {
		middle := (low + high) / 2
		if studentTTwoSided(middle, degrees) > alpha {
			low = middle
		} else {
			high = middle
		}
	}

	return (low + high) / 2
}

// regularizedIncompleteBeta evaluates I_x(a, b) by the continued fraction of
// Numerical Recipes section 6.4, switching branches where it converges fastest.
func regularizedIncompleteBeta(x, a, b float64) float64 {
	switch {
	case x <= 0:
		return 0
	case x >= 1:
		return 1
	}

	logA, _ := math.Lgamma(a)
	logB, _ := math.Lgamma(b)
	logSum, _ := math.Lgamma(a + b)

	front := math.Exp(logSum - logA - logB + a*math.Log(x) + b*math.Log1p(-x))
	if x < (a+1)/(a+b+2) {
		return front * betaContinuedFraction(x, a, b) / a
	}

	return 1 - front*betaContinuedFraction(1-x, b, a)/b
}

// betaContinuedFraction evaluates the continued fraction of the incomplete beta
// function by the modified Lentz algorithm.
func betaContinuedFraction(x, a, b float64) float64 {
	const (
		maxIterations = 300
		epsilon       = 1e-15
		tiny          = 1e-300
	)

	guard := func(value float64) float64 {
		if math.Abs(value) < tiny {
			return tiny
		}

		return value
	}

	sum, plus, minus := a+b, a+1, a-1
	numerator := 1.0
	denominator := 1 / guard(1-sum*x/plus)
	fraction := denominator

	for step := 1; step <= maxIterations; step++ {
		index := float64(step)
		doubled := 2 * index

		term := index * (b - index) * x / ((minus + doubled) * (a + doubled))
		denominator = 1 / guard(1+term*denominator)
		numerator = guard(1 + term/numerator)
		fraction *= denominator * numerator

		term = -(a + index) * (sum + index) * x / ((a + doubled) * (plus + doubled))
		denominator = 1 / guard(1+term*denominator)
		numerator = guard(1 + term/numerator)
		delta := denominator * numerator
		fraction *= delta

		if math.Abs(delta-1) < epsilon {
			break
		}
	}

	return fraction
}

func readResults(path string, wantJobs int) ([]resultRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)

	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}

	if len(records) != wantJobs+1 {
		return nil, fmt.Errorf("results contain %d jobs, want %d", len(records)-1, wantJobs)
	}

	rows := make([]resultRow, 0, wantJobs)

	for index, record := range records[1:] {
		line := index + 2
		if len(record) != resultColumns && len(record) != legacyResultColumns {
			return nil, fmt.Errorf(
				"results line %d has %d columns, want %d or %d",
				line, len(record), resultColumns, legacyResultColumns,
			)
		}

		parser := rowParser{record: record, line: line}

		row := resultRow{
			manifestRow: manifestRow{
				Arm: record[0], Block: parser.integer(1), Seed: parser.integer64(2), JobID: record[3],
			},
			State: record[4], Termination: record[5], OptimizerVersion: record[6],
			Score:             parser.float(7),
			ScoredEvaluations: parser.integer(8),
			FinalEvaluations:  parser.integer(9),
			Iterations:        parser.integer(10),
			ElapsedSeconds:    parser.float(11),
		}
		if len(record) == resultColumns {
			row.Backend = record[12]
		}
		if parser.err != nil {
			return nil, parser.err
		}

		rows = append(rows, row)
	}

	return rows, nil
}

// rowParser reads the numeric columns of one result record and keeps the first
// failure. Discarding the strconv errors instead would let a hand-edited or
// truncated CSV parse as zeros and be reported as a statistic, which is the
// one failure mode a measurement collector must not have.
type rowParser struct {
	record []string
	err    error
	line   int
}

func (p *rowParser) fail(column int, err error) {
	if p.err == nil {
		p.err = fmt.Errorf("results line %d column %d (%q): %w", p.line, column+1, p.record[column], err)
	}
}

func (p *rowParser) integer(column int) int {
	value, err := strconv.Atoi(p.record[column])
	if err != nil {
		p.fail(column, err)
	}

	return value
}

func (p *rowParser) integer64(column int) int64 {
	value, err := strconv.ParseInt(p.record[column], 10, 64)
	if err != nil {
		p.fail(column, err)
	}

	return value
}

func (p *rowParser) float(column int) float64 {
	value, err := strconv.ParseFloat(p.record[column], 64)
	if err != nil {
		p.fail(column, err)
	}

	return value
}

func costs(rows []resultRow) []float64 {
	values := make([]float64, len(rows))
	for index, row := range rows {
		values[index] = row.Score
	}

	return values
}

func meanSD(values []float64) (float64, float64) {
	mean := 0.0
	for _, value := range values {
		mean += value
	}

	mean /= float64(len(values))

	variance := 0.0

	for _, value := range values {
		delta := value - mean
		variance += delta * delta
	}

	variance /= float64(len(values) - 1)

	return mean, math.Sqrt(variance)
}

func median(values []float64) float64 {
	ordered := append([]float64(nil), values...)
	slices.Sort(ordered)
	middle := len(ordered) / 2

	return (ordered[middle-1] + ordered[middle]) / 2
}

func pairedImprovement(control, candidate []resultRow) (float64, float64, int) {
	_, differences := pairedDifferences(control, candidate)
	mean, _, statistic, wins := pairedStatistics(differences)

	return mean, statistic, wins
}

// pairedDifferences is one contrast's per-block gain, in block order, with the
// blocks it was taken over.
//
// The ordering is not cosmetic. Floating-point addition is not associative, so
// summing the differences in Go's randomized map order would make a reported
// mean depend on the run rather than on the data.
func pairedDifferences(control, candidate []resultRow) ([]int, []float64) {
	controlByBlock := make(map[int]float64, len(control))
	for _, row := range control {
		controlByBlock[row.Block] = row.Score
	}

	ordered := append([]resultRow(nil), candidate...)
	slices.SortFunc(ordered, func(a, b resultRow) int { return a.Block - b.Block })

	blocks := make([]int, 0, len(ordered))
	differences := make([]float64, 0, len(ordered))

	for _, row := range ordered {
		blocks = append(blocks, row.Block)
		differences = append(differences, controlByBlock[row.Block]-row.Score)
	}

	return blocks, differences
}

// pairedStatistics reduces a set of paired differences to what a contrast row
// prints, in that order: the mean, its sample standard deviation, the
// one-sample t statistic on those differences, and how many of them favour the
// candidate.
//
// A zero standard deviation is an infinite statistic rather than a division by
// zero, which is what docs/cmaes-covariance-report.md's void secondary
// contrast produced and why that report says the value means nothing.
func pairedStatistics(differences []float64) (float64, float64, float64, int) {
	wins := 0

	for _, difference := range differences {
		if difference > 0 {
			wins++
		}
	}

	mean, deviation := meanSD(differences)

	statistic := math.Copysign(math.Inf(1), mean)
	if deviation != 0 {
		statistic = mean / (deviation / math.Sqrt(float64(len(differences))))
	}

	return mean, deviation, statistic, wins
}
