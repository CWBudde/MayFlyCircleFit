# CMA-ES measurement campaign

This driver owns the registered twelve-block campaigns behind go-cma-es
`PLAN.md` Phase 11 and this repository's Phase 23. It submits jobs through a
running server so the dashboard shows the queue and every active job. It
refuses to overwrite a manifest, which prevents an accidental second submission
from corrupting the paired design.

Two designs are registered, selected with `-design`:

- `phase21` (the default) — the original five arms: two Mayfly controls and
  three CMA-ES arms, all at `popSize` 1024. 60 jobs.
- `lambda` — the eight-arm screen that crosses both covariance modes with four
  restart-and-population shapes. 96 jobs.

Designs are enumerated in code rather than assembled from flags, so a campaign
cannot silently differ from the one that was registered. `-action plan` prints
a design without submitting it; a manifest may only be written once, so it is
worth reading before twelve blocks are queued.

Build one identified binary and keep it for the whole campaign:

```sh
commit=$(git rev-parse HEAD)
go build -trimpath \
  -ldflags "-X github.com/cwbudde/circlefit/cmd.commit=$commit" \
  -o ./data/cmaes-phase11/circlefit .

./data/cmaes-phase11/circlefit serve \
  --port 8085 \
  --data-root ./data/cmaes-phase11 \
  --max-jobs 1 \
  --queue-size 100 \
  --input-root .
```

The dashboard is then at <http://localhost:8085/>. In another shell:

```sh
go run ./scripts/cmaes-measurement -action submit
go run ./scripts/cmaes-measurement -action collect
```

`collect` is safe to repeat while the queue runs. It prints state counts and
does not write the committed CSVs until every job in the design completes. Once
complete it writes `docs/cmaes-measurement.csv`, writes the downsampled
optimizer mechanism data to `docs/cmaes-trajectories.csv`, and prints Markdown
statistics. Use `-action analyze` to reproduce the result table from the first
CSV alone.

The campaign was run to completion on 2026-08-28 on a 64-core host, six jobs
at a time, in 4.9 hours of wall clock. Its result is
[`docs/cmaes-report.md`](../../docs/cmaes-report.md) and its raw data is
committed as `docs/cmaes-measurement.csv` and `docs/cmaes-trajectories.csv`.
Re-running `submit` against a fresh data root would produce a second campaign,
not an extension of that one.

The first campaign was stopped by operator request after three completed jobs
and one interrupted job because the calibrated queue duration was several days.
Do not restart its server unless resuming that work is intentional. Its
persisted subset can be collected entirely offline, without touching the queue:

```sh
go run ./scripts/cmaes-measurement \
  -action preliminary \
  -results docs/cmaes-preliminary-results.csv \
  -trajectories docs/cmaes-preliminary-trajectories.csv
```

That action collects only job directories which have checkpoint metadata and
labels a non-completed checkpoint `interrupted`. It deliberately prints no
inferential statistics. See
[`docs/cmaes-preliminary-report.md`](../../docs/cmaes-preliminary-report.md).

## The lambda screen

`-design lambda` answers the two questions `docs/cmaes-report.md` left open.
The CMA-ES adapter sets `Lambda = popSize`, so every Phase 21 arm searched 56
dimensions with a population of 1024 against Hansen's default of
`4 + floor(3 ln 56)` = 16. The screen visits 1024, 64 and 20 — 20 being the
floor `app.MinPopulation` permits — crossed with full and separable covariance,
under IPOP restarts.

| | full | separable |
| --- | --- | --- |
| no restarts, lambda 1024 | `cmaes-single` | `sep-cmaes-single` |
| IPOP, lambda 1024 | `cmaes-ipop` | `sep-cmaes-ipop` |
| IPOP, lambda 64 | `cmaes-ipop-l64` | `sep-cmaes-ipop-l64` |
| IPOP, lambda 20 | `cmaes-ipop-l20` | `sep-cmaes-ipop-l20` |

Three cells repeat Phase 21 at the same seeds, so cross-campaign drift is
measured rather than assumed, and `sep-cmaes-single` is the cell Phase 21 never
ran: without it nothing attributes `sep-cmaes-ipop`'s win to covariance mode
rather than to restarts (Task 23.2).

Every arm is evaluation-matched by construction — each lambda level divides the
6,502,400-evaluation budget exactly, and the driver refuses a level that does
not. `iters` is therefore a budget encoding, not a prediction: an IPOP arm
doubles its population on restart and reaches the evaluation cap long before
the nominal generation count.

Small populations cost wall clock at equal evaluations, because a generation is
a synchronisation barrier. Measured on the 64-core host at seven concurrent
jobs and eight evaluation workers, lambda 64 ran at 0.67x and lambda 20 at
0.61x the evaluation rate of lambda 1024.

## Budget and pairing

The current Mayfly v0.7.1 pin consumes 6,502,400 optimizer evaluations in the
2048-iteration control. CMA-ES therefore receives 6,350 generations at
lambda=1024. The r16 arm intentionally runs all sixteen 128-iteration attempts;
the collector scores its trace at the same 6,502,400-evaluation cap, excluding
the small tail beyond the control budget.

Submission is block-major. Block `b` uses seed prefix `111000+b` in every arm
of the design; the twelve prefixes are disjoint, and the restart
implementations derive their attempt seeds from only that block's prefix. The
driver refuses any block count other than twelve, so the paired test always has
`df=11`.

The exact 512x512 campaign is roughly 390 million render evaluations. A short
calibration on the documented Ryzen 5 4600H host measured about 1,450
evaluations/second at eight evaluation workers, so the queue takes on the order
of three days there. Keep `--max-jobs 1`: competing jobs do not increase this
six-core host's aggregate throughput and make wall-clock records harder to
interpret.
