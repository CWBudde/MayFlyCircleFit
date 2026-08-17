package server

import (
	"fmt"
	"sort"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/cwbudde/mayflycirclefit/internal/ui"
)

// A campaign view is a read model, not a second source of truth. It is built
// from the stage records a schedule already keeps, or — for a chain of extend
// and polish jobs driven by hand — from the lineage each checkpoint carries.
// Nothing is stored for it, so it cannot drift from what actually ran.

// maxChainLength bounds a lineage walk. It is the realized-stage ceiling a
// schedule already enforces, reused here so a corrupt or hand-edited chain
// cannot spin.
const maxChainLength = 4096

// campaignFromSchedule turns a schedule and its recorded stages into the view.
func campaignFromSchedule(record *store.ScheduleRecord, stages []store.ScheduleStageRecord) ui.Campaign {
	planned := 0
	if plan, err := record.Document.Expand(); err == nil {
		planned = len(plan)
	}
	seed, hasSeed := campaignSeed(record, stages)
	campaign := ui.Campaign{
		ID:            record.ScheduleID,
		Name:          record.Document.Name,
		State:         string(record.State),
		Source:        ui.CampaignFromSchedule,
		CampaignSeed:  seed,
		HasSeed:       hasSeed,
		PlannedStages: planned,
		Error:         record.Error,
	}
	campaign.Stages = make([]ui.CampaignStage, 0, len(stages))
	for i := range stages {
		campaign.Stages = append(campaign.Stages, campaignStageFromRecord(&stages[i]))
	}
	return campaign
}

// campaignSeed reports the seed the campaign actually ran with.
//
// A document that omits the seed leaves ScheduleRecord.CampaignSeed at zero,
// because zero is the "resolve one for me" sentinel rather than a seed. The
// resolved value does exist once a stage has run — every stage record keeps the
// configuration it ran with — so it is read back from there. Before the first
// stage there is nothing to read, and the view says the seed is absent instead
// of presenting the sentinel as if it were the seed.
func campaignSeed(record *store.ScheduleRecord, stages []store.ScheduleStageRecord) (int64, bool) {
	if record.CampaignSeed != 0 {
		return record.CampaignSeed, true
	}
	for i := range stages {
		if seed := stages[i].Config.EffectiveSeed; seed != 0 {
			return seed, true
		}
	}
	return 0, false
}

// campaignStageFromRecord maps one stage record onto a table row.
//
// A cost is only reported for a stage that finished. A running stage's record
// carries the zero it was created with, and showing that as a perfect fit would
// be worse than showing nothing.
func campaignStageFromRecord(record *store.ScheduleStageRecord) ui.CampaignStage {
	stage := ui.CampaignStage{
		Index:       record.Index,
		Kind:        string(record.Kind),
		State:       string(record.State),
		Circles:     record.Circles,
		Iterations:  record.Iterations,
		Evaluations: record.Evaluations,
		JobID:       record.JobID,
		ParentJobID: record.ParentJobID,
		Note:        stageNote(record),
	}
	if record.State == store.ScheduleStateCompleted {
		stage.BestCost = record.BestCost
		stage.HasBestCost = true
		if psnr, infinite := serializablePSNR(record.BestCost); infinite {
			stage.PSNRInfinite = true
		} else if psnr != nil {
			stage.PSNR, stage.HasPSNR = *psnr, true
		}
	}
	if record.StartedAt != nil && record.CompletedAt != nil {
		stage.ElapsedSec = record.CompletedAt.Sub(*record.StartedAt).Seconds()
		stage.HasElapsed = true
	} else if record.StartedAt == nil {
		stage.ElapsedAbsent = "The stage has not started"
	} else {
		stage.ElapsedAbsent = "The stage has not finished"
	}
	return stage
}

func stageNote(record *store.ScheduleStageRecord) string {
	if record.Error != "" {
		return record.Error
	}
	return record.Reason
}

