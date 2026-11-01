# Tutorial: your first pull-request check

This walks a Go project on GitHub from a bare repository to a pull request that
Forge CI checks, in about fifteen minutes. The same two commands, `init` and
`doctor`, do the setup for Cargo, Maven, Gradle, npm and Android projects;
only the example and the files differ. GitLab and Forgejo have their own
layouts; follow this page once, then read the [GitLab](gitlab.md#getting-started)
or [Forgejo](forgejo.md#getting-started) guide for the differences.

No secrets are needed for pull-request checks. Releases need them, and the last
section says where that setup lives.

## Before you start

You need:

- the `forge-ci` binary on your `PATH` (see [Install](../README.md#install));
- a Git repository with one root manifest, here `go.mod`, pushed to GitHub,
  with a Git identity configured;
- network access to `github.com`: `init` reads the examples from the Forge CI
  release it pins.

Run every command from the repository root.

## Step 1: preview what init would write

```bash
forge-ci init https://github.com/diggsweden/forge-ci --dry-run
```

The URL is the Forge CI repository to copy the examples from, not your
project's remote. The preview reads your build files, picks an example, pins
the newest release, and reports what it would do:

```text
Forge CI initialization preview

Template source: https://github.com/diggsweden/forge-ci
Release: v0.91.13
Forge: github
Example: go-cli

Would create:
  .forge-ci/artifacts.yml
  .github/workflows/pullrequest-workflow.yml
  .github/workflows/release-workflow.yml

Review and customize before running
  .forge-ci/artifacts.yml:11  my-cli
  .forge-ci/artifacts.yml:16  my-cli
  .forge-ci/artifacts.yml:17  my-cli
  .github/workflows/release-workflow.yml:33  my-cli

No project files were written.
```

Three things to check before going on:

- **Forge** says `github`, because the URL is on `github.com`. On another host,
  the forge comes from the mirror's `.forge-ci/consumer-defaults.yml`.
- **Example** matches your project. `go-cli` is a binary shipped as release
  files; a service shipped as a container is `go-service`. Pass
  `--example go-service` to choose it.
- **Would create** lists no file you already have. Existing files are never
  overwritten; a conflict is reported and the command returns nonzero.

## Step 2: write the files

```bash
forge-ci init https://github.com/diggsweden/forge-ci
```

The report is the same as the preview, with `Created:` in place of
`Would create:`, and it ends with a `Next:` line. The placeholders are the
same four lines: the example names your artifact `my-cli`.

Open `.forge-ci/artifacts.yml` and replace `my-cli` with your binary's name,
and `main-package: ./cmd/my-cli` with the path to your `main` package. Then
change the `release.attachartifacts` pattern in
`.github/workflows/release-workflow.yml` to the same name. Leave the rest.

## Step 3: check the result

```bash
forge-ci doctor
```

```text
[OK] artifacts.yml present: .../.forge-ci/artifacts.yml
[OK] artifacts.yml parses + validates: 1 artifact(s), 0 container(s), sign.method=sigstore
[OK] workflows pin forge-ci to a tag: no @main / @master references found
[OK] sign block valid: method=sigstore
[OK] workflow id-token permission: a workflow grants the OIDC id-token
[WARN] ssh git-signing allowlist: git-signing.method=ssh, but .forge-ci/allowed_signers is missing
       fix: commit .forge-ci/allowed_signers with the release signing public key ...

Scope: local configuration checks only; remote secrets, runner availability and publishing permissions were not checked.
Next: review the WARN lines above, then commit the Forge CI files and open a pull request; ...
```

`OK` and `WARN` exit 0; a `FAIL` exits 1 and comes with a `fix:` line. The
warning above is about releases, which this tutorial does not reach: the
release flow checks the person who requests a release against a committed
allowlist, and that file does not exist yet. Pull-request checks do not need
it.

`doctor` reads only the local repository. It cannot tell whether a secret is
set on GitHub or whether a runner is available; the pull request does that.

## Step 4: open the pull request

```bash
git checkout -b forge-ci
git add .forge-ci .github
git commit -s -m "ci: add Forge CI pull-request and release workflows"
git push -u origin forge-ci
```

Open the pull request on GitHub. The `Pull Request Workflow` runs the
`pr-checks` job from the pinned Forge CI release: lint through Nanolinter and
security scans, with a step summary of what ran. It does not run your own
tests; add your project's test job to the same workflow and require both
before merging. The first run pulls the runtime images, so it takes longer
than later ones.

A failing lint check names the tool and the finding in the job log. Fix it and
push; the workflow runs again.

Once the check is green, merge. Every later pull request against `main` or
`develop` gets the same check.

## Where to go next

- **A release** is the second workflow you just committed. It starts when you
  push a signed `release-request/vX.Y.Z` tag, and it needs secrets and a
  signing allowlist first: [set up release signing secrets](quickstart-setup-secrets.md),
  then [Secrets and tokens](github.md#secrets-and-tokens),
  [protect the release tags](github.md#protect-the-release-tags) and
  [Release authorisation](verification.md#release-authorisation).
- **A container instead of binaries:** the [Go service example](../examples/go-service/)
  and [Ecosystems](ecosystems.md) explain artifact-first against
  container-first.
- **Custom lint policy:** `init` writes no linter configuration. Copy the
  example's `nanolinter.toml` when you want to change the defaults; see
  [Pull-request gates and lint](github.md#pull-request-gates-and-lint).
- **Everything the CLI can do:** the [CLI reference](cli-reference.md). Most
  of it is run by the workflows, not by you.
