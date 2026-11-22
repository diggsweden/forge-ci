# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- `package-retention.yml` (Forgejo) can delete: `dry-run: false` applies
  the rule, and `true`, the default, still previews. It takes one package
  as `repository` in place of `packages-json`, and `keep-tags`. Its job is
  now `retention`. A release job that ran `container package-retention`
  itself with the registry token can call it instead; `validate isolation`
  asks no `forge-ci-sha` of that call, since the workflow checks nothing
  out, and still compares its `uses:` pin.
- **Breaking:** Pull request checks are named Quality checks, Build and
  test and PR check results on every forge, and the GitHub orchestrator's
  Summary job is gone. Update the required checks of your branch
  protection when you move your pin.
- **Breaking:** Consumer inputs take one name across forges. Rename yours
  when you move your pin; see "Names that changed" in docs/workflows.md for
  the old and new names.
- **Breaking:** `validate isolation` checks a workflow directory and finds
  the release workflows itself: every workflow with a job that calls one
  of the subject's signer workflows, under any file name; none found fails. `--workflow`, `--sign-job`,
  `--prepare-job` and `--dist-digest-output` are gone, replaced by the
  required `--workflows-dir`, and `--single-pin-subject` is required. The
  sign and prepare jobs are the calls of those workflows, and the job and
  output the sign job takes `dist-digest` from are read from its `with:`
  block. A prepare job built from the caller's own steps is no longer a
  prepare job: it may not hold a signing secret. The sign job is no longer
  required to pass every configured signing secret, and the guessed
  sibling-checkout read of the signer's secrets header is gone. Every
  workflow of the directory, not only the release workflow, is held to the
  signing-secret boundary, and violations are annotated at the file they
  are in. The `check-l3-isolation` action loses its `workflow`, `sign-job`,
  `prepare-job`, `dist-digest-output` and `build-job` inputs; remove them
  from your pull-request workflow.
- **Breaking:** `validate isolation`, and so `check-l3-isolation`, refuses
  a reference to the subject that is not pinned to a full commit: a branch
  (`@main`), a tag or a short commit. Such a reference was skipped by the
  pin check before, so it passed. Pin every reference to the full commit.
- **Breaking:** `validate isolation`, and so `check-l3-isolation`, holds the
  local actions a release runs to the pin and address checks: a reference to
  the subject inside `.forgejo/actions/*/action.yml` must be at the
  directory's one full commit and on the forge of the run. Before, only the
  workflow directory was read for them. Repin your local actions with your
  workflows.
- **Breaking:** `validate isolation --single-pin-subject` matches the
  subject's owner and repository in any case, as forges resolve them:
  `ExampleScope/CI-Layer` is held to the same pin and forge as
  `examplescope/ci-layer`, where it passed as another repository before.
  Give every spelling of the engine the same pin.
- **Breaking:** GitHub's `release-orchestrator.yml` runs `validate
  isolation` over the caller's `.github/workflows` before it reads the
  release configuration, and stops the release on a violation:
  `secrets: inherit` on a release job, a signing key in any job but the
  call of the orchestrator, an engine reference not at the one full commit,
  and the other release-isolation rules. `validate isolation` now reads a
  GitHub workflow directory: a workflow that calls the subject's
  `.github/workflows/release-orchestrator.yml` is a release workflow, and
  that call is the GitHub signer call. Pass the orchestrator its secrets by
  name.
- **Breaking:** `validate isolation`'s defaults are the layer's own:
  `--signing-secret` the `RELEASE_COSIGN_*` and `RELEASE_GPG_*` names
  (`COSIGN_PRIVATE_KEY` and `COSIGN_PASSWORD` are gone),
  `--single-pin-input` `forge-ci-sha`, and `--literal-input` the signer
  image, key digest, transparency-log and `enforce-*` inputs.
  `check-l3-isolation`'s `signing-secrets` defaults to empty, the engine's
  names. A repository with other secret names lists them there.
- **Breaking:** Forgejo's `prepare-release.yml` runs `validate isolation`
  over the caller's `.forgejo/workflows` at the release request commit, in
  its plan job and before any job holds a key, and stops the release on a
  violation, so a direct push or a skipped pull-request gate no longer
  passes the rules by. `--single-pin-subject` also takes the HTTPS URL of
  the engine repository. Make the caller's workflows pass
  `check-l3-isolation` before the next release request.
- **Breaking:** `validate isolation --single-pin-subject`, and so the
  `check-l3-isolation` action, takes a reference for the subject only when
  it names that repository, refuses one that writes another forge address
  than the forge of the run, and refuses a sign job or a signer workflow
  call in another repository. A workflow directory that takes the engine
  from another forge by URL, or signs through another repository than the
  subject, now fails. Take the engine from the forge the job runs on and
  sign through it.
- **Breaking:** `validate isolation`, and so `check-l3-isolation`, refuses
  `secrets: inherit` on every job of the release workflow. The prepare job,
  the sign job and the signer workflow calls passed it before. Pass each
  secret by name.
- **Breaking:** `validate isolation`, and so `check-l3-isolation`, also
  checks each local reusable workflow the release workflow calls, directly
  or through another: no signing secret, no `secrets: inherit`, no persisted
  checkout credential and no `setup-toolchain` cache in its jobs. Before,
  only the release workflow's own jobs were read. Hold those workflows to
  the same rules.
- **Breaking:** `validate isolation --single-pin-subject`, and so
  `check-l3-isolation`, refuses a `uses:` in the release workflow, the local
  workflows it calls or their local actions that is neither a path in the
  repository nor the subject, and a local action that is not a composite
  action. A third-party action, a `docker://` image or a Node action in a
  release job now fails. Move it out of the release, or replace it with an
  engine action.
