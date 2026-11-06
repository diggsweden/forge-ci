<!-- SPDX-FileCopyrightText: The Forge CI Authors -->
<!-- SPDX-License-Identifier: CC0-1.0 -->

# Release setup requirements

Run `forge-ci doctor --requirements` after initialization to see the credential
names, caller mappings and public settings used by known release entry points.
It reads local files only. It never reads secret values, generates keys, uploads
secrets or contacts a forge. Normal doctor checks and exit codes are unchanged.

For purposes, credential issuers, job/environment scopes and unused settings:

```sh
forge-ci --json doctor --requirements
```

## Coverage and confidence

The report recognizes the GitHub release orchestrator, Forgejo release
preparation and signing/publication workflows, and GitLab's direct `release`
component include. `--forge-ci-repository owner/repository` selects a mirror's
repository identity. Indirect GitLab includes and arbitrary custom jobs are not
expanded. Expressions, aliases, merged or malformed YAML receive coverage
warnings instead of being evaluated. Mobile publishers and backend-specific
KMS authentication need their own setup instructions.
Unrelated reusable build/check calls are skipped. Unknown release-named entry
points remain coverage gaps rather than being assigned another workflow's rules.

The report's rules describe the delivery contracts shipped with the CLI.
When its recorded source commit matches the workflow's immutable pin, the report
labels the reference rules revision-matched. Other pins, tags, and builds with
no commit metadata still receive **reference guidance**, explicitly marked
unverified. Check the pinned release's documentation before configuring
credentials; this report does not authenticate remote workflow content.
GitHub's runtime selection is checked separately: an absent or empty
`forge-ci-binary-ref` resolves a stable release at runtime, so matching the
orchestrator pin alone does not establish that engine's contract. An override
that differs from the workflow pin, or is an expression, also remains unverified.
Forgejo's `forge-ci-sha` must likewise match its workflow pin; a missing,
different or unresolved helper revision downgrades the contract label.

GitHub container `build-secrets` require the caller's
`FORGE_CI_BUILD_SECRETS_JSON` envelope mapping. The report lists the envelope,
never its values. Preparation settings include the release author's name and
email and the helper repository selection. Forgejo's empty or absent
`forge-ci-repository` uses `vars.FORGE_CI_REPOSITORY`; the report identifies that
fallback without checking whether the remote variable exists.

`complete` in JSON means static coverage of recognized calls, not successful
setup. `unknowns` records coverage gaps. `notes` explains conditional requirements,
such as images discovered only in a runtime ledger. A credential listed as
required is not necessarily missing: remote secrets have not been inspected.

## Configure credentials at their actual destination

- Keep source-forge API credentials separate from external registry credentials.
  A Forgejo repository publishing to GHCR needs different authorities for those
  operations. A registry token's scopes do not grant its account package access.
- Pass Actions secrets explicitly. The report recognizes direct
  `${{ secrets.NAME }}` mappings, including renamed caller secrets; it does not
  infer values from `secrets: inherit` or arbitrary expressions.
- GitLab's key-based release component uses protected project variables with
  environment scopes. Follow [the GitLab contract](gitlab.md#secrets-and-tokens),
  not a global all-jobs variable group. Its registry access preflight currently
  requires `RELEASE_REGISTRY_TOKEN` even for archive-only releases.
- Public keys and fingerprints are public configuration, although some workflows
  transport them through secret inputs. Trust digests belong in reviewed code.
  Never replace a trust root merely to resolve a key mismatch.

See [signing-key setup](quickstart-setup-secrets.md),
[Forgejo secrets](forgejo.md#secrets-and-tokens),
[GitHub permissions](reference.md), and [publisher credentials](publishing.md).
Local checks cannot establish runner isolation, protected-branch access, package
visibility or successful publication. Verify these through the appropriate
trusted preflight and hosted release acceptance process.
