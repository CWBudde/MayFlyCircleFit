package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cwbudde/circlefit/internal/app"
	"github.com/cwbudde/circlefit/internal/store"
	"github.com/google/uuid"
)

// The schedule endpoints follow the job endpoints exactly: unknown fields are
// refused, errors use the shared writeAPIError shape, and the boundary is the
// trusted-local one documented in docs/behavior-invariants.md.
//
// Schedules live in the server's default store and their stages therefore run
// in the default project. Per-project campaigns are deliberately out of scope
// here; the store is keyed by schedule identifier and does not know about
// projects.

// scheduleSummary is a campaign without its stages, which is what a listing
// needs. The realized stage count comes from expanding the document rather than
// from a stored total, so it cannot disagree with the plan.
type scheduleSummary struct {
	ScheduleID   string              `json:"scheduleId"`
	Name         string              `json:"name,omitempty"`
	State        store.ScheduleState `json:"state"`
	CampaignSeed int64               `json:"campaignSeed"`
	TotalStages  int                 `json:"totalStages"`
	CreatedAt    time.Time           `json:"createdAt"`
	UpdatedAt    time.Time           `json:"updatedAt"`
	Error        string              `json:"error,omitempty"`

	// Warnings are the document's advisories: settings the campaign runs
	// exactly as authored and a measurement says are wasted. They are not
	// errors, which is why they travel beside Error rather than in it — the
	// schedule was accepted, and a client that ignores them still sees a
	// campaign that ran.
	Warnings []string `json:"warnings,omitempty"`
}

// scheduleDetail adds the stage listing, which is the single source of truth
// for how far the campaign has got.
//
// The listing is a projection, not the stage records. A stage record carries
// the whole normalized JobConfig it ran with, so a full listing grows by about
// 1.2 kB per stage and passes the CLI's 1 MiB response cap at roughly 865 of
// the 4096 stages a schedule may legally expand to. The projection carries what
// a reader asks a campaign — where each stage got to and how long it took — and
// a stage's configuration is fetched one stage at a time from
// /api/v1/schedules/:id/stages/:index, which is the granularity replaying a
// single stage actually needs.
type scheduleDetail struct {
	scheduleSummary

	Document app.ScheduleDocument   `json:"document"`
	Stages   []scheduleStageSummary `json:"stages"`

	// Projection is the estimate of what is left, and it is absent whenever
	// there is nothing to estimate: a campaign that will not advance again, a
	// stored document that no longer expands, or a plan every stage of which
	// has already completed or been skipped. See projectSchedule.
	Projection *scheduleProjectionWire `json:"projection,omitempty"`
}

// scheduleProjectionWire is the finish and cost estimate for one campaign.
//
// It carries both answers because a campaign can be short of two different
// things. The kind table below says when the plan finishes; Cost says where the
// fit lands, once against the plan's circle ceiling and once against that
// clock. The measured campaign behind Task 16.9 is why: the settings that win
// per hour lose per circle, so a surface reporting only wall clock hands a
// campaign against a ceiling the wrong answer.
//
// Durations are nanoseconds for the reason elapsedNanos already is: it is
// shorter than the timestamps it is derived from, and it is exact, so a figure
// read off this response equals the one the CLI computes locally. The two
// finish timestamps are pointers rather than zero times, because a projection
// that could not be completed has no finish time at all and a zero-valued
// RFC 3339 instant reads as one.
type scheduleProjectionWire struct {
	AsOf time.Time `json:"asOf"`

	RemainingStages int  `json:"remainingStages"`
	Complete        bool `json:"complete"`

	RemainingNanos int64      `json:"remainingNanos"`
	FirmNanos      int64      `json:"firmNanos"`
	FinishBy       *time.Time `json:"finishBy,omitempty"`
	EarliestFinish *time.Time `json:"earliestFinish,omitempty"`

	Kinds []scheduleKindProjectionWire `json:"kinds"`
	Cost  scheduleCostProjectionWire   `json:"cost"`
}

