# Administrator check enforcement

Forge CI separates **running a check**, **reporting its findings**, and
**blocking progression**. The administrator owns four settings, all enabled
by default:

| Setting | Findings it decides |
|---|---|
| `enforce-lint` | Lint and source-check findings |
| `enforce-vulnerabilities` | Dependency and image vulnerability findings |
| `enforce-application-tests` | Recognized application test assertion failures |
| `enforce-reproducible-builds` | Missing declared reproducible-build settings |

Setting a value to `false` permits that class of recognized findings to be
reported without failing the gate. It does not skip the check and does not turn
findings into a clean pass. Compilation, packaging, scanner startup, invalid
reports, cancellation, credentials, signatures, source binding and digest
verification are outside these exceptions.

## Who sets them, and how

The engine carries an embedded default, `internal/domain/enforcement/policy.json`,
with every setting `true`. The administrator replaces a default at run time,
without rebuilding the engine, through one channel per forge: an input of the
workflow, action or component that runs the gate, set in the administrator's
own protected caller.

| Forge | Where the administrator sets them |
|---|---|
| GitLab | Inputs of the components included by the independently pinned policy project: `release`, `check`, `nanolinter`, `snapshot-images` and the `build-*` components. |
| GitHub | Inputs of the reusable workflows the administrator's protected wrapper calls: the orchestrators forward them to `lint-nanolinter.yml`, `lint-swift.yml`, `build-*.yml`, `validate-release-prerequisites.yml`, `publish-container.yml` and `snapshot-images.yml`. |
| Forgejo | Inputs of the `lint-nanolinter` and `build-*` actions and of `snapshot-images.yml`, in the administrator's protected workflow. |

Each input reaches the engine as a flag of the gate's own command line:

```bash
forge-ci policy nanolinter --enforce-lint=false --enforce-vulnerabilities=true -- \
  nanolinter verify --require secrets,sast --report sarif --out -
```

The flags `--enforce-lint`, `--enforce-vulnerabilities`,
`--enforce-application-tests` and `--enforce-reproducible-builds` are global,
so every gate honours them. They take exactly `true` or `false`; any other
spelling, an empty value or the same flag given twice is refused. A setting
not given keeps the embedded default.

There is deliberately no second source. The flags have no environment
variable; no plan file, policy-file path, artifact, file in the checkout or
mutation command sets one; and a file named `policy.json` in a consumer
checkout has no effect. A gate whose environment has a variable named like a
setting, `ENFORCE_LINT` or any name ending in `_ENFORCE_LINT` (likewise for
the others, in any letter case), refuses to run instead of ignoring it. On
GitLab every project, group and instance variable reaches the job's
environment, so a variable set there to relax a gate stops it, visibly.

### What a consumer can and cannot change

The settings belong to whoever writes the gate's command line, and the
administrator's protection of that caller is what makes them administrator
policy:

- **GitLab.** The components assign each input to a read-only shell value in
  the job's script, as they do the other policy values, and pass it as the
  flag. A project, group or instance variable cannot supply or change it.
  Under a pipeline policy the consumer's own `.gitlab-ci.yml` does not run;
  a consumer's merge-request gate file, which the policy includes, is the
  consumer's, and its includes keep the defaults unless it sets them, as it
  could omit the gate altogether.
- **GitHub and Forgejo.** The reusable workflows and actions read the
  settings only from their own `inputs`, never from `vars.*`, repository
  variables or the environment. Only the administrator's protected wrapper
  may set them. A consumer that calls the reusable workflow directly owns
  that call, so it gets the defaults unless it passes the inputs; branch
  protection and required checks must name the administrator's wrapper jobs,
  as they must for the gates themselves. Workflow-dispatch inputs the
  consumer controls and workflow files a pull request modifies are not on the
  protected path; see [Threat model](threat-model.md).
