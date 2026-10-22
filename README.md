# Forge CI

[![Tag](https://img.shields.io/github/v/tag/diggsweden/forge-ci?style=for-the-badge&color=green)](https://github.com/diggsweden/forge-ci/tags)

[![License: EUPL-1.2 OR GPL-3.0-or-later](https://img.shields.io/badge/License-EUPL--1.2%20OR%20GPL--3.0--or--later-blue?style=for-the-badge)](LICENSE)
[![REUSE](https://img.shields.io/badge/dynamic/json?url=https%3A%2F%2Fapi.reuse.software%2Fstatus%2Fgithub.com%2Fdiggsweden%2Fforge-ci&query=status&style=for-the-badge&label=REUSE&color=lightblue)](https://api.reuse.software/info/github.com/diggsweden/forge-ci)

[![OpenSSF Scorecard](https://img.shields.io/ossf-scorecard/github.com/diggsweden/forge-ci?label=openssf+scorecard&style=for-the-badge)](https://scorecard.dev/viewer/?uri=github.com/diggsweden/forge-ci)

Forge CI gives a project pull-request checks, builds, publishing and signed
releases on GitHub, GitLab and Forgejo, driven by one Go CLI, `forge-ci`.
You call its workflows from your repository instead of building SBOMs,
signing, SLSA provenance and pinned third-party actions yourself.

- One release flow: push a signed `release-request/vX.Y.Z` tag, and Forge CI
  validates it, creates the final `vX.Y.Z` tag, builds, publishes and signs.
- A single-manifest repository needs no configuration file; add
  `.forge-ci/artifacts.yml` only when you need more.

## Quick Start

Install the CLI, then add two workflows — one for pull-request checks, one
for releases. The same two flows work on GitHub, GitLab and Forgejo.

### Install

```bash
# With mise (manages the Go toolchain):
mise use -g go@1.27.3
go install github.com/diggsweden/forge-ci/cmd/forge-ci@v0.90.0

# Or with Go alone:
go install github.com/diggsweden/forge-ci/cmd/forge-ci@v0.90.0
```

`forge-ci init <repo-url>` writes a project's first CI files;
`forge-ci doctor` checks the setup.

### Pull-request checks

One caller workflow runs lint and security scans on every PR. Add your
own test job alongside it:

```yaml
# .github/workflows/pr.yml
on:
  pull_request:
permissions:
  contents: read
jobs:
  pr-checks:
    uses: diggsweden/forge-ci/.github/workflows/pullrequest-orchestrator.yml@v0.90.0
    permissions:
      actions: read
      contents: read
    with:
      forge-ci-binary-ref: v0.90.0
      project-type: go  # maven, npm, gradle, cargo, ...
```

### Release

Push a signed tag — the flow builds, signs, publishes and creates the
final version tag:

```yaml
# .github/workflows/release.yml
on:
  push:
    tags: ["release-request/v*"]
permissions:
  contents: read
jobs:
  release:
    uses: diggsweden/forge-ci/.github/workflows/release-orchestrator.yml@v0.90.0
    permissions:
      contents: write
      packages: write
      id-token: write  # Sigstore keyless signing
      actions: read
    secrets:
      RELEASE_TOKEN: ${{ secrets.RELEASE_TOKEN }}
      RELEASE_SSH_SIGNING_KEY: ${{ secrets.RELEASE_SSH_SIGNING_KEY }}
    with:
      branch: main
      forge-ci-binary-ref: v0.90.0
```

```bash
git tag -s release-request/v1.0.0 -m "Release v1.0.0"
git push origin release-request/v1.0.0
```

GitLab and Forgejo use the same two flows through their own CI syntax —
see the [examples](examples/README.md) for complete per-forge files.

## Using Forge CI

Start with the [Documentation Index](docs/README.md) for the full map.

- **Examples** • [Ready-made pipelines](examples/README.md) for Go, Cargo,
  Maven, Gradle, npm, Android and a monorepo, on each forge that builds them
- **Forge guides** • [GitHub](docs/github.md) · [GitLab](docs/gitlab.md) ·
  [Forgejo](docs/forgejo.md), in the same section order
- **How-to** • [Publishing](docs/publishing.md) ·
  [Runtime Images](docs/runtime-images.md) ·
  [Artifact Verification](docs/verification.md) · [SBOM](docs/sbom.md) ·
  [Failed Release Recovery](docs/failed-release-recovery.md)
- **Reference** • [Artifacts Reference](docs/artifacts-reference.md) ·
  [Components Reference](docs/components.md) ·
  [CLI Reference](docs/cli-reference.md) ·
  [Secrets, Permissions, and Validation](docs/reference.md) ·
  [Project Reference](docs/project.md)
- **Concepts** • [Workflow Architecture](docs/workflows.md) ·
  [Artifact and Image Flows](docs/flows.md) ·
  [Ecosystems](docs/ecosystems.md) ·
  [Providers and Runners](docs/providers.md) ·
  [CLI Black Box](docs/cli-black-box.md) ·
  [Threat Model](docs/threat-model.md)

## Working on Forge CI

- **[Development](docs/DEVELOPMENT.md)** • Source checkout, installing the CLI
  from it, prerequisites, checks and branch testing
- **[Testing](docs/testing.md)** • Test helpers and conventions
- **[Black-box suite running guide][blackbox-running]** • Local, strict and
  focused compatibility runs
- **[Workflow Design Policy](docs/workflow-design-policy.md)** ·
  **[Scripts](docs/scripts.md)** ·
  **[Architecture Decisions](docs/README.md#architecture-decisions)** ·
  **[Open Questions](docs/open-questions.md)**
- **[Security](SECURITY.md)** • How to report a vulnerability
- **[Changelog](CHANGELOG.md)**

## License

[blackbox-running]: https://github.com/diggsweden/forge-ci-blackbox-tests/blob/main/docs/running.md

The `forge-ci` program, meaning the Go source, is dual-licensed
[EUPL-1.2](LICENSES/EUPL-1.2.txt) **OR**
[GPL-3.0-or-later](LICENSES/GPL-3.0-or-later.txt), at your option.

Everything you copy into your own repository is
[CC0-1.0](LICENSES/CC0-1.0.txt): the reusable workflows, the GitLab Catalog
components in `templates/`, the Forgejo workflows and actions in `.forgejo/`,
the shell scripts, and all configuration, documentation and examples. Lift them
freely. The shell files under `.forgejo/` are the exception: they carry the
program's licence.

See [LICENSE](LICENSE) for the full statement. Every file carries an SPDX
identifier, which is authoritative for that file.
