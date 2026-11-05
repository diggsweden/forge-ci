# Forge CI project reference

Forge CI is a forge-neutral build and release engine with GitHub, GitLab and
Forgejo delivery layers.

## Project identity

| Surface | Name |
|---|---|
| Engine repository and executable | `forge-ci` |
| Engine Go module | `github.com/diggsweden/forge-ci` |
| External black-box suite repository | `forge-ci-blackbox-tests` |
| External black-box suite Go module | `github.com/diggsweden/forge-ci-blackbox-tests` |
| Configuration and build metadata directory | `.forge-ci` |
| Product environment prefix | `FORGE_CI_*` |
| Engine binary under test | `FORGE_CI_TEST_BINARY` |
| Workflow input prefix | `forge-ci-*` |
| Installer | `install-forge-ci.sh` |
| Layer setup action | `setup-forge-ci` |
| Layer binary checksums | `.engine/bin/forge-ci-linux-amd64.sha256`, `.engine/bin/forge-ci-linux-arm64.sha256` |
| Package and image prefix | `forge-ci` |
| Development branch | `main` |
| Release baseline | `v0.90.0` |
| Rolling prerelease channel | `v0.90.0-pre` |

The Go module has no major-version suffix. Third-party module paths and API
versions follow their own contracts. Provider names, the `blackbox` build tag,
and generic `CI_*`, `RC_*` and `BLACKBOX_*` controls retain their meanings.

Project-owned headers and REUSE metadata use
`2026 Digg - Agency for Digital Government`. Third-party licence notices retain
their original attribution.

## External black-box suite integration

The engine's self-workflows select the exact candidate with `FORGE_CI_TEST_BINARY`.
A binary-identity test checks that its help names Forge CI. Strict mode requires
complete test selection and the egress audit. For local source builds, select a
suite revision supporting `FORGE_CI_TEST_SOURCE` and set it to the absolute engine
checkout path; `FORGE_CI_TEST_BINARY` takes precedence when both are set.

Both engine self-workflows pin the suite by a reviewed commit. The
suite's drift workflow defaults to `diggsweden/forge-ci` on `main` and
records the resolved engine commit for each run.

## Before 0.90.0 publication

- Complete the engine race, integration and smoke gates and the suite's
  complete strict black-box gate against the intended release binary.
- Regenerate the CLI reference, schemas and engine checksum from that revision.
- Publish the engine source, checksum-bound package, runtime images and signer
  image at matching coordinates before enabling remote workflows.
- Authenticate each selected revision independently before setting approval
  variables. A pin selects code; an approval states trust in that code.
- Exercise the selected forge's bootstrap, signing and publication paths in the
  disposable lab and retain evidence for that exact revision and binary.

See [testing](testing.md), [runtime images](runtime-images.md), and
[verification](verification.md) for current requirements. Historical `v0.90.0` examples do not identify the current
source revision or establish availability of its matching CLI assets and images.
Confirm the selected coordinates before adoption.
