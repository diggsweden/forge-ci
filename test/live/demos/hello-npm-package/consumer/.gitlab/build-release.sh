#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
# SPDX-License-Identifier: CC0-1.0

# Release build, called by the pinned release policy in a key-free job. It
# leaves the unsigned hand-off in dist/: the packed package, its CycloneDX
# Build SBOM, release notes, checksums and the manifest. Forge CI's signer job
# signs and publishes it; the tarball is a file of the release. The release
# requests the version package.json declares; the build refuses another.
set -euo pipefail
umask 077

: "${RELEASE_TAG:?}" "${RELEASE_VERSION:?}" "${RELEASE_SHA:?}"
test "$(git rev-parse HEAD)" = "${RELEASE_SHA}"

NODE_VERSION=24 CYCLONEDX_VERSION=6.0.1 forge-ci build npm run \
	--scope @example-org --expect-version "${RELEASE_VERSION}"

# npm pack names a scoped package <scope>-<name>-<version>.tgz.
package="example-org-hello-npm-package-${RELEASE_VERSION}.tgz"
test -f "${package}"
rm -rf -- dist
mkdir -p dist
cp -- "${package}" dist/
cp -- bom.json "dist/hello-npm-package-${RELEASE_VERSION}.cdx.json"

if ! forge-ci release notes --changelog-file CHANGELOG.md \
	--release-version "${RELEASE_TAG}" --target-file dist/release-notes.md; then
	forge-ci release notes --source-file /dev/null/absent --target-file dist/release-notes.md \
		--release-version "${RELEASE_TAG}" --release-commit "${RELEASE_SHA}"
fi

forge-ci release files declare \
	--asset 'dist/*.tgz' \
	--sbom "dist/hello-npm-package-${RELEASE_VERSION}.cdx.json"

printf 'Release handoff files (bytes, path relative to dist):\n'
find dist -type f -printf '%s\t%P\n' | LC_ALL=C sort -k2