// chainCheckpoints walks a job's lineage back to the root of its chain and
// returns the checkpoints in run order. It is what lets a campaign be
// reconstructed for jobs created before schedules existed: the extend and
// polish endpoints record the parent on the checkpoint, so the chain is
// recoverable from the job tree alone.
func chainCheckpoints(checkpoints store.Store, leafJobID string) ([]*store.Checkpoint, error) {
	seen := make(map[string]struct{})
	chain := make([]*store.Checkpoint, 0, 8)
	for jobID := leafJobID; jobID != ""; {
		if _, repeated := seen[jobID]; repeated {
			return nil, fmt.Errorf("checkpoint lineage for %q is cyclic at %q", leafJobID, jobID)
		}
		seen[jobID] = struct{}{}
		if len(chain) >= maxChainLength {
			return nil, fmt.Errorf("checkpoint lineage for %q exceeds %d stages", leafJobID, maxChainLength)
		}
		checkpoint, err := checkpoints.LoadCheckpoint(jobID)
		if err != nil {
			return nil, err
		}
		chain = append(chain, checkpoint)
		parent, ok := checkpoint.ContinuedFrom()
		if !ok {
			break
		}
		jobID = parent
	}
	// The walk runs leaf first because that is the only direction the lineage
	// points; the view wants run order.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain, nil
}

// campaignFromChain presents a walked lineage as a campaign.
func campaignFromChain(leafJobID string, chain []*store.Checkpoint) ui.Campaign {
	campaign := ui.Campaign{
		ID:     leafJobID,
		State:  chainState(chain),
		Source: ui.CampaignFromChain,
	}
	campaign.Stages = make([]ui.CampaignStage, 0, len(chain))
	for index, checkpoint := range chain {
		parent, _ := checkpoint.ContinuedFrom()
		stage := ui.CampaignStage{
			Index:       index,
			Kind:        chainStageKind(checkpoint),
			State:       chainStageState(checkpoint.Termination),
			Circles:     checkpoint.ActualCircles,
			BestCost:    checkpoint.BestCost,
			HasBestCost: true,
			Iterations:  checkpoint.Iterations,
			Evaluations: checkpoint.Evaluations,
			JobID:       checkpoint.JobID,
			ParentJobID: parent,
			// A checkpoint records when it was written, never how long its job
			// ran. The gap to the previous checkpoint is not the same thing —
			// jobs need not have run back to back — so the column stays empty
			// rather than reporting a number that only looks like a duration.
			ElapsedAbsent: "A checkpoint does not record how long its job ran",
		}
		if stage.Circles == 0 {
			stage.Circles = checkpoint.RequestedCircles
		}
		if psnr, infinite := serializablePSNR(checkpoint.BestCost); infinite {
			stage.PSNRInfinite = true
		} else if psnr != nil {
			stage.PSNR, stage.HasPSNR = *psnr, true
		}
		campaign.Stages = append(campaign.Stages, stage)
	}
	return campaign
}

func chainStageKind(checkpoint *store.Checkpoint) string {
	switch {
	case checkpoint.ExtendedFrom != "":
		return "extend"
	case checkpoint.PolishedFrom != "":
		return "polish"
	default:
		return "base"
	}
}

// chainStageState reports the checkpoint's own termination, which is the only
// state an imported stage has: the job records are long gone for a chain read
// back off disk.
//
// The mapping is the one jobFromCheckpoint in restore.go applies when it
// rebuilds a job, and TestChainStageStateMatchesRestore pins the two together.
// An unknown or legacy termination is not a finished run — it is a checkpoint
// whose job never recorded how it ended — so it reads as cancelled here too
// rather than claiming a campaign completed.
func chainStageState(termination string) string {
	switch termination {
	case "completed", "target_cost", "stagnation", "stage_convergence", "refill_limit":
		return string(StateCompleted)
	case "failed":
		return string(StateFailed)
	default:
		return string(StateCancelled)
	}
}

func chainState(chain []*store.Checkpoint) string {
	if len(chain) == 0 {
		return "pending"
	}
	return chainStageState(chain[len(chain)-1].Termination)
}

