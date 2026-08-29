package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
)

const (
	// ScheduleSchemaVersion is the schedule document format this build writes
	// and accepts. A document may omit it, which means "the current version";
	// any other value is refused rather than guessed at.
	ScheduleSchemaVersion = 1

	// MaxScheduleSteps bounds the authored step list. It is generous next to the
	// thirteen steps the 512-circle campaign needs, and exists only so a
	// hand-edited document cannot turn into an unbounded expansion.
	MaxScheduleSteps = 256

	// MaxScheduleStages bounds the realized stage list. One stage per circle
	// plus a polish between every pair is already far beyond any campaign that
	// finishes in a weekend.
	MaxScheduleStages = 4096

	// MaxScheduleNameLen bounds the campaign label. The name is free text with
	// no effect on execution, and it is echoed everywhere a campaign is listed
	// — in every summary, in the stage table's header, and in the detail
	// response twice over — so an unbounded one is a response size nobody
	// authored on purpose.
	MaxScheduleNameLen = 200

	// MaxScheduleDocumentBytes bounds the document itself, which the detail
	// response carries in full because the finish projection is computed from
	// the plan it expands to.
	//
	// It is what keeps that response readable: the stage listing costs about
	// 172 B per stage, so a campaign at MaxScheduleStages is roughly 706 kB, and
	// the document has to fit in what is left of MaxCLIResponseBytes. This bound
	// leaves around 200 kB of headroom while being some 500 B per step at
	// MaxScheduleSteps — two orders of magnitude more than the worked example
	// spends on one. A document that needs more is one written stanza by stanza
	// where `repeat` says the same thing.
	MaxScheduleDocumentBytes = 128 << 10

	// MaxScheduleRepeat bounds a single generator step.
	MaxScheduleRepeat = 1024
)

// ScheduleStepKind names the two continuations the server can already mint from
// a completed batch checkpoint.
type ScheduleStepKind string

const (
	ScheduleStepExtend ScheduleStepKind = "extend"
	ScheduleStepPolish ScheduleStepKind = "polish"
)

// ScheduleStageKind labels a realized stage. The base stage is the run that
// creates the first checkpoint; every later stage continues from its
// predecessor.
type ScheduleStageKind string

const (
	ScheduleStageBase   ScheduleStageKind = "base"
	ScheduleStageExtend ScheduleStageKind = "extend"
	ScheduleStagePolish ScheduleStageKind = "polish"
)

// ScheduleDocument is a whole campaign stated once: the base run, then an
// ordered list of continuations. It is the authored form, not the realized one
// — Expand turns it into the stage list a run actually follows.
//
// The document is deliberately JSON. It is the format the extend and polish
// endpoints already speak, the format the store already writes, and it needs no
// new dependency.
//
//nolint:recvcheck // the value receiver on Validate is deliberate: it normalizes a copy.
type ScheduleDocument struct {
	// SchemaVersion is ScheduleSchemaVersion, or absent for the current version.
	SchemaVersion int `json:"schemaVersion,omitempty"`

	// Name is a human label carried through to the stage table. It has no
	// effect on execution.
	Name string `json:"name,omitempty"`

	// Seed is the campaign seed. Every stage inherits it, so a campaign is one
	// seed rather than one seed per stage. Zero means "resolve one", and the
	// resolved value is recorded on each realized stage so a single stage can be
	// replayed on its own.
	Seed int64 `json:"seed,omitempty"`

	// Base is the configuration of the first run. Steps continue from its
	// checkpoint, and the extend and polish paths both require a batch
	// checkpoint, so a document with steps must run its base in batch mode.
	Base JobConfig `json:"base"`

	// Steps are applied in order. Each one may carry a repeat count, which is
	// what keeps a 64-stage campaign from being 64 stanzas.
	Steps []ScheduleStep `json:"steps,omitempty"`
}

