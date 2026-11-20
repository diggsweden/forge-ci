# Hello base images

A tiny graph of base images, a demo of the `base-images` example for the
live tests: `base`, which starts from nothing, and `tools` and `debug`, two flavors
built on it as one stage.

Its CI files, its planner and its build contexts are the example's, filled
in as an adopter would, and are rewritten from it rather than edited here;
the live tier puts the Forge CI under test in place of the pinned one. The
Forgejo workflow releases: it builds, signs and promotes `base`,
authenticates it, builds `tools` and `debug` on that one digest, and
publishes a signed inventory of all three images. The GitLab release does
the same from a release policy: `.gitlab/base-stages` holds the graph the
live tier gives that policy as `base-stages`.

Pass `REQUIRE_DIRECT_PARENT=true` to `just test-live-consumer forgejo` to
enable direct-parent acceptance, together with the expected child matrix:
`'DIRECT_PARENT_BUILDS_JSON=[{"name":"tools","arch":"amd64"},{"name":"debug","arch":"amd64"}]'`.
List both architectures when both are selected, and omit the root base. These
expectations are admitted before trial resources are created. Acceptance requires
successful parent-fetch and layout-build steps for every expected child/architecture,
and no `flavors-stage-inputs-*` artifacts. A run that only reuses existing
bases, or whose fetch succeeds but builder fails, does not pass this check.
The two child jobs pull the verified parent by digest. Leave the registry
settings unset for anonymous pulls. For a private parent pass
`PARENT_REGISTRY_REPOSITORY=<host/path>`, `PARENT_REGISTRY_USER=<user>` and
`secret-file=PARENT_REGISTRY_READ_TOKEN:<path>` with a separate pull-only
credential. Do not pass the lab's release token as the read credential.
If the registry uses a separate token service, also pass
`PARENT_REGISTRY_AUTH_ORIGIN=<trusted HTTPS origin>` (without the token path).

## Acceptance boundaries

Source-repository visibility and registry-package visibility are independent.
A private source trial does not prove private parent pulls; successful
signature verification with a publication token does not prove builder access.
Record both visibility settings, the builder, native architecture, parent
digest, credential mode and frozen engine/consumer revisions with each run.
An amd64 Buildah pass is not an arm64 or Kaniko pass.

Before declaring the parent-pull migration accepted, retain separate receipts
for public anonymous access; private access denied without a credential;
private access with a dedicated pull-only credential; and rejection of an
invalid credential or a different repository. The local registry regression
tests cover the access-mode distinction, but are not hosted acceptance.

The tiny graph does not stand in for a consumer's nested flavor workflows.
Exercise a disposable clone of that consumer with its actual builder and
native architecture matrix, including cold builds and warm reuse. Include
failure after preparation followed by a downstream-only retry, checking that
the prepared commit/tag and successful job attempts remain unchanged. A
whole-run retry after tagging must fail without mutating those identities.
The existing interruption-after-handoff scenario checks only its narrower
checkpoint; consult the testing matrix for live evidence. These additional
recovery cases require their own live evidence.
