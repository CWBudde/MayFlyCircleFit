#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
build_work="$(mktemp -d)"
trap 'rm -rf -- "$build_work"' EXIT

supported_targets=(
	"linux/amd64"
	"linux/arm64"
	"darwin/amd64"
	"darwin/arm64"
	"windows/amd64"
	"linux/386"
)

targets=("${supported_targets[@]}")
if [[ $# -gt 1 ]]; then
	printf 'usage: %s [GOOS/GOARCH]\n' "$0" >&2
	exit 2
fi
if [[ $# -eq 1 ]]; then
	targets=("$1")
	known=false
	for supported_target in "${supported_targets[@]}"; do
		if [[ "$1" == "$supported_target" ]]; then
			known=true
			break
		fi
	done
	if [[ "$known" != true ]]; then
		printf 'unsupported validation target: %s\n' "$1" >&2
	exit 2
	fi
fi

require_source() {
	local sources="$1"
	local required="$2"
	if [[ " $sources " != *" $required "* ]]; then
		printf 'required source %s was not selected from: %s\n' "$required" "$sources" >&2
		exit 1
	fi
}

reject_source() {
	local sources="$1"
	local rejected="$2"
	if [[ " $sources " == *" $rejected "* ]]; then
		printf 'unexpected source %s was selected from: %s\n' "$rejected" "$sources" >&2
		exit 1
	fi
}

for target in "${targets[@]}"; do
	target_os="${target%/*}"
	target_arch="${target#*/}"
	sources="$({
		cd "$repo_root"
		CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
			go list -f '{{range .GoFiles}}{{.}} {{end}}{{range .SFiles}}{{.}} {{end}}' ./internal/fit
	})"

	case "$target_arch" in
	amd64)
		require_source "$sources" ssd_amd64.go
		require_source "$sources" ssd_dispatch_amd64.go
		require_source "$sources" ssd_amd64.s
		require_source "$sources" ssd_sse2_amd64.go
		require_source "$sources" ssd_sse2_amd64.s
		reject_source "$sources" ssd_dispatch_arm64.go
		reject_source "$sources" ssd_dispatch_generic.go
		;;
	arm64)
		require_source "$sources" ssd_arm64.go
		require_source "$sources" ssd_dispatch_arm64.go
		require_source "$sources" ssd_arm64.s
		reject_source "$sources" ssd_dispatch_amd64.go
		reject_source "$sources" ssd_dispatch_generic.go
		reject_source "$sources" ssd_sse2_amd64.go
		reject_source "$sources" ssd_sse2_amd64.s
		;;
	*)
		require_source "$sources" ssd_dispatch_generic.go
		reject_source "$sources" ssd_amd64.go
		reject_source "$sources" ssd_arm64.go
		reject_source "$sources" ssd_amd64.s
		reject_source "$sources" ssd_arm64.s
		reject_source "$sources" ssd_sse2_amd64.go
		reject_source "$sources" ssd_sse2_amd64.s
		;;
	esac

	output="$build_work/mayflycirclefit-${target_os}-${target_arch}"
	if [[ "$target_os" == windows ]]; then
		output+=".exe"
	fi
	(
		cd "$repo_root"
		CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
			go build -buildvcs=false -o "$output" .
		CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" \
			go test -c -o "$build_work/fit-${target_os}-${target_arch}.test" ./internal/fit
	)
	printf 'validated %s (CGO_ENABLED=0; SSD sources: %s)\n' "$target" "$sources"
done
