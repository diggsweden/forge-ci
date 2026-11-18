# Hello iOS

An XcodeGen iOS application with unit-test sources, a neutral bundle identity
and `versions.xcconfig` for Forge CI's marketing-version bump. No certificate,
profile or production publisher is included.

The GitHub PR workflow calls Forge CI's reusable lint gate. Branch workflow
dispatches call its reusable `build-xcode-ios.yml` workflow
on hosted macOS ARM64 with Xcode 26.6, XcodeGen and `generic/platform=iOS`.
The build creates an unsigned native archive: no simulator, emulator or
runtime tests are run. The build leaf refuses PR context; the consumer keeps
that guard rather than bypassing it. This is a branch build, not development
publication or signed IPA export.

The standard hosted consumer trial verifies this build with `dev-release
workflow=build-workflow.yml`: it checks the downloadable archive and that main
is unchanged, with no stable tag or release. Use the normal guarded GitHub
session and prepared engine, just as for other consumer trials; no iOS-specific
runner or test harness is needed.

The release workflow runs the same unsigned archive through the release
orchestrator and passes the Xcode version itself, as a project does from a
repository variable. The declaration names an Xcode no runner has, so the
release proves the caller's version is the one used. With code signing off
the build exports no IPA and the release offers no file.

The demo runs only on GitHub: the Forgejo and GitLab labs have no macOS runner.
Archive success establishes native build coverage, not device/runtime testing.
