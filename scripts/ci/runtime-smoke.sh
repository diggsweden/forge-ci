#!/usr/bin/env bash
# SPDX-FileCopyrightText: The Forge CI Authors
# SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

# Run inside the candidate image, natively and without publication credentials.
# The caller supplies the Go version from the source Containerfile, not the image.
set -euo pipefail
target=${1:?runtime target required}
if [[ "$target" == go ]]; then
	expected_go=${2:?Go version from the source Containerfile required}
	[[ "$expected_go" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
		echo 'Invalid source Go version' >&2
		exit 1
	}
fi
bash --version
git --version
gpg --version
jq --version
yq --version
zip -v >/dev/null
git-cliff --version
gh --version
glab --version
mise --version
cosign version
forge-ci --version
test -d /opt/forge-ci/scripts/bootstrap
test -f /opt/forge-ci/templates/gitcliff/default.toml
case "$target" in
base) ;;
full)
	syft version
	trivy --version
	;;
go)
	test "$(go env GOVERSION)" = "go$expected_go"
	gcc --version
	go test -race runtime/race
	cyclonedx-gomod version
	msgmerge --version
	msgen --version
	msgattrib --version
	msgfmt --version
	;;
rust)
	rustc --version
	cargo --version
	cargo cyclonedx --version
	;;
node)
	node --version
	ldconfig -p | grep -F libatomic.so.1
	npm --version
	corepack --version
	;;
java | android)
	java -version
	gradle --version
	if [[ "$target" == java ]]; then
		mvn -version
	else
		sdkmanager --version
		adb --version
		test -d /opt/android-sdk/platforms
		test -d /opt/android-sdk/build-tools
	fi
	;;
*)
	printf 'unknown runtime target: %s\n' "$target" >&2
	exit 1
	;;
esac
