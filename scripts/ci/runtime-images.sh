#!/usr/bin/env bash
# SPDX-FileCopyrightText: The Forge CI Authors
# SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

# Repository publication orchestration, not an engine CLI or consumer contract.
# Image operations belong to forge-ci; this script binds this runtime roster to
# the workflow's source commit, registry namespace, run and artifact names.
set -euo pipefail
roster=containers/runtime/images.json
fail() {
	printf 'runtime-images: %s\n' "$*" >&2
	exit 1
}
output() { printf '%s=%s\n' "$1" "$2" >>"${FORGEJO_OUTPUT:?}"; }
digest() { sha256sum -- "$1" | cut -d ' ' -f 1; }

coordinates() {
	: "${RUNTIME_IMAGE_NAMESPACE:?}" "${SOURCE_SHA:?}" "${RUNTIME_RUN_ID:?}"
	[[ "$RUNTIME_IMAGE_NAMESPACE" =~ ^[a-z0-9][a-z0-9.-]*(:[0-9]+)?/[a-z0-9][a-z0-9._/-]*$ &&
		"$RUNTIME_IMAGE_NAMESPACE" != *..* && "$RUNTIME_IMAGE_NAMESPACE" != */ ]] || fail 'invalid registry namespace'
	[[ "$SOURCE_SHA" =~ ^([a-f0-9]{40}|[a-f0-9]{64})$ ]] || fail 'invalid source commit'
	[[ "$RUNTIME_RUN_ID" =~ ^[0-9]+-[0-9]+$ ]] || fail 'invalid run-attempt identity'
	tag="staging-${RUNTIME_RUN_ID}-${SOURCE_SHA:0:12}"
}

selected_roster() {
	jq -ce --argjson platforms "${PLATFORM_MATRIX:?}" '
		if ($platforms | type) != "array" or ($platforms | length) == 0 or
		  (all($platforms[]; type == "object" and (.suffix == "amd64" or .suffix == "arm64")) | not) or
		  (($platforms | map(.suffix) | unique | length) != ($platforms | length))
		then error("invalid platform selection") else . end |
		($platforms | map(.suffix)) as $selected |
		map(.archs |= map(select(. as $arch | $selected | index($arch)))) |
		map(select(.archs | length > 0)) |
		if length == 0 then error("empty runtime selection") else . end' "$roster"
}

row() {
	local target=${1:?}
	selected_roster | jq -ce --arg target "$target" '[.[] | select(.target == $target)] | if length == 1 then .[0] else error("unknown runtime target") end'
}

metadata() {
	local target=$1 arch=$2 repo=$3 file="runtime-$1-arch-$2/runtime-$1-$2.json"
	jq -ce --arg arch "$arch" --arg repo "$repo" --arg tag "$repo:$tag-$arch" '
		select(.arch == $arch and .tag == $tag and
		  (.digest | test("^sha256:[a-f0-9]{64}$")) and
		  .ref == ($repo + "@" + .digest))' "$file" || fail "invalid $target/$arch metadata"
}

download_metadata() {
	local target=$1 arch archs
	archs=$(row "$target" | jq -er '.archs[]')
	while read -r arch; do
		forge-ci artifact download --name "runtime-$target-arch-$arch" --dir "runtime-$target-arch-$arch/"
	done <<<"$archs"
}

# Inspect the retained filesystem using trusted tools, never the candidate's
# shell or sha256sum. A subshell keeps cleanup independent of scanner auth.
verify_runtime_cli() (
	arch=${1:?} ref=${2:?}
	[[ "$arch" == amd64 || "$arch" == arm64 ]] || fail 'unsupported CLI architecture'
	[[ "$ref" =~ ^[a-z0-9./:_-]+@sha256:[a-f0-9]{64}$ ]] || fail 'candidate must be digest-pinned'
	read -r expected _ <".engine/bin/forge-ci-linux-$arch.sha256"
	[[ "$expected" =~ ^[a-f0-9]{64}$ ]] || fail 'invalid recorded CLI checksum'
	work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/runtime-cli-check.XXXXXX")
	trap 'rm -rf -- "$work"' EXIT
	skopeo copy --preserve-digests --authfile "${REGISTRY_AUTH_FILE:?}" "docker://$ref" "oci:$work/layout:candidate"
	forge-ci container layout extract-files --layout-dir "$work/layout" \
		--output-dir "$work/extracted" --paths /usr/local/bin/forge-ci
	[[ "$(digest "$work/extracted/forge-ci")" == "$expected" ]] || fail 'baked CLI does not match source checksum'
)

