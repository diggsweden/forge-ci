# Sigstore-keyless signing example

A Go CLI whose release artifacts use Sigstore-keyless signatures via cosign.
There is no long-lived **artifact-signing** key: the artifact identity is the
GitHub Actions workflow that produced the release.

Release commits and tags are signed independently. This example sets
`git-signing.method: ssh`, so it still needs `RELEASE_SSH_SIGNING_KEY` and a
matching principal and public key in `.forge-ci/allowed_signers`. Start from
the included `.forge-ci/allowed_signers.example`, replace its demonstration
key with the public half of `RELEASE_SSH_SIGNING_KEY` and its principal with
the `bot-email` you set in `github/release-workflow.yml`, and commit it as
`.forge-ci/allowed_signers`. The SSH key protects Git objects; it is not used
for artifact signatures. Git objects cannot be signed keyless.

## Files

What this example adds is the `sign:` and `git-signing:` blocks of
`artifacts.yml` and the `secrets:` lines of `github/release-workflow.yml`:
copy the blocks into your `.forge-ci/artifacts.yml` and the lines into your
release workflow. The rest of both files is the [Go CLI](../../go-cli/)
example, which ships this setup.

- `artifacts.yml` declares `sign.method: sigstore` and
  `git-signing.method: ssh`.
- `github/release-workflow.yml` is the caller workflow with the required
  `id-token: write` permission so cosign can mint the runner-issued
  OIDC token, and `RELEASE_SSH_SIGNING_KEY` as its one signing secret.
- `.forge-ci/allowed_signers.example` shows the OpenSSH allowed-signers
  format required to verify release commits and tags. Its bundled key is only a
  demonstration key and its principal a FIXME placeholder; replace both.

## What gets produced

Each release binary gets a Sigstore bundle, `<file>.bundle`, with the
signature, the Fulcio certificate for the workflow identity and the Rekor
proof. If you add a container, its signature is stored in the registry next
to the image. See
[what the signing run publishes](../../../docs/verification.md#signtransparency-what-the-signing-run-publishes-about-itself).

## How to verify

The certificate names the forge-ci workflow that signed, at the ref your
release workflow pins it to, not your release workflow: the signing jobs run in
forge-ci's reusable workflows. Those sign under the same name for every
repository that calls them, so the repository claim says whose release it is.

Artefacts (a downloaded `.bundle` and the matching file):

```bash
forge-ci validate artifact-signature \
    --artifact my-cli-linux-amd64 \
    --cert-identity-regexp '^https://github\.com/diggsweden/forge-ci/\.github/workflows/' \
    --cert-oidc-issuer https://token.actions.githubusercontent.com \
    --cert-github-workflow-repository examplescope/myorg
```

Container image:

```bash
forge-ci validate container-signature \
    ghcr.io/examplescope/myorg/my-cli@sha256:abc123... \
    --method sigstore \
    --cert-identity-regexp '^https://github\.com/diggsweden/forge-ci/\.github/workflows/' \
    --cert-oidc-issuer https://token.actions.githubusercontent.com \
    --cert-github-workflow-repository examplescope/myorg
```

What this proves: "the file was signed by one of forge-ci's workflows in a run
of examplescope/myorg". Anyone who can push a workflow to examplescope/myorg
can start such a run, on any branch, so write access to the repository is the
artifact-signing security posture: no reusable artifact-signing private key
exists. To also require the run of one release, pin its tag with cosign, which
matches the run's ref exactly:

```bash
cosign verify-blob --bundle my-cli-linux-amd64.bundle \
    --certificate-identity-regexp '^https://github\.com/diggsweden/forge-ci/\.github/workflows/' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    --certificate-github-workflow-repository examplescope/myorg \
    --certificate-github-workflow-ref refs/tags/release-request/v1.2.3 \
    my-cli-linux-amd64
```

With a tag ruleset that limits who may create `release-request/*` tags, that
is the release posture. Protect and rotate the separate SSH Git-signing key
normally.

## Switching from GPG

Follow the [artifact-signing migration guidance](../../../docs/verification.md#changing-artifact-signing-method)
when changing existing consumers from GPG to this example's Sigstore bundles.
