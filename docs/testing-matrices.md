# Testing matrices

These tables say what Forge CI is proven to do live, on which forge, and
through which demo. Every cell the product offers should be filled, and an
empty cell names its reason (a product gap, a demo gap, an owner decision or a
deliberate exclusion).

Matrix 1 crosses each demo, standing for a project kind, with the flows the
orchestrators offer. Matrix 2 lists the whole-flow `lifecycle` event once per
variant axis value (ecosystem, route, images, signing, image builder,
visibility, enforcement, architecture), plus the kept scenario lines.

- The tables are generated from the Go case tables, the demos' declarations
  and the demo admission; a test fails when the committed tables are stale.
- Expected outcomes come from the planner, never written per case. The live
  marks are a hand-kept overlay in `test/live/evidence.txt`.
- Every forge runs amd64; arm64 is cross-compiled and never emulated in a
  release.

## Frozen-engine campaign, 2026-11-18

The latest runnable acceptance batch is finished, against frozen engine
`0e513a29e2d55678e2d79914600e3e5f5e608069`. Its progress tally is **250
passing case/leaf results**, not 250 distinct scenarios or a coverage
denominator. Existing matching evidence was reused; this is not a claim that
the full, small or medium profile has been accepted on one source/input set.

The latest retained verdicts under `forge-ci/live-runs/` are:

| Scope | Verdict root | Result |
|---|---|---|
| GitHub native iOS branch build | `20261118T142334Z-ios-consumer-build-3953945` | Shared reusable Xcode builder, unsigned archive downloaded and verified; main unchanged, no stable tag/release |
| Forgejo npm-package consecutive releases | `20261118T142618Z-3960787` | Both versions passed with four verified signatures each and cleanup |
| Forgejo Maven consecutive releases | `20261118T143456Z-4064783` | Both versions passed with four verified signatures each and cleanup |
| Forgejo Go CLI consecutive releases | `20261118T144349Z-4158637` | Both versions passed with five verified signatures each and cleanup |
| GitLab external policy, registry job-token boundary and child serialization | `20261118T144051Z-4132144` | All three scenarios passed |
| GitLab native release-handoff integrity | `20261118T144643Z-7431` | Valid bytes accepted; stale digest and changed payload/bundle refused |
| Forgejo missing-secret and untrusted-request guards | `20261118T145017Z-57446` | All three refusal leaves passed |
| Forgejo preseeded provenance and interrupted recovery | `20261118T145342Z-97862` | Preseeded statement refused; interrupted version released nothing, next patch passed with five verified signatures |
| GitLab promotion retry after registry write | `20261118T153327Z-246338` | Passed in 33 seconds; original failure journal retained, downstream effects blocked, retry converged to the same digest; repository cleanup confirmed |
| GitHub revised iOS PR gate | `20261118T180112Z-ios-pr-lint-414868` | Public run `44365178200` passed in 364.43 seconds; frozen engine reused, reviewed native-only consumer tree `cf77a061`; configured secrets/SAST and canonical PR gate, not Swift lint or Xcode/device testing |

Signed Android AAR-file releases also passed on Forgejo
(`20261118T135546Z-3863432`) and GitLab (`20261118T134637Z-3767540`).
These explicit file-only fixtures do **not** establish Maven publication.
The iOS consumer now uses the shared native builder on hosted macOS without
a simulator/emulator; runtime/device tests and app signing are excluded.
Its revised lint-only PR workflow passed on the reviewed native-only consumer
tree; the earlier simulator failures remain failures. Native Xcode build
acceptance is the separate branch-build verdict above, not part of this PR run.

Preparation metadata is now assembled from existing inputs: 18 clean demo
commits match the frozen catalog receipt, and 15 existing archive configurations
were checked as linux/amd64 without extracting layers or rebuilding images.
The existing Go prepared-env, clean-source and required-runtime-target checks
passed. Normal archive/credential admission still applies to a real run; this
is not self-build acceptance. Reviewed consumer variants remain separate from
the original catalog; the retired iOS simulator path must not be selected.
The latest ten verdict roots above reconcile to 15 new passing case/leaf
records; the earlier tally of 235 is preserved, not independently recounted.