# A green job from an earlier attempt does not qualify rebuilt candidate bytes.
# Read the successful smoke job's receipt, not a newly computed smoke matrix.
verify_smoke() (
	target=${1:?} arch=${2:?} ref=${3:?}
	work=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/runtime-smoke-check.XXXXXX")
	trap 'rm -rf -- "$work"' EXIT
	forge-ci artifact download --name "runtime-smoke-$target-$arch" --dir "$work/receipt"
	jq -e --arg source "${SOURCE_SHA:?}" --arg target "$target" --arg arch "$arch" --arg ref "$ref" \
		'.source == $source and .target == $target and .arch == $arch and .ref == $ref' \
		"$work/receipt/receipt.json" >/dev/null ||
		fail "candidate $target/$arch does not match its successful smoke receipt; rerun collect and smoke for the rebuilt candidates"
)

case "${1:-}" in
plan)
	coordinates
	[[ "${PUBLICATION_REF:-}" == refs/heads/main ]] || fail 'publication requires main'
	[[ "$(git rev-parse HEAD)" == "$SOURCE_SHA" ]] || fail 'source checkout mismatch'
	jq -e 'length == 7 and ([.[].target] | unique | length) == 7 and
		([.[].name] | unique | length) == 7 and all(.[];
		(.target | test("^(base|full|go|rust|node|java|android)$")) and
		(.name | test("^forge-ci-runtime(-[a-z0-9.-]+)?$")) and
		(if .target == "android" then .archs == ["amd64"] else .archs == ["amd64","arm64"] end))' "$roster" >/dev/null
	selected=$(selected_roster)
	build_matrix=$(jq -ce '{include: [.[] | . as $image | .archs[] | {target:$image.target,name:$image.name,arch:.}]}' <<<"$selected")
	image_matrix=$(jq -ce '{include:.}' <<<"$selected")
	output build-matrix "$build_matrix"
	output image-matrix "$image_matrix"
	output candidate-tag "$tag"
	;;
stage-cli)
	arch=${2:?}
	[[ "$arch" == amd64 || "$arch" == arm64 ]] || fail 'unsupported CLI architecture'
	mkdir -p cli-prebuilt
	read -r expected _ <".engine/bin/forge-ci-linux-$arch.sha256"
	[[ "$expected" =~ ^[a-f0-9]{64}$ && "$(digest "runtime-cli/forge-ci-$arch")" == "$expected" ]] || fail 'CLI artifact does not match source checksum'
	install -m 0755 "runtime-cli/forge-ci-$arch" cli-prebuilt/forge-ci
	;;
verify-cli)
	verify_runtime_cli "${2:?}" "${3:?}"
	;;
verify-smoke)
	verify_smoke "${2:?}" "${3:?}" "${4:?}"
	;;
build)
	coordinates
	target=${2:?} arch=${3:?}
	image=$(row "$target")
	jq -e --arg arch "$arch" '.archs | index($arch) != null' <<<"$image" >/dev/null
	repo="$RUNTIME_IMAGE_NAMESPACE/$(jq -r .name <<<"$image")"
	created=$(git show -s --format=%cI "$SOURCE_SHA")
	forge-ci container multiarch-image build-arch \
		--auth-file "${REGISTRY_AUTH_FILE:?}" --arch "$arch" \
		--source-sha "$SOURCE_SHA" --server-url "${FORGEJO_SERVER:?}" --repository "${FORGEJO_REPO:?}" \
		--image-repository "$repo" --tag "$tag" --name "runtime-$target" \
		--title "$(jq -r .name <<<"$image")" --containerfile containers/runtime/Containerfile --target "$target" \
		--label "org.opencontainers.image.url=$FORGEJO_SERVER/$FORGEJO_REPO" \
		--label "org.opencontainers.image.version=sha-$SOURCE_SHA" \
		--label "org.opencontainers.image.created=$created"
	forge-ci artifact upload --name "runtime-$target-arch-$arch" --dir "runtime-$target-arch-$arch/" --retention-days 1 --if-no-files error
	;;
