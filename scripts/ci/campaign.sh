#!/usr/bin/env bash

# SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
# SPDX-License-Identifier: CC0-1.0

# A live campaign: the inputs of one commit, prepared once, and lanes of cases
# that run against them. docs/live-campaigns.md, Run a campaign, shows the use.
#
#   campaign.sh prepare <forge-lab checkout> <dir> <forgejo|gitlab>=<cases file> ... [job-image=<flavor>:<local image> ...]
#   campaign.sh lane <dir> <forgejo|gitlab> <cases file>
#
# prepare builds and publishes the images of this checkout's HEAD with the
# live recipes: the signer and kaniko job images, the Buildah job image
# archive, and the archive of each runtime image the named lanes' cases need
# (preflight campaign-runtime-targets). It exports every demo as a one-commit
# checkout under <dir>/demos/<name>, and writes <dir>/campaign.env: KEY=value
# lines of image references and archive paths, no secrets.
#
# lane runs the plan the preflight makes of a cases file (internal/livetest,
# campaign.go, says what a case is and refuses what it is not), in order,
# past failures. Each case appends a line to <dir>/<provider>.results; the
# lane fails when a case did. Neither file is ever sourced.
set -euo pipefail
umask 077

script_dir=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(CDPATH='' cd -- "$script_dir/../.." && pwd -P)
go_bin=${GO:-go}
# The lane's plan, and its copy of a case's standard output.
plan=
case_output=

usage() {
	printf 'usage: %s prepare <forge-lab checkout> <dir> <forgejo|gitlab>=<cases file> ... [job-image=<flavor>:<local image> ...]\n       %s lane <dir> <forgejo|gitlab> <cases file>\n' "$0" "$0" >&2
	exit 2
}

fail() {
	printf 'campaign: %s\n' "$1" >&2
	exit 2
}

preflight() {
	(cd -- "$repo_root" && "$go_bin" run ./internal/livetest/preflight "$@")
}

