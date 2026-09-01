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
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/opt"
	"github.com/cwbudde/circlefit/internal/store"
)

// The registered design names, so every place that has to name one -- the
// switch in campaignDesign, the artifact defaults, and the tests that
// enumerate them -- cannot drift apart.
const (
	designPhase21 = "phase21"
	designLambda  = "lambda"
	designPilot   = "stagnation-pilot"
	designStag    = "stagnation"
	designSplit   = "budget-split"
	designLadder  = "restart-ladder"
	designHunt    = "deep-hunt"
	designCov     = "covariance"
	designActive  = "active-cma"
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
// recordReference: cost 752.5220120747884, seed 111018, job
// 2997714f-aa17-4a0d-9a10-45c746f2d270, produced under the sep-ipop
// configuration -- separable covariance, IPOP, lambda 1024. Naming a campaign
// here would be wrong whichever one were named: the stagnation campaign set the
// cost and the restart ladder returned it bit for bit from four independent
// lambda-1024 schedules on that seed, which is what makes it an attractor
// rather than one run's luck.
//
// Every value is inside fit.NewBounds for a 512x512 reference -- x and y in
// [-256, 767], r in [1, 512] -- which matters because app refuses an
// out-of-bounds initialCircles rather than clamping it. Two of them sit exactly
// on a bound. The colours go through an 8-bit hex round trip on the way to the
// server, so the run starts a hair off the recorded optimum.
func recordCircles() []recordCircle {
	return []recordCircle{
		{x: 329.9423119106, y: 373.0225476704, r: 431.5423031015, red: 0.0952935261, green: 0.0899109656, blue: 0.0019265758, opacity: 0.8890438275},
		{x: 365.0914174402, y: 190.2735475663, r: 173.8904554558, red: 0.7839592864, green: 0.9188836416, blue: 0.9168788995, opacity: 0.8546525007},
		{x: 577.8767134563, y: -63.3337480908, r: 385.0442728104, red: 0.1283559047, green: 0.1321413762, blue: 0.0001885401, opacity: 0.9161314775},
		{x: 766.9993601946, y: 274.8295172743, r: 438.8285807262, red: 0.3837069103, green: 0.3680930374, blue: 0.2076307413, opacity: 0.9989963181},
		{x: 404.5302577836, y: 113.0090295166, r: 35.9794288312, red: 0.9995151396, green: 0.9470507642, blue: 0.7532006364, opacity: 0.9992998456},
		{x: -142.4033416647, y: 75.6240703214, r: 265.6286379537, red: 0.7076551967, green: 0.6835767334, blue: 0.5242550009, opacity: 0.7240497837},
		{x: -255.9431084465, y: 388.9910244196, r: 458.6624137213, red: 0.1020058899, green: 0.1080295816, blue: 0.0490751067, opacity: 0.8886370426},
		{x: 281.5353202309, y: 2.3742393186, r: 511.9960144115, red: 0.2968523207, green: 0.2570314710, blue: 0.1317415412, opacity: 0.4405397128},
	}
}

// recordCost is the cost recordCircles produces. reportDescriptive prints each
// arm's minimum against it, which is the whole point of a design that reports
// an order statistic instead of a paired test.
const recordCost = 752.5220120747884

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
	JobID string
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
		"stagnation, budget-split, restart-ladder, deep-hunt, covariance or active-cma")
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
			record: recordCost,
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
	default:
		return design{}, fmt.Errorf(
			"unknown design %q (want phase21, lambda, stagnation-pilot, stagnation, "+
				"budget-split, restart-ladder, deep-hunt, covariance or active-cma)",
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

	n := float64(dimension)
	muEff := 1 / squareSum
	//nolint:varnamelen // c1 and cmu are Hansen's notation and go-cma-es's own
	// field names. Renaming them breaks the correspondence this function exists
	// to preserve, which is the only thing that makes it reviewable.
	c1 := 2 / ((n+1.3)*(n+1.3) + muEff)
	cmu := math.Min(1-c1, 2*(muEff-2+1/muEff)/((n+2)*(n+2)+muEff))

	if blockDimension < dimension {
		cmu = math.Min(1-c1, cmu*(n+2)/float64(blockDimension+2))
	}

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

// assertDesignShape refuses a -blocks or -seed-base that contradicts the
// registered design. Both are the design's to choose, so the flags are an
// assertion a caller can make rather than a way to alter a campaign: a
// mistyped seed base would silently reuse another campaign's seeds, and a
// mistyped block count would change df after the fact.
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
	if err := writer.Write([]string{"arm", "block", "seed", "jobId"}); err != nil {
		return err
	}

	writer.Flush()

	client := &http.Client{Timeout: 30 * time.Second}

	for block := 1; block <= plan.blocks; block++ {
		seed := plan.seedBase + int64(block)
		for _, current := range arms {
			jobID, submitErr := submitJob(client, config, plan, current, seed)
			if submitErr != nil {
				return fmt.Errorf("submit %s block %d: %w", current.name, block, submitErr)
			}

			record := []string{current.name, strconv.Itoa(block), strconv.FormatInt(seed, 10), jobID}
			if err := writer.Write(record); err != nil {
				return fmt.Errorf("record submitted job: %w", err)
			}

			writer.Flush()

			if err := writer.Error(); err != nil {
				return fmt.Errorf("flush manifest: %w", err)
			}

			fmt.Printf("submitted block %02d %-16s %s\n", block, current.name, jobID)
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

func collect(config settings) error {
	manifest, err := readManifest(config.manifestPath)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Second}
	results := make([]resultRow, 0, len(manifest))
	counts := make(map[string]int)

	for _, record := range manifest {
		status, statusErr := fetchStatus(client, config.server, record.JobID)
		if statusErr != nil {
			return fmt.Errorf("status %s: %w", record.JobID, statusErr)
		}

		counts[status.State]++
		if status.State != "completed" {
			continue
		}

		result, collectErr := collectJob(config, record, status)
		if collectErr != nil {
			return collectErr
		}

		results = append(results, result)
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

	if err := writeTrajectories(config, manifest); err != nil {
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
	fmt.Printf("fixture %s, %d circles in one batch\n", reference, circles)
	fmt.Println("| arm | optimizer | covariance | restarts | epochs | popSize (lambda) | iters | " +
		"sigma | active | start | stagnation | evaluations |")
	fmt.Println("| --- | --- | --- | --- | ---: | ---: | ---: | --- | --- | --- | --- | ---: |")

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
		evaluations := strconv.FormatInt(
			int64(current.iters)*int64(current.popSize)*
				int64(max(current.optimizerEpochs, 1))*int64(max(current.optimizerRestarts, 1)), 10)
		if current.optimizer == "mayfly" {
			covariance = "-"
			restarts = fmt.Sprintf("%d cold run(s)", current.optimizerRestarts)
			evaluations = "scored from trace"
		} else if current.optimizerRestarts > 1 {
			// A CMA-ES arm can carry both the adapter's own ladder and the
			// generic cold-restart wrapper; naming only the strategy would hide
			// the second.
			restarts = fmt.Sprintf("%s + %d cold run(s)", restarts, current.optimizerRestarts)
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

			if current.warmStart {
				start = "record"
			}
		}

		fmt.Printf("| `%s` | %s | %s | %s | %d | %d | %d | %s | %s | %s | %s | %s |\n",
			current.name, current.optimizer, covariance, restarts, max(current.optimizerEpochs, 1),
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
		jobDir := filepath.Join(config.dataRoot, "projects", config.project, "jobs", record.JobID)

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

		result, collectErr := collectJob(config, record, status)
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

func collectJob(config settings, record manifestRow, status jobStatus) (resultRow, error) {
	jobDir := filepath.Join(config.dataRoot, "projects", config.project, "jobs", record.JobID)

	trace, err := readTrace(filepath.Join(jobDir, "trace.jsonl"))
	if err != nil {
		return resultRow{}, fmt.Errorf("read %s trace: %w", record.JobID, err)
	}

	score := math.Inf(1)
	scoredEvaluations := 0

	for _, entry := range trace {
		if entry.Evaluations <= config.budget && entry.Cost < score {
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

	if len(records) < 2 || !slices.Equal(records[0], []string{"arm", "block", "seed", "jobId"}) {
		return nil, errors.New("manifest has an unexpected header or no jobs")
	}

	rows := make([]manifestRow, 0, len(records)-1)
	for _, record := range records[1:] {
		if len(record) != 4 {
			return nil, fmt.Errorf("invalid manifest row %q", record)
		}

		block, blockErr := strconv.Atoi(record[1])

		seed, seedErr := strconv.ParseInt(record[2], 10, 64)
		if blockErr != nil || seedErr != nil {
			return nil, fmt.Errorf("invalid manifest row %q", record)
		}

		rows = append(rows, manifestRow{Arm: record[0], Block: block, Seed: seed, JobID: record[3]})
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
	if err := writer.Write([]string{"arm", "block", "seed", "iteration", "evaluations", "bestCost", "populationSpread", "sigma", "conditionNumber", "distributionExtent", "restart"}); err != nil {
		return err
	}

	early := map[int]bool{1: true, 5: true, 10: true, 20: true, 40: true, 80: true, 160: true, 255: true}

	for _, record := range manifest {
		jobDir := filepath.Join(config.dataRoot, "projects", config.project, "jobs", record.JobID)

		entries, readErr := readTrace(filepath.Join(jobDir, "trace.jsonl"))
		if readErr != nil {
			return readErr
		}

		lastEligible := -1

		for index, entry := range entries {
			if entry.OptimizerDiagnostics != nil && entry.Evaluations <= config.budget {
				lastEligible = index
			}
		}

		lastBucket := -1

		for index, entry := range entries {
			if entry.OptimizerDiagnostics == nil || entry.Evaluations > config.budget {
				continue
			}

			bucket := entry.Evaluations * 256 / config.budget

			isLast := index == lastEligible
			if !isLast && !early[entry.Iteration] && bucket == lastBucket {
				continue
			}

			lastBucket = bucket
			diagnostic := entry.OptimizerDiagnostics
			populationSpread, sigma, conditionNumber, extent := formatDiagnostics(diagnostic)

			record := []string{
				record.Arm, strconv.Itoa(record.Block), strconv.FormatInt(record.Seed, 10),
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
	gain      float64
	statistic float64
	pValue    float64
	wins      int
	rejected  bool
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

	contrasts := buildContrasts(plan, byArm)
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
func (d design) evaluationCap() int {
	highest := 0

	for _, current := range d.arms {
		spend := defaultBudget
		if current.optimizer != "mayfly" {
			spend = current.iters * current.popSize *
				max(current.optimizerEpochs, 1) * max(current.optimizerRestarts, 1)
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
func buildContrasts(plan design, byArm map[string][]resultRow) []contrast {
	contrasts := make([]contrast, 0, len(plan.contrasts))
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

	return contrasts
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
	controlByBlock := make(map[int]float64, len(control))
	for _, row := range control {
		controlByBlock[row.Block] = row.Score
	}

	differences := make([]float64, 0, len(candidate))
	wins := 0

	for _, row := range candidate {
		difference := controlByBlock[row.Block] - row.Score

		differences = append(differences, difference)
		if difference > 0 {
			wins++
		}
	}

	mean, sd := meanSD(differences)
	if sd == 0 {
		return mean, math.Copysign(math.Inf(1), mean), wins
	}

	return mean, mean / (sd / math.Sqrt(float64(len(differences)))), wins
}
