// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"maps"
	"slices"
	"strings"
)

// layerActions classifies every action of the layer, by its directory under
// .forgejo/actions/, by whether a release job that holds a secret may run it.
// True is an action that runs only the engine on its inputs. False is one
// that runs the project's code, a tool the project configures, or commands
// the caller supplies: in a job that holds a secret, that code reads it.
// test/forgejoguard holds the keys to the directory, so a new action is
// classified before it ships, and the isolation rule refuses an action this
// list does not name.
var layerActions = map[string]bool{
	"assemble-release-dist":  true,  // downloads artifacts, merges ledgers and digests them with the engine
	"build-cargo":            false, // builds and tests the project
	"build-go":               false, // builds and tests the project
	"build-goreleaser":       false, // runs GoReleaser on the project's config and hooks
	"build-maven":            false, // runs the project's Maven lifecycle
	"check-l3-isolation":     true,  // the engine's validation of workflow files
	"checkout-consumer":      false, // brings the project's code into the workspace
	"generate-changelog":     false, // runs a changelog tool on the project's config, which can run commands
	"guard-release-tag":      true,  // the engine's validation of a tag
	"lint-nanolinter":        false, // runs linters, and the builds some of them need, over the project
	"pr-check-results":       true,  // the engine's report of job results
	"prepare-release":        false, // the prepare workflow's own step: changelog tooling and a release push
	"prune-base-images":      true,  // the engine's attested retention pass
	"publish-forge-packages": true,  // the engine's upload, except the mvn deploy path: see runsMavenDeploy
	"record-release-image":   true,  // the engine's ledger entry
	"registry-login":         true,  // the engine writes an auth file
	"sbom-cargo":             false, // drives the project's toolchain over its manifests
	"sbom-go":                false, // drives the project's toolchain over its manifests
	"setup-buildah":          false, // installs the packages the caller names
	"setup-forge-ci":         true,  // fetches the engine by its checksum
	"setup-image-builder":    false, // installs the packages the caller names
	"setup-toolchain":        false, // installs the tools the project's config names, with third-party actions
	"upload-dist":            true,  // the engine digests and uploads the hand-off
	"verify-forgejo-token":   true,  // the engine probes a token
}

// LayerActions returns the classification of the layer's actions: true for
// one a release job that holds a secret may run.
func LayerActions() map[string]bool { return maps.Clone(layerActions) }

// PrivilegedActions returns the names of the actions a release job that
// holds a secret may run, sorted.
func PrivilegedActions() []string {
	var names []string

	for _, name := range slices.Sorted(maps.Keys(layerActions)) {
		if layerActions[name] {
			names = append(names, name)
		}
	}

	return names
}

// layerActionDir is the path of the layer's actions in its repository.
const layerActionDir = "/.forgejo/actions/"

// NamesLayerAction reports a remote uses: at the path of a layer action,
// whichever repository it names.
func NamesLayerAction(uses string) bool {
	_, ok := layerActionName(uses)

	return ok
}

// layerActionName returns the layer action a remote uses: names, by its
// directory. Which repository and pin it names is the pin check's.
func layerActionName(uses string) (string, bool) {
	_, rest, found := strings.Cut(uses, layerActionDir)
	if !found {
		return "", false
	}

	name, _, found := strings.Cut(rest, "@")
	if !found || name == "" || strings.Contains(name, "/") {
		return "", false
	}

	return name, true
}

// runsMavenDeploy reports whether a publish-forge-packages step publishes
// a Maven project by running mvn deploy, which runs the project's build. Only
// a literal project type that is not Maven, or a literal from-build: true,
// takes another path; anything else is read as the Maven default.
func runsMavenDeploy(projectType, fromBuild string) bool {
	switch projectType {
	case "npm", "gradle", "gradle-android":
		return false
	}

	return fromBuild != "true"
}