// discoveredChain is a lineage found in a checkpoint listing, before it is
// shaped for either the HTML view or the JSON API.
type discoveredChain struct {
	LeafJobID string
	RootJobID string
	Stages    int
	Circles   int
	BestCost  float64
	UpdatedAt time.Time

	// Termination is the leaf checkpoint's own termination, carried through so
	// the listing card can state the chain the same way the detail page does.
	// Without it the listing would have to assume every chain finished.
	Termination string
}

// discoverChains finds every multi-stage lineage in a store. A checkpoint that
// no other checkpoint continues from is a leaf, and a leaf whose chain has more
// than one member is a campaign somebody ran by hand.
//
// Checkpoints that already name a schedule are left out: they have a real
// campaign view built from the stage records, which knows strictly more than a
// reconstruction does.
func discoverChains(infos []store.CheckpointInfo) []discoveredChain {
	byID := make(map[string]store.CheckpointInfo, len(infos))
	parents := make(map[string]string, len(infos))
	continued := make(map[string]struct{}, len(infos))
	for _, info := range infos {
		if info.ScheduleID != "" {
			continue
		}
		byID[info.JobID] = info
		parent := info.ExtendedFrom
		if parent == "" {
			parent = info.PolishedFrom
		}
		if parent != "" {
			parents[info.JobID] = parent
			continued[parent] = struct{}{}
		}
	}

	chains := make([]discoveredChain, 0)
	for jobID, info := range byID {
		if _, hasChild := continued[jobID]; hasChild {
			continue
		}
		length, root := chainLength(jobID, byID, parents)
		if length < 2 {
			continue
		}
		chains = append(chains, discoveredChain{
			LeafJobID:   jobID,
			RootJobID:   root,
			Stages:      length,
			Circles:     chainCircles(info),
			BestCost:    info.BestCost,
			UpdatedAt:   info.Timestamp,
			Termination: info.Termination,
		})
	}
	sortDiscoveredChains(chains)
	return chains
}

// sortDiscoveredChains puts the most recently updated chain first. It is a
// function of its own because a listing merged from several project stores has
// to be ordered again after the merge.
func sortDiscoveredChains(chains []discoveredChain) {
	sort.Slice(chains, func(i, j int) bool {
		if !chains[i].UpdatedAt.Equal(chains[j].UpdatedAt) {
			return chains[i].UpdatedAt.After(chains[j].UpdatedAt)
		}
		return chains[i].LeafJobID < chains[j].LeafJobID
	})
}

// chainCampaignSummaries shapes discovered chains for the campaign listing.
func chainCampaignSummaries(chains []discoveredChain) []ui.CampaignSummary {
	summaries := make([]ui.CampaignSummary, 0, len(chains))
	for _, chain := range chains {
		summaries = append(summaries, ui.CampaignSummary{
			ID:             chain.LeafJobID,
			Name:           "from " + shortJobID(chain.RootJobID),
			State:          chainStageState(chain.Termination),
			Source:         ui.CampaignFromChain,
			RecordedStages: chain.Stages,
			Circles:        chain.Circles,
			BestCost:       chain.BestCost,
			HasBestCost:    true,
			UpdatedAt:      chain.UpdatedAt,
		})
	}
	return summaries
}

func shortJobID(jobID string) string {
	if len(jobID) <= 8 {
		return jobID
	}
	return jobID[:8]
}

// chainLength counts the members of a leaf's chain and names its root, walking
// only the listing so no checkpoint has to be loaded.
func chainLength(leafJobID string, byID map[string]store.CheckpointInfo, parents map[string]string) (int, string) {
	seen := make(map[string]struct{})
	length := 0
	current, root := leafJobID, leafJobID
	for current != "" && length < maxChainLength {
		if _, repeated := seen[current]; repeated {
			break
		}
		seen[current] = struct{}{}
		if _, known := byID[current]; !known {
			break
		}
		length++
		root = current
		current = parents[current]
	}
	return length, root
}

