# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Removed

- **BREAKING:** `artifact upload`, `artifact download` and `artifact digest`
  no longer read `ARTIFACT_NAME`, `ARTIFACT_DIR`, `ARTIFACT_FILES`,
  `ARTIFACT_PATHS`, `ARTIFACT_PATTERN`, `ARTIFACT_MERGE_MULTIPLE`,
  `ARTIFACT_RETENTION_DAYS`, `ARTIFACT_IF_NO_FILES`,
  `ARTIFACT_INCLUDE_HIDDEN` or `ARTIFACT_BLOB_STORE`. `ARTIFACT_NAME` and
  `ARTIFACT_DIR` are project settings for other commands, so a release job
  that set `ARTIFACT_NAME` made `artifact download --pattern` fail with "use
  --name or --pattern". Pass `--name`, `--dir`, `--pattern` and the rest as
  flags. `--run-id` and `--repository` still default to the current run.
- Remove `just bump-runtime-tags`. No workflow or component pins a runtime
  image release any more, so a release cut has nothing to move, and a guard
  refuses one written back. 0.91.x shipped with the v0.90.0 pins it missed.
- **BREAKING:** Remove the `fail-on-warning` input of `lint-swift.yml` and
  `lint swift swiftlint --fail-on-warning` (`FAIL_ON_WARNING`), which had
  no effect: the administrator lint policy decides whether SwiftLint
  findings block.
- **BREAKING:** Remove the container `enable-scan` setting of
  `.forge-ci/artifacts.yml`, which only printed a warning: every exported or
  published image is scanned. Strict parsing now refuses the key as unknown,
  as it does `enable-sbom`, and an `image-plan-json` entry with `scan: false`
  is refused rather than overridden. The config plan loses `enable_scan`, and
  `container build` loses `--scan` (`ENABLE_SCAN`), which push-by-digest and
  oci builds ignored. Delete the key and the flag.
- **BREAKING:** Remove GitLab component inputs that nothing read:
  `artifact-name`, `version` and `project-type` from `publish-apple-appstore`,
  and `artifact-name` from `publish-forge-packages` and `publish-maven-central`.
  Drop them from `include: inputs:`; GitLab refuses an unknown input.
- **BREAKING:** Remove the `cron-schedule` input of
  `security-openssf-scorecard.yml`, which had no effect. Schedule the caller
  with `on.schedule`.
- **BREAKING:** Remove the `runtime-image-java`, `runtime-image-node` and
  `runtime-image-rust` inputs of `release-prepare-stage.yml`, which had no
  effect: version files are prepared on the runner, where a repository can mix
  ecosystems. Remove the `enable-scan` input of `publish-container.yml`, which
  had no effect either: every published layout is scanned. A test now refuses
  any workflow, action or component input that nothing reads.
- **BREAKING:** Replace `release-type` and `derive-release --require-stable`
  with `release-policy: stable|stable-prerelease`, without compatibility aliases.
  Keep workflow, binary and runtime-image versions aligned; select prerelease
  admission in trusted configuration.
- Remove ignored GitHub-style permission declarations from Forgejo workflows,
  examples and live consumers. Automatic-token rights, explicit credentials and
  OIDC behavior are unchanged; the Forgejo guide states their actual boundaries.
- **BREAKING:** Remove the `build-release-image` composite action after migrating
  the Maven, npm and Cargo demos and proving their Forgejo release hand-offs.
  Use `release-images.yml` for native image builds and join its evidence with
  the unsigned file artifact before signing. The direct container CLI remains.
- **BREAKING:** Remove the `build-push-oci-image` composite action. Its remaining
  registry-smoke fixture calls the existing `forge-ci container build-push-oci-image`
  command directly, preserving direct final-tag publication and image/digest
  outputs. Release consumers should use `release-images.yml`.
- **BREAKING:** The GitLab `release` component builds the release images
  itself and no longer takes images the consumer's `build` pushed: a
  `dist/release-images.json` from the build stops the release. With
  `release-images: true` it plans the `containers:` of `artifacts-config`,
  builds each architecture natively in jobs of its own (amd64, and arm64 with
  `build-runner-tags-arm64`), scans and stages them with the job token and
  hands the ledger to signing, as Forgejo's `release-images.yml` does, except
  that no SBOM is pinned before publication: signing generates each image's
  SBOM from the pushed image. The builder is `image-builder: buildah|kaniko`,
  either in `builder-job-image`.
  Migration: drop the `build-push-oci-image` and `ledger add` steps from the
  build script, declare the image under `containers:`, leave the files its
  Containerfile copies in `image-context/`, and set `release-images: true`.
  The Maven, npm and Cargo service demos are migrated. `docs/gitlab.md`.

### Added

- GitHub container and snapshot image workflows select Buildah (default) or
  Kaniko through `image-builder` and an approved digest-pinned job image. Kaniko
  runs in a disposable Docker action; native setup, artifact transfers and
  publication checks stay in the existing hosted workflow.
- Forgejo development image scenarios use the shared builder selector, admit
  compatible native recipes before creating resources, and reject conflicting
  consumer/runtime bindings consistently.
- The GitLab release takes a `labels` input, as the Forgejo release-images
  workflow does: newline-separated `key=value` labels every release image
  carries after the standard ones. A label that would replace one of the
  standard provenance labels is refused at the release request, as are
  `labels` without `release-images`. `container release-images plan` takes
  them as `--label`.
- The hello-go-cli, hello-go-service and hello-gradle demos run on GitHub:
  the go-cli, go-service and gradle-app examples' GitHub workflows now pass
  the runtime images of the release they call, pinned with it, which a
  GitHub trial points at the engine under test.
- The hello-android demo has a GitLab merge-request gate: the kotlin
  nanolinter lint gate and the unsigned build and unit tests in Forge CI's
  Android runtime image, as its nonroot user, with no signing credential.
- The hello-npm-package demo runs on Forgejo and GitLab as well as GitHub:
  a pull or merge-request gate that installs the packed package offline, and
  a release that ships the tarball and its Build SBOM as signed release
  files. Neither layer publishes it to a package registry.
- The hello-cargo-workspace demo runs on Forgejo and GitLab as well as
  GitHub: a pull or merge-request gate, and a release that exports both
  workspace binaries from one Containerfile stage per platform
  `RELEASE_PLATFORMS` selects (`linux/amd64` when unset), with the selected
  image builder, and builds its two runtime images as the tag variants
  `<tag>-worker` and `<tag>-keytool`.
- The four enforcement settings can be set at run time, without rebuilding
  the engine: `--enforce-lint`, `--enforce-vulnerabilities`,
  `--enforce-application-tests` and `--enforce-reproducible-builds`, global
  flags taking exactly `true` or `false`, each given at most once. They have
  no environment variable; the embedded `policy.json` stays the enforcing
  default for every setting not given. `policy show` adds each setting's
  `sources` (`embedded-default` or `flag`), and every decision line ends in
  `source=`. A flag is administrator policy only where the administrator's
  entry point writes it; see `docs/admin-policy.md`.
