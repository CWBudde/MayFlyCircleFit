# Releasing

CircleFit uses Semantic Versioning tags and publishes portable CPU builds.
The repository's `CI` workflow (`.github/workflows/ci.yml`) is the authoritative
automated release path; its `release` job cannot run until every required CI
dependency succeeds for the tagged commit. The gates themselves live in reusable
`ci-<concern>.yml` workflows that `ci.yml` calls.

Not every gate blocks publication. `release` lists the release-blocking caller
jobs in its `needs:` block, currently `generation`, `quality`, `staticcheck`,
`test`, `e2e`, `coverage`, `race`, `bundle`, `web`, `build`, `cross-build`,
`native-simd`, `gpu-compile`, and `vulnerability`. `benchmarks` runs on every
commit but is deliberately not among them, because the timing comparison is
report-only. A new gate is only release-blocking once it is added to that
`needs:` list.

## Check names

Because the gates are reusable workflows, each one reports as
`<caller job>` / `<job name>` rather than `<job name>` alone: the bundle gate is
`bundle / Committed island bundle is current`, the generation gate is
`generation / Generated UI is current`, the browser gate is
`web / Browser matrix, accessibility, and live reconciliation`, the vet gate is
`quality / Format and vet`, and matrix gates expand per entry, such as
`native-simd / Native SSD (Linux AMD64 / AVX2)`. Only `Publish release` keeps a
bare name, because it still lives in `ci.yml`.

Use these prefixed names anywhere a check is referenced by string. The default
branch currently has no required-status-check rule, so nothing needed migrating
when the workflow was split, but a ruleset or branch-protection entry added later
must use the prefixed form or it will wait forever on a check that never reports.

## Prepare and verify

1. Work from a clean commit that is part of `main`.
2. Move relevant entries from `CHANGELOG.md`'s Unreleased section into a dated
   version section.
3. Run the local gates:

   ```sh
   just check
   just web-check
   go test -race -short ./...
   just test-e2e
   just release 0.2.0
   (cd dist && sha256sum --check SHA256SUMS)
   ```

   `just release` accepts a version without the leading `v`. It replaces the
   ignored `dist/` directory with archives for Linux/AMD64, Linux/ARM64,
   macOS/AMD64, macOS/ARM64, and Windows/AMD64.

4. Confirm the required CI workflow has passed for the commit. Phase 14 also
   requires two consecutive clean workflow runs before declaring the release
   candidate ready.

## Tag and publish

Create an annotated SemVer tag and push it:

```sh
git tag -a v0.2.0 -m "Release v0.2.0"
git push origin v0.2.0
```

Tags such as `v0.2.0-rc.1` create GitHub prereleases. The tag workflow validates
the tag and verifies that its commit belongs to `main`, reruns the complete CI
matrix, builds the archives, checks their SHA-256 hashes, creates a draft with
generated release notes, uploads every artifact, and publishes the draft last.

Each archive contains the platform binary, README, LICENSE,
`THIRD-PARTY-NOTICES.md`, CHANGELOG, and `assets/test.png`. The notices file
covers the npm packages compiled into the embedded island bundle, which the
binary redistributes. Release binaries report the version, full commit hash, and
tag-commit date through both `version` and `--version`.

## Install a release archive

Download the archive and `SHA256SUMS` from the GitHub release. Verify the
archive from their download directory before extracting it:

```sh
sha256sum --check --ignore-missing SHA256SUMS
tar -xzf circlefit_0.2.0_linux_amd64.tar.gz
./circlefit_0.2.0_linux_amd64/circlefit --version
```

Use `unzip` for the Windows archive. Move the binary to a directory on `PATH`
if desired; no runtime files are required for CPU operation. Keep the bundled
`assets/test.png` to exercise the README quick start.

If publication fails after creating a draft, inspect that draft and its assets
and remove the incomplete draft before rerunning. Never replace or move a
published tag; issue a new patch version instead.

## Policy boundary

The workflow gates repository-controlled automated releases. GitHub users with
sufficient repository permissions may still create tags or releases manually
unless repository rules and roles prevent it. Maintainers must configure and
verify those administrative controls before checking Phase 14's absolute
release-policy acceptance item.

Release archives are portable CPU builds with `CGO_ENABLED=0`. OpenCL builds,
artifact signing, provenance attestations, SBOMs, and real-vendor GPU
certification are not part of this release path.