Remaining gaps are explicit: Forgejo/GitLab Android Maven publication;
native hosted Linux arm64 preparation and acceptance under the frozen-source
and no-amd64-rebuild constraints; Forge CI self-build consumer readiness and
acceptance on all three forges; the minimal Nano scheduler
witness (local policy/completion evidence is reused, but no short committed
live fixture is ready); independent image readback for the original cleaned Forgejo GPG-file
release; and final row/input equivalence reconciliation. The original Nano
final PR and Forgejo workspace Buildah failures remain failures. Reviewed
local fixes and successful distinct variants do not rewrite those verdicts.

The bounded follow-up does not repeat the Forgejo same-run recovery release
cycle or any full Nano run. Same-run recovery remains unverified at the frozen
commit within this short-run scope. The Nano scheduling witness remains a
prerequisite before deploying its isolation change, not a new live claim.

The registry-only follow-up used reviewed harness commit
`b77156b6be57a52b9b4cd3cc364dc7d3bba0ec7c`, based on the frozen engine with
changes confined to three test files. Forgejo verdict
`20261118T164751Z-325859` and GitLab verdict `20261118T164751Z-325860` each
passed five leaves: dry-run promote/cleanup/rollback (with wet positive
controls), journal rollback removing its created release tag, and journal
rollback restoring a moving tag while preserving the previous immutable tag.
Cleanup readback passed on both forges. These are **10 additional reviewed-harness
leaves**, separate from the 250 frozen-source progress tally. The original
`20261118T164011Z-311336` preflight remains unadmitted, with no lab effects;
both revised preflights were admitted before the live launches.

Repeat runs use the existing case tables, fixtures and commands in
[Live runs](live-runs.md), with explicit pinned inputs. The evidence-draft
command recognises prepared `demos/<name>` paths and named cases; standalone
snapshot receipts need a reviewed overlay entry, not a rerun or a rewritten
receipt. The generated tables below retain their dated historical marks;
they must not be interpreted as complete coverage of this frozen campaign.

## Bounded closeout

The reviewed Android Maven feature (`050df4a3`, `78b6e6fb`) is merged locally
into the separate integration branch at `23d3c818`. The merged focused
publication/admission, native-job/secret boundaries, producer handoff, parity
and matrix tests passed with the race detector; generated CLI documentation
and the affected shell scripts also passed their checks. This is implementation
verification, **not native Maven publication acceptance**. No live release,
SDK build, image rebuild or deployment was launched for that feature.

The remaining selected work is classified rather than queued indefinitely:

| Remaining scope | Readiness finding | Disposition |
|---|---|---|
| Android Maven publication on Forgejo/GitLab | Local implementation and focused checks passed; a new-source native publication trial remains missing | Separate acceptance work; do not repeat accepted AAR-file releases or relabel them as Maven publication |
| Nano isolation scheduling witness | Local policy/completion evidence is unchanged; no committed short live witness is ready, and `PIN_FORGE_CI_SHA` remains deliberately non-deployable | Deployment hold; no full Nano demo/release or pin-guard bypass |
| Forgejo self-build consumer | The frozen source supplies reusable Forgejo workflows, not a self-consumer build entry point | Route/fixture work, not an admitted short test waiting to run |
| GitLab self-build consumer | Root `.gitlab-ci.yml` publishes catalog releases; it is not a self-build gate | Route/fixture work; prepared archives do not supply this missing gate |
| GitHub self-build consumer | `self-pullrequest.yml` exists but includes the full lint/test/tool-bootstrap gate | Deferred under the short-run limit; self-release scheduling evidence is not a substitute |
| Exact canonical row/input equivalence | The 160-row frozen inventory and retained receipts exist; full equivalence reconciliation is unfinished | Evidence blocker to whole-profile acceptance; no broad rerun to manufacture a complete profile |

