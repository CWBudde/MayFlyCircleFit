package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/cwbudde/mayflycirclefit/internal/app"
	"github.com/cwbudde/mayflycirclefit/internal/fit"
	"github.com/cwbudde/mayflycirclefit/internal/store"
	"github.com/spf13/cobra"
)

// The schedule command is a thin client over the schedule HTTP surface. It
// deliberately runs nothing itself: a campaign belongs to the server, which
// owns the worker pool and survives the client hanging up, and a second
// executor in the CLI is exactly the drift this phase set out to remove.

var (
	scheduleServerURL = "http://localhost:8080"
	scheduleDryRun    bool
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Create and inspect multi-stage campaigns",
	Long: `Drives the server's schedule endpoints.

A schedule is a whole campaign — a base run and an ordered list of extend and
polish stages — stated once and executed by the server as a single run.`,
}

var scheduleCreateCmd = &cobra.Command{
	Use:   "create <document.json>",
	Short: "Submit a schedule document and start the campaign",
	Long: `Validates a schedule document and submits it to the server.

With --dry-run the document is expanded and printed — every realized stage, its
parameters, and the nominal optimizer iteration count — and nothing is
submitted. Expansion needs no runtime state, so a dry run creates no schedule,
no stage record, and no job.`,
	Args: cobra.ExactArgs(1),
	RunE: runScheduleCreate,
}

var scheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the campaigns on the server",
	Args:  cobra.NoArgs,
	RunE:  runScheduleList,
}

var scheduleStatusCmd = &cobra.Command{
	Use:   "status <schedule-id>",
	Short: "Show a campaign's stage table",
	Args:  cobra.ExactArgs(1),
	RunE:  runScheduleStatus,
}

var scheduleCancelCmd = &cobra.Command{
	Use:   "cancel <schedule-id>",
	Short: "Cancel a campaign and its in-flight stage",
	Args:  cobra.ExactArgs(1),
	RunE:  scheduleActionRunner("cancel"),
}

var schedulePauseCmd = &cobra.Command{
	Use:   "pause <schedule-id>",
	Short: "Pause a campaign at the next stage boundary",
	Args:  cobra.ExactArgs(1),
	RunE:  scheduleActionRunner("pause"),
}

var scheduleResumeCmd = &cobra.Command{
	Use:   "resume <schedule-id>",
	Short: "Resume a paused campaign",
	Args:  cobra.ExactArgs(1),
	RunE:  scheduleActionRunner("resume"),
}

var scheduleImportCmd = &cobra.Command{
	Use:   "import <job-id>",
	Short: "Read a hand-driven extend/polish chain back as one campaign",
	Long: `Reconstructs a campaign from the checkpoint lineage of a job.

The extend and polish endpoints record the job they continued on the resulting
checkpoint, so a chain driven by hand before schedules existed is still
readable as one run. Pass the identifier of the chain's last job.`,
	Args: cobra.ExactArgs(1),
	RunE: runScheduleImport,
}

func init() {
	for _, command := range []*cobra.Command{
		scheduleCreateCmd, scheduleListCmd, scheduleStatusCmd,
		scheduleCancelCmd, schedulePauseCmd, scheduleResumeCmd, scheduleImportCmd,
	} {
		command.Flags().StringVar(&scheduleServerURL, "server", "http://localhost:8080", "Server URL")
		scheduleCmd.AddCommand(command)
	}

	scheduleCreateCmd.Flags().BoolVar(&scheduleDryRun, "dry-run", false,
		"Print the realized stage list and nominal iteration count without submitting anything")
	rootCmd.AddCommand(scheduleCmd)
}

