#!/usr/bin/env bash
# SPDX-FileCopyrightText: The Forge CI Authors
# SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

# Forgejo can acknowledge finalization without making every entry readable.
# Verify stored bytes before declaring the assembly-to-signing handoff complete.
set -euo pipefail
name=${1:?artifact name required}
source_dir=${2:?evidence directory required}
work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/runtime-evidence-check.XXXXXX")
trap 'rm -rf -- "$work"' EXIT

inventory() (
	cd "$1"
	# Match artifact upload's default exclusion of hidden files/directories.
	hidden_filter=()
	if [[ "${2:-}" != all ]]; then
		hidden_filter=(-name '.*' -prune -o)
	fi
	find . -mindepth 1 "${hidden_filter[@]}" -type f -print0 |
		LC_ALL=C sort -z | xargs -0 -r sha256sum
)

inventory "$source_dir" >"$work/expected.sha256"
test -s "$work/expected.sha256"
forge-ci artifact upload --name "$name" --dir "$source_dir" --retention-days 30 --if-no-files error
if ! forge-ci artifact download --name "$name" --dir "$work/download"; then
	printf 'runtime-evidence: artifact %s was acknowledged but read-back failed; assembly is incomplete\n' "$name" >&2
	exit 1
fi
if ! (cd "$work/download" && sha256sum --check --strict "$work/expected.sha256"); then
	printf 'runtime-evidence: artifact %s differs from the uploaded evidence; assembly is incomplete\n' "$name" >&2
	exit 1
fi
inventory "$work/download" all >"$work/actual.sha256"
if [[ "$(<"$work/expected.sha256")" != "$(<"$work/actual.sha256")" ]]; then
	printf 'runtime-evidence: artifact %s contains unexpected files; assembly is incomplete\n' "$name" >&2
	exit 1
fi
printf 'Verified stored evidence: %s\n' "$name"