// ScheduleStep is one authored continuation, optionally repeated. Its override
// fields mirror the extend and polish request bodies field for field, so a step
// is readable by anyone who has driven those endpoints by hand. Each override
// is a pointer for the same reason the request types use pointers: an omitted
// override and an explicit zero must not look alike.
type ScheduleStep struct {
	Type ScheduleStepKind `json:"type"`

	// Repeat is the generator form. Absent means one stage; `repeat: 63` with
	// `additionalCircles: 8` is a 63-stage climb stated in one stanza.
	Repeat int `json:"repeat,omitempty"`

	// AdditionalCircles is the extend-only append width. It is required on an
	// extend step and refused on a polish step.
	AdditionalCircles int `json:"additionalCircles,omitempty"`

	// BatchSize overrides the extend-only batch width, which otherwise follows
	// the appended-circle count exactly as the extend endpoint sets it.
	BatchSize *int `json:"batchSize,omitempty"`

	// Polish-only overrides.
	Strategy        *PolishingStrategy `json:"strategy,omitempty"`
	ActiveSetSize   *int               `json:"activeSetSize,omitempty"`
	MaxSweeps       *int               `json:"maxSweeps,omitempty"`
	StagnationIters *int               `json:"stagnationIters,omitempty"`
	MinImprovement  *float64           `json:"minImprovement,omitempty"`

	// Budget overrides valid on either kind. On a polish step Epochs and Iters
	// address the polishing budget, matching the polish endpoint.
	Epochs  *int `json:"epochs,omitempty"`
	Iters   *int `json:"iters,omitempty"`
	PopSize *int `json:"popSize,omitempty"`

	// When is the optional runtime condition. It does not affect expansion —
	// the stage is planned either way — it decides whether the planned stage is
	// started once the stages before it have measured something. See
	// ScheduleCondition.
	When *ScheduleCondition `json:"when,omitempty"`

	// PauseBefore turns this step into a barrier. The campaign runs everything
	// before it and then pauses, leaving the step and everything after it
	// planned but unstarted; `schedule resume` releases the barrier and carries
	// on. It is how a document says "go this far for now" without the plan
	// having to be edited down and edited back.
	//
	// It is deliberately not a `when` condition. A condition decides whether a
	// stage runs at all and is refused on extend because skipping one would
	// move every later stage's circle count; a barrier skips nothing, it only
	// stops, so it is legal on either kind.
	//
	// On a repeated step the barrier belongs to the first repetition only.
	// `repeat: 4` with a barrier means "stop before the first of the four", not
	// "stop before each of them", which is what makes it readable as a single
	// point in the plan.
	PauseBefore bool `json:"pauseBefore,omitempty"`
}

// ScheduleStage is one realized stage: what the step became once repeats were
// unrolled and overrides applied. Config is the fully normalized configuration
// the stage runs with, which is what makes a stage replayable in isolation and
// what a dry run prints.
//
// Runtime-derived values are deliberately absent. ResumeCount and the parent
// job ID come from the checkpoint the stage actually continues from, so a plan
// cannot claim them before the predecessor has run.
type ScheduleStage struct {
	Index      int               `json:"index"`
	Kind       ScheduleStageKind `json:"kind"`
	StepIndex  int               `json:"stepIndex"`
	Repetition int               `json:"repetition"`

	// Circles is the circle count the canvas holds once this stage completes.
	Circles int `json:"circles"`

	// AdditionalCircles is how many circles this stage appends, zero for base
	// and polish stages.
	AdditionalCircles int `json:"additionalCircles,omitempty"`

	// When is the condition the stage is subject to, carried through from its
	// step so a plan states on its face which stages are conditional and on
	// what. A dry run prints it; the executor evaluates it against the recorded
	// outcomes when the stage comes up.
	When *ScheduleCondition `json:"when,omitempty"`

	// PauseBefore makes this stage a barrier: the campaign runs up to it and
	// pauses rather than starting it. The stage stays planned and the campaign
	// stays resumable, which is the difference between a barrier and deleting
	// the step. See ScheduleStep.PauseBefore.
	PauseBefore bool `json:"pauseBefore,omitempty"`

	Config JobConfig `json:"config"`
}

