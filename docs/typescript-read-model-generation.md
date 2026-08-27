# Generating the TypeScript read models

**Decision, Task 18.6, 2026-08-27. Not adopted.** The hand-written read models
in `web/src` stay hand-written, and the parity tests in
`internal/server/seed_parity_test.go` are the contract that keeps them honest.
This document records what was measured, what the alternatives cost, and what
would reopen the question.

The plan's premise was that "roughly 25 Go↔TypeScript type and formatter pairs
are kept in sync by convention", and that generation would prevent the drift the
parity tests only catch afterwards. The premise is right about the risk and
wrong about the size: the surface is **54 pairs, not 25** — 27 type pairs and 27
formatter pairs — and generation reaches about 30% of it.

## The surface, counted

Every TypeScript declaration in `web/src` that models data the Go server emits,
and every TypeScript function that reimplements a Go helper.

### Type pairs — 27

| TypeScript | Go | 1:1? |
| --- | --- | --- |
| `live.ts` `ProgressEvent` | `server.ProgressEvent` | yes (`state` widened from `JobState`) |
| `live.ts` `UIEvent` | `server.UIEvent` | yes (`type` narrowed to a 4-member union) |
| `dashboard.tsx` `DashboardResponse` | `server.dashboardResponse` **and** `ui.DashboardPageData` | no — one type over two Go structs |
| `dashboard.tsx` `RunningJob` | `server.dashboardRunningJob` **and** `ui.DashboardRunningJob` | no — `metricHistory?` is optional only because the seed omits it |
| `dashboard.tsx` `DashboardAggregates` | `server.dashboardAggregates` | yes |
| `dashboard.tsx` `HostFacts` | `ui.HostFacts` | no — drops `supportedBackends` and `gpu.platforms` that `server.HostFacts` carries |
| `dashboard.tsx` `CampaignSummary` | `ui.CampaignSummary` | yes, but a duplicate of the `Campaigns.tsx` declaration with `source` widened |
| `dashboard.tsx` `CampaignSeriesPoint` | `ui.CampaignSeriesPoint` | yes |
| `dashboard.tsx` `MetricSample` | `ui.MetricSample` | no — 2 of 8 fields, on purpose |
| `dashboard.tsx` `ProgressEventPayload` | `server.ProgressEvent` | no — 5 of 14 fields, on purpose |
| `Campaigns.tsx` `CampaignPoint` | `ui.CampaignSeriesPoint` | yes |
| `Campaigns.tsx` `CampaignSummary` | `ui.CampaignSummary` | yes (`source` narrowed) |
| `Campaigns.tsx` `CampaignList` | `server.campaignViewList` | yes |
| `Campaigns.tsx` `CampaignStage` | `ui.CampaignStage` | yes |
| `Campaigns.tsx` `CampaignProjection` | `ui.CampaignProjection` | yes |
| `Campaigns.tsx` `Campaign` | `ui.Campaign` | yes (`warnings` nullability differs — see below) |
| `JobList.tsx` `JobListItem` | `ui.JobListItem` | no — flattened view model, built by `fromRaw` |
| `JobList.tsx` `JobPage` | `ui.JobListPage` | yes |
| `JobList.tsx` `RawJob` | `server.JobSummary` | no — declares `config.ref`, which no Go field has, as a legacy-key fallback |
| `JobList.tsx` `RawJobPage` | `server.jobListPage` | yes |
| `JobDetail.tsx` `JobActions` | `server.jobActions` | yes |
| `JobDetail.tsx` `JobDetailSeed` | `ui.JobDetail` | yes |
| `JobDetail.tsx` `JobStatusPayload` | `server.jobStatusResponse` | no — an honest subset, narrowed to what the detail panel reads |
| `JobDetail.tsx` `MetricSample` | `ui.MetricSample` | near — Go also carries `optimizerDiagnostics` |
| `JobDetail.tsx` `CircleParameter` | `ui.CircleParameter` | yes |
| `JobDetail.tsx` polish response | `map[string]any`, `server.go:1901` | no — untyped on the Go side, so ungeneratable |
| `JobDetail.tsx` error envelope | `server.apiErrorResponse` | no — drops `error.code` |
| `CampaignCostChart.tsx` `CampaignCostChartPoint` | `ui.CampaignSeriesPoint` | yes |
| `format.ts` `ProjectionShape` | `ui.CampaignProjection` | no — a structural clone minus 3 fields, existing to break an import cycle |