func chainCircles(info store.CheckpointInfo) int {
	if info.ActualCircles > 0 {
		return info.ActualCircles
	}
	return info.RequestedCircles
}

// summarizeCampaign is the listing row for a schedule.
func summarizeCampaign(record *store.ScheduleRecord, stages []store.ScheduleStageRecord) ui.CampaignSummary {
	planned := 0
	if plan, err := record.Document.Expand(); err == nil {
		planned = len(plan)
	}
	summary := ui.CampaignSummary{
		ID:             record.ScheduleID,
		Name:           record.Document.Name,
		State:          string(record.State),
		Source:         ui.CampaignFromSchedule,
		RecordedStages: len(stages),
		PlannedStages:  planned,
		UpdatedAt:      record.UpdatedAt,
	}
	// The best cost of a campaign is the last stage that produced one, not the
	// smallest: a polish that made things worse is still where the chain is.
	for i := range stages {
		if stages[i].State != store.ScheduleStateCompleted {
			continue
		}
		summary.BestCost, summary.HasBestCost = stages[i].BestCost, true
		summary.Circles = stages[i].Circles
	}
	if summary.Circles == 0 && len(stages) > 0 {
		summary.Circles = stages[len(stages)-1].Circles
	}
	return summary
}

// chainStageWire is the JSON form of an imported stage. It is a flat record
// rather than the UI view model: the API describes what is on disk, and leaves
// derived presentation to the caller.
type chainStageWire struct {
	Index       int       `json:"index"`
	Kind        string    `json:"kind"`
	JobID       string    `json:"jobId"`
	ParentJobID string    `json:"parentJobId,omitempty"`
	Circles     int       `json:"circles"`
	BestCost    float64   `json:"bestCost"`
	Iterations  int       `json:"iterations"`
	Evaluations int64     `json:"evaluations"`
	Termination string    `json:"termination,omitempty"`
	Timestamp   time.Time `json:"timestamp"`
}

type chainSummaryWire struct {
	LeafJobID string    `json:"leafJobId"`
	RootJobID string    `json:"rootJobId"`
	Stages    int       `json:"stages"`
	Circles   int       `json:"circles"`
	BestCost  float64   `json:"bestCost"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type chainDetailWire struct {
	LeafJobID string           `json:"leafJobId"`
	RootJobID string           `json:"rootJobId"`
	Stages    []chainStageWire `json:"stages"`
}

func chainDetailFrom(leafJobID string, chain []*store.Checkpoint) chainDetailWire {
	detail := chainDetailWire{LeafJobID: leafJobID, Stages: make([]chainStageWire, 0, len(chain))}
	if len(chain) > 0 {
		detail.RootJobID = chain[0].JobID
	}
	for index, checkpoint := range chain {
		parent, _ := checkpoint.ContinuedFrom()
		circles := checkpoint.ActualCircles
		if circles == 0 {
			circles = checkpoint.RequestedCircles
		}
		detail.Stages = append(detail.Stages, chainStageWire{
			Index:       index,
			Kind:        chainStageKind(checkpoint),
			JobID:       checkpoint.JobID,
			ParentJobID: parent,
			Circles:     circles,
			BestCost:    checkpoint.BestCost,
			Iterations:  checkpoint.Iterations,
			Evaluations: checkpoint.Evaluations,
			Termination: checkpoint.Termination,
			Timestamp:   checkpoint.Timestamp,
		})
	}
	return detail
}

func chainSummaryWires(chains []discoveredChain) []chainSummaryWire {
	wires := make([]chainSummaryWire, 0, len(chains))
	for _, chain := range chains {
		wires = append(wires, chainSummaryWire{
			LeafJobID: chain.LeafJobID,
			RootJobID: chain.RootJobID,
			Stages:    chain.Stages,
			Circles:   chain.Circles,
			BestCost:  chain.BestCost,
			UpdatedAt: chain.UpdatedAt,
		})
	}
	return wires
}
