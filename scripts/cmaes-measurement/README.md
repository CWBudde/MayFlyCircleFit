# CMA-ES measurement campaign

This driver owns the five-arm, twelve-block campaign required by go-cma-es
`PLAN.md` Phase 11. It submits jobs through a running server so the dashboard
shows the queue and every active job. It refuses to overwrite a manifest,
which prevents an accidental second submission from corrupting the paired
design.

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
does not write the committed CSVs until all 60 jobs complete. Once complete it
writes `docs/cmaes-measurement.csv`, writes the downsampled optimizer mechanism
data to `docs/cmaes-trajectories.csv`, and prints Markdown statistics. Use
`-action analyze` to reproduce the result table from the first CSV alone.

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

## Budget and pairing

The current Mayfly v0.7.1 pin consumes 6,502,400 optimizer evaluations in the
2048-iteration control. CMA-ES therefore receives 6,350 generations at
lambda=1024. The r16 arm intentionally runs all sixteen 128-iteration attempts;
the collector scores its trace at the same 6,502,400-evaluation cap, excluding
the small tail beyond the control budget.

Submission is block-major. Block `b` uses seed prefix `111000+b` in all five
arms; the twelve prefixes are disjoint, and the restart implementations derive
their attempt seeds from only that block's prefix. The driver refuses any block
count other than twelve, so the paired test always has `df=11`.

The exact 512x512 campaign is roughly 390 million render evaluations. A short
calibration on the documented Ryzen 5 4600H host measured about 1,450
evaluations/second at eight evaluation workers, so the queue takes on the order
of three days there. Keep `--max-jobs 1`: competing jobs do not increase this
six-core host's aggregate throughput and make wall-clock records harder to
interpret.
