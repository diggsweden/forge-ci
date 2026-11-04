<!--
SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government

SPDX-License-Identifier: CC0-1.0
-->

# Demos

Each directory here is one demo `just demo <forgejo|gitlab|github> <name>`
shows: a small consumer that uses Forge CI the way a project would, released
and then reviewed in a pull or merge request, and kept for people to look at.

```text
<name>/
  consumer/            the project, committed as it is into the demo repository
```

On GitLab every demo runs in Forge CI's own images, built from this tree:
its checks and release jobs in the runtime image of its toolchain (`just
runtime-image-archive java|node|rust|go|android <directory>`), as the image's
`nonroot` user (`job-user: "65532"` in the gate, `job-user=65532:65532` for
the release), and its release image builds in the Buildah job image (`just
buildah-job-image-archive <directory>`). No demo carries an image of its own.

The consumer names Forge CI as a published project does; the live tier puts
the layer under test in its place, as it does for any consumer. Its Forgejo
workflows sit in `.forgejo/workflows`, its GitLab merge-request gate in
`.gitlab/merge-request.yml` with the scripts the release runs, and its GitHub
workflows in `test/live/github/workflows`. `hello-maven` is a Maven jar;
`hello-maven-service` adds the runtime image built from that jar;
`hello-npm-service` is an npm package and the image built from it.
`hello-cargo-container` is a Rust service whose Containerfile is the build,
its binary extracted from the image. `hello-npm-package` adds an installable,
scoped npm package without a container. `hello-cargo-workspace` adds two
workspace binaries, each with its own runtime image and binary export target.
`hello-go-cli`, `hello-go-service`, `hello-gradle` and `hello-monorepo` show
the `go-cli`, `go-service`, `gradle-app` and `monorepo` examples: their source
is their own, and every CI file is the example's,
filled in by `internal/livetest/exampledemos_internal_test.go`, which fails when
one drifts. After changing an example, rewrite them with
`go test ./internal/livetest -run TestExampleDemosAreTheirExamples -args -update-demos`.
A GitLab gate that builds with a component runs the engine of its job image,
so the Go and Gradle demos take the candidate's own runtime image, which
`just demo gitlab <name> job-archive=<archive>` takes. `hello-monorepo`
builds only on a release tag; its gate checks the backend in the Java runtime
image, `job-archive=`, and the frontend in the Node one, `$CI_JOB_IMAGE_NODE`
in the gate and `job-archive-node=` for the demo.
A demo for another ecosystem or feature is one more directory in the same shape;
`internal/livetest/demos_internal_test.go` renders every consumer here for
each forge it declares and parses its artifact configuration, so malformed
inputs fail `just test-unit`. Rendering is not a live-flow pass.

## Coverage still to execute

Each cell is the demo's workflows for that forge: the pull or merge-request
gate, then the release. A dash is a flow the demo does not declare; the
reasons follow the table.

| Fixture | GitHub | Forgejo | GitLab | Distinct coverage |
|---|---|---|---|---|
| hello-maven | PR, release | PR, release | MR, release | Maven JAR |
| hello-maven-service | PR, release | PR, release | MR, release | Executable JAR and runtime image |
| hello-npm-service | PR, release | PR, release | MR, release | Private npm application archive and image |
| hello-cargo-container | PR, release | PR, release | MR, release | Container-first Rust binary |
| hello-cargo-workspace | PR, release | PR, release | MR, release | Shared version; two binaries and images |
| hello-npm-package | PR, release | PR, release | MR, release | Publishable scoped package; offline packed-package installation test |
| hello-android | PR/build | - | MR | Demo flavor, unit tests, unsigned release APK/AAB |
| hello-ios | PR/build | - | - | macOS ARM64 simulator tests, XcodeGen app, unsigned archive |
| hello-go-cli | PR, release | PR, release | MR | The go-cli example as shipped; `cmd/my-cli` layout |
| hello-go-service | PR, release | PR, release | MR | The go-service example as shipped; binaries and runtime image |
| hello-gradle | PR, release | - | MR | The gradle-app example as shipped; reproducible Java library jars |
| hello-monorepo | - | - | MR | The monorepo example as shipped; Maven and npm services checked together |

Why a flow is missing:

- **iOS on Forgejo and GitLab**, deliberately: the build needs macOS ARM64
  runners with Xcode, and neither lab has one. GitLab's components build no
  Xcode either.
- **Mobile releases**: neither mobile demo declares a release on any forge.
  Signing-capable mobile build leaves stay branch-only, and no disposable
  release-signing setup exists for them yet.
- **Android on Forgejo**: the Forgejo lane's job image is a nanolinter
  flavour, which has no Android SDK, and the lane publishes no runtime image
  to run the build in.
- **hello-monorepo on GitHub**: its example has no GitHub pull-request
  workflow, and its release workflow passes no `runtime-image`, which the
  GitHub renderer requires to point a trial at the engine under test.
- **The example demos' GitLab releases**: the GitLab examples are the
  merge-request gate; the release policy's commands are the adopter's, and no
  example ships them. The live release also renders with git-chglog from
  `.chglog/`, which the examples, on git-cliff, do not carry.
- **hello-gradle and hello-monorepo on Forgejo**: their examples have no
  Forgejo workflows; the Forgejo layer has no Gradle or npm build action.

The npm package releases on Forgejo and GitLab ship the tarball as a signed
release file; neither publishes it to a package registry.

Record live results separately by commit, provider, visibility and flow. Local
package, binary and Android build tests do not establish registry permissions,
release signing or promotion. Web archive coverage and development-release
parity remain outstanding. Run public
amd64 first, then private PR/MR and release flows; mixed architectures are deferred.

PR build/test jobs are separate from the reusable PR lint orchestrator. A green
lint job alone does not prove the project built or ran its tests. Signing-capable
mobile build leaves remain branch-only; PRs receive no signing secrets. macOS
ARM64 iOS tests are a supported independent track, not deferred Linux multi-arch
coverage. Forgejo hello releases verify a signed request from a disposable
requester, using the trial harness's public allowed-signers entry.

## Java runtime scan regression

The Java service uses a digest-pinned distroless Java 25 non-root runtime.
The previous Temurin base included `/usr/bin/pebble` built with Go 1.27.1,
which caused three fixable HIGH findings. Updating Trivy alone did not remove
them. The distroless image removes that unneeded helper and retains the same
executable JAR contract. Check the application response as well as the scan
when changing the base. A passing gate may still report unfixed HIGH findings;
do not describe that result as vulnerability-free.

docs/testing.md, Showing a consumer, has the commands, the images each forge
needs, and how to delete a demo.
