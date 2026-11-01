# SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
#
# SPDX-License-Identifier: CC0-1.0

# The hello-monorepo demo's job image on GitLab: the check component runs the
# backend's Maven build and the frontend's npm tests in it, as a non-root user,
# with Git, curl and sha256sum for the engine bootstrap. The JDK and Maven come
# from the Maven image, Node and npm from the Node image the npm demo uses.
# `just demo-image hello-monorepo <dir>` builds it and saves the archive
# `just demo gitlab` takes as job-archive=.
FROM docker.io/library/node:24-trixie-slim@sha256:7f486cabe892bff8fef04c0f7da8dba84a560be3e4e2740b7a86afc57ebec315 AS node

FROM docker.io/library/maven:3.9-eclipse-temurin-25@sha256:96f175708f766ea049ab6c794a85335d238894c75f8eb364bddd98086a5b7cfd

USER 0:0

COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules

# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install -y --no-install-recommends git jq curl ca-certificates openssh-client bash coreutils findutils \
    && rm -rf /var/lib/apt/lists/* \
    && ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
    && groupadd --gid 65532 nonroot \
    && useradd --uid 65532 --gid 65532 --home-dir /home/nonroot --create-home --shell /bin/bash nonroot

ENV MAVEN_CONFIG=/home/nonroot/.m2 HOME=/home/nonroot
USER 65532:65532
WORKDIR /home/nonroot

RUN java -version && mvn -v && node --version && npm --version && git --version && command -v sha256sum