- Every forge takes the four settings as inputs of what runs the gate,
  passed to the engine as flags. Whoever writes the call can pass them, so
  they belong in the administrator's entry point: a GitLab pipeline execution
  policy (or, weaker, an external CI configuration path) whose own YAML
  includes the gates with the settings as literals; a GitHub organization
  ruleset's required workflow; on Forgejo, protected, code-owner-reviewed
  workflow files. GitLab: `release`, `check`, `nanolinter`, `snapshot-images`
  and the `build-*` components, which assign them as read-only literals of
  the job's script; the `check` job and the release's checks and build jobs
  point both `forge-ci` on `PATH` and `FORGE_CI_BIN` at a wrapper that carries
  them, and print `policy show`. GitHub: the pull-request, release and
  snapshot orchestrators, forwarded to `lint-nanolinter`, `lint-swift`,
  `build-*`, `validate-release-prerequisites`, `publish-container` and
  `snapshot-images`. Forgejo: the `lint-nanolinter` and `build-*` actions,
  `release-images.yml` and `snapshot-images.yml`, and `check-l3-isolation`
  requires each value a job or step passes to be written out. No workflow
  reads them from `vars` or the environment.
- The GitLab `release` component takes `export-files` and `export`: commands
  that export files from a Containerfile stage, run once per selected
  architecture in `Build: export amd64` and `Build: export arm64`, natively,
  in `builder-job-image` with `image-builder`, and laid into the build job's
  `dist/`. A release's binary export no longer runs in the build job, which
  had one architecture and no image builder selection. The request check
  refuses an export without its job image or arm64 runner, and export
  commands without `export-files`.
- The kaniko builder supports `container build --mode local`: it builds the
  target to a private OCI layout and extracts the image's flattened
  filesystem into `--output-dir` through the engine's bounded tar
  extraction, which refuses parent-traversing or absolute entries, escaping
  or absolute symlinks, hardlinks and special files before anything is
  installed. `CI_IMAGE_BUILDER=kaniko` now also covers a release's file
  export, which it was refused for.
- `snapshot-images.yml` takes a `registry` input, the registry host of the
  default image repository (default `ghcr.io`), which the snapshot
  orchestrator passes on.
- GitLab `check` component input `job-user`: the UID or `UID:GID` the job
  runs as, on the Docker and Kubernetes executors; empty, the default, keeps
  the image's user. `job-user: "65532:65532"` runs a runtime image as `nonroot`.
- GitLab `release` component input `job-user`: the UID or `UID:GID` the
  `job-image` jobs run as, such as a runtime image's `nonroot`; empty, the
  default, keeps the image's user. In either component a job stops at its
  start when a variable sets its user to another value than `job-user`, or
  when it runs as another user, and logs `job user: uid=… gid=…`. The
  release jobs in their own images, the build, image build and signer jobs,
  hold that variable to empty; a job's role in the template, not its name,
  says which it is.
- `containers/buildah-job/Containerfile`: a Buildah job image built from the
  digest-pinned Debian base with Buildah, netavark, aardvark-dns and
  fuse-overlayfs from Debian's packages, so image jobs install nothing at run
  time. Like the kaniko job image, it is built and pinned by the organization.
- Administrator policy setting `enforce-reproducible-builds` (strict
  reproducible builds, default `true`). With `false`, missing Maven
  `project.build.outputTimestamp` or Gradle reproducible-archive settings are
  reported with their fix and policy decision without stopping the release.
  Unreadable or malformed build files still block. See `docs/admin-policy.md`.
- A report-only engine that still stops a failed test run now says why: an
  `application-tests result=not-waivable` decision line with the reason (test
  errors are never waived, an unrecognised exit status, output over the capture
  limit). See `docs/admin-policy.md`.
- The GitHub pull-request starters `init` copies carry a `test` job that runs
  the project's tests with pinned actions and no secrets, as the Forgejo and
  GitLab starters already do.
- The release starters explain their trigger, secrets and choices in a header.
- A tutorial, `docs/tutorial-first-pull-request.md`, from a bare Go repository
  on GitHub to a checked pull request with `forge-ci init` and `forge-ci doctor`;
  the docs index lists it under Tutorials and files the signing-secrets guide
  under How-To.
- Signed numbered prereleases use the existing release pipeline with
  `release-policy: stable-prerelease`, including release evidence and retry
  behavior. Prereleases do not move stable image aliases or promotion stages;
  GitLab identifies them in release notes because it has no native prerelease flag.
- Declared in-progress snapshot containers publish by default on GitHub, Forgejo
  and GitLab; `publish-containers: false` explicitly opts out. Shared planning,
  native architecture staging, scan/SBOM policy and exact digest receipt collection
  use a frozen source SHA. Container tags always include the original branch hash
  and end in lower-case `-snapshot`; package naming is unchanged. GitHub integrates
  after artifact builds and reuses their exact uploads. Standalone GitHub and
  native Forgejo/GitLab automatically build required Go, Cargo, Maven, Gradle JVM
  and npm artifact-first producers; prepared native contexts are optional advanced
  overrides. Shared Go commands validate producer sets and safely restore confined
  inputs with source-bound digests and canonical `0644`/`0755` string modes across
  ZIP transport. Npm tarballs use isolated per-artifact context destinations.
  Snapshot OCI signing/provenance, build secrets and extraction remain unsupported.
  Minimal three-forge snapshot acceptance is pending operator execution.
- Package retention accepts repeatable `--keep-digest` roots, preserving old
  consumer pins and their platform/signature closure. A missing pin refuses the
  pass. A weekly/manual opt-in example previews a caller's reviewed runtime-image
  policy without deleting packages.
- GitHub's standalone and multi-artifact version preparation run native project
  tools in a separate key-free job. Signing reuses version-only patch validation;
  `version bump-plan --patch --patch-sha256` applies those prepared changes without
  constructing native-tool adapters.
- GitLab `release` takes `build-runner-tags`, the runner tags of its build job
  alone, so an image build can run where user namespaces are allowed while the
  signing jobs stay on untagged runners. Empty, the default, changes nothing.
- Forgejo artifact reads retry HTTP 500 with the shared bounded backoff policy.
  Recovery preserves evidence verification; uploads and finalization remain
  single-attempt, and exhausted reads retain artifact/file diagnostics.
- Runtime evidence uploads are downloaded and checked against the original
  file inventory before assembly succeeds. Forgejo download errors identify
  the artifact and failing entry without exposing signed URLs or response bodies.
- Successful native smoke jobs record their exact candidate digest. Assembly
  requires matching source, flavour, architecture and digest receipts before
  scanning or publishing an index, rejecting stale smoke results after rebuilds.
- Runtime publication supports private source repositories and private image
  packages. Source operations use the forge job token; final verification
  authenticates to the registry and checks the promoted tag's exact digest.
- A configurable Forgejo publication example for the complete runtime family
  to a reviewed GHCR namespace. Reuse platform planning, native runner routing/lending and
  the Buildah/kaniko selector; smoke-test exact candidate digests before
  assembling, signing and promoting source-commit tags. Keep signing keys
  out of source builds and candidate execution. Preserve per-platform SBOMs,
  advisory vulnerability reports and signed SLSA provenance.
  Both runtime publishers default to amd64 and arm64; `RELEASE_PLATFORMS`
  can narrow that selection. Android remains amd64-only.
- Shared runtime smoke checks for the GitHub and Forgejo publishers, with
  tests keeping their flavour inventories aligned. Document the example's
  publication boundaries and evidence needed for future forge migrations.
- The Go runtime image supplies GCC, libc development headers and gettext
  for CGO, race tests and translation checks. Its Go toolchain and the live
  Go consumer fixtures use 1.27.3; runtime smoke checks exercise the race
  detector and require all four gettext catalog tools.
- `container release-images plan` without an `artifacts.yml` releases the
  repository's own image from its root `Containerfile`, so the simple case
  needs no declaration wherever a layer calls the plan (Forgejo's
  `release-images.yml` today). An `artifacts.yml` may now declare only
  `containers:`, for a repository that builds its binaries itself.
