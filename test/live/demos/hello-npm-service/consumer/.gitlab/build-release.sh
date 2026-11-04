#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
# SPDX-License-Identifier: CC0-1.0

# Release build, called by the pinned release policy in a key-free job. It
# leaves the unsigned hand-off in dist/: the packed package, its CycloneDX
# Build SBOM, release notes, checksums and the manifest. The runtime image is
# the release policy's: its image jobs build .forge-ci/release-images.yml over
# the release checkout with image-context/, the package unpacked, laid on top.
# The policy's isolated version-bump job must commit the package and lockfile
# version first; the unsigned build never repairs a stale release commit.
set -euo pipefail
umask 077

: "${RELEASE_TAG:?}" "${RELEASE_VERSION:?}" "${RELEASE_SHA:?}"
test "$(git rev-parse HEAD)" = "${RELEASE_SHA}"

NODE_VERSION=24 CYCLONEDX_VERSION=6.0.1 forge-ci build npm run --expect-version "${RELEASE_VERSION}"

package="hello-npm-service-${RELEASE_VERSION}.tgz"
test -f "${package}"
# The image is built from the package unpacked, as Forgejo's release-images
# builds it. The build script's dist/ is in the package, and dist/ is the
# hand-off, so the build's copy goes.
rm -rf -- dist image-context
mkdir -p dist image-context
tar -xzf "${package}" --strip-components=1 -C image-context
cp -- "${package}" dist/
cp -- bom.json "dist/hello-npm-service-${RELEASE_VERSION}.cdx.json"

if ! forge-ci release notes --changelog-file CHANGELOG.md \
	--release-version "${RELEASE_TAG}" --target-file dist/release-notes.md; then
	forge-ci release notes --source-file /dev/null/absent --target-file dist/release-notes.md \
		--release-version "${RELEASE_TAG}" --release-commit "${RELEASE_SHA}"
fi

forge-ci release files declare \
	--asset 'dist/*.tgz' \
	--sbom "dist/hello-npm-service-${RELEASE_VERSION}.cdx.json"

printf 'Release handoff files (bytes, path relative to dist):\n'
find dist -type f -printf '%s\t%P\n' | LC_ALL=C sort -k2