- **Breaking:** `validate isolation`, and so `check-l3-isolation`, lets a
  release job hold a secret other than the job token and read-only
  `DEPENDENCY_*` credentials, in any step, under any name or through the
  workflow env, only when the job runs the layer's actions alone: no `run:`
  step, no local action and no checkout (`actions/checkout`,
  `checkout-consumer`). A job that runs `forge-ci` or a check of its own
  beside a publish or registry token now fails; pass the token to the
  layer's action instead (`publish-forge-packages`, `registry-login`), or
  move the command to a job without the token. It replaces the rule that
  read only the token variables of `platform checkout` and `release
  validate-tag` steps, and the checkout-command patterns that an archive
  download, a submodule update or a downloaded artifact passed.
- **Breaking:** `validate isolation`, and so `check-l3-isolation`, lets such
  a job run only the layer's actions that run the engine alone, from one
  list that classifies every action: `assemble-release-dist`,
  `check-l3-isolation`, `guard-release-tag`, `pr-check-results`,
  `prune-base-images`, `publish-forge-packages`, `record-release-image`,
  `registry-login`, `setup-forge-ci`, `upload-dist` and
  `verify-forgejo-token`. A build, `lint-nanolinter`, an SBOM action,
  `setup-toolchain`, an image-builder setup, `generate-changelog`,
  `prepare-release` or an action the list does not name beside such a
  secret now fails, as does `publish-forge-packages` publishing Maven by
  `mvn deploy`: publish the build's hand-off with `from-build: true`.
- **Breaking:** `validate isolation`, and so `check-l3-isolation`, refuses a
  job `container:` image or a `services:` image in a release workflow, or a
  local workflow it calls, that is neither pinned by digest (`@sha256:`)
  nor one repository variable (`${{ vars.CI_JOB_IMAGE }}`). An image at a
  tag such as `:latest`, or from an input or the event, passed before. Pin
  it by digest or read it from a repository variable.
- GitHub's `release-orchestrator.yml` skips its isolation check of the
  caller's workflows only when GitHub's `job.workflow_repository`, the
  repository serving the orchestrator, is the caller: the engine's own
  release. `resolve-forge-ci-bootstrap.yml` returns it as
  `serving-repository`. A caller whose `FORGE_CI_REPOSITORY` variable named
  itself skipped the check before; on GitHub Enterprise Server, which lacks
  the context, the check now always runs.
- **Breaking:** `validate isolation`, and so `check-l3-isolation` and the
  release's own check, takes a workflow that calls a release workflow as a
  local reusable workflow (`uses: ./.forgejo/workflows/release.yml`),
  through any number of them, for a release workflow too, and holds it to
  every release rule. A tag-triggered wrapper with its own checkout job
  holding a publish token, or with `secrets: inherit`, passed before. Give
  such a wrapper the release job shape.
- **Breaking:** `validate isolation` takes a workflow that calls any of the
  layer's signer workflows for a release workflow:
  `sign-container-images.yml`, `runtime-images.yml` and
  `base-image-lifecycle.yml` as well as the release entry points. A workflow
  that calls only those was held to the signing-secret rule alone before;
  its other jobs now meet every release rule, so bring them to it.
- **Breaking:** Forgejo's `base-image-lifecycle.yml` removes the staging
  candidate it pushed, in its authenticate job, once the final tag is
  authenticated, with `RELEASE_REGISTRY_TOKEN`, which needs delete permission
  for that (`delete:packages` on GHCR); without it the tag stays, reported.
  Drop the caller's own
  `container base-images cleanup` and the registry token from its completion
  job: it reads `ledger-json` and `parent-base-json` without a secret.

### Added

- `container base-images cleanup --ledger` takes the publication ledger, as
  `base-images promote --ledger` does, in place of `--base-images-json`.
- A Forgejo job takes the layer's binary from the runner's cache when the
  cache holds it, and downloads it from the forge only otherwise, in place
  of one download per job.
- Opt-in Node-free Forgejo Actions caching for `build cargo fetch --cache`.
  Only crates.io archives verified against Cargo.lock are cached, under a
  dependency-only key; a hit needs every locked crate, and the command falls
  back to locked fetching when the cache is unavailable. No action passes the
  flag yet.

## [0.94.3] - 2026-11-20

### Changed

- Consolidate verified native base lifecycles
- Update gommitlint to 0.94.1
- Bootstrap source builds and document opt-in self-release
- Fetch verified parents directly on native builders

### Fixed

- Retry artifact file uploads with bounded backoff
- Explain anonymous and scoped parent pull denials
- Strengthen release read-back and scenario admission
- Bind publication to reviewed full branch refs
- Validate author settings before release side effects
- Retain publication authority across builder cleanup
- Normalize helper URLs and bound artifact finalization
## [0.94.2] - 2026-11-18

### Fixed

- Publish runtime images on GitHub release publication

## [0.94.1] - 2026-11-18

### Changed

- Disable tests for initial rel
- Pin the squashed black-box main
- Initial commit

[0.94.3]: https://github.com/diggsweden/forge-ci/compare/v0.94.2..v0.94.3
[0.94.2]: https://github.com/diggsweden/forge-ci/compare/v0.94.1..v0.94.2
[0.94.1]: https://github.com/diggsweden/forge-ci/compare/v0.93.2..v0.94.1

<!-- generated by git-cliff -->