- `release-images.yml`, a Forgejo reusable workflow that builds the release
  images `artifacts.yml` declares with the organization's image builder,
  Buildah or kaniko: one native build per container and architecture to an
  OCI layout, without registry writes or secrets, then a publish job that
  scans every architecture, uploads only when all pass, and records the
  index under its staging tag for the signer. Each image carries the
  standard labels `build-release-image` set: source, revision, title,
  version, url, documentation, ref.name and created.
  `docs/forgejo.md#release-images`.
- `container release-images plan` turns the declared containers into that
  workflow's build and publish matrices, and refuses what it does not carry
  out before any build. A release publishes to one repository, the one the
  signer is bound to: `--image-repository` (the workflow's
  `image-repository` input), or the forge's own registry when empty.
  Containers are tag variants there, told apart by `tag-suffix`; the plan
  refuses an `image-name`.
- `multiarch-image assemble --from-layouts --scan-output-template` scans every
  staged architecture with the container publication gate before the first
  upload.
- Container `image-name` and `tag-suffix` in `artifacts.yml`, so two
  containers can publish tag variants of one repository.
- `setup-image-builder` prepares whichever image builder `CI_IMAGE_BUILDER`
  selects, Buildah's storage or the digest-pinned kaniko job image, so an
  image-build job names no builder. The signer example uses it.
- `multiarch-image build-arch` takes `--target`, `--build-arg` and `--label`,
  with both builders. With `--stage` it needs no `--auth-file`: a staged
  build writes no registry, and without one it pulls its bases anonymously,
  never with a login the runner keeps.
- Runner lending: `forge-ci platform request-runner` asks a lender for one
  ephemeral runner before a job that builds container images, for a runner
  pool that denies the user namespaces rootless Buildah needs. The GitHub
  lender dispatches `examples/runner-lender/github-actions/lend-runner.yml`,
  which lends a GitHub-hosted VM to the Forgejo instance for exactly one job
  through `scripts/runner-lender/lend-forgejo-runner.sh`; `none` is the
  installation whose container runners exist already. `docs/runners.md`.
- `setup-buildah` names the runner, the variable that moves the job, and
  `CI_IMAGE_BUILDER=kaniko`, when Buildah is refused a user namespace.
- kaniko as a second image builder, `--image-builder kaniko`, for `container
  build` and `multiarch-image build-arch` on a pool that denies user
  namespaces; the Forgejo examples select it by the builder type
  `CI_IMAGE_BUILDER` (`buildah` when unset) and run it in the job image
  `CI_IMAGE_BUILDER_JOB_IMAGE`. It builds to an OCI
  layout for the same digest push, scan and ledger; load and local modes and
  build secrets are refused by name. `containers/kaniko-job/Containerfile` is
  the job image such jobs run in, and
  `examples/forgejo/kaniko-job-image.yml` its bootstrap, run by dispatch
  once the pin's engine binary is published. `docs/runners.md`.
- `container release-images build` and `publish` run the Forgejo
  release-images workflow's build and publish steps for a forge without a
  build matrix, from the plan file `release-images plan --output` writes;
  `--runner-archs` refuses a planned architecture no job builds.
- The GitLab release and image-development live scenarios build with kaniko
  under `FORGE_CI_LIVE_GITLAB_IMAGE_BUILDER=kaniko`, without the privileged
  runner. The harness labels the kaniko job image archive with the layer it
  serves, so the release admits it. `docs/testing.md`.

### Fixed

- GitLab source-only gate components explicitly replace scheduling, artifact
  dependency, cache and checkout controls from an earlier same-named job. Policy
  inclusion selects when gates apply, including merge-request pipelines;
  arbitrary job mappings still merge, so
  administrator-controlled policy injection remains the authority boundary.
- The GitLab guide requires *Minimum role to use pipeline variables* set to
  *No one allowed*. A pipeline variable outranks GitLab's predefined
  variables, so whoever could set one could point `CI_SERVER_URL`,
  `CI_API_V4_URL` or `CI_REGISTRY` at another host, where the release jobs
  send `RELEASE_TOKEN` and `RELEASE_REGISTRY_TOKEN`.
- `container image-evidence` names the platform it scans to Trivy with
  `--platform`, as the scan gate does, for each platform's layout and for a
  registry image pulled by digest, so Trivy reads that platform's image and
  no other.
- The snapshot live scenarios and the GitLab base-image fixture build with
  the run's image builder instead of Buildah regardless. On a lab without a
  privileged runner they failed asking for one, though the snapshot flow
  builds with kaniko on Forgejo and GitLab. They now push the kaniko job
  image under kaniko, and the base fixture builds in the release's export
  jobs with `multiarch-image build-arch --stage`. Forgejo scenarios select
  with the new `FORGE_CI_LIVE_FORGEJO_IMAGE_BUILDER`. A lab that cannot run
  the selected builder reports them not covered. `docs/testing.md`.
- A live run that asks for `FORGE_CI_LIVE_CONSUMER_VISIBILITY=private`
  makes every repository holding the code under test private. The snapshot,
  migration and diagnostic consumers were public whatever the run asked.
  Engine layers, job images and probe assets stay public in every run, with
  a recorded reason, as the public forge-ci distribution they stand in for.
  A guard refuses a literal visibility, a public repository without a
  reason, or a creating request outside the helpers.
- The GitLab `release` component accepts an `artifacts-config` under
  `.forge-ci/`, such as the default `.forge-ci/artifacts.yml` named
  explicitly. Its regex refused a path that starts with a dot, so GitLab
  failed the policy pipeline as it created it and a release with images
  never started its child pipeline.
- The `job-user` input of the GitLab `check` and `release` components refuses
  a UID or GID with a leading zero, such as `065532`. It passed the input check
  and then never equalled what `id -u` prints, so the job failed its own user
  check after it started. Write the number without the zero.
- The GitHub image workflows, `publish-container.yml` and
  `snapshot-images.yml`, build with Buildah only and now refuse a
  `CI_IMAGE_BUILDER` other than `buildah` before they build, instead of
  building with Buildah regardless.
- The hello-cargo-container demo's release exports its binary with the
  selected image builder, one native job per platform `RELEASE_PLATFORMS`
  selects (`linux/amd64` when unset), on Forgejo and GitLab, and builds its
  image for the same platforms. It used Buildah on amd64 whatever
  `CI_IMAGE_BUILDER` said, so a kaniko release failed on a pool that denies
  user namespaces. A guard refuses a shipped step that names Buildah outside
  the builder selection, and a demo container step with a hard-coded
  platform.
- A GitLab project, group or instance variable no longer changes a policy
  value. These variables outrank a job's own `variables:`, so a consumer
  project's maintainer could replace the allowed request signers, the Cosign
  key digest, the publication project and image repository, the engine
  project, base-image and package signing, the release branch, the version
  bump, asset retention, Sigstore transparency, the release Git URL and host
  key, the bot identity, the changelog settings or the consumer-project check
  of `release`. The same went for the engine commit and project, image
  builder, image repository, branch and job images of `snapshot-images`, the
  engine project of `check`, and the lint floor of `nanolinter`. Each job's
  start now assigns these from its inputs as read-only literals. The runner
  settings that must stay variables, `RC_JOB_USER`, `GIT_STRATEGY` and
  `GIT_SUBMODULE_STRATEGY`, are refused when they differ from their
  literals. The generated snapshot jobs pass their image, architecture and
  context job as arguments.
