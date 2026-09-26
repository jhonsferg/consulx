#!/usr/bin/env bash
# Formats and lints the whole repository with containerised tools, so every
# machine runs the same versions and nothing needs installing but Docker.
#
# Usage: tools/lint.sh [-w] [-M]
#
#   (default)  check only: fails when a file needs formatting or a linter
#              reports a problem
#   -w         write: format Markdown and Go in place, then lint
#   -M         also render every Mermaid diagram (slow: starts Chromium)
#
# Steps:
#   Markdown   Prettier (tables, lists, spacing) and markdownlint-cli2
#   Go         golangci-lint fmt (gofmt, goimports) and golangci-lint run,
#              with and without the bench build tag, in every module
#   Shell      shellcheck on tools/*.sh
#   Workflows  actionlint on .github/workflows
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"

write=false
mermaid=false
while getopts "wMh" opt; do
	case $opt in
	w) write=true ;;
	M) mermaid=true ;;
	h | *)
		sed -n '2,18p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	esac
done

readonly prettier=prettier@3.6.2
readonly node_image=node:22-alpine
readonly markdownlint_image=davidanson/markdownlint-cli2:v0.18.1
readonly golangci_image=golangci/golangci-lint:v2.14.0
readonly shellcheck_image=koalaman/shellcheck:stable
readonly actionlint_image=rhysd/actionlint:1.7.12
readonly mermaid_image=minlag/mermaid-cli:latest
readonly modules=". contrib/prometheus contrib/otel contrib/fiber examples integration"

host=$(pwd -W 2>/dev/null || pwd) # Git Bash on Windows needs the Windows path
export MSYS_NO_PATHCONV=1

# go_run WORKDIR ARGS... runs golangci-lint with shared module caches.
go_run() {
	local workdir=$1
	shift
	docker run --rm -v "$host:/src" -v consulx-gomod:/go/pkg/mod -v consulx-gocache:/root/.cache/go-build \
		-w "/src/$workdir" "$golangci_image" "$@"
}

failed=()
step() {
	local name=$1
	shift
	echo "== $name" >&2
	if ! "$@"; then
		failed+=("$name")
	fi
}

prettier_md() {
	local mode=--check
	$write && mode=--write
	docker run --rm -v "$host:/src" -v consulx-npm:/root/.npm -w /src "$node_image" \
		npx --yes "$prettier" "$mode" --log-level warn "**/*.md"
}

markdownlint() {
	local args=("**/*.md")
	$write && args=(--fix "**/*.md")
	docker run --rm -v "$host:/workdir" "$markdownlint_image" "${args[@]}"
}

go_fmt() {
	local m status=0
	for m in $modules; do
		if $write; then
			go_run "$m" golangci-lint fmt ./... || status=1
		elif [[ -n $(go_run "$m" golangci-lint fmt --diff ./...) ]]; then
			echo "$m: files need formatting (run tools/lint.sh -w)" >&2
			status=1
		fi
	done
	return $status
}

go_lint() {
	local m status=0
	for m in $modules; do
		go_run "$m" golangci-lint run ./... || status=1
		go_run "$m" golangci-lint run --build-tags bench ./... || status=1
	done
	return $status
}

shell_lint() {
	docker run --rm -v "$host:/mnt" -w /mnt "$shellcheck_image" -x tools/*.sh
}

workflow_lint() {
	docker run --rm -v "$host:/repo" -w /repo "$actionlint_image" -no-color
}

mermaid_render() {
	local f status=0
	mkdir -p .bench/mermaid # rendered output is discarded
	while IFS= read -r f; do
		docker run --rm -v "$host:/data" -v "$host/.bench/mermaid:/out" "$mermaid_image" \
			-i "/data/${f#./}" -o "/out/$(basename "$f")" >/dev/null || status=1
	done < <(grep -rl --include='*.md' '```mermaid' . | grep -v -e '^./.claude' -e '^./.bench')
	rm -rf .bench/mermaid
	return $status
}

step "Markdown format (Prettier)" prettier_md
step "Markdown lint (markdownlint-cli2)" markdownlint
step "Go format (gofmt, goimports)" go_fmt
step "Go lint (golangci-lint)" go_lint
step "Shell lint (shellcheck)" shell_lint
step "Workflow lint (actionlint)" workflow_lint
if $mermaid; then
	step "Mermaid diagrams (mermaid-cli)" mermaid_render
fi

if ((${#failed[@]})); then
	echo >&2
	echo "Failed: ${failed[*]}" >&2
	exit 1
fi
echo >&2
echo "All checks passed." >&2
