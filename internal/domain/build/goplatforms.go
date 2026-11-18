// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package build

// DefaultPlatform is the one build target assumed when none is named: a Go
// or Cargo build's, a container's and an image scan's, on every forge and
// the CLI. The release plan fills it in for an artifact-first Go or Cargo
// artifact, so the GitHub matrix and the GitLab child pipeline take it too.
const DefaultPlatform = "linux/amd64"