- **Commands a job runs from its inputs.** GitLab's `check` job and the
  release's checks and build jobs run commands their caller lists, often a
  script of the consumer's repository. The job puts a `forge-ci` first on
  `PATH` that carries the administrator's four settings as flags, so
  `forge-ci build maven run` in such a script gates under them; given a
  second time, a setting is refused. `FORGE_CI_BIN` names the bare engine,
  which applies the embedded defaults. A consumer script that runs code can
  always decide not to run a gate; these settings are not proof that it did,
  and the gate's receipt is.

Changing the embedded default remains possible for an organization that
wants a different default everywhere: change `policy.json` in its
independently controlled Forge CI distribution, review it, build the engine
and runtime images, and update the protected pipeline pins and binary/image
digests together. A malformed embedded policy fails gates closed; omitted
keys default to `true`; unknown keys, duplicate keys and non-boolean values
are refused.

## The receipt

Inspect the effective policy with:

```bash
forge-ci policy show
forge-ci policy show --enforce-lint=false
```

The output gives each setting, its source (`embedded-default` or
`admin-input`) and the SHA-256 of the embedded default. The GitLab jobs that
run caller-listed commands print it at their start. Every finding decision
prints the check class, actual findings, whether enforcement is enabled and
where that came from:

```text
Admin policy sha256:<digest>: lint result=findings count=3 enforcement=false source=admin-input
```

`admin-input` records that the value was given on the engine's command line;
which caller wrote it is the pipeline's to show.

## Boundaries that stay

The legacy `skip-tests=true` input is refused, including `SKIP_TESTS=true`;
report-only tests must still run. Maven options that skip,
ignore or narrow tests (`skipTests`, `maven.test.skip[.exec]`,
`maven.test.failure.ignore`, fail-never, rerunning failed tests, and test
selection such as `-Dtest` or `-Dgroups`, in any spelling Maven accepts) and Gradle task exclusions are refused at the engine's build
gates. A project's own POM and `.mvn/maven.config` stay its responsibility.
Every exported or published layout is scanned; there is no consumer switch to
turn the scan off.
Forgejo lint/build actions checksum-verify their pinned engine even when a
consumer supplies `FORGE_CI_BIN`. GitLab's Nanolinter component bootstraps and
verifies that engine before lint execution, independently of optional SAST upload.

The protected pipeline must select the administrator's immutable engine and
workflow. GitLab uses the independently pinned external CI policy; GitHub and
Forgejo administrators must protect their wrappers, bootstrap coordinates and
credential-bearing workflow environments. A consumer-controlled engine build is
not administrator policy. Keep the same reviewed distribution in producer and
trusted signing jobs: the signer independently scans the delivered image bytes.
Producer variables and reports never become signing-policy authority.

## Lint and dependency findings

The shipped Nanolinter callers run once with full SARIF on stdout, then apply
the two policies independently:

```bash
forge-ci policy nanolinter --enforce-lint=true --enforce-vulnerabilities=true \
  --sarif-file nanolinter-security.sarif -- \
  nanolinter verify --require secrets,sast --report sarif --out -
```

OSV, Trivy filesystem, Go vulnerability and Rust audit findings use
`enforce-vulnerabilities`. Known lint/source checks use `enforce-lint`.
Unclassified security or external findings conservatively require both policies
to allow continuation. Disabling lint therefore cannot waive an independently
enforced vulnerability gate. Nanolinter configuration/runtime failures and a
failed invocation without classified findings remain blocking.
Structured operational-failure categories and the supported older report's
`tool failed` diagnostics remain blocking even if Nanolinter itself exits `1`.
The optional SARIF destination contains security results for existing provider upload steps;
its previous regular-file report is retired before execution, so a failed run
cannot upload that stale report as current evidence.