// scheduleKindProjectionWire is one stage kind's share of the finish estimate.
// Projected is sent rather than left for the client to re-derive from Samples,
// because the rule behind it — enough samples *and* a per-stage figure above
// zero — is the application layer's and not the browser's.
type scheduleKindProjectionWire struct {
	Kind    app.ScheduleStageKind `json:"kind"`
	Samples int                   `json:"samples"`

	ObservedNanos int64 `json:"observedNanos"`
	PerStageNanos int64 `json:"perStageNanos"`

	RemainingStages   int `json:"remainingStages"`
	ConditionalStages int `json:"conditionalStages"`

	RemainingNanos            int64 `json:"remainingNanos"`
	ConditionalRemainingNanos int64 `json:"conditionalRemainingNanos"`

	Projected bool   `json:"projected"`
	Note      string `json:"note,omitempty"`
}

// scheduleCostProjectionWire is what the measured stages say about cost.
//
// PSNR is derived here rather than in the client. The conversion is
// fit.PSNR's, one implementation of it already serves every other metric this
// server sends, and a second one written in TypeScript could only drift from
// it. It follows serializablePSNR's contract exactly: a nil pointer with the
// Infinite flag set is a perfect fit, and a nil pointer without it is a cost
// PSNR says nothing about.
type scheduleCostProjectionWire struct {
	Projected bool `json:"projected"`
	Samples   int  `json:"samples"`

	Gain         float64 `json:"gain"`
	AddedCircles int     `json:"addedCircles"`
	ElapsedNanos int64   `json:"elapsedNanos"`

	GainPerCircle float64 `json:"gainPerCircle"`
	GainPerHour   float64 `json:"gainPerHour"`

	// RecentCircles and RecentElapsedNanos are the denominators the two trailing
	// rates were divided by, so a client can say what a rate was measured over
	// without labelling it with the whole campaign's span.
	RecentGainPerCircle float64 `json:"recentGainPerCircle"`
	RecentGainPerHour   float64 `json:"recentGainPerHour"`
	RecentLegs          int     `json:"recentLegs"`
	RecentCircles       int     `json:"recentCircles"`
	RecentElapsedNanos  int64   `json:"recentElapsedNanos"`

	LatestCircles int     `json:"latestCircles"`
	LatestCost    float64 `json:"latestCost"`

	RemainingCircles    int      `json:"remainingCircles"`
	CostAtPlanEnd       float64  `json:"costAtPlanEnd"`
	PlanEndPSNR         *float64 `json:"planEndPsnr,omitempty"`
	PlanEndPSNRInfinite bool     `json:"planEndPsnrInfinite"`
	HasCircleCeiling    bool     `json:"hasCircleCeiling"`

	RemainingElapsedNanos int64    `json:"remainingElapsedNanos"`
	CostAtFinish          float64  `json:"costAtFinish"`
	FinishPSNR            *float64 `json:"finishPsnr,omitempty"`
	FinishPSNRInfinite    bool     `json:"finishPsnrInfinite"`
	HasTimeBudget         bool     `json:"hasTimeBudget"`

	Note string `json:"note,omitempty"`
}

// projectScheduleWire shapes a projection for the wire.
func projectScheduleWire(projection app.ScheduleProjection) *scheduleProjectionWire {
	wire := &scheduleProjectionWire{
		AsOf:            projection.AsOf,
		RemainingStages: projection.RemainingStages,
		Complete:        projection.Complete,
		RemainingNanos:  projection.Remaining.Nanoseconds(),
		FirmNanos:       projection.Firm.Nanoseconds(),
		Kinds:           make([]scheduleKindProjectionWire, 0, len(projection.Kinds)),
		Cost:            costProjectionWire(projection.Cost),
	}

	if projection.Complete {
		finishBy, earliest := projection.FinishBy, projection.EarliestFinish
		wire.FinishBy, wire.EarliestFinish = &finishBy, &earliest
	}

	for _, kind := range projection.Kinds {
		wire.Kinds = append(wire.Kinds, scheduleKindProjectionWire{
			Kind:                      kind.Kind,
			Samples:                   kind.Samples,
			ObservedNanos:             kind.Observed.Nanoseconds(),
			PerStageNanos:             kind.PerStage.Nanoseconds(),
			RemainingStages:           kind.RemainingStages,
			ConditionalStages:         kind.ConditionalStages,
			RemainingNanos:            kind.Remaining.Nanoseconds(),
			ConditionalRemainingNanos: kind.ConditionalRemaining.Nanoseconds(),
			Projected:                 kind.Projected(),
			Note:                      kind.Note,
		})
	}

	return wire
}