The bounded campaign therefore stops at its recorded scopes: **250 frozen-engine
case/leaf passes plus 10 separate reviewed-harness leaves**. Small, medium and
full profiles are not declared accepted. Original failures and unavailable
historical evidence remain recorded. Main, the frozen execution source and
accepted images are unchanged; nothing is pushed. No live lane remains active.

### Profile reconciliation boundaries

Reconciliation is an offline comparison, not another live run. Expand each
frozen case into the visibilities required by its set before matching receipts:
`PreparedCase.Visibilities` and `GitHubCell.Visibilities` define those obligations.
The 160-row inventory is not an expanded run denominator. Keep the frozen
tables separate from the integration branch's reviewed Android and iOS changes.

The first small-profile checks already show why a passing demo/event is not
enough to accept a profile:

| Frozen obligation | Retained standalone pass | Reconciliation finding |
|---|---|---|
| Forgejo small Go-service snapshot, public | `20261117T231651Z-2030416`, private | Different visibility; this receipt does not prove the public obligation |
| Forgejo small private Maven-service release, Kaniko | `20261117T224724Z-1780590`, Buildah with `release-images-config=.forge-ci/artifacts.yml` | Different builder and declaration; this receipt does not prove the requested image-release inputs |
| GitLab small Go-service snapshot, both visibilities | `20261117T234624Z-2248111` and `20261118T004131Z-2452462`, private | These receipts do not prove the public obligation |
| GitHub small cells, both visibilities | Campaign execution is public-only | The unchanged canonical profile requires private coverage outside the authorised campaign scope |

These are receipt comparisons, not an exhaustive search for reusable lifecycle
phases or hosted logs. Original passes remain valid within their recorded
scopes. A public-only campaign result must be labelled as such, not called an
accepted canonical profile or used to silently remove its private obligations.
No profile is accepted by this initial review; remaining source, image,
declaration, enforcement, version and phase equivalence still needs checking.

## How the tables are made

Matrices 1 and 2 are generated from one source by `just gen-testing-matrices`
(`livetest.RenderTestingMatrices`): the shipped demos' declarations as the
planner and the demo admission read them, the cases and lanes of the Go
tables, and the live marks of `test/live/evidence.txt`, a
hand-kept overlay keyed by forge, demo and flow, since whether a run proved
something is a judgement; `go run ./test/live/internal/livetest/preflight
testing-evidence ~/.local/state/forge-ci/live-runs` drafts the lab forges'
lines for review from the recorded verdicts; a hosted GitHub trial's line is
written by hand from its log. A refusal carries the planner's or the
admission's reason. `TestTestingMatricesInSync` in
`test/live/internal/livetest` fails when the committed tables are stale.
No prepared lane runs on hosted GitHub. There a
line is the fixture workflow a demo ships under `test/live/github/workflows`
for the event the hosted trial settings name, and the lifecycle has one when
the demo ships all three and the hosted settings accept the lifecycle. Its
image-builder rows are the hosted cells' (`livetest.GitHubCells`): a
lifecycle builds with Buildah, the default, and a cell that names kaniko
stands for it with its own event, such as a release.