prepare() {
	local forge_lab=${1:-} dir=${2:-}
	[[ $# -ge 2 && "$forge_lab" == /* && -f "$forge_lab/justfile" && "$dir" == /* ]] || usage
	shift 2

	# The flavor and image reach command lines, so they are checked first. The
	# preflight runs in the repository; a relative cases file is the caller's.
	local argument flavor image key
	local -a flavors=() lanes=()
	local -A job_images=()
	for argument in "$@"; do
		case "$argument" in
		forgejo=?* | gitlab=?*)
			[[ "${argument#*=}" == /* ]] || argument=${argument%%=*}=$PWD/${argument#*=}
			lanes+=("$argument")
			continue
			;;
		esac
		[[ "$argument" == job-image=*:* ]] || fail "want <forgejo|gitlab>=<cases file> or job-image=<flavor>:<local image>, got $(printf '%q' "$argument")"
		argument=${argument#job-image=}
		flavor=${argument%%:*} image=${argument#*:}
		[[ "$flavor" =~ ^[a-z][a-z0-9-]*$ ]] || fail "a job image flavor is lowercase letters, digits and hyphens, got $(printf '%q' "$flavor")"
		[[ "$image" =~ ^[A-Za-z0-9][A-Za-z0-9._/:@-]*$ ]] || fail "not a local image reference: $(printf '%q' "$image")"
		[[ -z "${job_images[$flavor]:-}" ]] || fail "job image flavor $flavor is named twice"
		flavors+=("$flavor")
		job_images[$flavor]=$image
	done
	((${#lanes[@]} > 0)) || fail 'name the lanes the campaign runs, <forgejo|gitlab>=<cases file>'

	# What is prepared is HEAD's, so a difference from it would be published
	# under HEAD's name and tested by nobody.
	if [[ -n "$(git -C "$repo_root" status --porcelain --untracked-files=all)" ]]; then
		printf 'campaign: the checkout has uncommitted changes, and the campaign is HEAD without them; commit or stash them first:\n' >&2
		git -C "$repo_root" status --short --untracked-files=all >&2
		exit 2
	fi
	bash "$repo_root/scripts/ci/engine-binary.sh" check
	# A runtime image archive per target the lanes' cases need, read before
	# anything is built so a cases file the preflight refuses costs no build.
	local targets
	targets=$(preflight campaign-runtime-targets "$repo_root" "${lanes[@]}") || exit 2
	local commit
	commit=$(git -C "$repo_root" rev-parse --verify HEAD)

	# The directory is the campaign's alone: new, empty or an earlier campaign,
	# whose campaign.env goes first, so a prepare that fails leaves none.
	if [[ -e "$dir" && ! -d "$dir/demos" && -n "$(ls -A -- "$dir")" ]]; then
		fail "$dir is neither empty nor an earlier campaign"
	fi
	install -d -m 0700 -- "$dir"
	chmod 0700 -- "$dir"
	rm -rf -- "$dir/campaign.env" "$dir/demos" "$dir/images"
	install -d -m 0700 -- "$dir/demos" "$dir/images"

	local consumer name
	for consumer in "$repo_root"/test/live/demos/*/consumer; do
		name=${consumer%/consumer}
		name=${name##*/}
		bash "$repo_root/scripts/ci/demo-checkout.sh" "$name" "$dir/demos/$name"
	done

	local -a entries=("CAMPAIGN_COMMIT=$commit")
	local value signer_archive=$dir/images/forge-ci-live-signer.tar
	value=$(bash "$repo_root/scripts/ci/publish-live-image.sh" kaniko-job "$forge_lab")
	entries+=("KANIKO_JOB_IMAGE=$value")
	value=$(bash "$repo_root/scripts/ci/publish-live-image.sh" signer "$forge_lab" "$signer_archive")
	entries+=("FORGE_CI_LIVE_SIGNER_IMAGE=$value" "FORGE_CI_LIVE_SIGNER_IMAGE_ARCHIVE=$signer_archive")
	value=$(just --justfile "$repo_root/justfile" buildah-job-image-archive "$dir/images")
	entries+=("FORGE_CI_LIVE_BUILDAH_JOB_IMAGE_ARCHIVE=$value")
	# Each as FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE_<TARGET>; the scenarios' go image
	# is also FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE.
	local target
	for target in $targets; do
		[[ "$target" =~ ^[a-z][a-z0-9]*$ ]] || fail "not a runtime target: $(printf '%q' "$target")"
		value=$(just --justfile "$repo_root/justfile" runtime-image-archive "$target" "$dir/images")
		entries+=("FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE_${target^^}=$value")
		[[ "$target" != go ]] || entries+=("FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE=$value")
	done
	for flavor in "${flavors[@]}"; do
		value=$(just --justfile "$forge_lab/justfile" --working-directory "$forge_lab" \
			publish-image --account forgeci "nanolinter-$flavor" "${job_images[$flavor]}")
		key=${flavor^^}
		entries+=("JOB_IMAGE_${key//-/_}=$value")
	done

	# Written beside its place, checked as a lane will read it, then moved in.
	local staged
	staged=$(mktemp "$dir/.campaign.env.XXXXXXXX")
	printf '%s\n' "${entries[@]}" >"$staged"
	preflight campaign-env-check "$staged" || {
		rm -f -- "$staged"
		exit 2
	}
	mv -f -- "$staged" "$dir/campaign.env"
	printf 'campaign: prepared %s for %s\n' "$dir/campaign.env" "$commit" >&2
}

lane() {
	local dir=${1:-} provider=${2:-} cases=${3:-}
	[[ $# -eq 3 ]] || usage
	# The preflight runs in the repository; a relative cases file is the caller's.
	[[ "$cases" == /* ]] || cases=$PWD/$cases

	plan=$(mktemp "${TMPDIR:-/tmp}/forge-ci-campaign-plan.XXXXXXXX")
	case_output=$(mktemp "${TMPDIR:-/tmp}/forge-ci-campaign.XXXXXXXX")
	trap 'rm -f -- "$plan" "$case_output"' EXIT
	preflight campaign-plan "$dir" "$provider" "$cases" >"$plan" || exit 2

	local results=$dir/$provider.results kind field count text started status verdict failed=0 ran=0
	local -a args
	while IFS= read -r -d '' -u 3 kind; do
		IFS= read -r -d '' -u 3 field
		case "$kind" in
		env)
			[[ "$field" =~ ^[A-Z][A-Z0-9_]*= ]] || fail "the plan exports $(printf '%q' "$field")"
			export "${field?}"
			continue
			;;
		case) text=$field ;;
		*) fail "the plan has a record $(printf '%q' "$kind")" ;;
		esac
		IFS= read -r -d '' -u 3 count
		args=()
		while ((${#args[@]} < count)); do
			IFS= read -r -d '' -u 3 field
			args+=("$field")
		done

		printf 'campaign: %s\n' "$text" >&2
		started=$(date -u +%Y-%m-%dT%H:%M:%SZ)
		# tee ignores an interrupt, so the run's cleanup output still has a reader.
		set +e
		just --justfile "$repo_root/justfile" "${args[@]}" 3<&- | tee -i -- "$case_output"
		status=${PIPESTATUS[0]}
		set -e
		verdict=$(sed -n 's/^Live verdict: //p' "$case_output" | tail -n 1)
		printf '%s/%s\t%s\texit=%s\tverdict=%s\n' "$started" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
			"$text" "$status" "${verdict:--}" >>"$results"
		ran=$((ran + 1))
		[[ "$status" -eq 0 ]] || failed=$((failed + 1))
		# An interrupted case stops the lane; the operator asked for it.
		if [[ "$status" -eq 130 ]]; then
			printf 'campaign: interrupted; the cases after it are not run\n' >&2
			break
		fi
	done 3<"$plan"
	printf 'campaign: %d of %d cases failed; results in %s\n' "$failed" "$ran" "$results" >&2
	((failed == 0)) || exit 1
}

case "${1:-}" in
prepare)
	shift
	prepare "$@"
	;;
lane)
	shift
	lane "$@"
	;;
*) usage ;;
esac
