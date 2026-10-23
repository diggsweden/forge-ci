# Checked multi-architecture images

The consumer owns its platform selection, native runner routing, product smoke
tests, release names and base-image policy. Forge CI owns immutable image
validation, registry-only index assembly and checksum-bound SBOM composition.
The commands are provider-neutral and contain no consumer-specific policy.

## Build and publish the index

`container multiarch-image build-arch` and `assemble` accept `--tag` for an
explicit index tag name. Architecture tags append `-<arch>`. Without `--tag`,
the existing `--tag-prefix` plus source-commit naming remains unchanged.
`--tag` and `--tag-prefix` cannot be combined.

Each producing job builds and checks its own architecture. Run `assemble` only
after **every selected build, scan and smoke job succeeds**. Metadata is not
proof that a job's tests passed. Registry-backed candidates can exist before
the index; `--stage` and `--from-layouts` retain the stronger whole-matrix upload
barrier when the caller transfers local layouts instead.

For example, after both native jobs have produced `widget` metadata:

```bash
forge-ci container multiarch-image assemble \
  --server-url https://forge.example \
  --repository acme/widget \
  --source-sha "$SOURCE_SHA" \
  --image-repository registry.example/acme/widget \
  --tag staging-v1.2.3 \
  --name widget \
  --archs $'amd64\narm64' \
  --require-label "org.opencontainers.image.revision=$SOURCE_SHA" \
  --auth-file /workspace/registry-auth.json \
  --manifest-output /workspace/dist/index.json
```

The records are `widget-arch-amd64/widget-amd64.json` and the equivalent ARM64
path, relative to the current workspace. Their schema is:

```json
{
  "arch": "amd64",
  "tag": "registry.example/acme/widget:staging-v1.2.3-amd64",
  "digest": "sha256:<64 lowercase hex>",
  "ref": "registry.example/acme/widget@sha256:<same 64 lowercase hex>"
}
```

Custom builders can provide these records after their own checks. The engine
requires agreement between their architecture, expected tag, digest and
repository. It inspects the actual immutable child platforms and all repeated
`--require-label key=value` constraints before writing an index. Staged mode
also checks the local image labels before uploading any layout. An empty label
value is supported but the label must exist.

Supply the revision constraint explicitly when source identity is required.
`--source-sha` controls build labels and default tag naming; the commit hash and
metadata alone are not an authenticated release or a replacement for label checks.

The registry adapter writes only the index: it reuses the children already in
the repository. The served index digest must equal the locally constructed
index's digest. `--manifest-output` retains the exact checked bytes, without
JSON re-encoding or an added newline, for the offline SBOM step.
The snapshot path must not alias authentication inputs, architecture metadata,
or the generated image metadata file; directory and symlink outputs are refused.

## Verify a reused index

`container multiarch-image verify` takes the same metadata, tag, architecture,
repository and label inputs, plus `--ref` with the tag-free index digest
reference. It performs no registry writes. A mutable tag is refused.

It checks the actual child platforms, the index's byte digest and the exact
platform/child set. Missing, extra, repeated and substituted children fail.
A single-image manifest is accepted only when its bytes are the sole expected
child. `--manifest-output` is optional; without it no metadata file is written.
The operation emits `image-ref` and `image-digest` CI outputs after validation.

The current native build/assembly interface supports Linux AMD64 and ARM64.
Index verification does not execute either architecture's code.

## Compose the index SBOM

Keep each native CycloneDX document unchanged. Supply a version-1 manifest such
as `platform-sboms.json` alongside the verified `index.json` and native BOMs in
the release evidence directory:

```json
{
  "version": 1,
  "platforms": [
    {
      "platform": "linux/amd64",
      "image_ref": "registry.example/acme/widget@sha256:<amd64 digest>",
      "file": "sbom-amd64.json",
      "url": "https://downloads.example/widget/v1.2.3/sbom-amd64.json",
      "sha256": "<producer checksum of sbom-amd64.json>"
    },
    {
      "platform": "linux/arm64",
      "image_ref": "registry.example/acme/widget@sha256:<arm64 digest>",
      "file": "sbom-arm64.json",
      "url": "https://downloads.example/widget/v1.2.3/sbom-arm64.json",
      "sha256": "<producer checksum of sbom-arm64.json>"
    }
  ]
}
```

```bash
forge-ci sbom index \
  --image-ref "$INDEX_REF" \
  --root /workspace/dist \
  --index-file index.json \
  --platform-sboms platform-sboms.json \
  --output /workspace/dist/image-sbom-widget.cyclonedx.json
```

This is an offline operation. All inputs are regular files under `--root`;
traversal, symlink files, unknown manifest fields and trailing JSON are refused.
The index snapshot must hash to `--image-ref`, and its declared children must
match the complete platform record set. Each native BOM must match its
producer-supplied checksum. Overwriting an input document is refused.

The output is deterministic CycloneDX 1.6 with one container component per
platform and checksum-bound external BOM references. Native package graphs and
`bom-ref` values are not merged or rewritten. The command checks CycloneDX
headers and byte bindings, not full schema validity or inventory accuracy.
Use checked assembly or verification to establish actual child config identity
before composing from an index snapshot.

Public URLs must be absolute HTTPS without user information, query strings or
fragments. URLs are not fetched, and availability is not established here.
The caller must publish every referenced native document at the declared URL.
Register those files and the index BOM in the release-file manifest before
computing the final signing hand-off digest.

The command prints the index BOM checksum and emits `sbom-path`, `sbom-sha256`
and `evidence-files` CI outputs. Pass the output path and checksum to
`container ledger add --sbom ... --sbom-sha256 ...`. The existing signer then
attests those exact bytes instead of regenerating a host-selected inventory.

## Verification scope

Regression cases cover complete and incomplete architecture sets, duplicate
requests, wrong OS/digests, label mismatches, conflicting tags, read-back drift,
single-manifest substitution, deterministic SBOM links, native document drift,
unsafe paths, malformed manifests and credential-bearing URLs. Staged failures
are checked before registry writes using an in-process registry fixture.

These checks do not establish runner isolation, native smoke success, hosted
credential sharing, public URL availability or a production release's signing
authority. Those remain caller and hosted-acceptance responsibilities.