- The Go, Cargo and Gradle workflows cached `~/go`, `~/.cargo` and
  `~/.gradle`, so a `runtime-image` keeping them elsewhere missed every
  cache without failing. They now ask the image (`go env GOMODCACHE
  GOCACHE`, `CARGO_HOME`, `GRADLE_USER_HOME`) where the caches are.
- A job running as the runtime images' `nonroot` user could not install the
  Rust toolchain, components or targets its `rust-toolchain.toml` pins, nor
  an Android platform or build-tools the image lacks: rustup's and the
  Android SDK's homes were root's. `nonroot` now owns both, as a rustup home
  belongs to its user. Corepack is already enabled; `corepack enable` needs
  `--install-directory /opt/job-tools/bin` as `nonroot`.
- The build, publish, SBOM, changelog, version-bump and prerequisite
  workflows defaulted `runtime-image` to a fixed upstream image and release,
  so a fork or a later release ran upstream's older image. Each now resolves
  its image like the orchestrators, from a new `forge-ci-binary-ref` input
  (empty: the current stable release) and the serving repository's image
  namespace, and the orchestrators hand it their resolved ref.
- `release publish-self-runtime-cli` requires `--allowed-repository`
  (`FORGE_CI_REPOSITORY`) instead of falling back to the upstream repository.
- A GitLab `release` job's release secrets, home and hand-off no longer
  follow its name. The job start read `CI_JOB_NAME`, which a project, group
  or instance variable can set like any other: one could give a key-free
  job a signer's secrets, or a signer the consumer jobs' home. Each job now
  opens its `before_script` with its role, and a producer its `after_script`
  with its hand-off directory, literals of the template.
- Container scans no longer take settings from the job or the checkout. Trivy
  read every `TRIVY_*` variable, and a forge injects project and group
  variables into each job, so one such variable could skip directories, swap
  the vulnerability database, add an ignore policy or VEX statements, and pass
  the gate. It also loaded `trivy.yaml` and `.trivyignore` from the checkout,
  around the rule that approvals carry a reason and expiry. Trivy now gets only
  the path, home, cache, registry-credential, proxy and CA variables; the
  multi-arch build passes each leg's platform as `--platform` instead of
  `TRIVY_PLATFORM`. It runs in an empty directory with configuration loading
  disabled, and the ignore file reaches it by absolute path.
- SBOM scans no longer take settings from the job or the checkout either.
  Syft read every `SYFT_*` variable and `.syft.yaml` from the checkout, so a
  project variable or a committed file could exclude paths or catalogers from
  an SBOM the engine signs. Syft now gets the same variables as Trivy, an
  explicit empty configuration and an empty working directory; relative
  targets and outputs resolve against the caller's directory first.
- Image paths derived from the repository owner or name are lowercased, as
  an OCI repository path requires: the snapshot image default for a
  non-GHCR registry, the publish build cache, the bootstrap runtime-image
  namespace, and the runtime images, build cache and release images of the
  repository's own workflows. A test refuses an unlowercased one.
- The MegaLinter GitLab component no longer ends the job when its
  best-effort SAST conversion fails; GitLab's errexit used to stop the
  script there. `just test-fuzz-all` and `just upgrade-list` now fail when
  `go list` or `go test -list` fails instead of skipping the package.
- The `install-forge-ci` action installs the binary of the repository it
  comes from, and trusts that repository's signer. It ran the installer
  without naming a repository, so a fork's action installed upstream's
  binary. A new `repository` input overrides the default; pass it where
  the action's own repository is unknown, as on GitHub Enterprise Server.
- Bootstrap installers reuse a tool already on `PATH` only when it reports the
  pinned version; any other version is replaced by the pinned,
  checksum-verified download, so a runner's older Trivy no longer decides the
  scan gate.
- GitHub workflows called from a fork install that fork's engine, bootstrap
  scripts and signing identity. `FORGE_CI_REPOSITORY` now defaults to the
  repository serving the called workflow (`job.workflow_repository`) instead
  of `diggsweden/forge-ci`; the variable still overrides it, and GitHub
  Enterprise Server, which lacks that context, must set it.
- A report-only engine can waive the assertion failures of a large test suite.
  The summary was parsed from at most 8 MiB of captured output, so a larger
  suite was never waivable, and Go's stderr could land inside a `go test -json`
  event and fail the parse. Output is now classified line by line while the
  tests run, stdout and stderr each split on their own; only a line over 1 MiB
  is refused. See `docs/admin-policy.md`.
- `forge-ci init` on Forgejo and GitLab writes the release's changelog
  configuration to `.forge-ci/gitcliff/`, which their releases read and stop
  without, keeping either file the repository already has, and puts only
  workflow files in `.forgejo/workflows/`; the
  go-service example's `Containerfile.example` no longer lands there.
- The examples build the same Go main package in pull requests and releases
  on every forge, and the GitLab cargo-app builds name their `platforms`,
  so the toolchain pin they need is visible where they are included.
- A Cargo build whose working-directory is a workspace member builds that
  member. Cargo metadata lists every member wherever it runs, and the
  build took the first one, so a second member's build asked cargo for a
  binary it could not see from that directory.
- The GitLab cargo-app example and its README say the toolchain pin must
  list `components = ["rustfmt", "clippy"]`. Its check job runs both, and a
  pin with the `minimal` profile installs neither, so the job failed with
  "'cargo-fmt' is not installed". They also say the pin must list the target
  of every platform the builds select (Linux amd64 and arm64 by default on
  GitLab); a missing one fails with "can't find crate for `std`".
- The GitLab go-cli example no longer passes `skip-tests: true` to
  `build-go`. The engine refuses it, because an administrator may require
  tests, so a merge request copied from the example failed before building.
  A guard keeps every example from skipping tests.
- A run artifact downloaded on Forgejo, or restored from the blob store, now
  gets the job's ordinary file mode instead of a private 0600. An image
  built from a downloaded context copied those files unreadable to its
  non-root user: Nanolinter's release images could not read their own
  linter defaults.
- Kaniko image builds read the ignore file a Buildah build reads: the
  Containerfile's own `<Containerfile>.containerignore`, else its
  `.dockerignore`, else the context's `.containerignore`, else its
  `.dockerignore`. Kaniko reads only `.dockerignore` names: a root
  `.containerignore` was not applied, and a release Containerfile's own
  `.containerignore` lost to a root `.dockerignore`, under which kaniko's
  `COPY` of the prebuilt binary only the release allowlist admits silently
  copied nothing. A `.containerignore` is now handed to kaniko beside a staged
  copy of the Containerfile.
