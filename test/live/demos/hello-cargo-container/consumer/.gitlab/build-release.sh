#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
# SPDX-License-Identifier: CC0-1.0

# Release build, called by the pinned release policy in a key-free job, in
# the Buildah job image; the tests ran in the checks. The Containerfile is
# the build: its builder stage compiles the service. It
# leaves the unsigned hand-off in dist/: the binary extracted from the
# Containerfile's export-binary stage, release notes, checksums and the
# manifest. The runtime image is the release policy's: its image jobs build
# .forge-ci/release-images.yml from the release checkout alone.
set -euo pipefail
umask 077

: "${RELEASE_TAG:?}" "${RELEASE_VERSION:?}" "${RELEASE_SHA:?}"
test "$(git rev-parse HEAD)" = "${RELEASE_SHA}"

name=hello-cargo-container

mkdir -p dist
if ! forge-ci release notes --changelog-file CHANGELOG.md \
	--release-version "${RELEASE_TAG}" --target-file dist/release-notes.md; then
	forge-ci release notes --source-file /dev/null/absent --target-file dist/release-notes.md \
		--release-version "${RELEASE_TAG}" --release-commit "${RELEASE_SHA}"
fi

# extract.binary: the export-binary stage of the same Containerfile,
# exported as files, its binary renamed <name>-linux-amd64. The builder
# stage RUNs cargo, so it builds for this job's own platform only.
forge-ci container build --mode local --target export-binary \
	--file Containerfile --context . --platform linux/amd64 \
	--output-dir dist/linux-amd64
forge-ci container suffix-extracted-binaries \
	--binaries-dir dist/linux-amd64 --arch amd64 --expected-names "${name}"

forge-ci release files declare --asset "dist/linux-amd64/${name}-linux-amd64"

printf 'Release handoff files (bytes, path relative to dist):\n'
find dist -type f -printf '%s\t%P\n' | LC_ALL=C sort -k2
