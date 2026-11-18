# Hello Android library

A minimal Kotlin Android library: one `com.android.library` module, `lib`, with
unit tests and its version read from the `version` property of
`gradle.properties`. It is the library beside the Android app demo,
`hello-android`, with the same Gradle wrapper, Android Gradle Plugin and
SDK 35, the line of the Forge CI Android runtime image.

With Android SDK 35 installed, run `./gradlew lib:test lib:lintRelease
lib:assembleRelease`. The release AAR is `lib/build/outputs/aar/lib-release.aar`.
A library has no keystore, no APK or AAB and no Google Play: `.forge-ci/artifacts.yml`
declares it `build-type: library`, and Forge CI then builds `lib:test` and
`lib:assembleRelease`.

GitHub pull requests run the Forge CI lint gate and a separate build and test
job. A branch dispatch also calls Forge CI's Android builder with
`library: true`, which uploads the AAR and a build SBOM. On GitLab the
merge-request gate lints in a nanolinter image and runs the same build and
tests in the Android runtime image, as its nonroot user. That image is
linux/amd64 only.

Both forges lint in the Kotlin nanolinter flavour. The plan runs and gates
`kotlin-ktlint` and `kotlin-detekt` alongside secrets checks, with SAST advisory.
The library and its unit tests are Kotlin; AGP 9 supplies built-in Kotlin
support, so no legacy Kotlin Android plugin is applied. JVM target 17 matches
the Android compile options. Builds still use the Android SDK runtime;
the GitHub publication workflow uses its Java runtime. Local policy-release
publishers upload exported files as data and need neither a JVM nor an SDK.

The module declares one Maven publication, `release`, of the release variant
with its sources: `org.example.trial:hello-android-library` at the version of
`gradle.properties`. It names no repository and signs nothing. Forge CI
exports it to a directory (`forge-ci build gradle export-publications
--project lib`) and publishes the exported files, as
`publish-to: [forge-packages]` in `.forge-ci/artifacts.yml` asks.

On GitHub a signed `release-request/vX.Y.Z` tag starts the release
(`test/live/github/workflows/release-workflow.yml`): the orchestrator bumps the
version, builds the AAR in the Android runtime image, publishes the exported
publication to GitHub Packages in the Java runtime image and signs the
release with GPG, which writes to no transparency log. The build uploads the
AAR as the workflow artifact `hello-android-library` and the publication as
`hello-android-library-maven-publications`.

Forgejo pull requests run Kotlin lint in the digest-pinned `CI_JOB_IMAGE`
and build/test the AAR in the separate digest-pinned Android SDK image,
`CI_JOB_IMAGE_ANDROID`, as its nonroot user. Neither job receives release
or signing secrets. The summary requires both jobs to pass.

The Forgejo release workflow builds signed AAR release files: it
builds the unsigned AAR and CycloneDX SBOM in `CI_JOB_IMAGE_ANDROID`, then
hands their digest-bound `dist/` to the shared signer and release publisher.
By default it selects `test/live/aar-files/.forge-ci/artifacts.yml`, which
declares the same library and GPG signing without requesting Maven publication.
The build receives no signing keys or app keystore. The producer script lives
at `.gitlab/build-release.sh`, following the other consumer fixtures' handoff
layout; its `check-release.sh` companion runs the same test, lint and AAR
checks as the GitLab MR gate in the Android SDK runtime. A native GitLab
release trial configures its policy to call these handoffs. This route does not
publish the Maven publication requested by `publish-to: [forge-packages]`
for the GitHub release path. Signed-file build/signing and Maven publication
are distinct acceptance scopes.

Before a Forgejo trial, provision the digest-pinned Android SDK 35 runtime
as `CI_JOB_IMAGE_ANDROID` in addition to the lint image, and replace the
workflow's helper, signer-image and public-key digest placeholders through
the live harness. The existing prepared Android cases cover pull requests,
not the opt-in file-only release. Use the normal consumer release trial on
either local forge with `fixtures=test/live/aar-files`.

For a GitLab signed-file trial, select `fixtures=test/live/aar-files` with
the Android SDK runtime as both job and build archive, plus the Kotlin lint
and shared signer archives. The native release policy selects the `android`
build family. A signed-file trial is not Maven-publication acceptance.

For the new Maven route, keep the default `.forge-ci/artifacts.yml` declaration
on GitLab; on Forgejo set `CI_ARTIFACTS_CONFIG=.forge-ci/artifacts.yml`. The
producer exports the repository into `dist/hello-android-library-maven-publications`
without credentials and declares its payload files alongside the AAR. The signer
admits and signs the handoff; the separate packages job publishes those bytes
only after release publication. On GitLab scope `RELEASE_PACKAGE_TOKEN` to
`release/packages`, never to SDK/build/sign jobs. Forgejo uses the existing
isolated packages job and `RELEASE_REGISTRY_TOKEN`.

Pipeline wiring and local fixtures are not native publication acceptance. The
new Maven route still needs its own admitted live trial; do not rerun the
accepted file-only cases as a substitute.
