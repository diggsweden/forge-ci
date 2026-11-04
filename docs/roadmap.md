# Roadmap

Product features agreed for later, each with the reason it waits.

## Authenticated engine bootstrap for a private self-hosted distribution

Today the forge-ci engine layer is a public distribution. Every reusable
workflow and component fetches the layer's source and its recorded binary
package without credentials: the Forgejo workflows' helper checkout, the
GitLab templates' layer fetch and `fetch_forge_ci` in
`.engine/bin/lib-forge-ci.sh`. [GitLab](gitlab.md) says the engine
source and package must be publicly readable.

An organization that self-hosts forge-ci privately needs that bootstrap to
authenticate:

- GitLab: the job token, sent only to `CI_SERVER_URL` through a credential
  helper for git and as HTTP Basic auth for the package, never to a
  redirected host. The engine project's job-token allowlist then admits each
  consumer project.
- Forgejo: a credential that can read another private repository of the
  owner. The automatic job token reads only its own repository, so this
  needs a scoped secret, or a forge that grants cross-repository reads.
  Packages follow the owner's visibility, not the repository's.
- The same change in every flow at once (check, release, images, snapshot,
  signing, bases), so no flow fetches anonymously while the others
  authenticate.

The live suite keeps engine layers, job-image projects and probe assets
public in every run, with the reason recorded (`NewPublicScratchRepo`). The
repositories that hold the code under test follow
`FORGE_CI_LIVE_CONSUMER_VISIBILITY`. Once the bootstrap authenticates, the
layer's reason goes and its repository follows the run too.