Sixteen of the 27 are close enough to 1:1 for a generator to own. Eleven are
not, and their divergence is deliberate in every case: a subset the island
actually renders, a view model the island builds, a merge of a seed type and an
endpoint type, or a tolerance for a key the Go side never sends.

### Formatter pairs — 27

`web/src/format.ts` exports 27 functions and its own header says why: every one
mirrors a Go helper in `internal/ui`, and the island replaces server-rendered
markup, so a formatter that disagrees rewrites the page on mount. The
`stateClass`/`stateLabel`/`stateBadgeStyle` trio mirrors `ui.StateBadge`;
`formatCostGain` mirrors `formatJobImprovement`; `formatDurationSeconds`
reimplements `time.Duration.String()` after rounding to whole seconds; the
fifteen `campaign*` helpers mirror their namesakes in `schedule.templ`. **No
type generator addresses any of these.** They are pinned by
`web/src/format.test.ts`, whose cases were checked against what Go produces for
the same input rather than against what the TypeScript happens to return.

The badge trio is the worked example of the pattern this decision endorses, and
of how the same problem is solved without a generator. It used to be four
independent maps — `format.ts` plus one apiece in `JobList.tsx`,
`JobControls.tsx` (since folded into `JobDetail.tsx`) and `Campaigns.tsx` — that
disagreed in corners: one rendered
the raw state where the others title-cased it, and one printed `undefined` for
an empty state where another printed `Unknown`. Phase 18 collapsed them onto the
single `format.ts` trio and pinned the mapping in `web/src/state-badge-parity.json`,
which is read by **both** `internal/ui/state_badge_parity_test.go` and
`web/src/format.test.ts`. Neither implementation is the source of truth; the
fixture is, and a mapping changed on one side alone fails on both. That is a
contract no Go→TypeScript type generator could have expressed, because it is
about behavior rather than shape.

Duplications that remain outside `format.ts`, covered by nothing:

- `CampaignCostChart.tsx` `plottableStages` reimplements `buildCampaignPlot`'s
  skip rule, and its tooltip hand-copies the SVG `<title>` format string.
- `JobList.tsx` `compareJobOrder` reimplements `ListJobSummaries`' ordering.
- `dashboard.tsx` `JobCard` carries a second, inline copy of `formatCostGain`'s
  math with weaker guards, alongside local `formatFixed`, `formatInteger`,
  `clampProgress` and `architectureBadge` helpers whose Go twins live in
  `dashboard.templ`.

That is where the real exposure is, and it is exactly the half generation cannot
reach.

## What the parity tests catch, and what they missed

Five tests span `internal/server/dashboard_test.go:526` and
`internal/server/seed_parity_test.go`. Each pairs a server-rendered page seed
with the JSON endpoint the island refetches, flattens both to dotted leaf paths
with `jsonLeafKeys`, and compares them.

They catch, **between the two Go payloads of one pairing**: a renamed JSON tag,
an added or removed field, a value that differs under the same name, and a
restructuring (the job list's `refPath`/`mode`/`circles` flattening is declared
as an explicit rename map rather than silently tolerated).

They missed, before this task:

- **Everything on the TypeScript side.** The wire names the islands read appear
  only as hand-maintained string literals in `assertEndpointHas`. A field
  renamed in an island — or invented in an island — failed nothing.
- Endpoints with no page seed to pair against: `/api/v1/stream`,
  `/api/v1/events`, `/api/v1/jobs/:id/metrics`.
- Formatter behavior, which is not a shape at all.

