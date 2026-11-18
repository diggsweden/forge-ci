#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
# SPDX-License-Identifier: CC0-1.0

# Key-free AAR and producer SBOM handoff. The pinned shared signer completes
# the release bundle; this script neither signs nor publishes packages.
set -euo pipefail
umask 077

: "${RELEASE_TAG:?}" "${RELEASE_VERSION:?}" "${RELEASE_SHA:?}"
test "$(git rev-parse HEAD)" = "${RELEASE_SHA}"
file=()
if [ -n "${RC_ARTIFACTS_CONFIG:-}" ]; then file=(--file "${RC_ARTIFACTS_CONFIG}"); fi

case "${FORGE_CI_BUILD_FAMILY:-}" in
android)
  forge-ci build gradle-android run --library --build-module lib \
    --build-types release --include-aab=false --enable-signing=false --include-date-stamp=false \
    --name-override hello-android-library --sbom-tool-version 3.5.1
  mkdir -p dist
  cp -- lib/build/outputs/aar/lib-release.aar "dist/hello-android-library-${RELEASE_VERSION}.aar"
  cp -- build/reports/cyclonedx/bom.json "dist/hello-android-library-${RELEASE_VERSION}.cdx.json"
  publications=$(forge-ci publish forge-packages android-release --list "${file[@]}")
  while IFS= read -r publication; do
    [ -n "${publication}" ] || continue
    forge-ci build gradle export-publications --project :lib \
      --output "dist/${publication}" --version "${RELEASE_VERSION}"
  done <<<"${publications}"
  ;;
'')
  mkdir -p dist
  forge-ci release notes --changelog-file CHANGELOG.md --stub-missing-section \
    --release-version "${RELEASE_TAG}" --release-commit "${RELEASE_SHA}" \
    --target-file dist/release-notes.md
  publication_files=$(forge-ci publish forge-packages android-release --list-files "${file[@]}" --version "${RELEASE_VERSION}")
  assets=(--asset 'dist/*.aar')
  while IFS= read -r publication_file; do
    [ -n "${publication_file}" ] || continue
    assets+=(--asset "${publication_file}")
  done <<<"${publication_files}"
  forge-ci release files declare "${assets[@]}" \
    --sbom "dist/hello-android-library-${RELEASE_VERSION}.cdx.json"
  ;;
*)
  printf 'build-release.sh: no %s build here\n' "${FORGE_CI_BUILD_FAMILY}" >&2
  exit 1
  ;;
esac
