# Failed release recovery

Use this runbook after a production release job fails. The safe default is to
preserve immutable evidence, identify the last successful mutation, and choose
the forge- and stage-specific recovery path below. Never make a failed run appear to
have succeeded by moving or force-replacing a tag.

## Simple case

Identify the failed stage before choosing a retry. Do not delete or move a
final tag, release or published image to make a retry pass. Preparation and
downstream publication have different recovery contracts:

| Failed stage / delivery path | Recovery direction |
|---|---|
| GitHub publication after successful signing | Retry the failed publisher with the original signed checkpoint, as described below. |
| GitHub preparation/prerequisites with an existing final tag | Use the [exact-tag recovery criteria](#final-git-tag-exists); a matching tag name alone is insufficient. |
| Forgejo preparation after pushing the final tag | Preparation refuses an existing final tag. Preserve successful handoffs for downstream jobs; if preparation itself cannot complete, use a new version and request. |
| GitLab or a custom caller | Follow that selected policy project's job and handoff contract. Do not infer GitHub's preparation recovery behavior from the shared CLI or another forge's retry instructions. |

Releases start from the original release request, not a fresh event on the final
tag. A retry is supported only while the required source identities, handoffs
and destination state remain compatible with that operation.

For GitHub publication failures, retry the failed publisher job, preserving
the successful signing job. Its `release-signed-<run>-<attempt>` checkpoint is
kept for 30 days and contains the exact signed files and notes. Publication
verifies the signer's digest and rechecks the prepared tag. Do not regenerate
signatures to repair an already-published release: Sigstore bundles can differ
even when the binary is unchanged, and immutable releases refuse replacement.

## 1. Freeze and inventory

1. Stop automatic retries while the failure is classified.
2. Record the release-request tag and commit, intended final tag and release bump
   commit (if created), workflow run, assembly manifest, image ledger, and
   promotion journal.
3. Check which external mutations exist: final Git tag, forge release, uploaded
   assets, package versions, container version tags, moving container pointers,
   and transparency-log entries.
4. Preserve logs and manifests. They are recovery inputs, not disposable build
   output.

Do not expose secret values while collecting evidence. Credential presence and
provider error classes are sufficient for the inventory.

## 2. Choose the recovery path

### No external mutation

Fix the prerequisite or build failure and rerun the same release-request tag at
the same commit. Use the snapshot flow for branch testing; changing a stable
request to a prerelease is not recovery of the original version.

### Final Git tag exists

Verify that the remote final tag resolves to the intended release commit.

- On GitHub, a matching final tag can be reused only if all exact-tag recovery
  criteria below hold. Retry from the same release request, not from the final tag.
- Forgejo's prepare guard refuses an existing final tag. Retry downstream jobs
  from successful preparation handoffs, or use a new version if preparation
  cannot complete; do not remove the tag to bypass that guard.
- For GitLab/custom policies, check the selected policy's recovery contract
  before retrying preparation.
- If it differs, stop. Do not force-move or delete the tag. Choose a new version
  after investigating how the conflicting immutable tag was created.

An existing tag is not sufficient evidence by name alone; the commit identity
must match exactly. GitHub's release prerequisites enforce this. A final tag is
reused only when its release bump commit sits directly on the commit the
release-request tag names, is the branch head, and verifies with the release
key.

### Forge release or assets are partial

Keep the final tag immutable. Where the selected publisher supports a retry,
use the same commit, version and signed handoff. Before retrying, verify that any
existing asset with the same name has the expected digest. A conflicting asset
is evidence of a non-idempotent or compromised release and requires a new
version, not silent replacement.

### A package registry accepted the version

Treat an externally published package version as consumed. Do not overwrite it,
even if the forge release later failed. Complete any safe same-version steps
that the registry supports as idempotent; otherwise fix the cause and issue a
new patch version. Registry-specific deletion is not a normal rollback path.

### Container promotion partially completed

Use the promotion journal as the authority. `container ledger rollback` may
restore moving pointers from that journal; do not infer a previous digest from
tag names or logs. Immutable version tags remain evidence and are not deleted as
part of pointer rollback.

After a successful forward retry or journal-backed rollback, use the ledger
cleanup operation for staging tags. Cleanup is tag-scoped and must not delete a
manifest still referenced by a release tag.

## 3. Signing failures

Artifact signing fails unless every selected assembly asset exists and every
configured backend produces its advertised non-empty sidecar. A failed signing
run is not publishable. Remove only local staging output before retrying; do not
delete transparency-log entries or alter an already-published Git tag.

Artifact signing credentials and Git-object signing credentials are separate.
For example, repairing a keyless Sigstore failure does not repair a missing SSH
or GPG key needed for the release commit and tag.

## 4. Retry checklist

- The release-request tag still identifies the original request commit.
- Any final tag satisfies the selected forge's [recovery contract](#final-git-tag-exists);
  GitHub preparation recovery additionally checks the exact signed bump commit.
- The version is admitted by the original trusted `release-policy`: stable by
  default, or an explicitly permitted numbered prerelease.
- Existing assets and package versions do not conflict by digest/content.
- The assembly, checksums, signatures, SBOMs, ledger, and journal are bound to
  the intended release commit and their originating workflow attempts.
- Required credentials are present for the specific signing and publish
  backends in the resolved plan.
- Cleanup or rollback commands are driven by validated manifests/journals, not
  reconstructed shell arguments.

## 5. Escalate instead of retrying

Stop and investigate when any immutable identity disagrees: tag-to-commit,
asset-to-digest, package version-to-content, ledger entry-to-image digest, or
signature-to-publisher identity. Repeated retries cannot safely reconcile those
states. Preserve the evidence and release a new version only after the cause is
understood.