## Option 1: tygo as a Go tool — measured, rejected

`github.com/gzuidhof/tygo` is pure Go and vendorable through the `tool` block,
so it satisfies the binding constraint that `go build ./...` never needs node or
npm. Its cost and its output were both measured rather than estimated.

**Dependency cost: three modules.** Copying this repository's `go.mod`/`go.sum`
into a scratch directory and running `go get github.com/gzuidhof/tygo@v0.2.21`
added exactly `github.com/gzuidhof/tygo`, `github.com/fatih/structtag` and
`gopkg.in/yaml.v2`, and bumped nothing. That part is cheap.

**Output: measured on this repository.** tygo v0.2.21 was run against
`internal/ui` and `internal/server`, producing 501 and 314 lines. Four findings
decide the question.

1. **It emits almost none of the endpoint types, and several types that are not
   wire at all.** Of the 32 structs `internal/server` serializes, **24 are
   unexported and tygo emitted none of them** — including every envelope the
   islands read: `dashboardResponse`, `dashboardRunningJob`,
   `dashboardAggregates`, `jobListPage`, `jobStatusResponse`, `jobActions`,
   `campaignViewList`, `apiErrorResponse`. In their place it emitted six types
   that never touch the wire: `JobManager`, `Server`, `ServerOptions`,
   `EventBroadcaster`, `UIEventHub`, `BuildMetadata`. Adopting tygo therefore
   means exporting two dozen response structs of the trust-boundary package for
   a tool's benefit, and maintaining an exclusion list for the machinery that
   leaks in. From `internal/ui` it likewise emitted `JobDetail`, `JobReport`,
   `ImageViewerData` and `CircleParameter`, which no island reads and three of
   which have no JSON tags, so their fields come out PascalCase.

2. **`time.Time` becomes `any`** — nine fields across the two packages, against
   the hand-written `string`. `JobConfig` becomes `any /* store.JobConfig */`
   because the referenced package is not in the config. Both are fixable with
   `type_mappings` entries, but the default is a regression.

3. **The generated types are wrong where nullability matters.**
   `ui.Campaign.Warnings` is `[]string` with `json:"warnings"` and no
   `omitempty`, so a nil slice marshals to `null`. tygo emits
   `warnings: string[]`. The hand-written declaration is
   `warnings?: string[] | null`, which is what the wire actually does. A
   generator that turns a correct type into one that invites a crash on
   `.length` is not a drift defense.

4. **The generated types are looser where precision matters.**
   `CampaignSource` becomes `export type CampaignSource = string` beside two
   consts; the hand-written union is `"schedule" | "chain"`. `UIEvent.type` is
   plain `string` in Go and a four-member union in `live.ts`. Both narrowings
   are load-bearing in island `switch` statements, and generation removes them.

Findings 3 and 4 are not configuration problems. They are the consequence of Go
tags carrying less information than the islands need.

## Option 2: a bespoke in-repo generator

A `cmd/`-level program using `go/types` or reflection could fix all four
findings: it can model nil-slice-to-`null`, it can widen `time.Time` to
`string`, it can be given an explicit include list. It cannot reach the
unexported types from outside `internal/server` — reflection over an unexported
type needs a value, and a value needs an exported constructor or hook that would
exist only for codegen. And it would still generate nothing for the eleven
non-1:1 pairs, nothing for the four untyped `map[string]any` responses, and
nothing for the 26 formatters. The write-and-own cost is a new tool, a `just`
recipe, a CI gate and a committed artifact, against the same 30% coverage.

## Option 3: hand-written types plus parity tests — adopted

The arithmetic: generation could own **16 of the 54 pairs**, about 30%, and in
four measured respects would replace them with something less precise than what
is there now. The measured drift across those pairs today is **zero** — the
convention is holding. Against that, generation adds a dependency, a third drift
gate, a config file, an exclusion list, a `type_mappings` table, and a
source-level change to `internal/server` made for a tool rather than for the
server.

Not adopted.