func costProjectionWire(cost app.ScheduleCostProjection) scheduleCostProjectionWire {
	wire := scheduleCostProjectionWire{
		Projected:             cost.Projected(),
		Samples:               cost.Samples,
		Gain:                  cost.Gain,
		AddedCircles:          cost.AddedCircles,
		ElapsedNanos:          cost.Elapsed.Nanoseconds(),
		GainPerCircle:         cost.GainPerCircle,
		GainPerHour:           cost.GainPerHour,
		RecentGainPerCircle:   cost.RecentGainPerCircle,
		RecentGainPerHour:     cost.RecentGainPerHour,
		RecentLegs:            cost.RecentLegs,
		RecentCircles:         cost.RecentCircles,
		RecentElapsedNanos:    cost.RecentElapsed.Nanoseconds(),
		LatestCircles:         cost.LatestCircles,
		LatestCost:            cost.LatestCost,
		RemainingCircles:      cost.RemainingCircles,
		CostAtPlanEnd:         cost.CostAtPlanEnd,
		HasCircleCeiling:      cost.HasCircleCeiling,
		RemainingElapsedNanos: cost.RemainingElapsed.Nanoseconds(),
		CostAtFinish:          cost.CostAtFinish,
		HasTimeBudget:         cost.HasTimeBudget,
		Note:                  cost.Note,
	}
	// A projected cost that is not there has no PSNR either: converting the
	// zero the field carries would report a perfect fit for a campaign nobody
	// could estimate.
	if cost.HasCircleCeiling {
		wire.PlanEndPSNR, wire.PlanEndPSNRInfinite = serializablePSNR(cost.CostAtPlanEnd)
	}

	if cost.HasTimeBudget {
		wire.FinishPSNR, wire.FinishPSNRInfinite = serializablePSNR(cost.CostAtFinish)
	}

	return wire
}

// scheduleStageTimings projects the stage records onto the values both
// projections read.
//
// It is the timing counterpart of scheduleStageOutcomes and applies that
// function's rule for a measured cost, for the same reason: only a completed
// stage produced a cost, and only a finite one is a number, so a completed
// stage's zero is a perfect fit while every other zero is an absence.
func scheduleStageTimings(stages []store.ScheduleStageRecord) []app.ScheduleStageTiming {
	timings := make([]app.ScheduleStageTiming, 0, len(stages))

	for i := range stages {
		stage := &stages[i]

		state := app.ScheduleOutcomePending

		switch stage.State {
		case store.ScheduleStateCompleted:
			state = app.ScheduleOutcomeCompleted
		case store.ScheduleStateSkipped:
			state = app.ScheduleOutcomeSkipped
		}

		timing := app.ScheduleStageTiming{
			Index: stage.Index,
			Kind:  stage.Kind,
			State: state,
			// The projection divides a cost gain by the circles that bought it,
			// so it needs the count the stage really built: a stage that
			// stopped at its refill limit spent fewer circles than it planned
			// and would otherwise be charged for ones that never existed.
			Circles:  stage.MaterializedCircles(),
			BestCost: stage.BestCost,
			CostMeasured: state == app.ScheduleOutcomeCompleted &&
				!math.IsNaN(stage.BestCost) && !math.IsInf(stage.BestCost, 0),
		}
		if stage.StartedAt != nil && stage.CompletedAt != nil {
			timing.Elapsed = stage.CompletedAt.Sub(*stage.StartedAt)
		}

		timings = append(timings, timing)
	}

	return timings
}

