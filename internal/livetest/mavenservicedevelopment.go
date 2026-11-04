// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/container"
)

var (
	errServiceRecipeOrImage       = errors.New("name an absolute native recipe directory and a full digest-pinned Java job image")
	errServiceReadinessURL        = errors.New("readiness must be an explicit http://127.0.0.1:<port>/<project-endpoint> URL")
	errServiceReadinessPort       = errors.New("readiness requires a valid container port")
	errServiceSourceBranch        = errors.New("select a valid source branch different from policy main")
	errServiceFixtureRelative     = errors.New("fixture must be an absolute CI script path")
	errServiceFixtureNotRegular   = errors.New("fixture must be a regular CI script")
	errConflictingServiceVariable = errors.New("conflicting service variable")
)

// MavenServiceDevelopmentConfig supplies trusted trial infrastructure. Fixture
// is a CI-side script producing trial-runtime/runtime.env and optional files;
// it runs in the Java job, never on the controller or in the source checkout.
type MavenServiceDevelopmentConfig struct {
	RecipeDirectory, JavaImage, Branch, ReadinessURL, Fixture string
}

// MavenServiceDevelopmentVariables writes shared bindings once and refuses a
// caller plan that would change the operator-supplied repository authority.
func MavenServiceDevelopmentVariables(plan, runtime map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(plan)+len(runtime))
	maps.Copy(result, plan)

	for key, value := range runtime {
		if previous, found := result[key]; found && previous != value {
			return nil, fmt.Errorf("%w %s", errConflictingServiceVariable, key)
		}

		result[key] = value
	}

	return result, nil
}

//go:embed testdata/maven-service-development.yml.tmpl
var mavenServiceDevelopmentRecipe string

// NewMavenServiceDevelopmentFixture reuses the native Maven and snapshot layer
// recipes, overlaying CI only in Consumer's private index. The original
// Containerfile builds independently; checked JAR/image byte equality is NOT claimed.
func NewMavenServiceDevelopmentFixture(tb TB, source Consumer, config MavenServiceDevelopmentConfig) Consumer {
	tb.Helper()

	ctx, cancel := context.WithTimeout(Context(tb), timeoutLayerPush)
	defer cancel()

	fixture, err := mavenServiceDevelopmentFixture(ctx, source, config)
	if err != nil {
		tb.Fatalf("Maven service development fixture: %v", err)
	}

	return fixture
}

func mavenServiceDevelopmentFixture(ctx context.Context, source Consumer, config MavenServiceDevelopmentConfig) (Consumer, error) {
	if err := config.validateInfrastructure(); err != nil {
		return source, err
	}

	if _, err := source.git.run(ctx, source.stage, "check-ref-format", "refs/heads/"+config.Branch); err != nil || config.Branch == defaultBranch || config.Branch == "" {
		return source, errServiceSourceBranch
	}

	artifact, err := mavenLibraryArtifact(ctx, source)
	if err != nil {
		return source, err
	}

	if _, err = githubWorkflow(ctx, source, "Containerfile"); err != nil {
		return source, err
	}

	fixture, err := readMavenServiceFixture(config.Fixture)
	if err != nil {
		return source, err
	}

	body, err := os.ReadFile(filepath.Join(config.RecipeDirectory, ".forgejo/workflows/release.yml"))
	if err != nil {
		return source, err
	}

	pin, err := ReadConsumerPin(map[string]string{trialReleaseWorkflow: string(body)})
	if err != nil {
		return source, err
	}
	// YAML quoting prevents branch/endpoint metadata becoming syntax.
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

	workflow := strings.NewReplacer("@ACTIONS@", pin.Actions, "@WORKFLOWS@", pin.Workflows,
		"@PIN@", pin.Commit, "@IMAGE@", config.JavaImage, "@BRANCH@", quote(config.Branch),
		"@URL@", quote(config.ReadinessURL), "@ARTIFACT@", artifact).Replace(mavenServiceDevelopmentRecipe)
	if _, err = source.git.run(ctx, source.stage, "read-tree", source.Commit); err != nil {
		return source, err
	}
	// A container-only declaration deliberately creates no unused Maven producer.
	// Maven's original checked app lifecycle runs once in the separate check job.
	declaration := "containers:\n  - name: service\n    container-file: Containerfile\n    context: .\n    platforms: linux/amd64\n    enable-slsa: false\n"
	for path, text := range map[string]string{
		forgejoDevelopmentWorkflow:          workflow,
		".forge-ci/service-development.yml": declaration,
		".forge-ci/service-fixture.sh":      fixture,
	} {
		if err = source.stageFile(ctx, "100644", path, text); err != nil {
			return source, err
		}
	}

	source.Commit, err = source.commit(ctx, consumerBaseMessage)

	return source, err
}

// validateInfrastructure checks the recipe directory, the job image and the
// readiness endpoint before any Git work.
func (c MavenServiceDevelopmentConfig) validateInfrastructure() error {
	if !filepath.IsAbs(c.RecipeDirectory) || !container.ValidDigestReference(c.JavaImage) {
		return errServiceRecipeOrImage
	}

	endpoint, err := url.Parse(c.ReadinessURL)
	if err != nil || endpoint.Scheme != "http" || endpoint.Hostname() != "127.0.0.1" || endpoint.Port() == "" || endpoint.Path == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return errServiceReadinessURL
	}

	if port, err := strconv.Atoi(endpoint.Port()); err != nil || port < 1 || port > 65535 {
		return errServiceReadinessPort
	}

	return nil
}

// readMavenServiceFixture reads the optional operator-supplied CI script; an
// empty path yields an empty script.
func readMavenServiceFixture(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	if !filepath.IsAbs(path) {
		return "", errServiceFixtureRelative
	}

	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errServiceFixtureNotRegular
	}

	body, err := os.ReadFile(path) //nolint:gosec // operator-supplied fixture, checked above as an absolute regular file.
	if err != nil {
		return "", err
	}

	return string(body), nil
}

// DispatchConsumerDevelopment dispatches policy main once; selected source is
// resolved by the shared snapshot planner, not by this controller.
func DispatchConsumerDevelopment(tb TB, target Target, repo string) {
	tb.Helper()
	requireAccepted(tb, target)

	ctx, cancel := context.WithTimeout(Context(tb), timeoutLayerPush)
	defer cancel()

	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+$`).MatchString(repo) {
		tb.Fatalf("invalid scratch repository")
	}

	err := discard(ctx, target, http.MethodPost, actionsURL(target, repo, "/actions/workflows/development.yml/dispatches"), map[string]string{"ref": defaultBranch}, http.StatusNoContent)
	if err != nil {
		tb.Fatalf("dispatch development policy: %v", err)
	}
}