## What the parity tests are now the contract for

The decision only stands if the tests carry the weight generation would have
carried. Task 18.6 therefore added the missing direction:
`TestIslandReadModelsMatchTheGoWire` in `internal/server/seed_parity_test.go`
reads the hand-written declarations straight out of `web/src` and asserts that
every field they declare exists on the Go type serving it. It covers 27
pairings across `live.ts`, `dashboard.tsx`, `Campaigns.tsx`, `JobList.tsx`,
`JobDetail.tsx`, `CampaignCostChart.tsx` and `format.ts`, reading tags by
reflection rather than marshalling a fixture, so an `omitempty` field is
compared even when its zero value would drop it from the wire. All 27 pass, and
the check was confirmed to fail when a declaration is pointed at the wrong Go
type.

It is deliberately one-directional. A Go struct may carry fields no island
reads — most do. An island field with no Go field behind it is always
`undefined` at runtime, and TypeScript cannot see it, because every payload
enters through `fetchJSON<T>`'s unchecked `as T` cast.

Taken together, the contract is now:

| Drift | Caught by |
| --- | --- |
| A JSON tag renamed on one Go side only | the five seed↔endpoint tests |
| A field added to or dropped from a Go payload the seed also carries | the five seed↔endpoint tests |
| A wire name an island reads disappearing from the endpoint | `assertEndpointHas` |
| An island declaring a field the Go type does not serialize | `TestIslandReadModelsMatchTheGoWire` |
| A Go tag renamed without the island following | `TestIslandReadModelsMatchTheGoWire` |
| A formatter diverging from its Go twin | `web/src/format.test.ts`, whose expectations are what Go produces |
| The state badge diverging in either language | `web/src/state-badge-parity.json`, read by `internal/ui/state_badge_parity_test.go` and `web/src/format.test.ts` |

Still uncovered, and named here so nobody assumes otherwise:

- **Types, not names.** The tests compare field names, not widths or
  nullability. `int64` versus `number` and `*float64` versus `number | null` are
  on the reader.
- **The four untyped `map[string]any` responses** (`resume`, `polish`,
  `extend`). Nothing can pin a shape that has no type.
- **The duplicated helpers outside `format.ts`**, listed above. Moving them into
  `format.ts` — where a test can import them — is the cheap fix, and the badge
  trio shows the shape of it.
- **The duplicated declarations inside `web/src`**: three of
  `ui.CampaignSeriesPoint` (`CampaignSeriesPoint`, `CampaignPoint`,
  `CampaignCostChartPoint`) and two of `ui.CampaignSummary`, differing only in
  how tightly `source` is typed. Collapsing them into one shared module is a
  `web/src` refactor that needs no codegen and would remove three pairs from the
  count outright. That is the single largest cleanup generation was offering,
  and it is available without generation.

## What would reopen this

- The `internal/server` wire structs becoming exported for a reason of their
  own. Finding 1 is most of the cost, and it would be gone.
- A consumer of this JSON API outside this repository. The argument above rests
  on the only client being three thousand lines of TypeScript in the same tree,
  reviewed in the same pull request as the Go it reads.
- A drift incident that `TestIslandReadModelsMatchTheGoWire` does not catch.
  Record it here first; the answer may be another row rather than a generator.

## Reproducing the measurements

```sh
# Dependency cost (in a scratch directory, not the repository):
cp go.mod go.sum /tmp/probe/ && cd /tmp/probe
go get github.com/gzuidhof/tygo@v0.2.21 && git diff --no-index go.mod.orig go.mod

# Generator output:
GOBIN=/tmp/bin go install github.com/gzuidhof/tygo@v0.2.21
# tygo.yaml listing internal/ui and internal/server with output_path outside the repo
/tmp/bin/tygo generate --config /tmp/tygo.yaml

# The contract:
go test ./internal/server/ -run 'SeedMatchesEndpointShape|IslandReadModelsMatchTheGoWire' -v
cd web && npm run test:unit
```
