#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
# SPDX-License-Identifier: CC0-1.0

# Release-candidate checks in the key-free Android SDK runtime, matching the MR gate.
set -euo pipefail
./gradlew --no-daemon lib:test lib:lintRelease lib:assembleRelease
