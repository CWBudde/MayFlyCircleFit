# Releasing

MayFlyCircleFit uses Semantic Versioning tags and publishes portable CPU builds.
The repository workflow is the authoritative automated release path; its release
job cannot run until every required CI dependency succeeds for the tagged
commit.

## Prepare and verify

1. Work from a clean commit that is part of `master`.
2. Move relevant entries from `CHANGELOG.md`'s Unreleased section into a dated
   version section.
3. Run the local gates:

   ```sh
   just check
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
the tag and verifies that its commit belongs to `master`, reruns the complete CI
matrix, builds the archives, checks their SHA-256 hashes, creates a draft with
generated release notes, uploads every artifact, and publishes the draft last.

Each archive contains the platform binary, README, LICENSE, CHANGELOG, and
`assets/test.png`. Release binaries report the version, full commit hash, and
tag-commit date through both `version` and `--version`.

## Install a release archive

Download the archive and `SHA256SUMS` from the GitHub release. Verify the
archive from their download directory before extracting it:

```sh
sha256sum --check --ignore-missing SHA256SUMS
tar -xzf mayflycirclefit_0.2.0_linux_amd64.tar.gz
./mayflycirclefit_0.2.0_linux_amd64/mayflycirclefit --version
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