Legend: ✅ live-proven · ◐ partial · ⏳ running or queued · ❌ failed, root fix
committed · ⛔ refused by planner admission (reason pinned) · 📝 line exists,
unrun · ⬚ no line · — not applicable. A ✅ that reads "passed on rerun after
an unavailable dependency" is a row the launcher ran twice
([the one rerun of a row](live-runs.md#the-one-rerun-of-a-row)).

### 1 Capabilities by project kind × flow (cells: Forgejo / GitLab / GitHub)

Rows are what a consumer is (the demo that embodies it), columns are the
flows the orchestrators offer. "Artifact-first" builds the snapshot image from
the planned artifacts; "container-first" builds from the declared container.

<!-- generated:project-kinds -->

| Demo (project kind) | PR / MR gate | Artifact-first snapshot | Container-first snapshot | Release | Full lifecycle |
|---|---|---|---|---|---|
| hello-android (gradle-android · no container · no image) | 📝 / ✅ (2026-11-13) / ✅ (2026-11-12, runs 43159891577 and 43160497879) | — | — | — / — / — | ⛔ / — / ⛔ |
| hello-android-library (gradle-android · no container · no image) | 📝 / ✅ (2026-11-13) / ✅ (2026-11-11, runs 43085301830 and 43086200652) | — | — | ⬚ / ⬚ / ✅ (2026-11-12, runs 43134153965 and 43135583711, 3 GPG signatures verified against the trial key) | ⛔ / ⛔ / ⛔ |
| hello-base-images (meta · no container · no image) | ⬚ / ⬚ / ⬚ | — | — | ✅ (2026-11-13) / ✅ (2026-11-13) / — | ⛔ / ⛔ / ⛔ |
| hello-cargo-cli (rust · no container · no image) | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-12, runs 43147401452 and 43148004422) | — | — | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-12, runs 43126466168 and 43127750273, 3 Cosign bundles verified against the trial key) | ⛔ / ⛔ / ⛔ |
| hello-cargo-container (rust · container-first · one image) | ✅ (2026-11-14) / ✅ (2026-11-14) / ✅ (2026-11-10, run 42848860564) | — | ✅ (lifecycle phase 3, 2026-11-10) / 📝 lifecycle / 📝 lifecycle | ✅ (2026-11-14) / ✅ (2026-11-14) / 📝 | ✅ (2026-11-14) / ✅ (2026-11-14) / ✅ (2026-11-10, release run 42919737094) |
| hello-cargo-workspace (rust · container-first · 2 images) | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-10, run 42849993300) | — | ✅ (2026-11-13) / ✅ (2026-11-13) / ⛔ | ✅ (2026-11-14) / ✅ (2026-11-13) / ✅ (2026-11-10, run 42915679647, 4 Cosign bundles verified against the trial key) | ⛔ / ⛔ / ⛔ |
| hello-go-cli (go · no container · no image) | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-12, runs 43148821568 and 43149415886) | — | — | ✅ (2026-11-14) / ✅ (2026-11-14) / ✅ (2026-11-10, run 42855974665, 6 Cosign bundles verified against the trial key; GPG variant, run 42853659291; on 2026-11-12 the variant with no SBOM bundle, runs 43163726687 and 43164848434, and the one with no changelog, runs 43166247915 and 43167368648) | ⛔ / ⛔ / ⛔ |
| hello-go-service (go · container-first · one image) | ✅ (2026-11-14) / ✅ (2026-11-14) / ✅ (2026-11-10, run 42845340779) | — | ✅ (2026-11-14) / ✅ (2026-11-14) / ✅ (2026-11-12, runs 43159984233 and 43160847885) | ✅ (2026-11-14) / — / ✅ (2026-11-12, the variant with GPG-signed files, runs 43137312566 and 43138850750) | ✅ (2026-11-13) / — / ✅ (2026-11-10, release run 42912107999) |
| hello-gradle (java · no container · no image) | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-12, runs 43144460883 and 43156917809; the private one failed once on a dependency its runner did not find (run 43145103157)) | — | — | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-10, run 42860266897, 5 Cosign bundles verified against the trial key) | ⛔ / ⛔ / ⛔ |
| hello-gradle-service (java · artifact-first · one image) | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-11, runs 43085300596 and 43086150292) | 📝 lifecycle / 📝 lifecycle / 📝 lifecycle | — | ✅ (2026-11-13) / ✅ (2026-11-13) / 📝 | ✅ (2026-11-14) / ✅ (2026-11-14) / ✅ (2026-11-12, release run 43135845817) |
| hello-ios (xcode-ios · no container · no image) | ⬚ / ⬚ / ✅ (2026-11-18, V 20261118T180112Z-ios-pr-lint-414868, run 44365178200, native-only consumer tree cf77a061; configured secrets/SAST and canonical PR gate passed; Swift lint, Xcode build, simulator and device tests not claimed) | — | — | — / — / ✅ (2026-11-18, run 44245796152, unsigned Xcode release v0.2.0; GPG-signed release commit/tag, no IPA or release file declared) | ⛔ / — / ⛔ |
| hello-maven (java · no container · no image) | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-12, runs 43143015490 and 43143618148) | — | — | ✅ (2026-11-14) / ✅ (2026-11-13) / ✅ (2026-11-12, runs 43151439440 and 43152520181) | ⛔ / ⛔ / ⛔ |
| hello-maven-reactor (java · no container · no image) | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-11, runs 43087350088 and 43088177855) | — | — | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-11, runs 43102069751 and 43103635645) | ⛔ / ⛔ / ⛔ |
| hello-maven-service (java · artifact-first · one image) | ✅ (2026-11-14) / ✅ (2026-11-13) / ✅ (2026-11-10, run 42842794730) | ✅ (2026-11-13) / ✅ (2026-11-13) / 📝 lifecycle | — | ✅ (2026-11-13) / ✅ (2026-11-14) / 📝 | ✅ (2026-11-14) / ✅ (2026-11-14) / ✅ (2026-11-12, release run 43155392448) |
| hello-meta (meta · no container · no image) | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-12, runs 43150118621 and 43150691588) | — | — | ✅ (2026-11-13) / ✅ (2026-11-14) / ✅ (2026-11-10, run 42831178553) | ⛔ / ⛔ / ⛔ |
| hello-monorepo (java, node · artifact-first · nested artifacts (monorepo)) | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-10, run 42851535452) | ✅ (2026-11-13) / ✅ (2026-11-13) / ⛔ | — | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-11, runs 43108226275 and 43110448315) | ⛔ / ⛔ / ⛔ |
| hello-npm-package (node · no container · no image) | ✅ (2026-11-13) / ✅ (2026-11-13) / ✅ (2026-11-12, from a fork, refused before checkout as it must be, run 43163527676) | — | — | ✅ (2026-11-14) / ✅ (2026-11-13) / ✅ (2026-11-11, runs 43104656725 and 43106178358) | ⛔ / ⛔ / ⛔ |
| hello-npm-service (node · artifact-first · one image) | ✅ (2026-11-14) / ✅ (2026-11-14) / ✅ (2026-11-10, run 42846629993) | 📝 lifecycle / 📝 lifecycle / 📝 lifecycle | — | ✅ (2026-11-14) / ✅ (2026-11-14) / 📝 | ✅ (2026-11-13) / ✅ (2026-11-14) / ✅ (2026-11-12, release run 43127999884) |

