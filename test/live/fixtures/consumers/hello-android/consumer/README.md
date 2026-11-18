# Hello Android

A minimal Kotlin Android app with a demo flavor, unit tests and versionName /
versionCode read from `gradle.properties`. The standard Gradle wrapper verifies
its distribution checksum. SDK 35 matches the Forge CI Android runtime image.

With Android SDK 35 installed, run `./gradlew testDemoDebugUnitTest
assembleDemoDebug assembleDemoRelease bundleDemoRelease`. GitHub PRs run the
Forge CI lint gate and a separate secret-free build/test job. A branch dispatch
also calls Forge CI's signing-capable Android builder and requests tests, APKs,
AAB and a build SBOM. The build is initially unsigned; debug keystore signing is
not release-signing evidence.

Forge CI's default release task selection must also run the selected module's
enabled unit tests. A successful APK/AAB build alone is not test evidence: inspect fresh
`app/build/test-results/` after a clean build. An explicit task override owns
its test selection; `skip-tests` deliberately omits the default unit-test tasks.

To sign, pass a throwaway keystore through Forge CI's signing inputs. The
build reads its path and passwords from `ANDROID_KEYSTORE_PATH`,
`ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS` and `ANDROID_KEY_PASSWORD`.
No key is committed.

On GitLab the merge-request gate lints in a nanolinter image and runs the
same unsigned build and tests in the Android runtime image, as its nonroot
user. That image is linux/amd64 only.

Both forges lint in the Kotlin nanolinter flavour. The plan runs and gates
`kotlin-ktlint` and `kotlin-detekt` alongside secrets checks, with SAST advisory.
The app and its unit tests are Kotlin; AGP 9 supplies built-in Kotlin support,
so no legacy Kotlin Android plugin is applied. JVM target 17 matches the
Android compile options. Builds still run in the Android SDK runtime, not
the lint image.

Forgejo pull requests run Kotlin lint in the digest-pinned `CI_JOB_IMAGE`
and the unsigned build/tests in the separate digest-pinned Android SDK image,
`CI_JOB_IMAGE_ANDROID`, as its nonroot user. Neither job receives a release
or signing secret. The summary requires both jobs to pass.

The demo has no release or development publishing. It does not publish to
Google Play.
