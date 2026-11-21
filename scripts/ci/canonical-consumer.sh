#!/bin/bash
# SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
# SPDX-License-Identifier: CC0-1.0

# Receiver glue only. Source must be a fresh administrator-pinned engine checkout.
# Verification/transport admission remain owned by canonical-cli-delivery.sh.
#
# prepare, admit, receive   the CLI built in this run, delivered as an artifact
# image-prepare, image-extract, image-receive
#                           the CLI the pinned runtime image carries: a runtime
#                           image holds the build its engine commit records, so
#                           nothing is compiled and nothing is downloaded but
#                           the image the administrator already pinned
#
# Either way the launcher runs only bytes that match the checksum the pinned
# engine commit records, checked again at every invocation.
set -euo pipefail
export PATH=/usr/bin:/bin
[[ $# == 1 ]]
# One message for every way the image can fail to supply the recorded build:
# no file, a link in its place, or other bytes.
not_carried() {
  printf 'canonical-consumer: the runtime image does not carry the Forge CI build that commit %s records. Pin the runtime image of the same release, or set forge-ci-source: build.\n' "${ENGINE_COMMIT:-}" >&2
  exit 1
}
case "$1" in
prepare | admit | receive) [[ "${CANONICAL_PRODUCER_RESULT:-}" == success ]] ;;
image-prepare | image-extract | image-receive) ;;
*) exit 78 ;;
esac
# The admitted workflow states the tool profile as a literal; the helper owns
# the finite set and refuses an unknown name at the first verified probe.
[[ "${TOOL_PROFILE:-}" =~ ^[a-z][a-z-]*$ ]]
export ENGINE_CHECKOUT=${RUNNER_TEMP:?}/forge-ci-approved-engine
if [[ "$1" == prepare || "$1" == image-prepare ]]; then
  [[ ! -e "$ENGINE_CHECKOUT" && ! -L "$ENGINE_CHECKOUT" ]]
  # Retain the authenticated checkout outside the consumer workspace, while
  # leaving the composite action's source in place until that action completes.
  cp -a -- "${GITHUB_WORKSPACE:?}/_forge_ci_bootstrap" "$ENGINE_CHECKOUT"
fi
case "$(uname -m)" in
x86_64) arch=amd64 ;;
aarch64) arch=arm64 ;;
*) exit 78 ;;
esac
if [[ "$1" == image-* ]]; then
  # The image is the one the resolver admitted by digest. Its own CLI is
  # trusted for nothing here: only the checksum of the pinned commit decides.
  [[ "${RUNTIME_IMAGE:-}" =~ ^[a-z0-9][a-z0-9./:_-]*@sha256:[0-9a-f]{64}$ ]]
  [[ "${GITHUB_RUN_ID:-}" =~ ^[1-9][0-9]*$ && "${GITHUB_RUN_ATTEMPT:-}" =~ ^[1-9][0-9]*$ ]]
  delivery=$RUNNER_TEMP/forge-ci-image-$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT
  binary=$delivery/forge-ci-linux-$arch
  case "$1" in
  image-prepare)
    [[ ! -e "$delivery" && ! -L "$delivery" ]]
    mkdir -m0700 -- "$delivery"
    exit 0
    ;;
  image-extract)
    [[ -d "$delivery" && ! -L "$delivery" && ! -e "$binary" && ! -L "$binary" ]]
    case "$TOOL_PROFILE" in
    # The job container is the admitted image.
    runtime) cp -P -- /usr/local/bin/forge-ci "$binary" || not_carried ;;
    # A hosted runner: read the file out of the image without running it.
    host)
      container=$(docker create -- "$RUNTIME_IMAGE" /usr/local/bin/forge-ci)
      copied=0
      docker cp -- "$container:/usr/local/bin/forge-ci" "$binary" || copied=$?
      docker rm -- "$container" >/dev/null
      [[ "$copied" -eq 0 ]] || not_carried
      ;;
    *) exit 78 ;;
    esac
    exit 0
    ;;
  esac
  [[ -f "$binary" && ! -L "$binary" ]] || not_carried
  chmod 0700 -- "$binary"
  if ! /bin/bash -p "$ENGINE_CHECKOUT/scripts/ci/trusted-bootstrap.sh" "$ENGINE_CHECKOUT" \
    "${ENGINE_ORIGIN:?}" "${ENGINE_COMMIT:?}" "$binary" "$TOOL_PROFILE" -- --version >/dev/null; then
    not_carried
  fi
else
  /bin/bash -p "$ENGINE_CHECKOUT/scripts/ci/canonical-cli-delivery.sh" "$1"
  [[ "$1" == receive ]] || exit 0
  # Delivery admitted the file of this host's architecture; the launcher names it.
  binary=$RUNNER_TEMP/forge-ci-canonical-${GITHUB_RUN_ID:?}-${GITHUB_RUN_ATTEMPT:?}/forge-ci-linux-$arch
fi

# The binary has passed its admission, raw hash and bootstrap.
# Each subsequent invocation repeats the existing strict absolute-file verifier.
# Keep the engine/launcher outside consumer checkout, which may replace workspace.
directory=$RUNNER_TEMP/forge-ci-canonical-bin
[[ ! -e "$directory" && ! -L "$directory" ]]
mkdir -m0700 -- "$directory"
printf '#!/bin/bash -p\nexec /bin/bash -p %q %q %q %q %q %q -- "$@"\n' \
  "$ENGINE_CHECKOUT/scripts/ci/trusted-bootstrap.sh" "$ENGINE_CHECKOUT" \
  "${ENGINE_ORIGIN:?}" "${ENGINE_COMMIT:?}" \
  "$binary" \
  "$TOOL_PROFILE" >"$directory/forge-ci"
chmod 0700 -- "$directory/forge-ci"
printf '%s\n' "$directory" >>"${GITHUB_PATH:?}"
printf 'verified=true\n' >>"${GITHUB_OUTPUT:?}"