collect)
	coordinates
	rows='[]'
	selected=$(selected_roster | jq -c '.[]')
	while read -r image; do
		target=$(jq -r .target <<<"$image")
		repo="$RUNTIME_IMAGE_NAMESPACE/$(jq -r .name <<<"$image")"
		download_metadata "$target"
		archs=$(jq -er '.archs[]' <<<"$image")
		while read -r arch; do
			data=$(metadata "$target" "$arch" "$repo")
			ref=$(jq -er .ref <<<"$data")
			read -r checksum _ <".engine/bin/forge-ci-linux-$arch.sha256"
			[[ "$checksum" =~ ^[a-f0-9]{64}$ ]] || fail 'invalid recorded CLI checksum'
			rows=$(jq -c --arg target "$target" --arg arch "$arch" --arg ref "$ref" --arg checksum "$checksum" \
				'. + [{target:$target,arch:$arch,ref:$ref,checksum:$checksum}]' <<<"$rows")
		done <<<"$archs"
	done <<<"$selected"
	smoke_matrix=$(jq -ce '{include:.} | select(.include | length > 0)' <<<"$rows")
	output smoke-matrix "$smoke_matrix"
	;;
assemble)
	coordinates
	# Syft and Trivy use Docker's keychain, not the containers-auth variable.
	# Bridge the same isolated login without putting credentials in argv or
	# artifacts. The engine operations below still take the explicit auth file.
	DOCKER_CONFIG=$(mktemp -d "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/runtime-registry.XXXXXX")
	export DOCKER_CONFIG
	trap 'rm -rf -- "$DOCKER_CONFIG"' EXIT
	install -m 0600 "${REGISTRY_AUTH_FILE:?}" "$DOCKER_CONFIG/config.json"
	target=${2:?}
	image=$(row "$target")
	repo="$RUNTIME_IMAGE_NAMESPACE/$(jq -r .name <<<"$image")"
	archs=$(jq -r '.archs[]' <<<"$image")
	download_metadata "$target"
	# Check every architecture before scanning or publishing a combined index.
	while read -r arch; do
		data=$(metadata "$target" "$arch" "$repo")
		verify_smoke "$target" "$arch" "$(jq -er .ref <<<"$data")"
	done <<<"$archs"
	mkdir -p evidence
	while read -r arch; do
		data=$(metadata "$target" "$arch" "$repo")
		ref=$(jq -r .ref <<<"$data")
		verify_runtime_cli "$arch" "$ref"
		# Scan the native digest, not a tag or a default platform of an index.
		syft "registry:$ref" -o "cyclonedx-json=evidence/sbom-$arch.json"
		# Match the upstream runtime workflow's advisory CVE policy. Scanner
		# execution errors still fail; findings are retained for review.
		trivy image --scanners vuln --severity HIGH,CRITICAL --exit-code 0 \
			--format json --output "evidence/trivy-$arch.json" "$ref"
		cp -r "runtime-$target-arch-$arch" evidence/
	done <<<"$archs"
	forge-ci container multiarch-image assemble --auth-file "${REGISTRY_AUTH_FILE:?}" \
		--source-sha "$SOURCE_SHA" --server-url "${FORGEJO_SERVER:?}" --repository "${FORGEJO_REPO:?}" \
		--image-repository "$repo" --tag "$tag" --name "runtime-$target" --archs "$archs" \
		--manifest-output evidence/index.json
	cp "runtime-$target-dist/runtime-$target.json" evidence/image.json
	(cd evidence && sha256sum sbom-*.json >sboms.sha256)
	bash scripts/ci/runtime-evidence.sh "runtime-evidence-$target" evidence/
	;;
*) fail 'expected plan, stage-cli, verify-cli, verify-smoke, build, collect or assemble' ;;
esac