Outside the live harness's scope, the trial's acceptance and not a product rule:

- hello-cargo-workspace, forgejo: .forge-ci/release-images.yml is not a service snapshot's declaration, so the demo has no development route: the trial judges one container of the root Containerfile and context, planned for amd64, without image name, tag suffix or declared provenance, from at most one artifact
- hello-cargo-workspace, gitlab: .forge-ci/release-images.yml is not a service snapshot's declaration, so the demo has no development route: the trial judges one container of the root Containerfile and context, planned for amd64, without image name, tag suffix or declared provenance, from at most one artifact
- hello-cargo-workspace, github: .forge-ci/release-images.yml is not a service snapshot's declaration, so the demo has no development route: the trial judges one container of the root Containerfile and context, planned for amd64, without image name, tag suffix or declared provenance, from at most one artifact
- hello-monorepo, forgejo: the demo has no root Containerfile, so no service snapshot and no development route
- hello-monorepo, gitlab: the demo has no root Containerfile, so no service snapshot and no development route
- hello-monorepo, github: the demo has no root Containerfile, so no service snapshot and no development route

<!-- /generated:project-kinds -->

### 2 E2E scenarios × variants (lifecycle unless stated)

<!-- generated:e2e-variants -->

| Axis | Variant | Forgejo | GitLab | GitHub |
|---|---|---|---|---|
| ecosystem | go | ✅ hello-go-service (2026-11-13) | ⛔ hello-go-cli | ✅ hello-go-service (2026-11-10, release run 42912107999) |
|  | gradle-android | ⛔ hello-android-library | ⛔ hello-android-library | ⛔ hello-android-library |
|  | java | ✅ hello-gradle-service (2026-11-14) | ✅ hello-gradle-service (2026-11-14) | ✅ hello-gradle-service (2026-11-12, release run 43135845817) |
|  | meta | ⛔ hello-base-images | ⛔ hello-base-images | ⛔ hello-base-images |
|  | node | ✅ hello-npm-service (2026-11-13) | ✅ hello-npm-service (2026-11-14) | ✅ hello-npm-service (2026-11-12, release run 43127999884) |
|  | rust | ✅ hello-cargo-container (2026-11-14) | ✅ hello-cargo-container (2026-11-14) | ✅ hello-cargo-container (2026-11-10, release run 42919737094) |
|  | xcode-ios | ⛔ hello-ios | — hello-ios | ⛔ hello-ios |
| route | artifact-first | ✅ hello-gradle-service (2026-11-14) | ✅ hello-gradle-service (2026-11-14) | ✅ hello-gradle-service (2026-11-12, release run 43135845817) |
|  | container-first | ✅ hello-cargo-container (2026-11-14) | ✅ hello-cargo-container (2026-11-14) | ✅ hello-cargo-container (2026-11-10, release run 42919737094) |
|  | no container | ⛔ hello-android-library | ⛔ hello-android-library | ⛔ hello-android-library |
| images | 2 images | ⛔ hello-cargo-workspace | ⛔ hello-cargo-workspace | ⛔ hello-cargo-workspace |
|  | nested artifacts (monorepo) | ⛔ hello-monorepo | ⛔ hello-monorepo | ⛔ hello-monorepo |
|  | no image | ⛔ hello-android-library | ⛔ hello-android-library | ⛔ hello-android-library |
|  | one image | ✅ hello-cargo-container (2026-11-14) | ✅ hello-cargo-container (2026-11-14) | ✅ hello-cargo-container (2026-11-10, release run 42919737094) |
| signing | GPG declaration | ⛔ hello-go-cli | ⛔ hello-go-cli | ⛔ hello-go-cli |
|  | default | ✅ hello-cargo-container (2026-11-14) | ✅ hello-cargo-container (2026-11-14) | ✅ hello-cargo-container (2026-11-10, release run 42919737094) |
|  | gpg | ⛔ hello-android-library | ⛔ hello-android-library | ⛔ hello-android-library |
|  | sigstore | ✅ hello-gradle-service (2026-11-14) | ✅ hello-gradle-service (2026-11-14) | ✅ hello-gradle-service (2026-11-12, release run 43135845817) |
| image builder | buildah | ✅ lifecycle-forgejo (2026-11-14) | ✅ lifecycle-gitlab (2026-11-14) | 📝 hello-cargo-container |
|  | kaniko | ✅ lifecycle-forgejo (2026-11-13) | ✅ lifecycle-gitlab (2026-11-13) | 📝 hello-cargo-container release |
| visibility | public | ✅ lifecycle-forgejo (2026-11-14) | ✅ lifecycle-gitlab (2026-11-13) | ✅ hello-cargo-container (2026-11-10, release run 42919737094) |
|  | private | ✅ lifecycle-forgejo (2026-11-14) | ✅ lifecycle-gitlab (2026-11-14) | ✅ hello-cargo-container (2026-11-12, release run 43165254088; its release stalled once on an asset upload (run 43161570088)) |
| enforcement | admin lint | 📝 lifecycle-forgejo | 📝 lifecycle-gitlab | ⬚ no lane |
|  | admin vulnerabilities | 📝 lifecycle-forgejo | 📝 lifecycle-gitlab | ⬚ no lane |
|  | admin application-tests | 📝 lifecycle-forgejo | 📝 lifecycle-gitlab | ⬚ no lane |
|  | admin reproducible-builds | 📝 lifecycle-forgejo | 📝 lifecycle-gitlab | ⬚ no lane |
|  | none enforced | 📝 lifecycle-forgejo | 📝 lifecycle-gitlab | ⬚ no lane |
| architecture | amd64 | ✅ hello-cargo-container (2026-11-14) | ✅ hello-cargo-container (2026-11-14) | ✅ hello-cargo-container (2026-11-10, release run 42919737094) |
|  | arm64 | — D-5 | — D-5 | — D-5 |