- Buildah image builds name a Containerfile's own ignore file
  (`<Containerfile>.containerignore`, else `.dockerignore`) with
  `--ignorefile`. Buildah 1.40 and later already prefer it over the context's,
  but Buildah 1.39 (Debian 13, the Forgejo runner's) reads only the context's,
  so a release image whose own allowlist admitted its prebuilt binary failed
  at `COPY` there while building locally.
- The Forgejo and GitLab consumer trials wait up to 150 minutes for the
  consumer's run, and a live test binary may run 3 hours. A release that
  rebuilds its base images (Nanolinter's, on one runner per architecture)
  ran past the former 75 minutes and the trial failed with the run still
  building.
- A re-run of a failed job that uploads to the `--blob-store` no longer takes
  its own blob artifact from the failed attempt as stored on sight; Forgejo
  lists an artifact from its first file on, so that copy may be partial. The
  job uploads it again, and keeps the earlier copy only once it downloads and
  verifies, so a partial blob fails the job instead of the later publish.
- Forgejo's `lint-nanolinter` action and the Forgejo guide no longer claim the
  pull-request lint installs nothing: it fetches the checksum-pinned forge-ci
  engine that applies the admin policy. With `offline: true` and no
  `FORGE_CI_BIN`, a failed fetch now says to provide that build.
- A Go test that panics, hits a runtime fatal error or times out is no longer
  counted as an assertion failure a report-only policy may waive. The crash
  ends the package's test binary, so the tests after it never ran.
- The Maven build gates also refuse fail-never (`-fn` or `-fail-never`,
  after one dash or two), every spelling of a `-D`/`-define` property,
  `-Dmaven.test.skip.exec`, rerunning failed tests, and the test-selection
  properties (`test`, `it.test`, `groups`, `excludedGroups`, the include and
  exclude files, relaxing `failIfNoSpecifiedTests`) in `MAVEN_CLI_OPTS`.
  Each let a build skip, ignore or narrow its tests and still be logged as
  an enforced, clean test run.
- Forgejo's `build-go` and `build-cargo` actions install the Build SBOM
  generator themselves, pinned by version and checksum at the version of
  forge-ci's runtime image: `cyclonedx-gomod` v1.10.0 and `cargo-cyclonedx`
  0.5.9 (a `.tar.xz`; the runner needs `xz`). They had required it from the
  consumer's mise config, which the examples did not say how to supply, so a
  copied Go release failed with "cyclonedx-gomod not found in $PATH". One
  already on `PATH` is still used. A missing `cyclonedx-gomod` now says how
  to bring it or turn the Build SBOM off.
- The monorepo example's Nanolinter plan lists only checks that run at the
  repository root. It had also blocked on `java-checkstyle`, `node-eslint` and
  `node-format`, which find no `pom.xml` or `package.json` at a monorepo's
  root and only reported "skip", so the projects looked linted when they were
  not.
- `artifact upload` and `artifact download` no longer cut a large file off
  after 30 seconds. File bodies on Forgejo and GitHub are bounded by progress:
  a transfer fails only once no bytes move for the HTTP timeout
  (`FORGE_CI_HTTP_TIMEOUT`, default 30s), and the error says it stalled. They
  were bound by the API client's whole-request timeout, so any body larger
  than the link moved in that time failed, as a large OCI layout blob did.
- Release asset uploads on GitHub, Forgejo and GitLab, the read-back of
  published assets for an immutable release, the self-runtime asset snapshot,
  Maven registry uploads and the Central bundle upload are bounded by progress
  the same way, so a large file is no longer cut off by a fixed wall-clock
  limit. Other API requests keep the whole-request timeout, now applied by the
  client's transport.
- `artifact upload` on Forgejo says what an HTTP 413 means: the forge refused
  the upload as too large, which on Forgejo is the repository owner's storage
  quota, shared by packages and Actions artifacts, or a proxy's request limit.
  It read only "HTTP 413: validation failed", which pointed at the file.
- The GitLab `nanolinter` and `megalinter` components trust the checkout
  themselves, by one block a guard keeps identical in both
  (`safe.directory` through `GIT_CONFIG_*`, beside the project's own
  entries). The lint image's user does not own the runner's checkout, so
  every merge request without hand-set `safe.directory` variables failed
  with "cannot fetch the merge request's target branch".
- The Forgejo examples' release builds pass `setup-toolchain` a read-only
  `DEPENDENCY_GITHUB_TOKEN` as `mise-github-token`. maven-app, cargo-app and
  go-cli passed neither a token nor `mise-locked`, so a copied release refused
  the repository's mise config or built without the JDK or Rust toolchain;
  go-service's `mise-locked` needs a lock URL for every tool, which tools from
  a `forgejo:` backend never get.
- The go-cli and Sigstore-keyless GitHub examples pass `bot-name` and
  `bot-email` with `RELEASE_SSH_SIGNING_KEY`. An SSH key carries no identity,
  and `bot-email` is the principal `.forge-ci/allowed_signers` must name, so a
  copied release could not make its signed release commit.
- The Gradle Artifact SBOM layer scans the JARs a release job downloaded under
  `release-artifacts/`, as Maven's does, before `build/libs/`. A GitHub Gradle
  release failed in "Publish: release files" with "no Gradle artifacts found
  in build/libs" although the build had uploaded its JAR.
- GitHub `build-npm` and `build-cargo` run the build from the workspace root.
  They also ran it inside `working-directory` and passed the same directory to
  the binary, so a project below the repository root (a monorepo package) was
  looked for at `<dir>/<dir>` and failed.
- Bootstrap source installs build a detached Git checkout instead of resolving
  a commit through a cached moving Go module tag. Host jobs bind the binary to
  their bootstrap checkout, support full SHA-1/SHA-256 IDs and relative install
  directories, and forward the selected revision to Swift lint jobs.
- GitHub binary defaults resolve one concrete published stable release tag,
  with matching default runtime-image references and preserved explicit
  overrides. Self Release continues to use its exact request commit.
- Pin PR and release compatibility gates to the upstream companion suite's
  current release-policy, SSH handshake and registry-authority tests.
- PR black-box CI explicitly rebuilds the exact source checkout and passes that
  binary to the companion suite. Go's race-test budget accommodates cold native
  engine rebuilds on two-core runners while retaining bounded fixture timeouts.
- Reject per-platform inventory filename collisions before scans or registry
  writes, including inventories in different local directories that would share
  one public release URL.
- The opt-in package-retention example uses its pinned published engine directly,
  so it can preview a consumer's policy without Forge CI source or a Go compiler.
- Prepared version-bump plans retain changelog destination admission. A symlinked
  or nonregular changelog now refuses before either the native version patch or
  any changelog write, rather than losing that refusal when native tools are skipped.
- The go-service Forgejo example uses `release-images.yml` with a separate
  non-extracting declaration and prebuilt versioned binaries. Native image
  builds run without registry tokens and join their ledger before signing.
  Its toolchain install uses committed `mise.lock` inputs instead of requiring
  a GitHub token.
- `platform checkout --sparse` checks out a private repository. It made a
  partial clone, whose deferred blob fetch during the checkout carried no
  credential and failed with "could not read Username"; `release-images.yml`
  could not publish a private repository's images.
- The Forgejo tag deleter refuses an image that is not on the server's own
  registry. The package API knows packages by owner and name alone, so a
  GHCR ref deleted through it removed a same-named Forgejo package's tag.
- `setup-image-builder` admits the kaniko job image when the layer's
  repository arrives as a validated clone URL ending in `.git`.
- Emit index SBOM evidence paths as a multiline CI output.
- The Forgejo signer-image example fetches the engine without credentials.
  `actions/checkout` sent the policy repository's job token, which Forgejo
  refused for the engine repository with 403.
- A release host-key pin that is not the `SHA256:<base64>` fingerprint
  alone, such as one copied with its host and key type, is refused by name.
  It was reported as a fingerprint mismatch naming the same fingerprint.
  The same fingerprint with its `=` base64 padding is accepted.
- An SSH signing key pasted into a forge secret with CRLF line ends is
  accepted; OpenSSH refused it as "error in libcrypto". A key that still
  fails is refused before anything is written, naming the expected format
  or that it is passphrase-protected.
- `version render-changelog` refuses git-cliff on a SHA-256 repository,
  naming git-chglog, instead of failing inside git-cliff with "unknown object
  format 'sha256'". It also refuses a changelog or commit-body config that
  does not exist; git-cliff rendered its built-in config instead.
- A failing `buildah info` reports Buildah's own message. `container
  setup-buildah` once failed a hosted runner with only "exit status 1",
  leaving the storage refusal unexplained.

### Changed

- **BREAKING:** GitHub container image workflows keep the checkout in `source/`
  and require context and Containerfile paths relative to that checkout. Use
  `.` or `services/api` for the context. Absolute workspace-root paths and paths
  escaping the checkout are refused before artifact downloads or image builds,
  keeping engine control payloads outside the application context.
- **BREAKING:** `PACKAGE_NAME` is split in two. `--name` of `publish npm
  deploy` and `publish npm validate-version` reads `NPM_PACKAGE_NAME`, and
  `report publish google-play --package-name` reads `ANDROID_PACKAGE_NAME`.
  The one name meant an npm package to one command and an Android
  application id to the other, so a job that set it for one handed it to
  the other. `PACKAGE_NAME` is no longer read; rename it in your job env or
  pass the flag.
- **BREAKING:** An empty GitLab `release-platforms` exports linux/amd64
  alone; `build-runner-tags-arm64` no longer selects linux/arm64 for the
  export jobs, it only serves it. The images still build each container's
  declared platforms, linux/amd64 when it declares none, as on every forge.
  A release whose containers declare linux/arm64 and that exports files is
  refused at the release request until `release-platforms` names the
  platforms, so the images and the exports build the same architectures.
- **BREAKING:** The GitLab policy components (`check`, `nanolinter`,
  `megalinter`, `release` and `snapshot-images`) require `server-url`, the
  instance's `https://host[:port]`, and `release` and `snapshot-images` also
  `registry`, its container registry `host[:port]` (empty when the project has
  none). A project or pipeline variable outranks the predefined
  `CI_SERVER_URL`, `CI_API_V4_URL` and `CI_REGISTRY`, so it could send the job
  token, `RELEASE_TOKEN` or `RELEASE_REGISTRY_TOKEN` to another host. Each
  job's start now refuses to run when `CI_SERVER_URL` or `CI_REGISTRY` is not
  the literal, sets `CI_API_V4_URL` and `CI_REGISTRY_IMAGE` from the literals,
  and sends tokens only there. The registry is stated, not derived: GitLab
  configures it apart from the web host. Add both inputs to the policy's
  includes. "No one allowed" for pipeline variables stays as defence in depth.
- **BREAKING:** `nanolinter-image` is required and admits only a digest pin,
  `<image>@sha256:<64 hex>`, on the GitLab `nanolinter` component, on
  `lint-nanolinter.yml` and, with `lint-engine: nanolinter`, on
  `pullrequest-orchestrator.yml`. The component and the leaf workflow
  defaulted to the `core` tag and the orchestrator derived the flavour tag
  from `project-type`, so the lint job, which receives the checkout and the
  run context, ran whatever the registry served. There is no default: no
  served digest can be reviewed for every mirror, as with `runtime-image`.
  `plan pr` refuses a tag in the plan job and names the flavour for the
  project type. Pin the flavour image by its digest. This repository's own
  `.gitlab-ci.yml` no longer self-tests the component with a tag.
- **BREAKING:** The `runtime-image` input of the GitLab `build-*`,
  `publish-forge-packages` and `publish-maven-central` components admits only
  a digest pin, `<image>@sha256:<64 hex>`, as GitLab checks it before any job
  runs. It took any text, though its description asked for a digest, and the
  job container receives the checkout and the job token. Pin the image by
  its digest. The components' Consume headers now show the input.
- **BREAKING:** A gate refuses to run when its environment has a variable
  named like an administrator setting: `ENFORCE_LINT`, or any name ending in
  `_ENFORCE_LINT` such as a GitLab project variable `FORGE_CI_ENFORCE_LINT`,
  and likewise for the other three. Such a variable was ignored before; it
  never set the policy. Remove it, and have the administrator's caller pass
  the flag.
- The GitLab gate components, `nanolinter`, `megalinter`, `check` and the
  `build-*` components, state `allow_failure: false`, `before_script`,
  `script` and `after_script` on their job. A job of the same name in a
  file included before the component, such as a consumer's merge-request
  file in the policy, is merged under it and can no longer make the gate
  optional or replace what it runs. Include the consumer's file first.
- **BREAKING:** The decision line of every gate starts with
  `Enforcement policy default-sha256:<digest>:` instead of
  `Admin policy sha256:<digest>:`, and `policy show` names the digest
  `default-sha256` instead of `sha256`: it identifies the embedded default
  only, not the effective policy. A reader of the old line should accept
  both; the live-run reader does.
- **BREAKING:** The GitLab `build-*` components pass
  `--enforce-application-tests` to the engine in their `runtime-image`, so an
  image whose engine predates this release refuses the flag. Select a runtime
  image from this release or later with the component version. A command in
  the `check` component or the release's `checks` and `build` that passes an
  `--enforce-*` flag itself is refused, since the job's `forge-ci` already
  gives it; set the component's input instead.
- **BREAKING:** The GitLab `release` and `snapshot-images` free-text inputs
  now have regexes, because the jobs quote them in their scripts. None may
  contain a single quote. `publication-project` is a project path,
  `release-git-host-key-fingerprint` is `SHA256:<base64>` alone,
  `publication-image-repository` and `image-repository` have no whitespace
  or digest, `bot-email` is one address, and `changelog-config`,
  `commit-body-config`, `artifacts-config` and `branch` are plain paths.
  `builder-job-image` is pinned by digest or empty, and `required-lints` of
  `nanolinter` is a comma-separated list of check ids. Supply values that
  match. The policy's job variables are gone: `RC_*` other than
  `RC_JOB_USER`, `SNAPSHOT_*`, `CI_IMAGE_BUILDER*`, `REQUIRED_LINTS` and
  `ENABLE_GITLAB_SAST`. A variable of one of those names has no effect.
- **BREAKING:** The GitLab build components and `publish-forge-packages` and
  `publish-maven-central` require `runtime-image`; they defaulted to
  upstream's `ghcr.io` images at an old release. `plan gitlab-build-pipeline`
  takes the images as `--runtime-image-java`, `-node`, `-go`, `-rust` and
  `-android` and refuses a plan whose family has none, and a mirror's
  `consumer-defaults.yml` fills the examples' images from `runtime-images`.
- **BREAKING (GitHub Enterprise Server):** A GitHub workflow that cannot tell
  which repository serves forge-ci stops in its bootstrap resolver, before
  any checkout, and says to set `FORGE_CI_REPOSITORY`. GitHub Enterprise
  Server has no `job.workflow_repository`, so the empty fallback used to check
  out the consumer's own repository and derive the `ghcr.io/` namespace from
  nothing. Jobs now take the admitted repository from the resolver.
- **BREAKING:** `scripts/bootstrap/install-forge-ci.sh` has no default
  repository. Except for a `local` install it refuses to run until
  `FORGE_CI_REPOSITORY` names one, instead of silently trusting the upstream
  releases and signer.
- Examples, templates, verification recipes and live fixtures use neutral
  names and coordinates (`examplescope/myapp`, `forge.example`) instead of
  organisation or consumer names; the cargo-app example's services are
  `api-service`, `worker` and `admin-tool`.
- **BREAKING:** `container build-push-oci-image` builds with Buildah alone and
  refuses a job whose `CI_IMAGE_BUILDER` selects kaniko, instead of running
  Buildah regardless. Build each native platform with `container build --mode
  push-by-digest`, which reads the selection.
- **BREAKING:** kaniko builds run with `--cleanup`: each build deletes the
  root filesystem it built on, so the next image a job builds starts from its
  own base, not from the previous image's files. `/tmp` goes with it, so the
  kaniko job image now sets `TMPDIR=/kaniko/tmp`; rebuild and re-pin it from
  `containers/kaniko-job/Containerfile`. `docs/runners.md`.
- The GitLab demos check in Forge CI's runtime images as `nonroot`
  (`job-user: "65532:65532"`) and build their images in the Buildah job
  image; their `job.Containerfile` and `build.Containerfile` are gone, and
  the monorepo example includes `check` once for Java and once for Node.
  `just demo-image` is replaced by `just buildah-job-image-archive`. Campaign
  prepare takes the lanes it prepares for, `<forgejo|gitlab>=<cases file>`,
  and builds the runtime archives their cases and those demos' gates name; a
  gate's further check image is `$CI_JOB_IMAGE_<NAME>`, given as
  `gitlab:job-archive-<name>=`.
- **BREAKING:** The runtime images carry the ordinary user `nonroot`
  (65532:65532, home `/home/nonroot`) beside their root default, and keep
  every toolchain cache in the job user's home: `CARGO_HOME` and `GOPATH` are
  no longer set, so crates and modules go to `~/.cargo` and `~/go`. The Go and
  Cargo workflows cache those paths. What a job installs (`go install`, `cargo
  install`, `npm install -g`) goes to `/opt/job-tools/bin`, on `PATH` for
  either user, instead of `/go/bin` and `/usr/local/cargo/bin`. Migration: a
  workflow `runtime-image` override older than this release misses the Go and
  Cargo caches; select the matching runtime image. `docs/runtime-images.md`,
  Users and caches.
- **BREAKING:** The GitLab `release` component's Buildah image jobs run in
  `builder-job-image`, the Buildah job image, instead of `build-image`, and
  install nothing: setup-buildah runs with `--install-packages=false`.
  `build-image` is the consumer's build tools alone. Without
  `release-images` no `builder-job-image` is needed: GitLab checks every
  job's image name before its rules, so the image jobs, which never run
  then, name `job-image`. Migration: build
  `containers/buildah-job/Containerfile`, pin it as `builder-job-image`.
- **BREAKING:** Every published image is scanned, and the administrator
  policy (`enforce-vulnerabilities`) alone decides whether findings block.
  A container can no longer skip the scan of release images (Forgejo
  `release-images.yml`) or snapshot images (GitHub, Forgejo and GitLab):
  the `enable-scan` setting is removed (see Removed), the providers always install Trivy, and a frozen snapshot plan with
  `scan: false` is refused. `container multiarch-image assemble
  --from-layouts` and `container build-push-oci-image` now require
  `--scan-output-template`, so a direct final-tag publication is scanned
  too; pass it, with Trivy on the runner. Images that relied on the opt-out
  publish only when their findings pass the gate or are approved in
  `.trivyignore.yaml`.
- **BREAKING:** The container vulnerability gate blocks only on CRITICAL by
  default. `scan-severity` (`container build --scan-severity`,
  `security scan container --fail-on-severity`, the `publish-container.yml`
  input) is now the blocking threshold alone, default `CRITICAL`; a
  comma-list blocks at its least severe entry. The scan still reports HIGH
  findings, listed as "reported, not blocking" and kept in the scan report,
  SARIF and GitLab report, and reports further down when the threshold is
  lower. An unfixed CRITICAL still blocks unless `.trivyignore.yaml`
  approves it. Set `scan-severity: "CRITICAL,HIGH"` to keep blocking on
  fixable HIGH findings. Release images gate at the same default, and
  `release-images.yml` now refuses a declared `scan-severity` other than
  `CRITICAL`. Snapshot images read `scan-severity` with the same parser, so
  lowercase and spaced lists are accepted there too, and record the
  normalised threshold.
- Forgejo `release-images.yml` and `snapshot-images.yml` hand image layouts
  from build to publish through a run-scoped, content-addressed blob store:
  each layout blob of 1 MiB or more is uploaded once per run as
  `<artifact-name>-blob-<sha256>-<uploader>`, and skipped when the run
  already stores it. Images sharing one base no longer each upload its
  layers, which spent the owner's storage quota until Forgejo refused an
  upload with HTTP 413. Publish restores every blob after checking its
  SHA-256 and size; a missing or mismatching blob stops it and names the
  digest. Build jobs still hold no registry credential. New flag
  `artifact upload|download --blob-store <prefix>`; the engine binary and
  the workflows must be updated together.
- One name for the read-only token of a consumer's own tool installs:
  `DEPENDENCY_GITHUB_TOKEN`, passed to `setup-toolchain` as
  `mise-github-token`. docs/forgejo.md documents it ("The dependency token
  of your own jobs"), setup-toolchain's refusal names it and the lock
  alternative's limits, and a guard holds every example to the name.
- `registry-login` (Forgejo) and `forge-ci container login` take the registry
  user by the rule `release-images.yml` already used: the user given, else
  the repository's owner on the forge's own registry; another registry must
  name its user. `username` is optional. A custom image job that left its user
  variable unset stopped with `REGISTRY_LOGIN_USERNAME: parameter null or not
  set` although `release-images.yml` in the same run logged in.
- **BREAKING:** Shared build gates refuse legacy `skip-tests: true` and
  `SKIP_TESTS=true`; exported/published OCI layouts are always
  vulnerability-scanned. Keep scanner prerequisites available.
  Migrate finding exceptions to a reviewed administrator-built engine policy
  and matching protected workflow, binary and image pins. The embedded
  `enforce-lint`, `enforce-vulnerabilities` and `enforce-application-tests`
  settings default to `true`. Report-only distributions still execute checks
  and retain findings; operational, compilation/packaging, credential, signature
  and source/digest identity errors remain blocking. SwiftLint
  findings follow `enforce-lint` (see Removed for `fail-on-warning`).
  Gradle invokes tests separately before packaging, preserving Android's
  module-bound test aggregate even with task overrides; cache and `NO-SOURCE`
  outcomes do not prove physical test execution. See `docs/admin-policy.md`.
- The snapshot flow's jobs are named by stage like the release flow's,
  `Prepare`, `Build: <name> (Maven)`, `Publish: npm package`, `Summary`;
  a branch rule that required one of the old names must be updated.
- The Forgejo `snapshot-images.yml` and `release-images.yml` jobs carry the
  same stage names, `Check: plan`, `Build: <name> (<arch>)`,
  `Publish: <image>`, `Summary`; the snapshot jobs had no display names and
  the release jobs had prose ones.
- **BREAKING:** `forge-ci` with no command is a usage error (exit 2), like a
  bare command group: it prints a short start card on stderr naming `init`
  and `doctor` instead of the full reference at exit 0. `--help` and `help`
  are unchanged. The root help groups commands into repository setup and
  workflow steps, `forge-ci version` leads with the `--version` hint, `init`
  explains what its URL argument is, and `doctor` run at a terminal ends with
  a `Next:` line and points a missing `artifacts.yml` at `forge-ci init
  --dry-run` instead of the source tree's `examples/`. Pointers the binary
  prints to the artifacts reference and the verification guide are URLs,
  not source-tree paths, as are the ones in the example `artifacts.yml`
  files `init` copies and in the schema's editor descriptions; `init` ends
  with the per-forge guide at the pinned release instead of a path and a
  commit hash. `sbom assemble` says what to do when it finds no built
  artifact. A failed release-prerequisites summary links the forge guide's
  Troubleshooting table. The Forgejo and GitLab starters link the docs served
  with the pinned forge-ci commit on the consumer's own forge.
- Temporarily pause Self Release's strict integration and companion black-box
  jobs while Nanolinter publication is pending. Branch resolution, engine
  checksum verification and runtime-image availability remain required. Set
  `SELF_RELEASE_INTEGRATION_GATES=true` in repository variables to restore both
  jobs and their mandatory release checks after bootstrap.
- **Breaking:** command environment inputs use `REGISTRY_USER`,
  `RELEASE_IMAGES_PATH`, `CHANGELOG_FILE` and `TOOLSET_TOOLS` consistently.
  Removed the `REGISTRY_USERNAME`, `RELEASE_IMAGES_LEDGER`, `CHANGELOG_PATH`,
  `TOOLS_SUBSET` and `MISE_TOOLS` spellings. Workflow callers move with the
  engine; explicit command flags are unchanged.
- The release push and `release ssh setup` read the signing key, and the
  push the server's host key, with `golang.org/x/crypto/ssh` instead of
  running `ssh-keygen -y`, `ssh-keyscan` and `ssh-keygen -l`;
  `known_hosts` is written from the handshake's key.
- `release-git-url` (Forgejo and GitLab) and `commit-changelog-release
  --git-url` are optional: unset, the release pushes to the repository on the
  forge's web host over SSH, port 22. Set it where SSH runs elsewhere. The
  host-key fingerprint stays required, and the engine no longer suggests
  fetching it from the host it verifies.
- **BREAKING** The engine tells another image registry, such as GHCR, from
  the forge's own by the image repository's host, so `--external-registry`
  and `EXTERNAL_IMAGE_REGISTRY` are gone from the `container release-images`
  and `container base-images` verbs; drop them. On those verbs `--registry`
  now names only the forge's own registry when it is not on the server's
  host (GitLab's `CI_REGISTRY`), and the login goes to the repository's host:
  a caller that passed `--registry` for another registry drops it too, or
  that registry counts as the forge's.
- **BREAKING** The Forgejo `sign-and-publish-release.yml` input
  `publication-image-repository` is `image-repository`, the value
  `release-images.yml` takes under the same name. Rename it in the caller.
- The Forgejo image workflows read the registry user from the variable
  `RELEASE_REGISTRY_USER` when `registry-user` is empty, so the registry
  credential is one named pair, the secret `RELEASE_REGISTRY_TOKEN` and that
  variable, and a caller passes no user.
- **BREAKING** An empty `registry-user`, with no `RELEASE_REGISTRY_USER`, is
  the repository owner only on the forge's own registry. On another registry the Forgejo image workflows stop
  before they log in, and `release-images.yml` before it builds, until
  `registry-user` names the user there; they no longer try the forge owner.
  `container release-images plan` writes the decided user as its
  `registry-user` output.
- Base-image `sign`, `import` and `promote` no longer require the repository
  below the source repository's owner: the registry token already decides
  where they can write. Every image must still be in the one base-image
  repository.
- **BREAKING** The Forgejo signer-image example reads the engine package
  token as `ENGINE_PACKAGE_TOKEN`, no longer `RELEASE_REGISTRY_TOKEN`, which
  every consumer uses for its image registry. Rename the secret where the
  example is deployed.
- Forgejo jobs that build container images may be moved off the pool:
  `CI_RUNNER_LABEL_CONTAINER` and `CI_RUNNER_LABEL_CONTAINER_ARM64` name
  where they run instead, and a `request-runner` job asks the lender
  `CI_RUNNER_LENDER` and `CI_RUNNER_LENDER_REPOSITORY` name for an ephemeral
  one. All unset, the builds run on the pool and nothing is borrowed. The
  signer-image and go-service examples carry the shape.
- **BREAKING** Configure Forgejo `CI_RUNNER_LABEL`, `CI_RUNNER_LABEL_ARM64`
  and reusable `runner-label` inputs as JSON label arrays, including a single
  label (`["ci-builder"]`). Workflows decode them only at `runs-on`.
- **BREAKING** The GitHub SLSA attestor's configurable `runs-on` input also takes
  a JSON label array; its default remains the same Ubuntu runner. GitLab runner
  tags continue to use native YAML arrays.
- Keep signer architecture candidates in the image registry and transfer only
  digest metadata through Forgejo artifacts before assembling the final index.
- Resolve the external Nanolinter and Gommitlint development tools through their
  Codefloe release API, using published Gommitlint 0.93.0.

## [0.91.13] - 2026-10-23

### Added

- Add checked image indexes and index SBOMs
- Add release signing secret setup guide

### Fixed

- Keep multiarch output and selection coherent

## [0.91.10] - 2026-10-22

### Fixed

- Install goreleaser regression

## [0.91.9] - 2026-10-22

### Added

- Support explicitly registered fork self-CI

### Changed

- Refresh signed companion suite pin
- Pin the fork-portable companion suite

### Fixed

- Tighten companion checkout validation and refresh its pin
- Verify renamed forks and document migration
- Reject subsets that omit the selected Go runtime
- Accept full SHA-256 workflow pins

## [0.91.8] - 2026-10-22

### Added

- Add gpg-keys format check for coherent allowlist validation
- Add allowed-signers format check as CLI subcommand
- Add missing principal to allowed_signers entry
- Add RequireWorkingTool and use it for cargo
- Add maintainer release runbook to DEVELOPMENT.md
- Add diggadmin to allowed releasers

### Changed

- Use forge-neutral delivery paths
- Gate tests on fresh binary checksums
- Use library parsers instead of hand-rolled validation
- Validate allowed_signers entry format
- Replace forward-looking notice with actionable quick start

### Fixed

- Align allowlist checks with key parsers
- Fix stale gitleaks allowlist comment
- Bump blackbox test suite pin to 9fd37b4
- Verify cargo actually runs, not just exists on PATH
- Bump blackbox test suite pin to 792a045
- Install rust via apt instead of rustup on ubuntu-26.04
- Install rust toolchain explicitly before setting default
- Bump blackbox test suite pin to 31e7e72
- Set rustup default toolchain on ubuntu-26.04 runners
- Defer runtime image check until after test gates
- Skip CLI signing on tag pushes in self-runtime-container

### Removed

- **BREAKING:** Remove the `fail-on-warning` input of `lint-swift.yml` and
  `lint swift swiftlint --fail-on-warning` (`FAIL_ON_WARNING`), which had
  no effect: the administrator lint policy decides whether SwiftLint
  findings block.
- Remove rustup proxy shims after apt cargo install

## [0.91.0] - 2026-10-22

### Changed

- Initial commit

[0.91.13]: https://github.com/diggsweden/forge-ci/compare/v0.91.10..v0.91.13
[0.91.10]: https://github.com/diggsweden/forge-ci/compare/v0.91.9..v0.91.10
[0.91.9]: https://github.com/diggsweden/forge-ci/compare/v0.91.8..v0.91.9
[0.91.8]: https://github.com/diggsweden/forge-ci/compare/v0.91.0..v0.91.8
[0.91.0]: https://github.com/diggsweden/forge-ci/compare/v0.90.0..v0.91.0

<!-- generated by git-cliff -->
