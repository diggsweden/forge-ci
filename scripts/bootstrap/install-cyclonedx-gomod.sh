#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
# SPDX-License-Identifier: CC0-1.0

# Install cyclonedx-gomod, the Go Build SBOM generator, if not already available.
#
# forge-ci's runtime image carries it; a Forgejo build job runs on the
# consumer's runner instead, so build-go brings it the same way release-images
# brings Trivy. A cyclonedx-gomod already on PATH (from the consumer's mise
# config, say) is used as it is.
#
# Usage: source this file, then call install_cyclonedx_gomod
#   source "$(dirname "$0")/../bootstrap/install-cyclonedx-gomod.sh"
#   install_cyclonedx_gomod

# shellcheck source=install-common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/install-common.sh"

# Keep in step with CYCLONEDX_GOMOD_VERSION in containers/runtime/Containerfile.
# renovate: datasource=github-releases depName=CycloneDX/cyclonedx-gomod
readonly CYCLONEDX_GOMOD_VERSION="v1.10.0"
# Archive hashes are independently pinned here rather than downloaded beside
# the archive. Source: the upstream v1.10.0 release checksums asset.
readonly CYCLONEDX_GOMOD_SHA256_LINUX_AMD64="5cce8ae99a5181be6a610ea5ed9ca9d596937cc04dc1a8f6f6b5e462d8c9900e"
readonly CYCLONEDX_GOMOD_SHA256_LINUX_ARM64="b2bebbe569c39b6bd62b7a142269a8870795020ef608483c942e4b7f51f4de6b"
readonly CYCLONEDX_GOMOD_SHA256_DARWIN_AMD64="f4ec9f8bef518001c6d12b151cb965511d3e67fab22bed3a682f7cf4fd2f1728"
readonly CYCLONEDX_GOMOD_SHA256_DARWIN_ARM64="2a8e887efb07ed4e36d7ead879a15e055b1e50d60e5d45e407f9f2a27db4dc46"

install_cyclonedx_gomod() {
	if command -v cyclonedx-gomod &>/dev/null; then
		ci_print_already_installed "cyclonedx-gomod" "$(cyclonedx-gomod version 2>/dev/null | head -1)"
		return 0
	fi

	local install_dir os arch asset sha256 archive version
	install_dir="$(ci_install_dir cyclonedx-gomod)"
	mkdir -p "$install_dir"
	version="${CYCLONEDX_GOMOD_VERSION#v}"

	case "$(uname -s):$(uname -m)" in
	Linux:x86_64)
		os=linux
		arch=amd64
		sha256="$CYCLONEDX_GOMOD_SHA256_LINUX_AMD64"
		;;
	Linux:aarch64 | Linux:arm64)
		os=linux
		arch=arm64
		sha256="$CYCLONEDX_GOMOD_SHA256_LINUX_ARM64"
		;;
	Darwin:x86_64)
		os=darwin
		arch=amd64
		sha256="$CYCLONEDX_GOMOD_SHA256_DARWIN_AMD64"
		;;
	Darwin:arm64)
		os=darwin
		arch=arm64
		sha256="$CYCLONEDX_GOMOD_SHA256_DARWIN_ARM64"
		;;
	*)
		printf 'ERROR: unsupported platform for cyclonedx-gomod: %s/%s\n' "$(uname -s)" "$(uname -m)" >&2
		return 1
		;;
	esac
	asset="cyclonedx-gomod_${version}_${os}_${arch}.tar.gz"
	archive="$install_dir/$asset"

	printf "Installing cyclonedx-gomod (version: %s)...\n" "${CYCLONEDX_GOMOD_VERSION}"
	if ! ci_download_verified "https://github.com/CycloneDX/cyclonedx-gomod/releases/download/${CYCLONEDX_GOMOD_VERSION}/${asset}" "$archive" "$sha256" ||
		! tar -xzf "$archive" -C "$install_dir" cyclonedx-gomod; then
		rm -f "$archive" "$install_dir/cyclonedx-gomod"
		printf "ERROR: Failed to install cyclonedx-gomod %s\n" "${CYCLONEDX_GOMOD_VERSION}" >&2
		return 1
	fi
	rm -f "$archive"

	ci_prepend_path "$install_dir"
	ci_require_command cyclonedx-gomod cyclonedx-gomod || return 1
	ci_print_installed "cyclonedx-gomod" "$(cyclonedx-gomod version 2>/dev/null | head -1)"
}