// scheduleSummaryResponse mirrors the server's schedule summary field for
// field. The decoder refuses unknown fields, so a server that grows a field
// without the CLI growing one fails loudly rather than dropping it.
type scheduleSummaryResponse struct {
	ScheduleID   string    `json:"scheduleId"`
	Name         string    `json:"name,omitempty"`
	State        string    `json:"state"`
	CampaignSeed int64     `json:"campaignSeed"`
	TotalStages  int       `json:"totalStages"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Error        string    `json:"error,omitempty"`
}

type scheduleDetailResponse struct {
	scheduleSummaryResponse

	Document app.ScheduleDocument           `json:"document"`
	Stages   []scheduleStageSummaryResponse `json:"stages"`
}

// scheduleStageSummaryResponse mirrors the server's stage projection. The
// listing deliberately does not carry each stage's JobConfig: a campaign may
// expand to 4096 stages, and the configuration alone put the response past the
// CLI's response cap at roughly 865 of them. A stage's configuration is fetched
// one stage at a time from /schedules/:id/stages/:index.
type scheduleStageSummaryResponse struct {
	Index        int                   `json:"index"`
	Kind         app.ScheduleStageKind `json:"kind"`
	State        store.ScheduleState   `json:"state"`
	Circles      int                   `json:"circles"`
	BestCost     float64               `json:"bestCost,omitempty"`
	ElapsedNanos *int64                `json:"elapsedNanos,omitempty"`
	JobID        string                `json:"jobId,omitempty"`
	Error        string                `json:"error,omitempty"`
	Reason       string                `json:"reason,omitempty"`
}

// chainStageResponse mirrors the server's chain stage projection, which carries
// the table's columns and nothing else for the same reason the schedule listing
// does: a chain is as long as the campaign that built it.
type chainStageResponse struct {
	Index      int     `json:"index"`
	Kind       string  `json:"kind"`
	JobID      string  `json:"jobId"`
	Circles    int     `json:"circles"`
	BestCost   float64 `json:"bestCost"`
	Iterations int     `json:"iterations"`
}

type chainDetailResponse struct {
	LeafJobID string               `json:"leafJobId"`
	RootJobID string               `json:"rootJobId"`
	Stages    []chainStageResponse `json:"stages"`
}

func scheduleBaseURL() string {
	return strings.TrimRight(scheduleServerURL, "/") + "/api/v1"
}

func runScheduleCreate(cmd *cobra.Command, args []string) error {
	document, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("read schedule document: %w", err)
	}
	// Parsing before the request turns a typo into an error naming the field,
	// on the same code path the server validates with, instead of a round trip.
	parsed, err := app.ParseSchedule(document)
	if err != nil {
		return fmt.Errorf("invalid schedule document %q: %w", args[0], err)
	}

	if scheduleDryRun {
		return printSchedulePlan(cmd.OutOrStdout(), args[0], parsed, scheduleDocumentNamesSeed(document))
	}

	body, err := requestCLIBody(cmd.Context(), http.MethodPost, scheduleBaseURL()+"/schedules", document)
	if err != nil {
		return fmt.Errorf("create schedule: %w", err)
	}

	var summary scheduleSummaryResponse
	if err := decodeCLIResponse(body, &summary); err != nil {
		return fmt.Errorf("decode schedule response: %w", err)
	}

	output := cmd.OutOrStdout()
	fmt.Fprintf(output, "Schedule %s created (%s)\n", summary.ScheduleID, summary.State)
	fmt.Fprintf(output, "  Stages: %d\n", summary.TotalStages)
	fmt.Fprintf(output, "  Seed: %s\n", formatCampaignSeed(summary.CampaignSeed))

	return nil
}

// formatCampaignSeed renders the campaign seed for an operator.
//
// A document that omits the seed leaves campaignSeed at zero: that is the
// "resolve one for me" sentinel, not the seed the campaign ran with. The
// resolved value only reaches the record once a stage has run, so printing the
// zero would name a seed that replays nothing. The word is printed instead, in
// the same spirit as the unrecorded columns in the campaign view.
func formatCampaignSeed(seed int64) string {
	if seed == 0 {
		return "unresolved"
	}

	return strconv.FormatInt(seed, 10)
}

func runScheduleList(cmd *cobra.Command, _ []string) error {
	body, err := requestCLI(cmd.Context(), http.MethodGet, scheduleBaseURL()+"/schedules")
	if err != nil {
		return fmt.Errorf("list schedules: %w", err)
	}

	var summaries []scheduleSummaryResponse
	if err := decodeCLIResponse(body, &summaries); err != nil {
		return fmt.Errorf("decode schedules response: %w", err)
	}

	output := cmd.OutOrStdout()
	if len(summaries) == 0 {
		fmt.Fprintln(output, "No schedules found")
		return nil
	}

	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "SCHEDULE\tSTATE\tSTAGES\tSEED\tNAME")

	for _, summary := range summaries {
		fmt.Fprintf(writer, "%s\t%s\t%d\t%s\t%s\n",
			summary.ScheduleID, summary.State, summary.TotalStages,
			formatCampaignSeed(summary.CampaignSeed), summary.Name)
	}

	return writer.Flush()
}

func runScheduleStatus(cmd *cobra.Command, args []string) error {
	scheduleID := args[0]
	endpoint := fmt.Sprintf("%s/schedules/%s", scheduleBaseURL(), url.PathEscape(scheduleID))

	body, err := requestCLI(cmd.Context(), http.MethodGet, endpoint)
	if err != nil {
		var apiErr *cliAPIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return fmt.Errorf("schedule not found %q: %w", scheduleID, err)
		}

		return fmt.Errorf("query schedule %q: %w", scheduleID, err)
	}

	var detail scheduleDetailResponse
	if err := decodeCLIResponse(body, &detail); err != nil {
		return fmt.Errorf("decode schedule response: %w", err)
	}

	if detail.ScheduleID != scheduleID {
		return fmt.Errorf("invalid schedule response: schedule ID %q does not match requested ID %q", detail.ScheduleID, scheduleID)
	}

	printScheduleDetail(cmd.OutOrStdout(), detail, time.Now().UTC())

	return nil
}

func printScheduleDetail(output io.Writer, detail scheduleDetailResponse, asOf time.Time) {
	fmt.Fprintf(output, "Schedule: %s\n", detail.ScheduleID)

	if detail.Name != "" {
		fmt.Fprintf(output, "Name: %s\n", detail.Name)
	}

	fmt.Fprintf(output, "State: %s\n", detail.State)
	fmt.Fprintf(output, "Stages: %d recorded of %d planned\n", len(detail.Stages), detail.TotalStages)
	fmt.Fprintf(output, "Seed: %s\n", formatCampaignSeed(detail.CampaignSeed))

	if detail.Error != "" {
		// The same field carries both, so the label follows the state: a paused
		// campaign stopped where it was told to, and calling that an error
		// would send a reader looking for a fault that is not there.
		label := "Error"
		if detail.State == "paused" {
			label = "Paused"
		}

		fmt.Fprintf(output, "%s: %s\n", label, detail.Error)
	}

	if len(detail.Stages) == 0 {
		fmt.Fprintln(output, "\nNo stage has been recorded yet.")
		return
	}

	fmt.Fprintln(output)
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "#\tKIND\tSTATE\tCIRCLES\tCOST\tPSNR\tELAPSED\tJOB\tNOTE")

	for _, stage := range detail.Stages {
		cost, psnr := "-", "-"
		if stage.State == store.ScheduleStateCompleted {
			cost = fmt.Sprintf("%.3f", stage.BestCost)
			psnr = formatCLIPSNR(stage.BestCost)
		}

		fmt.Fprintf(writer, "%d\t%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			stage.Index, stage.Kind, stage.State, stage.Circles, cost, psnr,
			formatStageElapsed(stage), shortCLIJobID(stage.JobID), stageNoteText(stage))
	}

	_ = writer.Flush()
	// Accepted sweeps are deliberately absent: the polisher reports them only to
	// the log, so no stage record can carry the count and the table would have
	// to invent one.
	fmt.Fprintln(output, "\nPSNR is derived from the stage cost. Accepted polishing sweeps are not persisted.")
	printScheduleProjection(output, detail, asOf)
}

func runScheduleImport(cmd *cobra.Command, args []string) error {
	jobID := args[0]
	endpoint := fmt.Sprintf("%s/chains/%s", scheduleBaseURL(), url.PathEscape(jobID))

	body, err := requestCLI(cmd.Context(), http.MethodGet, endpoint)
	if err != nil {
		var apiErr *cliAPIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return fmt.Errorf("no checkpoint chain for job %q: %w", jobID, err)
		}

		return fmt.Errorf("import chain %q: %w", jobID, err)
	}

	var detail chainDetailResponse
	if err := decodeCLIResponse(body, &detail); err != nil {
		return fmt.Errorf("decode chain response: %w", err)
	}

	output := cmd.OutOrStdout()
	fmt.Fprintf(output, "Chain: %s\n", detail.LeafJobID)
	fmt.Fprintf(output, "Root: %s\n", detail.RootJobID)
	fmt.Fprintf(output, "Stages: %d\n\n", len(detail.Stages))
	writer := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "#\tKIND\tCIRCLES\tCOST\tPSNR\tITERATIONS\tJOB")

	for _, stage := range detail.Stages {
		fmt.Fprintf(writer, "%d\t%s\t%d\t%.3f\t%s\t%d\t%s\n",
			stage.Index, stage.Kind, stage.Circles, stage.BestCost,
			formatCLIPSNR(stage.BestCost), stage.Iterations, shortCLIJobID(stage.JobID))
	}

	return writer.Flush()
}

func scheduleActionRunner(action string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		return runScheduleAction(cmd.Context(), cmd.OutOrStdout(), args[0], action)
	}
}

func runScheduleAction(ctx context.Context, output io.Writer, scheduleID, action string) error {
	endpoint := fmt.Sprintf("%s/schedules/%s/%s", scheduleBaseURL(), url.PathEscape(scheduleID), action)

	body, err := requestCLI(ctx, http.MethodPost, endpoint)
	if err != nil {
		var apiErr *cliAPIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return fmt.Errorf("schedule not found %q: %w", scheduleID, err)
		}

		return fmt.Errorf("%s schedule %q: %w", action, scheduleID, err)
	}

	var summary scheduleSummaryResponse
	if err := decodeCLIResponse(body, &summary); err != nil {
		return fmt.Errorf("decode schedule response: %w", err)
	}

	fmt.Fprintf(output, "Schedule %s is now %s\n", summary.ScheduleID, summary.State)

	return nil
}

func formatCLIPSNR(cost float64) string {
	value := fit.PSNR(cost)
	switch {
	case math.IsInf(value, 1):
		return "inf"
	case math.IsNaN(value) || math.IsInf(value, -1):
		return "-"
	default:
		return fmt.Sprintf("%.2f", value)
	}
}

func formatStageElapsed(stage scheduleStageSummaryResponse) string {
	if stage.ElapsedNanos == nil {
		return "-"
	}

	return time.Duration(*stage.ElapsedNanos).Round(time.Second).String()
}

func stageNoteText(stage scheduleStageSummaryResponse) string {
	if stage.Error != "" {
		return stage.Error
	}

	return stage.Reason
}

func shortCLIJobID(jobID string) string {
	if jobID == "" {
		return "-"
	}

	if len(jobID) <= 8 {
		return jobID
	}

	return jobID[:8]
}
