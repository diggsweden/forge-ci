# Example: the signer image and engine binary on Forgejo

This directory is for the organization that serves the Forgejo layer, not for
a consumer. The consumer callers live with their ecosystem:
[Go CLI](../go-cli/forgejo/), [Go service](../go-service/forgejo/) and
[Cargo](../cargo-app/forgejo/); see the [examples index](../README.md).

[`signer-image.yml`](signer-image.yml) is the main file here. One repository
of your organization runs it, at the commit of `forge-ci` your consumers pin.
Its first job publishes the layer's binary for that commit to your instance,
without which no job of the layer runs. The others build and publish the
signer image, for linux/amd64 and linux/arm64, and output the digest-pinned
reference every consumer `release-workflow.yml` passes as `signer-image`. Its image
builds need user namespaces: on a pool that denies them, a `request-runner`
job borrows a runner, or the builds use kaniko; see
[Runners and runner lending](../../docs/runners.md).

[`kaniko-job-image.yml`](kaniko-job-image.yml) is for the kaniko case only:
it publishes the job image kaniko builds run in, from kaniko's own debug
image, and prints the digest to pin as `CI_IMAGE_BUILDER_JOB_IMAGE` with
`CI_IMAGE_BUILDER=kaniko`.

[`self-runtime-images.yml`](self-runtime-images.yml) is an opt-in runtime
publisher for a Forge CI mirror, sharing upstream's Containerfile and smoke
checks. It starts by manual dispatch only; its header shows the push and
weekly triggers to add. Replace
its placeholders: `PIN_FORGE_CI_SHA` is the mirror's published immutable
engine revision. Configure the image namespace, reviewed digest-pinned signer
and Go build images, native runner pools, registry credentials and public-key
digest as described in [Runtime images](../../docs/runtime-images.md#configuration).

[`package-retention.yml`](package-retention.yml) previews a separately reviewed
consumer-pin matrix using that namespace and registry account. It never applies
deletion. Copy either example into `.forgejo/workflows/` only after configuration.

## Releasing a Forge CI mirror

[`self-release.yml`](self-release.yml) is an opt-in caller for publishing Forge
CI's own CLI archives on Forgejo. It composes the existing preparation,
unsigned GoReleaser build, and signing/publication workflows. It does not
change the root GoReleaser configuration or publish runtime images.

Before copying it to `.forgejo/workflows/self-release.yml`:

- Replace `FORGE_HOST`, `OWNER` and every `PIN_*` with your reviewed server,
  repository, immutable engine commit, signer digest and public-key checksum.
  The pinned engine's bootstrap packages and matching signer must already
  be published and verified; releasing CLI archives does not publish them.
- Protect the release branch, `release-request/*` and final `v*` tags. Review
  the authorized request keys in `.forge-ci/allowed_signers`. The candidate
  and build jobs execute repository code and must have no release secrets.
- Configure `CI_RUNNER_LABEL`, `RELEASE_BOT_NAME`, `RELEASE_BOT_EMAIL`,
  `RELEASE_GIT_HOST_KEY_FINGERPRINT` and, if needed, `RELEASE_GIT_URL`.
  Supply the signing and publication secrets named in the caller, and
  `COSIGN_PUBLIC_KEY` matching the pinned checksum.
- Run the full development gate and a disposable consumer trial before
  enabling production release requests. The candidate job checks the
  release path; it is not a substitute for the development gate.
- Generate, review and commit `mise.lock` for the native platforms used by
  the candidate and build runners. Upstream does not ship this host-specific
  lockfile. It must cover Go, GoReleaser, syft and yq at the versions in
  `.mise.toml`, including their download URLs and checksums. Both jobs keep
  `mise-locked: 'true'`; the candidate installs the full build tool set before
  preparation can create the final tag. Do not generate the lock during a
  release request or switch to an unlocked install to bypass a missing lock.

Push a signed annotated `release-request/vX.Y.Z` at the reviewed head of the
configured release branch. The preparation workflow creates the final tag.
Read back the release files and signatures. If publication fails after tagging,
use [failed-release recovery](../../docs/failed-release-recovery.md); never
move a published tag. GitHub and GitLab keep their existing release routes.

## Before it runs

The file carries placeholders, all literal so that `git grep` finds them:

| Placeholder | Replace with |
|---|---|
| `OWNER` | who hosts `forge-ci` on your instance |
| `PIN_FORGE_CI_SHA` | the `forge-ci` commit your consumers pin |

Then set, in that repository:

- the variables `CI_RUNNER_LABEL` and `CI_RUNNER_LABEL_ARM64`, the amd64 and
  arm64 runner pools as JSON label arrays such as `["ci-builder"]`, and
  `SIGNER_REGISTRY_USER`, the user of the signer image registry;
- the secret `ENGINE_PACKAGE_TOKEN`, a token that may write packages of
  `OWNER`, for the binary, and the secret `SIGNER_REGISTRY_TOKEN` for the
  image.

`OWNER`'s packages must be readable without credentials, as its `forge-ci`
repository is. The file's header lists every variable and secret, including
the ones for kaniko and runner lending. The
[Forgejo guide](../../docs/forgejo.md#the-signer-image) describes the image,
and [Forks](../../docs/forgejo.md#using-the-layer-from-a-fork) the order of
the steps around it.
