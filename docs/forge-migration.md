# Forge migration review

A migration is an executable delivery path, not a hostname replacement. Keep
the product/build logic shared and provider workflows thin. This checklist
captures delivery gaps when moving between GitHub, Forgejo and GitLab while
keeping an external registry such as GHCR.

## Map the outputs before changing workflows

For each output, name its source revision, producing workflow, destination,
verification method and consumers. Record its state as **prepared locally**,
**published**, or **consumer-verified**, with a run URL and immutable digest
where applicable. A passing source test does not advance publication state.

| Output | Responsible workflow | Acceptance evidence |
|---|---|---|
| Product CLI releases | Provider's configured product release workflow | Release files, signatures and download verification |
| Checksum-addressed engine binaries | Engine-package publisher | Anonymous downloads match both architecture checksums |
| Trusted signer image | Trusted signer publisher | Multi-architecture index and expected source/revision labels |
| Kaniko job image | Builder-image publisher | Published digest, architecture and builder admission checks |
| Runtime base and flavours | Runtime-image publisher | Native smoke tests, baked CLI byte identity, SBOM/provenance signatures and accessible digest references under the intended visibility policy |
| Consumer PR checks | Each consumer's PR workflow | Actual PR run at a recorded commit; installed tool contract and isolation checks passed |
| Sites and other deployments | Each deployment workflow | Separate destination/access and deployment evidence |

Signer/builder publication does not cover runtime packages. A GitHub Packages
page is the registry view; it does not imply GitHub Actions built the images.

## Review the provider boundary

| Concern | GitHub | GitLab | Forgejo |
|---|---|---|---|
| Entry points | `.github/workflows/` | `.gitlab-ci.yml` and included components | `.forgejo/workflows/` |
| Job identity | GitHub contexts and token | CI variables and job token | Forgejo contexts and repo-scoped job token |
| Registry authority | GitHub token when granted, or separate credentials | Job/deploy token or separate credentials | Separate GHCR credentials when publishing outside the forge |
| Signing identity | Configured OIDC or key | Configured OIDC or key | Configured organization key |
| Runner routing | Hosted/self-hosted labels | Runner tags | JSON label arrays |
| Artifacts | GitHub artifact adapter | GitLab artifact adapter | Forgejo artifact adapter |

Review each boundary explicitly:

- **Checkout:** discover the actual Git object format and use it consistently.
  Repositories can use SHA-1 or SHA-256. A copied consumer
  setting can break checkout before any useful validation runs.
- **Pins:** the bootstrap engine must already be served and the signer must be
  reachable by digest. Separate the trusted helper revision from the source
  revision being built. Do not advance all consumers before publication.
  Check shared contract versions on both producer and consumer, and run the
  lab preflight before scheduling jobs. Generate contracts through the
  producer rather than hand-editing private JSON to satisfy a reader.
- **Events:** verify that the destination forge discovers the entry point and
  that the intended push, PR, schedule or dispatch actually triggers it.
  A push-only site job is not evidence that PR checks ran.
- **Platforms and capabilities:** reuse the existing platform planner, native
  runner selectors, image-builder selector and optional runner lender. Test
  amd64-only, arm64-only and combined plans, including unsupported-flavour
  exclusions. Architecture support does not imply user namespaces or nested
  container execution support.
- **Tools:** inspect what the selected image actually contains. PR jobs use
  pre-provisioned tools; do not install PR-controlled tool definitions as a
  bootstrap shortcut. Strict container integration suites additionally need
  their documented capable runner and tools; a generic Go runtime is not a
  promise of that complete environment.
- **Authority:** keep signing keys out of builds, scans and candidate execution.
  Provider token permission models differ; Forgejo ignores GitHub's
  `permissions` field. Keep registry, forge API and signing credentials scoped
  to the jobs that need them.
- **Evidence:** preserve native tests, byte identity, source/platform binding,
  signature verification, SBOMs and provenance when changing transports or
  signing identity. Measure baked binaries from a trusted context rather than
  asking the candidate image to verify itself. Reject malformed or empty job
  plans before emitting outputs; shell process substitutions do not propagate
  producer failures to their loops. Preserve the documented scan policy rather than silently
  turning a blocking check advisory, or vice versa.
- **Publication:** test authenticated reads for private images, anonymous reads
  where public distribution is intended, first-package visibility, retries,
  stale-source refusal, architecture completeness and retention. A candidate
  upload is not a completed consumer release.

## Keep the implementation small

Use the engine's existing commands and provider adapters. Put workflow-specific
composition in small scripts and share stable checks, such as the runtime smoke
script, across providers. Use a narrow repository roster and tests to prevent
flavour drift; do not build a second workflow engine or a general migration DSL.

Separate bootstrap blockers from design changes. Nanolinter remains the intended
lint orchestrator, and Gommitlint remains one of its supported linters. Their
first releases need a bootstrap, after which released versions can check each
other. A temporarily red PR is recorded as such, without removing those tools
or claiming the missing checks passed.

## Upstream contribution boundary

Good candidates for upstream are the Go runtime's CGO/race/gettext support,
shared runtime smoke checks, cross-provider flavour/architecture tests,
portable candidate-digest execution and publication evidence checks. Extract
provider-neutral improvements from the proven fork implementation, with tests
and a small example demonstrating their use.

Keep each deployment's namespace, repository URLs, public-key digest, signer digest,
runner settings and deployment credentials in fork self-CI. Do not replace
upstream defaults globally to make the fork run. Keep the upstream GitHub path
working, and use the same smoke checks when adding a GitLab publication entry
point rather than claiming a Forgejo run proves GitLab behavior.

Before declaring a migration complete, attach the output inventory and the
successful destination-forge runs. Missing images, missing tool releases or
unrun provider jobs stay explicit follow-ups.
