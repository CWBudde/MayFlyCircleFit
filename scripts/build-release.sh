#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 1 ]]; then
	printf 'usage: %s VERSION\n' "$0" >&2
	exit 2
fi

release_version="$1"
semver_re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'
if [[ ! "$release_version" =~ $semver_re ]]; then
	printf 'invalid semantic version %q (omit the leading v)\n' "$release_version" >&2
	exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "$script_dir/.." && pwd)"
release_output="$repo_root/dist"
release_work="$(mktemp -d)"
trap 'rm -rf -- "$release_work"' EXIT

release_commit="${MAYFLY_RELEASE_COMMIT:-}"
if [[ -z "$release_commit" ]]; then
	release_commit="$(git -C "$repo_root" rev-parse HEAD 2>/dev/null || printf unknown)"
fi

release_build_date="${MAYFLY_RELEASE_BUILD_DATE:-}"
if [[ -z "$release_build_date" ]]; then
	release_build_date="$(git -C "$repo_root" show -s --format=%cI HEAD 2>/dev/null || printf unknown)"
fi

case "$release_commit" in
	*[!0-9A-Fa-f]*|'')
		if [[ "$release_commit" != "unknown" ]]; then
			printf 'invalid release commit %q\n' "$release_commit" >&2
			exit 2
		fi
		;;
esac
case "$release_build_date" in
	*[!0-9A-Za-z:+.-]*|'')
		printf 'invalid release build date %q\n' "$release_build_date" >&2
		exit 2
		;;
esac

for required_file in README.md LICENSE THIRD-PARTY-NOTICES.md CHANGELOG.md assets/test.png; do
	if [[ ! -f "$repo_root/$required_file" ]]; then
		printf 'required release file is missing: %s\n' "$required_file" >&2
		exit 1
	fi
done

command -v go >/dev/null || { printf 'go is required\n' >&2; exit 1; }
command -v tar >/dev/null || { printf 'tar is required\n' >&2; exit 1; }
command -v zip >/dev/null || { printf 'zip is required\n' >&2; exit 1; }

rm -rf -- "$release_output"
mkdir -p "$release_output"

platforms=(
	"linux amd64"
	"linux arm64"
	"darwin amd64"
	"darwin arm64"
	"windows amd64"
)

ldflags="-s -w -X github.com/cwbudde/mayflycirclefit/cmd.version=$release_version -X github.com/cwbudde/mayflycirclefit/cmd.commit=$release_commit -X github.com/cwbudde/mayflycirclefit/cmd.buildDate=$release_build_date"

for platform in "${platforms[@]}"; do
	read -r release_os release_arch <<<"$platform"
	archive_name="mayflycirclefit_${release_version}_${release_os}_${release_arch}"
	stage_dir="$release_work/$archive_name"
	binary_name="mayflycirclefit"
	if [[ "$release_os" == "windows" ]]; then
		binary_name+=".exe"
	fi

	mkdir -p "$stage_dir/assets"
	(
		cd "$repo_root"
		CGO_ENABLED=0 GOOS="$release_os" GOARCH="$release_arch" \
			go build -buildvcs=false -trimpath -ldflags "$ldflags" -o "$stage_dir/$binary_name" .
	)
	# THIRD-PARTY-NOTICES.md covers the npm packages compiled into the
	# embedded island bundle; they ship inside the binary, so the notices
	# have to ship in the archive.
	cp "$repo_root/README.md" "$repo_root/LICENSE" "$repo_root/THIRD-PARTY-NOTICES.md" "$repo_root/CHANGELOG.md" "$stage_dir/"
	cp "$repo_root/assets/test.png" "$stage_dir/assets/"

	if [[ "$release_os" == "windows" ]]; then
		(
			cd "$release_work"
			zip -q -r "$release_output/$archive_name.zip" "$archive_name"
		)
	else
		tar -C "$release_work" -czf "$release_output/$archive_name.tar.gz" "$archive_name"
	fi
	rm -rf -- "$stage_dir"
done

(
	cd "$release_output"
	if command -v sha256sum >/dev/null; then
		sha256sum ./*.tar.gz ./*.zip > SHA256SUMS
	elif command -v shasum >/dev/null; then
		shasum -a 256 ./*.tar.gz ./*.zip > SHA256SUMS
	else
		printf 'sha256sum or shasum is required\n' >&2
		exit 1
	fi
)

printf 'release artifacts written to %s\n' "$release_output"
