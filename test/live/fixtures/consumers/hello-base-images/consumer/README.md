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
assert that the scratch run leaves no `flavors-stage-inputs-*` artifacts.
The two child jobs pull the verified parent by digest. Leave the registry
settings unset for anonymous pulls. For a private parent pass
`PARENT_REGISTRY_REPOSITORY=<host/path>`, `PARENT_REGISTRY_USER=<user>` and
`secret-file=PARENT_REGISTRY_READ_TOKEN:<path>` with a separate pull-only
credential. Do not pass the lab's release token as the read credential.
If the registry uses a separate token service, also pass
`PARENT_REGISTRY_AUTH_ORIGIN=<trusted HTTPS origin>` (without the token path).