Kept scenario lines, by lane:

| Lane | Scenario | State |
|---|---|---|
| build-capability-gitlab | `TestGitLabRelease_BuildImageCapability` | ✅ (2026-11-13, V 20261113T195248Z-1081137) |
| harness-inputs-gitlab | `TestGitLabRelease_ConsumerBranchProtection` | ✅ (2026-11-13, V 20261113T195248Z-1081137) |
| harness-inputs-gitlab | `TestGitLabRelease_RejectsPreseededProvenance` | ✅ (2026-11-13, V 20261113T195248Z-1081137) |
| harness-inputs-gitlab | `TestGitLabRelease_ConsumerInterruptedRecovery` | ✅ (2026-11-13, V 20261113T195248Z-1081137) |
| interruption-manual-gitlab | `TestGitLabWorkflow_InterruptedJob` | 📝 |
| k3s-private-gitlab | `TestGitLabRelease_MinimalSignedArtifacts` | ✅ (2026-11-13, V 20261113T195248Z-1081137) |
| k3s-private-gitlab | `TestGitLabCheck_ChecksOutPrivateConsumerWithJobToken` | ✅ (2026-11-13, V 20261113T195248Z-1081137) |
| k3s-private-gitlab | `TestGitLabCheck_ChecksOutTheConsumerPipelineCommit` | ✅ (2026-11-13, V 20261113T195248Z-1081137) |
| k3s-private-gitlab | `TestGitLabPolicy_ConsumerCannotReplaceTheSigningPipeline` | ✅ (2026-11-18, V 20261118T144051Z-4132144, frozen engine 0e513a29) |
| k3s-private-gitlab | `TestGitLabPolicy_JobTokenCannotWriteThePublicationRegistry` | ✅ (2026-11-18, V 20261118T144051Z-4132144, publication refusal and positive staging control, frozen engine 0e513a29) |
| k3s-private-gitlab | `TestGitLabRelease_MinimalBranchProtection` | ✅ (2026-11-13, V 20261113T195248Z-1081137) |
| k3s-private-gitlab | `TestGitLabRelease_ResourceGroupSpansTheChild` | ✅ (2026-11-18, V 20261118T144051Z-4132144, lock held through the child, frozen engine 0e513a29) |
| k3s-private-gitlab | `TestGitLabRelease_PromotionRetryAfterRegistryWrite` | ✅ (2026-11-18, V 20261118T153327Z-246338, post-write failure retained its journal and blocked downstream effects; retry preserved the digest; frozen engine 0e513a29) |
| k3s-private-gitlab | `TestGitLabBootstrap_PublishesAndDownloadsTheRecordedBinary` | ✅ (2026-11-13, V 20261113T195248Z-1081137) |
| recovery-forgejo | `TestForgejoRelease_InterruptedRecovery` | ✅ (2026-11-18, V 20261118T145342Z-97862, interrupted v0.2.0 published nothing; v0.2.1 recovered with 5 verified signatures; frozen engine 0e513a29) |
| trust-refusal-forgejo | `TestForgejoRelease_RejectsPreseededProvenance` | ✅ (2026-11-18, V 20261118T145342Z-97862, refused before signing/publication; frozen engine 0e513a29) |
| forgejo17-forgejo | `TestInRunner_NativeStepSummary` | ✅ (2026-11-14, V 20261114T011738Z-1069575) |
| forgejo17-forgejo | `TestForgejoRelease_RerunAfterInterruption` | ✅ (2026-11-14, V 20261114T014333Z-1342205) |

<!-- /generated:e2e-variants -->
