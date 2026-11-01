# Hello Monorepo

A tiny monorepo for showing the `monorepo` example on Forge Lab: a Maven
service in `services/backend` and an npm service in `packages/frontend`,
each with a `Containerfile` for its runtime image. Both answer `GET /` with
a greeting and `GET /health` with `ok`, on port 8080.

Its CI files are the example's, filled in as an adopter would, and are
rewritten from it rather than edited here; `just demo` puts the Forge CI
under test in place of the pinned one. The GitLab file is the merge-request
gate: the lint gate and the check component running `mvn verify`,
`npm ci` and `npm test`.