// scheduleEffectiveFields maps a configuration field that ApplyDefaults puts
// back onto the field that actually expresses the caller's intent. These are
// the traps that cost hours in the hand-driven runs: setting the first field to
// false looks accepted and does nothing.
var scheduleEffectiveFields = map[string]string{
	"convergenceEnabled": "disableConvergence",
	"enableTrace":        "disableTrace",
}

// scheduleRefusedBaseFields names base fields a document may never write
// because they are derived while a job runs, not authored. A nonzero
// effectiveSeed survives ApplyDefaults untouched, so accepting it would let a
// document declare one campaign seed and run every stage with another.
var scheduleRefusedBaseFields = map[string]string{
	"effectiveSeed": "is derived from the campaign seed; use seed instead",
	"resumeCount":   "is recorded by the run that resumes, not by the document",
}

// ParseSchedule decodes and fully validates a schedule document.
//
// Validation is strict in two senses. Unknown fields are refused, at every
// level, exactly as the HTTP request types refuse them. And a field the caller
// wrote that ApplyDefaults would silently replace is an error naming the field
// that works, rather than a value quietly dropped on the floor.
func ParseSchedule(data []byte) (*ScheduleDocument, error) {
	if len(data) > MaxScheduleDocumentBytes {
		return nil, invalid("schedule", fmt.Sprintf(
			"is %d bytes, over the %d a document may be; use repeat instead of writing a stanza per stage",
			len(data), MaxScheduleDocumentBytes))
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var doc ScheduleDocument

	err := decoder.Decode(&doc)
	if err != nil {
		return nil, fmt.Errorf("decode schedule: %w", err)
	}

	err = decoder.Decode(&struct{}{})
	if !errors.Is(err, io.EOF) {
		return nil, invalid("schedule", "must contain exactly one JSON object")
	}

	present, err := presentBaseFields(data)
	if err != nil {
		return nil, err
	}

	err = doc.validate(present)
	if err != nil {
		return nil, err
	}
	// The campaign seed is pinned here rather than at expansion time: a document
	// that omitted a seed must still expand the same way on every later call and
	// after a restart.
	err = doc.ResolveSeed()
	if err != nil {
		return nil, err
	}

	return &doc, nil
}

// presentBaseFields reports which base keys the document actually wrote. The
// typed decode cannot answer that: an omitted bool and an explicit false are
// the same zero value once decoded, which is precisely how
// `convergenceEnabled: false` used to disappear.
func presentBaseFields(data []byte) (map[string]json.RawMessage, error) {
	var raw struct {
		Base map[string]json.RawMessage `json:"base"`
	}

	err := json.Unmarshal(data, &raw)
	if err != nil {
		return nil, fmt.Errorf("decode schedule base: %w", err)
	}

	if raw.Base == nil {
		return map[string]json.RawMessage{}, nil
	}

	return raw.Base, nil
}

// validate is the parse-time check. It adds what only the raw document can
// answer — which base keys were actually written — on top of the structural
// validation any document must pass.
func (d *ScheduleDocument) validate(presentBase map[string]json.RawMessage) error {
	if d.SchemaVersion != 0 && d.SchemaVersion != ScheduleSchemaVersion {
		return invalid("schemaVersion", fmt.Sprintf("must be %d", ScheduleSchemaVersion))
	}

	d.SchemaVersion = ScheduleSchemaVersion

	for name, reason := range scheduleRefusedBaseFields {
		if _, written := presentBase[name]; written {
			return invalid("base."+name, reason)
		}
	}

	err := d.reconcileSeed()
	if err != nil {
		return err
	}

	err = validateNoDefaultOverrides("base", presentBase, d.Base)
	if err != nil {
		return err
	}

	return d.Validate()
}

// Validate checks a document that did not come from ParseSchedule — one
// reconstructed in code or read back from the store. It applies the same
// version, base and step checks the parser applies; only the "which keys were
// written" checks, which need the raw JSON, are parse-time only.
//
// The receiver is a value so the checks, which normalize a copy, cannot mutate
// the caller's document.
func (d ScheduleDocument) Validate() error {
	if d.SchemaVersion != 0 && d.SchemaVersion != ScheduleSchemaVersion {
		return invalid("schemaVersion", fmt.Sprintf("must be %d", ScheduleSchemaVersion))
	}

	if len(d.Name) > MaxScheduleNameLen {
		return invalid("name", fmt.Sprintf("must be at most %d characters", MaxScheduleNameLen))
	}

	if len(d.Steps) > MaxScheduleSteps {
		return invalid("steps", fmt.Sprintf("must contain at most %d steps", MaxScheduleSteps))
	}

	err := d.reconcileSeed()
	if err != nil {
		return err
	}

	if d.Base.PolishingOnly {
		return invalid("base.polishingOnly", "cannot be set on the base stage, which has no earlier parameters to polish; add a polish step instead")
	}

	base := d.Base

	err = base.ApplyDefaults()
	if err != nil {
		return err
	}

	err = base.Validate()
	if err != nil {
		return fmt.Errorf("base: %w", err)
	}

	if len(d.Steps) > 0 && base.Mode != ModeBatch {
		return invalid("base.mode", "must be batch for a schedule with steps, because extend and polish continue a batch checkpoint")
	}

	for i, step := range d.Steps {
		err := step.validate(i)
		if err != nil {
			return err
		}
	}
	// Expanding is the only honest way to know a document is runnable: the
	// per-stage configurations are what the optimizer sees, and only they can
	// report an extension that walks past the circle limit.
	_, err = d.Expand()

	return err
}

// ResolveSeed pins the campaign seed, generating one when the document omitted
// it. A schedule is persisted with the resolved value, so expanding it again —
// in another process, after a restart — produces the same stage seeds instead
// of a fresh random campaign.
func (d *ScheduleDocument) ResolveSeed() error {
	if d.Seed == 0 {
		base := d.Base
		base.Seed = 0

		base.EffectiveSeed = 0

		err := base.ApplyDefaults()
		if err != nil {
			return err
		}

		d.Seed = base.EffectiveSeed
	}

	d.Base.Seed = d.Seed

	return nil
}

// reconcileSeed collapses the campaign seed and the base seed into one value.
// Two seeds that disagree are a mistake worth naming, not a precedence rule
// worth memorizing.
func (d *ScheduleDocument) reconcileSeed() error {
	switch {
	case d.Seed != 0 && d.Base.Seed != 0 && d.Seed != d.Base.Seed:
		return invalid("seed", fmt.Sprintf("disagrees with base.seed (%d); a campaign has one seed", d.Base.Seed))
	case d.Seed == 0:
		d.Seed = d.Base.Seed
	}

	d.Base.Seed = d.Seed

	return nil
}

func (s ScheduleStep) validate(index int) error {
	field := func(name string) string { return fmt.Sprintf("steps[%d].%s", index, name) }

	switch s.Type {
	case ScheduleStepExtend, ScheduleStepPolish:
	default:
		return invalid(field("type"), "must be extend or polish")
	}

	if s.Repeat < 0 {
		return invalid(field("repeat"), "cannot be negative")
	}

	if s.Repeat > MaxScheduleRepeat {
		return invalid(field("repeat"), fmt.Sprintf("must be at most %d", MaxScheduleRepeat))
	}

	err := s.When.validate(field)
	if err != nil {
		return err
	}

	if s.Type == ScheduleStepExtend {
		if s.When != nil {
			return invalid(field("when"), "is valid on a polish step only, because skipping an extend would move the circle count of every later stage")
		}

		if s.AdditionalCircles < 1 {
			return invalid(field("additionalCircles"), "must be positive on an extend step")
		}

		for name, set := range map[string]bool{
			"strategy":        s.Strategy != nil,
			"activeSetSize":   s.ActiveSetSize != nil,
			"maxSweeps":       s.MaxSweeps != nil,
			"stagnationIters": s.StagnationIters != nil,
			"minImprovement":  s.MinImprovement != nil,
		} {
			if set {
				return invalid(field(name), "is a polish override and cannot appear on an extend step")
			}
		}

		return nil
	}

	if s.AdditionalCircles != 0 {
		return invalid(field("additionalCircles"), "is an extend override and cannot appear on a polish step")
	}

	if s.BatchSize != nil {
		return invalid(field("batchSize"), "is an extend override and cannot appear on a polish step")
	}

	return nil
}

// Repetitions is the realized stage count for a step. An omitted repeat is one
// stage; the generator form is the same field with a larger number.
func (s ScheduleStep) Repetitions() int {
	if s.Repeat <= 0 {
		return 1
	}

	return s.Repeat
}

// Expand realizes the document into the ordered stage list a run follows. Every
// stage carries a fully normalized configuration, so the plan is exactly what
// the optimizer would be handed.
//
// Expansion is total and unconditional: each step contributes its repetitions
// in order. Conditional policy — polishing only at listed circle counts, or
// abandoning polish after barren stages — is deliberately not here, because it
// cannot be decided before the stages have run. Each stage instead carries the
// condition it is subject to, and EvaluateScheduleStage decides it against the
// recorded outcomes when the stage comes up. A plan therefore stays meaningful
// with no outcomes at all: a dry run prints every planned stage and says which
// ones are conditional and on what.
func (d ScheduleDocument) Expand() ([]ScheduleStage, error) {
	config := d.Base
	// One campaign seed, inherited by every stage. The effective seed is taken
	// from the campaign seed rather than from the base configuration, so a
	// document cannot declare one seed and run the stages with another; a
	// document whose seed is still unresolved gets one from ApplyDefaults.
	config.Seed = d.Seed

	config.EffectiveSeed = d.Seed

	err := config.ApplyDefaults()
	if err != nil {
		return nil, err
	}

	err = config.Validate()
	if err != nil {
		return nil, fmt.Errorf("base: %w", err)
	}

	stages := []ScheduleStage{{
		Index:     0,
		Kind:      ScheduleStageBase,
		StepIndex: -1,
		Circles:   config.Circles,
		Config:    config,
	}}

	circles := config.Circles

	for stepIndex, step := range d.Steps {
		for repetition := 1; repetition <= step.Repetitions(); repetition++ {
			if len(stages) >= MaxScheduleStages {
				return nil, invalid("steps", fmt.Sprintf("expands to more than %d stages", MaxScheduleStages))
			}

			stage, next, err := step.realize(config, circles, len(stages), stepIndex, repetition)
			if err != nil {
				return nil, err
			}

			stages = append(stages, stage)
			circles = next
		}
	}

	return stages, nil
}

// realize builds one stage from the running configuration. The two branches
// mirror handleExtendJob and handlePolishJob so a scheduled stage and a
// hand-issued request produce the same configuration.
func (s ScheduleStep) realize(config JobConfig, circles, index, stepIndex, repetition int) (ScheduleStage, int, error) {
	field := func(name string) string { return fmt.Sprintf("steps[%d].%s", stepIndex, name) }
	stage := ScheduleStage{
		Index:      index,
		StepIndex:  stepIndex,
		Repetition: repetition,
		When:       s.When,
		// A barrier is one point in the plan, so an unrolled repeat carries it
		// on its first stage only.
		PauseBefore: s.PauseBefore && repetition == 1,
	}
	next := circles
	staged := config
	staged.Circles = circles
	// A continuation is seeded from its parent checkpoint, never from the
	// document. Carrying the base's hand-authored arrangement forward would at
	// best be ignored and at worst fail validation, because an extend stage
	// raises the circle count while the spec list stays the length the base
	// wrote.
	staged.InitialCircles = nil

	switch s.Type {
	case ScheduleStepExtend:
		if s.AdditionalCircles > MaxCircles-circles {
			return ScheduleStage{}, 0, invalid(field("additionalCircles"),
				fmt.Sprintf("grows the canvas past the %d circle limit at stage %d", MaxCircles, index))
		}

		next = circles + s.AdditionalCircles
		stage.Kind = ScheduleStageExtend
		stage.AdditionalCircles = s.AdditionalCircles
		staged.Circles = next
		staged.BatchSize = min(s.AdditionalCircles, MaxBatchSize)
		staged.PolishingEnabled = false

		staged.PolishingOnly = false
		if s.BatchSize != nil {
			staged.BatchSize = *s.BatchSize
		}

		if s.Epochs != nil {
			staged.OptimizerEpochs = *s.Epochs
		}

		if s.Iters != nil {
			staged.Iters = *s.Iters
		}

		if s.PopSize != nil {
			staged.PopSize = *s.PopSize
		}
	case ScheduleStepPolish:
		stage.Kind = ScheduleStagePolish
		staged.PolishingEnabled = true

		staged.PolishingOnly = true
		if s.Strategy != nil {
			staged.PolishingStrategy = *s.Strategy
		}

		if s.ActiveSetSize != nil {
			staged.PolishingActiveSetSize = *s.ActiveSetSize
		}

		if s.MaxSweeps != nil {
			staged.PolishingMaxSweeps = *s.MaxSweeps
		}

		if s.StagnationIters != nil {
			staged.PolishingStagnationIters = *s.StagnationIters
		}

		if s.MinImprovement != nil {
			staged.PolishingMinImprovement = *s.MinImprovement
		}

		if s.Epochs != nil {
			staged.PolishingEpochs = *s.Epochs
		}

		if s.Iters != nil {
			staged.PolishingIters = *s.Iters
		}
		// A polish step's budget overrides address the polishing budget, which is
		// what the format documents, so popSize sets the polishing population
		// rather than the job-wide one the stage never uses.
		if s.PopSize != nil {
			staged.PolishingPopSize = *s.PopSize
		}
	}

	err := staged.Validate()
	if err != nil {
		return ScheduleStage{}, 0, fmt.Errorf("steps[%d] stage %d: %w", stepIndex, index, err)
	}

	stage.Circles = next
	stage.Config = staged

	return stage, next, nil
}

// validateNoDefaultOverrides refuses any written field that ApplyDefaults would
// replace. The comparison is made against the caller's own document rather than
// against a fixed list, so a future default cannot start swallowing a field
// without this check noticing.
//
// An empty prefix names the field on its own, which is what a request body
// carrying a configuration directly needs; a schedule passes "base".
func validateNoDefaultOverrides(prefix string, present map[string]json.RawMessage, config JobConfig) error {
	if len(present) == 0 {
		return nil
	}

	defaulted := config

	err := defaulted.ApplyDefaults()
	if err != nil {
		return err
	}

	written := reflect.ValueOf(config)
	applied := reflect.ValueOf(defaulted)

	names := make([]string, 0, len(present))
	for name := range present {
		names = append(names, name)
	}

	sort.Strings(names)

	for _, name := range names {
		field, ok := jobConfigFieldByJSONName(name)
		if !ok {
			// Unreachable through ParseSchedule: the strict decoder has already
			// refused any key without a field.
			continue
		}

		before := written.FieldByIndex(field.Index).Interface()

		after := applied.FieldByIndex(field.Index).Interface()
		if reflect.DeepEqual(before, after) {
			continue
		}

		return invalid(qualifiedField(prefix, name), defaultOverrideReason(name))
	}

	return nil
}

func qualifiedField(prefix, name string) string {
	if prefix == "" {
		return name
	}

	return prefix + "." + name
}

func defaultOverrideReason(name string) string {
	if effective, ok := scheduleEffectiveFields[name]; ok {
		return fmt.Sprintf("is silently replaced by the configuration defaults; use %s instead", effective)
	}

	return "is silently replaced by the configuration defaults; omit it or give it a value the defaults keep"
}

var jobConfigJSONFields = buildJobConfigJSONFields()

func buildJobConfigJSONFields() map[string]reflect.StructField {
	configType := reflect.TypeFor[JobConfig]()

	fields := make(map[string]reflect.StructField, configType.NumField())
	for i := 0; i < configType.NumField(); i++ {
		field := configType.Field(i)

		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			name = field.Name
		}

		fields[name] = field
	}

	return fields
}

func jobConfigFieldByJSONName(name string) (reflect.StructField, bool) {
	field, ok := jobConfigJSONFields[name]
	return field, ok
}