// projectSchedule estimates what a campaign has left, and reports whether
// there was anything to estimate.
//
// Both surfaces that show a projection — this file's detail response and the
// campaign page — go through here, so they cannot disagree about whether a
// campaign has one. The rules are the CLI's, in printScheduleProjection. A
// campaign that will not advance gets nothing, because the estimate anchors at
// asOf and that is a claim about the clock which a completed, failed or
// cancelled campaign makes false. A stored document that no longer expands has
// no plan to project against, and that is the stage table's problem to report
// rather than the projection's. A plan whose every stage has completed or been
// skipped has a measurement, not an estimate.
func projectSchedule(
	record *store.ScheduleRecord, stages []store.ScheduleStageRecord, asOf time.Time,
) (app.ScheduleProjection, bool) {
	switch record.State {
	case store.ScheduleStateCompleted, store.ScheduleStateFailed, store.ScheduleStateCancelled:
		return app.ScheduleProjection{}, false
	}

	plan, err := record.Document.Expand()
	if err != nil {
		return app.ScheduleProjection{}, false
	}

	projection := app.ProjectScheduleFinish(plan, scheduleStageTimings(stages), asOf)
	if projection.RemainingStages == 0 {
		return app.ScheduleProjection{}, false
	}

	return projection, true
}

// scheduleStageSummary is one realized stage without its configuration.
//
// Every field here is either printed by the stage table or read by the finish
// projection; nothing else is included, because everything else is what made
// the listing unreadable. Elapsed is sent as nanoseconds rather than as the two
// timestamps it is derived from: it is shorter, and it is exact, so a
// projection computed from the summary equals the one computed from the
// records it summarizes. A stage that has not measured a wall clock leaves it
// unset, which is not the same as a stage that measured nothing.
//
// The seed is deliberately not here: every stage of a campaign inherits the one
// campaign seed, which the summary above already carries, so a per-stage copy
// would be the same number 4096 times.
type scheduleStageSummary struct {
	Index   int                   `json:"index"`
	Kind    app.ScheduleStageKind `json:"kind"`
	State   store.ScheduleState   `json:"state"`
	Circles int                   `json:"circles"`

	// ActualCircles is what a settled stage really built, absent until it has
	// settled and on every record written before the field existed. Circles
	// stays the planned count the stage table reports; this is what the cost
	// projection divides by, and it is sent so the CLI projects from the same
	// figure the server does.
	ActualCircles int `json:"actualCircles,omitempty"`

	BestCost     float64 `json:"bestCost,omitempty"`
	ElapsedNanos *int64  `json:"elapsedNanos,omitempty"`

	JobID string `json:"jobId,omitempty"`

	Error  string `json:"error,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// summarizeScheduleStages projects the recorded stages for the listing.
func summarizeScheduleStages(stages []store.ScheduleStageRecord) []scheduleStageSummary {
	summaries := make([]scheduleStageSummary, 0, len(stages))
	for i := range stages {
		summaries = append(summaries, summarizeScheduleStage(&stages[i]))
	}

	return summaries
}

func summarizeScheduleStage(stage *store.ScheduleStageRecord) scheduleStageSummary {
	summary := scheduleStageSummary{
		Index:         stage.Index,
		Kind:          stage.Kind,
		State:         stage.State,
		Circles:       stage.Circles,
		ActualCircles: stage.ActualCircles,
		BestCost:      stage.BestCost,
		JobID:         stage.JobID,
		Error:         stage.Error,
		Reason:        stage.Reason,
	}
	if stage.StartedAt != nil && stage.CompletedAt != nil {
		elapsed := stage.CompletedAt.Sub(*stage.StartedAt).Nanoseconds()
		summary.ElapsedNanos = &elapsed
	}

	return summary
}

func summarizeSchedule(record *store.ScheduleRecord) scheduleSummary {
	total := 0
	if plan, err := record.Document.Expand(); err == nil {
		total = len(plan)
	}

	return scheduleSummary{
		ScheduleID:   record.ScheduleID,
		Name:         record.Document.Name,
		State:        record.State,
		CampaignSeed: record.CampaignSeed,
		TotalStages:  total,
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
		Error:        record.Error,
		Warnings:     scheduleWarnings(record.Document),
	}
}

// scheduleWarnings renders a document's advisories for the wire.
//
// They are recomputed on every read and never persisted. An advisory is a pure
// function of the stored document, so a stored copy could only ever disagree
// with it — with the document, once the advice is revised, and with older
// records, which would need a migration to gain a field that was already
// derivable. Recomputing costs one plan expansion, which summarizeSchedule
// already pays for TotalStages, so nothing about the store changes for this.
func scheduleWarnings(document app.ScheduleDocument) []string {
	advisories := document.Advisories()
	if len(advisories) == 0 {
		return nil
	}

	warnings := make([]string, 0, len(advisories))
	for _, advisory := range advisories {
		warnings = append(warnings, advisory.Message)
	}

	return warnings
}

// handleSchedules handles /api/v1/schedules.
func (s *Server) handleSchedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.handleCreateSchedule(w, r)
	case http.MethodGet:
		s.handleListSchedules(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

// handleSchedulesWithID handles /api/v1/schedules/:id/*.
func (s *Server) handleSchedulesWithID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/schedules/")

	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_schedule_id", "schedule ID required")
		return
	}

	scheduleID := parts[0]

	parsed, err := uuid.Parse(scheduleID)
	if err != nil || parsed == uuid.Nil || parsed.String() != scheduleID {
		writeAPIError(w, http.StatusBadRequest, "invalid_schedule_id", "schedule ID must be a canonical UUID")
		return
	}

	switch {
	case len(parts) == 1:
		s.handleGetSchedule(w, r, scheduleID)
	case len(parts) == 2 && (parts[1] == "cancel" || parts[1] == "pause" || parts[1] == "resume"):
		s.handleScheduleAction(w, r, scheduleID, parts[1])
	case len(parts) == 3 && parts[1] == "stages" && parts[2] != "":
		s.handleGetScheduleStage(w, r, scheduleID, parts[2])
	default:
		writeAPIError(w, http.StatusNotFound, "not_found", "resource not found")
	}
}

// handleCreateSchedule handles POST /api/v1/schedules. The body is the schedule
// document itself, which is the format ParseSchedule validates and the store
// persists, so a campaign is written exactly as it was authored.
//
// A created schedule starts running, in the same way a created job does.
func (s *Server) handleCreateSchedule(w http.ResponseWriter, r *http.Request) {
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "schedules_unavailable", err.Error())
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, app.MaxRequestBody)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds the size limit")
			return
		}

		writeAPIError(w, http.StatusBadRequest, "invalid_request", "unable to read request body")

		return
	}

	document, err := app.ParseSchedule(body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_schedule", err.Error())
		return
	}
	// Refuse an unreachable reference now rather than as a stage failure hours
	// in. The path itself stays as authored: it is resolved again when each
	// stage runs, so a schedule does not bake in this process's view of disk.
	probe := document.Base

	failure := s.resolveConfigPaths(&probe, "schedule")
	if failure != nil {
		writeContinuationError(w, failure)
		return
	}

	// Building the record pins the campaign seed, so a zero seed is resolved once
	// here and never re-derived per stage.
	record, err := store.NewScheduleRecord(uuid.New().String(), *document)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_schedule", err.Error())
		return
	}

	// The advisories are logged once, here, and not on every read: they are a
	// property of the authored document, so a campaign that runs for a day
	// would otherwise repeat the same note for every poll. The resume guard's
	// version warning reaches the log the same way.
	for _, advisory := range record.Document.Advisories() {
		slog.Warn("Schedule advisory", "schedule_id", record.ScheduleID,
			"field", advisory.Field, "stages", advisory.Stages, "warning", advisory.Message)
	}

	record.State = store.ScheduleStateRunning
	if err := scheduleStore.SaveSchedule(record); err != nil {
		slog.Error("Failed to persist schedule", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to persist the schedule")

		return
	}

	s.publishScheduleChanged(record.ScheduleID)

	if err := s.startScheduleDriver(record.ScheduleID); err != nil {
		slog.Error("Failed to start schedule executor", "schedule_id", record.ScheduleID, "error", err)
		writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to start the schedule")

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(summarizeSchedule(record))
}

// handleListSchedules handles GET /api/v1/schedules.
func (s *Server) handleListSchedules(w http.ResponseWriter, _ *http.Request) {
	scheduleStore, err := s.scheduleStore()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "schedules_unavailable", err.Error())
		return
	}

	records, err := scheduleStore.ListSchedules()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to list schedules")
		return
	}

	summaries := make([]scheduleSummary, 0, len(records))
	for i := range records {
		summaries = append(summaries, summarizeSchedule(&records[i]))
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(summaries)
}

// handleGetSchedule handles GET /api/v1/schedules/:id.
func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request, scheduleID string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	scheduleStore, err := s.scheduleStore()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "schedules_unavailable", err.Error())
		return
	}

	record, err := scheduleStore.LoadSchedule(scheduleID)
	if err != nil {
		writeScheduleLoadError(w, err)
		return
	}

	stages, err := scheduleStore.LoadScheduleStages(scheduleID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to load schedule stages")
		return
	}

	detail := scheduleDetail{
		scheduleSummary: summarizeSchedule(record),
		Document:        record.Document,
		Stages:          summarizeScheduleStages(stages),
	}
	// The anchor is this instant, which is the same one the CLI passes when it
	// projects the identical stages locally, so the two answers differ only by
	// the round trip between them.
	if projection, ok := projectSchedule(record, stages, time.Now().UTC()); ok {
		detail.Projection = projectScheduleWire(projection)
	}

	w.Header().Set("Content-Type", "application/json")
	// This is the one schedule response that carries floats: the projection's
	// cost rates. Encoding them can fail where the sibling handlers' integer
	// and string payloads cannot, so the error is reported rather than dropped.
	err = json.NewEncoder(w).Encode(detail)
	if err != nil {
		slog.Error("Failed to encode schedule detail response",
			"schedule_id", scheduleID, "error", err)
	}
}

// handleGetScheduleStage handles GET /api/v1/schedules/:id/stages/:index and
// answers with the whole stage record, configuration included.
//
// This is the endpoint that keeps the listing small: replaying one stage is the
// reason the configuration is recorded, and that is a question about one stage.
func (s *Server) handleGetScheduleStage(w http.ResponseWriter, r *http.Request, scheduleID, rawIndex string) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	index, err := strconv.Atoi(rawIndex)
	if err != nil || index < 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_stage_index", "stage index must be a non-negative integer")
		return
	}

	scheduleStore, err := s.scheduleStore()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "schedules_unavailable", err.Error())
		return
	}
	// The schedule is loaded first so an unknown campaign answers as a missing
	// schedule rather than as a missing stage.
	if _, err := scheduleStore.LoadSchedule(scheduleID); err != nil {
		writeScheduleLoadError(w, err)
		return
	}

	stage, err := scheduleStore.LoadScheduleStage(scheduleID, index)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeAPIError(w, http.StatusNotFound, "not_found", "no stage recorded at that index")
			return
		}

		writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to load the schedule stage")

		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(stage)
}

// handleScheduleAction handles the cancel, pause, and resume verbs. All three
// act on the campaign as a whole.
//
// Pause takes effect at the next stage boundary: a stage is the atomic unit of
// a campaign, and interrupting one would throw away its work without recording
// anything a later resume could use. Cancel does interrupt, because a cancelled
// campaign is not going to want that work.
func (s *Server) handleScheduleAction(w http.ResponseWriter, r *http.Request, scheduleID, action string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")

		return
	}

	scheduleStore, err := s.scheduleStore()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "schedules_unavailable", err.Error())
		return
	}

	record, err := scheduleStore.LoadSchedule(scheduleID)
	if err != nil {
		writeScheduleLoadError(w, err)
		return
	}

	next, failure := scheduleTransition(record.State, action)
	if failure != nil {
		writeContinuationError(w, failure)
		return
	}

	record.State = next
	if next == store.ScheduleStateRunning {
		record.Error = ""
		// Releasing the barrier is part of the resume, not a later decision: a
		// resume that left the marker alone would restart the driver straight
		// into the same barrier and pause again, which reads as a broken
		// resume rather than as a campaign holding its ground.
		if released, err := releasedBarrierStage(scheduleStore, record); err == nil {
			record.ReleasedThroughStage = max(record.ReleasedThroughStage, released)
		} else {
			slog.Warn("Unable to determine which stage a resume releases",
				"schedule_id", scheduleID, "error", err)
		}
	}

	if err := scheduleStore.SaveSchedule(record); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to update the schedule")
		return
	}

	s.publishScheduleChanged(scheduleID)

	switch action {
	case "cancel":
		// The state is durable before the stage is touched, so a crash in
		// between leaves a cancelled schedule that starts no executor rather
		// than a running one whose stage was killed behind its back.
		s.cancelScheduleStage(scheduleID)
	case "resume":
		err := s.startScheduleDriver(scheduleID)
		if err != nil && !errors.Is(err, errScheduleDriverRunning) {
			writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to start the schedule")
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(summarizeSchedule(record))
}

// releasedBarrierStage reports the stage a resume is about to start, which is
// the barrier it releases. It re-derives the cursor the same way the driver
// does — from the records alone — so the handler and the executor cannot
// disagree about where a campaign stands.
func releasedBarrierStage(scheduleStore store.ScheduleStore, record *store.ScheduleRecord) (int, error) {
	plan, err := record.Document.Expand()
	if err != nil {
		return 0, fmt.Errorf("expand schedule: %w", err)
	}

	recorded, err := scheduleStore.LoadScheduleStages(record.ScheduleID)
	if err != nil {
		return 0, fmt.Errorf("load schedule stages: %w", err)
	}

	index, _, _ := nextScheduleStage(plan, recorded)
	if index < 0 {
		return 0, nil
	}

	return index, nil
}

// scheduleTransition reports the state an action moves a schedule to, or the
// answer for an action the current state does not allow.
func scheduleTransition(current store.ScheduleState, action string) (store.ScheduleState, *continuationError) {
	switch action {
	case "cancel":
		switch current {
		case store.ScheduleStatePending, store.ScheduleStateRunning, store.ScheduleStatePaused:
			return store.ScheduleStateCancelled, nil
		}

		return "", continuationFailure(http.StatusConflict, "invalid_state", "schedule is "+string(current))
	case "pause":
		if current == store.ScheduleStateRunning {
			return store.ScheduleStatePaused, nil
		}

		return "", continuationFailure(http.StatusConflict, "invalid_state", "only a running schedule can be paused")
	case "resume":
		switch current {
		case store.ScheduleStatePaused, store.ScheduleStatePending:
			return store.ScheduleStateRunning, nil
		}

		return "", continuationFailure(http.StatusConflict, "invalid_state", "only a paused schedule can be resumed")
	}

	return "", continuationFailure(http.StatusNotFound, "not_found", "resource not found")
}

func writeScheduleLoadError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeAPIError(w, http.StatusNotFound, "not_found", "schedule not found")
		return
	}

	writeAPIError(w, http.StatusInternalServerError, "schedule_error", "failed to load the schedule")
}