The check-selection floor still belongs in the administrator's trusted caller.
Enforcement is not proof that a consumer's test code is honest, that its chosen
plan covers every dependency, or that an arbitrary editable workflow executed
the approved gates. Provider protection and source review remain the authority
for those contracts. Unintegrated custom lint engines keep their own blocking
behavior; these settings are not a generic `continue-on-error` switch.

## Reproducible builds

`enforce-reproducible-builds` is strict reproducible builds. With the default
`true`, `validate jvm-reproducibility` (part of `validate prerequisites`) stops
the release when a planned Maven artifact's `pom.xml` lacks a non-empty
`<project.build.outputTimestamp>`, or a Gradle/Android build script lacks
`preserveFileTimestamps = false` and `reproducibleFileOrder = true`. With
`false`, the same check still runs, prints each missing setting with its fix
and the `reproducible-builds` policy decision, and annotates a warning instead
of an error. An unreadable or malformed `pom.xml`, a missing Gradle build
script and an unsafe working directory remain blocking. Cargo's `Cargo.lock`
and toolchain-pin requirements are not part of this setting.

## Application tests

The Go, Maven, npm, Cargo and Gradle/Android build paths apply the application
test policy after running tests. A non-enforcing engine only continues after an
ordinary native test-failure status with a recognized fresh summary. Unknown,
missing or malformed output stays blocking. The summary is recognized while the
tests run, so the size of a suite's output does not matter; a single output
line longer than 1 MiB stays blocking. Go's summary is read from the
`go test -json` events on stdout, and its stderr may only report module
downloads; the other tools' summaries are read from stdout and stderr. Maven
reports containing test setup errors are not assertion-only waivers; for
example a missing Testcontainers socket remains an error. Compilation and packaging invocations
are never waived. Where packaging needs a follow-up after failed tests, that
follow-up does not repeat the already reported tests.

When a non-enforcing engine stops anyway, the log says why, beside the test
tool's own error:

```text
Admin policy sha256:<digest>: application-tests result=not-waivable enforcement=false source=admin-input reason="no complete assertion-failure summary: test errors, setup and compile failures are never waived"
```

The other reasons name an exit status that is not the tool's assertion-failure
status, an output line over 1 MiB, or a command that failed without a test
exit status. A build blocked under report-only with no such line did not reach
the test gate.

Gradle invokes tests separately before packaging; Android retains its selected
module's test aggregate even when packaging tasks are overridden. A successful
invocation can report cached, up-to-date or `NO-SOURCE` tasks. Inspect native task
outcomes and test reports for physical execution and counts rather than treating
an invocation or summary checkmark as that evidence.

Supported Gradle project/system properties and global build options are passed
to both test and packaging invocations. Unknown or task-specific options (such
as `--tests`) are refused before execution rather than silently testing a
different configuration. `--dry-run`/`-m` applies only to packaging. Report-only
Gradle failures require matching assertion-only terminal task diagnostics and a
completed build-failure footer; daemon loss and unknown terminal causes block.

Custom trusted callers can use the same gate, including an iOS simulator job,
passing the administrator's setting as the shipped callers do:

```bash
forge-ci policy tests --enforce-application-tests=false --protocol go-json -- go test -json ./...
forge-ci policy tests --protocol xctest -- \
  xcodebuild test -scheme Example -destination 'platform=iOS Simulator,name=iPhone 17'
```

Protocols are `go-json`, `maven`, `cargo`, `node` (Node test-runner/TAP summary
with a native assertion diagnostic),
`gradle` and `xctest`. Other npm test frameworks remain blocking when their
failure output cannot be classified; disabling enforcement does not authorize
guessing that an unknown tool error is an assertion failure. An archive-only
Xcode build does not itself claim simulator-test coverage. Select the protocol
and command literally in the trusted caller, not from consumer-produced data.

Read the policy decision and original findings alongside native execution
reports, including absent/skipped and cached/no-source outcomes. An invocation
or summary checkmark alone does not prove physical test execution. A successful
non-enforcing job is not evidence of a findings-free application.
