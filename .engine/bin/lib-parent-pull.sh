#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
# SPDX-License-Identifier: CC0-1.0

# Usage: forge_ci_pull_parent TOKEN COMMAND [ARGS...]. TOKEN is a Bash
# function argument, never a subprocess argument. COMMAND is the verified
# engine (or a Bash function invoking it) and its fetch arguments.
forge_ci_pull_parent() (
  local parent_pull_secret=$1
  shift
  unset PARENT_REGISTRY_READ_TOKEN FORGE_CI_REGISTRY_AUTH_FILE
  if [[ -z "$parent_pull_secret" && -z "${PARENT_REGISTRY_REPOSITORY:-}" && -z "${PARENT_REGISTRY_USER:-}" && -z "${PARENT_REGISTRY_AUTH_ORIGIN:-}" ]]; then
    "$@"
    return
  fi

  : "${parent_pull_secret:?set PARENT_REGISTRY_READ_TOKEN for authenticated parent pulls}"
  printf '%s' "$parent_pull_secret" | "$@" \
    --parent-registry-repository "${PARENT_REGISTRY_REPOSITORY:-}" \
    --parent-registry-user "${PARENT_REGISTRY_USER:-}" \
    --parent-registry-auth-origin "${PARENT_REGISTRY_AUTH_ORIGIN:-}" \
    --parent-registry-password-file -
)
