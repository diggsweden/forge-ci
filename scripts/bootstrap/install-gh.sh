#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
# SPDX-License-Identifier: CC0-1.0

# Install the pinned GitHub CLI (gh), unless that version is already on PATH.
#
# Usage: source this file, then call install_gh
#   source "$(dirname "$0")/../bootstrap/install-gh.sh"
#   install_gh

# shellcheck source=install-common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/install-common.sh"

# renovate: datasource=github-releases depName=cli/cli
readonly GH_VERSION="v2.103.2"
readonly GH_SHA256_LINUX_AMD64="62ccf747115ddc61237efffdde66a3c8f70ba249433dc64f70801089b59c59c8"
readonly GH_SHA256_LINUX_ARM64="4c3e99a50bd8b181a3fe9d69b25dd978bac768148d88f01507c6b4d1ef61a443"
readonly GH_SHA256_DARWIN_AMD64="2dc7f1fc4761f96732995f6e4f4c6b4d705f2635da4126620c8b30eaa67b9c98"
readonly GH_SHA256_DARWIN_ARM64="5416000f5d17580a187339e0c4068a01ca5c87f7d4fe6ca64933696f967bc418"

resolve_gh_dist() {
	local os arch dist=""
	os="${OS:-$(uname -s)}"
	arch="${ARCH:-$(uname -m)}"

	case "$os" in
	Linux)
		case "$arch" in
		x86_64 | amd64) dist="gh_${GH_VERSION#v}_linux_amd64.tar.gz" ;;
		aarch64 | arm64) dist="gh_${GH_VERSION#v}_linux_arm64.tar.gz" ;;
		esac
		;;
	Darwin)
		case "$arch" in
		x86_64 | amd64) dist="gh_${GH_VERSION#v}_macOS_amd64.zip" ;;
		aarch64 | arm64) dist="gh_${GH_VERSION#v}_macOS_arm64.zip" ;;
		esac
		;;
	esac

	if [[ -z "$dist" ]]; then
		printf "ERROR: Unsupported gh platform: %s/%s\n" "$os" "$arch" >&2
		return 1
	fi

	printf '%s' "$dist"
}

resolve_gh_sha256() {
	case "$1" in
	gh_2.103.2_linux_amd64.tar.gz) printf '%s' "$GH_SHA256_LINUX_AMD64" ;;
	gh_2.103.2_linux_arm64.tar.gz) printf '%s' "$GH_SHA256_LINUX_ARM64" ;;
	gh_2.103.2_macOS_amd64.zip) printf '%s' "$GH_SHA256_DARWIN_AMD64" ;;
	gh_2.103.2_macOS_arm64.zip) printf '%s' "$GH_SHA256_DARWIN_ARM64" ;;
	*)
		printf 'ERROR: no pinned SHA-256 for gh asset %s\n' "$1" >&2
		return 1
		;;
	esac
}

install_gh() {
	ci_use_installed_pin gh gh "$GH_VERSION" --version && return 0

	local install_dir dist sha256 asset_url archive
	install_dir="$(ci_install_dir gh)"
	mkdir -p "$install_dir"

	dist="$(resolve_gh_dist)" || return 1
	sha256="$(resolve_gh_sha256 "$dist")" || return 1
	asset_url="https://github.com/cli/cli/releases/download/${GH_VERSION}/${dist}"
	archive="$install_dir/${dist}"

	printf "Installing gh (version: %s)...\n" "$GH_VERSION"
	if ! ci_download_verified "$asset_url" "$archive" "$sha256"; then
		printf "ERROR: Failed to download gh from %s\n" "$asset_url" >&2
		return 1
	fi

	local extracted_root
	case "$dist" in
	*.zip)
		unzip -q "$archive" -d "$install_dir"
		extracted_root="$install_dir/${dist%.zip}"
		;;
	*.tar.gz)
		tar -xzf "$archive" -C "$install_dir"
		extracted_root="$install_dir/${dist%.tar.gz}"
		;;
	esac
	mv "$extracted_root/bin/gh" "$install_dir/gh"
	rm -rf "$extracted_root" "$archive"
	chmod +x "$install_dir/gh"
	ci_prepend_path "$install_dir"

	ci_require_command gh gh || return 1
	ci_print_installed "gh" "$(gh --version 2>/dev/null | head -1 | tr -d '\r')"
}
