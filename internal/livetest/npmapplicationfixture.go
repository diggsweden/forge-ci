// SPDX-FileCopyrightText: The Forge CI Authors
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/container"
)

// NPMApplicationFixtureConfig is trusted CI policy, never application source.
// Oracle runs in the pinned Node job against an installed retained tarball.
type NPMApplicationFixtureConfig struct {
	RecipeDirectory, NodeImage, Oracle, Branch string
}

//go:embed testdata/npm-application-development.yml
var npmApplicationDevelopment string

var (
	errNPMRecipeOrImage     = errors.New("require absolute native recipe directory and digest-pinned Node image")
	errNPMApplicationModel  = errors.New("require npm application metadata with a package name and bin")
	errNPMOracleNotAbsolute = errors.New("require absolute installed-package oracle script")
	errNPMOracleNotRegular  = errors.New("oracle must be a regular CI policy file")
	errNPMSourceBranch      = errors.New("require source branch different from main")
	errNPMRecipeStructure   = errors.New("native npm recipe structure changed")
)

// NewNPMApplicationFixture overlays the native npm recipe, or its development
// variation, in Consumer's private index and commits it.
func NewNPMApplicationFixture(tb TB, source Consumer, config NPMApplicationFixtureConfig, development bool) Consumer {
	tb.Helper()

	ctx, cancel := context.WithTimeout(Context(tb), timeoutLayerPush)
	defer cancel()

	result, err := npmApplicationFixture(ctx, source, config, development)
	if err != nil {
		tb.Fatalf("npm application fixture: %v", err)
	}

	return result
}

func npmApplicationFixture(ctx context.Context, source Consumer, config NPMApplicationFixtureConfig, development bool) (Consumer, error) {
	if !filepath.IsAbs(config.RecipeDirectory) || !container.ValidDigestReference(config.NodeImage) || strings.ContainsAny(config.NodeImage, "\r\n '\"") {
		return source, errNPMRecipeOrImage
	}

	if err := validateNPMPackage(ctx, source); err != nil {
		return source, err
	}

	oracle, err := readNPMOracle(config.Oracle)
	if err != nil {
		return source, err
	}

	recipe, err := os.ReadFile(filepath.Join(config.RecipeDirectory, ".forgejo/workflows/release.yml"))
	if err != nil {
		return source, err
	}

	pin, err := ReadConsumerPin(map[string]string{"release.yml": string(recipe)})
	if err != nil {
		return source, err
	}

	var workflow string
	if development {
		workflow, err = npmDevelopmentWorkflow(ctx, source, config, pin)
	} else {
		workflow, err = npmStableReleaseWorkflow(string(recipe), config, pin)
	}

	if err != nil {
		return source, err
	}

	if _, err = source.git.run(ctx, source.stage, "read-tree", source.Commit); err != nil {
		return source, err
	}

	files, err := npmFixtureFiles(config, development, workflow, oracle)
	if err != nil {
		return source, err
	}

	for path, text := range files {
		if err = source.stageFile(ctx, "100644", path, text); err != nil {
			return source, err
		}
	}

	source.Commit, err = source.commit(ctx, consumerBaseMessage)

	return source, err
}

const npmApplicationBuildSteps = `      - name: Build original npm application
        env:
          RELEASE_TAG: ${{ needs.prepare.outputs.release-tag }}
          NODE_VERSION: '24.17.0'
          CYCLONEDX_VERSION: '6.0.0'
        run: forge-ci build npm run --expect-version "${RELEASE_TAG#v}"
      - name: Retain and install the package
        env:
          RELEASE_TAG: ${{ needs.prepare.outputs.release-tag }}
          RELEASE_SHA: ${{ needs.prepare.outputs.release-sha }}
        run: |
          set -euo pipefail
          mkdir -p retained-package
          cp -- ./*.tgz retained-package/
          node --input-type=module -e 'import fs from "node:fs"; const p=JSON.parse(fs.readFileSync("package.json")); fs.writeFileSync("retained-package/package-metadata.json", JSON.stringify({name:p.name,version:p.version}));'
          (cd retained-package && sha256sum ./*.tgz > SHA256SUMS)
          forge-ci artifact upload --name retained-npm-package --dir retained-package --if-no-files error
          mkdir -p oracle-policy
          cp -- .forge-ci/npm-oracle.sh oracle-policy/
          forge-ci artifact upload --name npm-oracle-policy --dir oracle-policy --if-no-files error
          cp -- retained-package/*.tgz dist/
          cp -- bom.json dist/application.cdx.json
          forge-ci release notes --source-file /nonexistent --target-file dist/release-notes.md --release-version "${RELEASE_TAG}" --release-commit "${RELEASE_SHA}"
          forge-ci release files declare --asset 'dist/*.tgz' --sbom dist/application.cdx.json
`

// Neither consumer checks out project code. Signing completes before either
// consumer starts, so a registry/oracle finding cannot block signed files.
const npmApplicationStableConsumers = `  installed-oracle:
    needs: sign-and-publish
    runs-on: ${{ fromJSON(vars.CI_RUNNER_LABEL) }}
    container:
      image: @IMAGE@
    steps:
      - uses: @ACTIONS@setup-forge-ci@@PIN@
      - run: forge-ci artifact download --name retained-npm-package --dir retained-package
      - run: forge-ci artifact download --name npm-oracle-policy --dir oracle-policy
      - run: bash oracle-policy/npm-oracle.sh "$PWD/retained-package"
  publish-package:
    needs: [prepare, sign-and-publish]
    runs-on: ${{ fromJSON(vars.CI_RUNNER_LABEL) }}
    container:
      image: @IMAGE@
    steps:
      - uses: @ACTIONS@setup-forge-ci@@PIN@
      - run: forge-ci artifact download --name retained-npm-package --dir retained-package
      - name: Validate retained bytes and metadata without project code
        env:
          EXPECTED_VERSION: ${{ needs.prepare.outputs.release-tag }}
        run: |
          set -euo pipefail
          cd retained-package
          sha256sum -c SHA256SUMS
          node --input-type=module -e 'import fs from "node:fs"; const p=JSON.parse(fs.readFileSync("package-metadata.json")); if(p.version!==process.env.EXPECTED_VERSION.replace(/^v/,"")) throw Error("release version mismatch");'
          mkdir verification
          cp -- ./*.tgz verification/
          (cd verification && forge-ci publish npm validate-tarball)
          node --input-type=module -e 'import fs from "node:fs"; const a=JSON.parse(fs.readFileSync("package-metadata.json")),b=JSON.parse(fs.readFileSync("verification/package.json")); if(a.name!==b.name||a.version!==b.version) throw Error("packed metadata mismatch");'
          rm -rf verification
          sha256sum -c SHA256SUMS
      - name: Publish retained bytes with version-specific tag
        env:
          NPM_REGISTRY: ${{ vars.NPM_APPLICATION_REGISTRY }}
          NODE_AUTH_TOKEN: ${{ secrets.RELEASE_TOKEN }}
          # Forgejo 16's version parser recognizes release-<version> as a
          # version and rejects it as a dist-tag, after storing the package.
          NPM_TAG: 'release.{version}'
        run: |
          set -euo pipefail
          set +x
          unset FORGEJO_TOKEN GITHUB_TOKEN GH_TOKEN RELEASE_TOKEN RELEASE_SSH_SIGNING_KEY RELEASE_COSIGN_PRIVATE_KEY RELEASE_GPG_PRIVATE_KEY
          cd retained-package
          export HOME=$(mktemp -d)
          trap 'rm -rf "$HOME"' EXIT
          export npm_config_cache="$HOME/cache"
          forge-ci publish npm write-npmrc --registry "$NPM_REGISTRY" --output "$HOME/.npmrc"
          export npm_config_userconfig="$HOME/.npmrc"
          name=$(node --input-type=module -e 'import fs from "node:fs"; console.log(JSON.parse(fs.readFileSync("package-metadata.json")).name)')
          version=$(node --input-type=module -e 'import fs from "node:fs"; console.log(JSON.parse(fs.readFileSync("package-metadata.json")).version)')
          forge-ci publish npm validate-tag --version "$version"
          forge-ci publish npm validate-version --name "$name" --version "$version" --registry "$NPM_REGISTRY"
          forge-ci publish npm deploy --working-dir . --registry "$NPM_REGISTRY" --npm-tag "$NPM_TAG"
          sha256sum -c SHA256SUMS
`

// npmDevelopmentWorkflow renders the development variation for a source
// branch other than main, refusing a consumer without a Containerfile.
func npmDevelopmentWorkflow(ctx context.Context, source Consumer, config NPMApplicationFixtureConfig, pin ConsumerPin) (string, error) {
	if _, err := source.git.run(ctx, source.stage, "check-ref-format", "refs/heads/"+config.Branch); err != nil || config.Branch == "main" || config.Branch == "" {
		return "", errNPMSourceBranch
	}

	if _, err := githubWorkflow(ctx, source, "Containerfile"); err != nil {
		return "", err
	}

	return strings.NewReplacer("@ACTIONS@", pin.Actions, "@WORKFLOWS@", pin.Workflows, "@PIN@", pin.Commit, "@IMAGE@", config.NodeImage, "@BRANCH@", "'"+strings.ReplaceAll(config.Branch, "'", "''")+"'").Replace(npmApplicationDevelopment), nil
}

// npmStableReleaseWorkflow reuses native preparation, artifact handoff and
// signing, and publishes the application package independently of container
// publication.
func npmStableReleaseWorkflow(workflow string, config NPMApplicationFixtureConfig, pin ConsumerPin) (string, error) {
	start, end := strings.Index(workflow, "  release-images:\n"), strings.Index(workflow, "  handoff:\n")

	buildStart, buildEnd := strings.Index(workflow, "      - uses: "+pin.Actions+"setup-toolchain@"), strings.Index(workflow, "      - uses: "+pin.Actions+"upload-dist@")
	if start < 0 || end <= start || buildStart < 0 || buildEnd <= buildStart {
		return "", errNPMRecipeStructure
	}

	workflow = workflow[:start] + workflow[end:]
	workflow = workflow[:buildStart] + npmApplicationBuildSteps + workflow[buildEnd:]
	workflow = strings.NewReplacer("@IMAGE@", config.NodeImage, "publish-container-images: 'true'", "publish-container-images: 'false'", "needs: [build, release-images]", "needs: [build]", "prepare, build, release-images, handoff", "prepare, build, handoff", "          IMAGES_RESULT: ${{ needs.release-images.result }}\n", "", `--job "Images=${IMAGES_RESULT}" `, "", "          forge-ci artifact download --name release-images --dir dist\n", "", "      release-images-path: dist/release-images.json\n", "").Replace(workflow)
	workflow = strings.Replace(workflow, "      # The default request already matches package.json; select version-bump", "      version-bump: npm\n      version-bump-image: "+config.NodeImage+"\n      # The default request already matches package.json; select version-bump", 1)
	workflow = strings.Replace(workflow, "    env:\n      ARTIFACT_NAME:", "    container:\n      image: "+config.NodeImage+"\n    env:\n      ARTIFACT_NAME:", 1)
	workflow += strings.NewReplacer("@ACTIONS@", pin.Actions, "@PIN@", pin.Commit, "@IMAGE@", config.NodeImage).Replace(npmApplicationStableConsumers)

	return workflow, nil
}

// validateNPMPackage requires the consumer's package.json to name the package
// and declare a bin.
func validateNPMPackage(ctx context.Context, source Consumer) error {
	body, err := githubWorkflow(ctx, source, "package.json")
	if err != nil {
		return err
	}

	var model struct {
		Name    string          `json:"name"`
		Version string          `json:"version"`
		Bin     json.RawMessage `json:"bin"`
	}
	if err = json.Unmarshal([]byte(body), &model); err != nil {
		return err
	}

	if !regexp.MustCompile(`^(@[a-z0-9._-]+/)?[a-z0-9][a-z0-9._-]*$`).MatchString(model.Name) || len(model.Bin) == 0 || string(model.Bin) == "null" {
		return errNPMApplicationModel
	}

	return nil
}

// readNPMOracle reads the installed-package oracle, an absolute regular file.
func readNPMOracle(oracle string) ([]byte, error) {
	if !filepath.IsAbs(oracle) {
		return nil, errNPMOracleNotAbsolute
	}

	info, err := os.Lstat(oracle)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errNPMOracleNotRegular
	}

	return os.ReadFile(oracle) //nolint:gosec // operator-supplied oracle, checked above as an absolute regular file.
}

// npmFixtureFiles lists the files the fixture stages in the consumer's index.
func npmFixtureFiles(config NPMApplicationFixtureConfig, development bool, workflow string, oracle []byte) (map[string]string, error) {
	name := ".forgejo/workflows/release.yml"
	if development {
		name = ".forgejo/workflows/development.yml"
	}

	files := map[string]string{name: workflow, ".forge-ci/npm-oracle.sh": string(oracle), ".forge-ci/npm-application.yml": "artifacts:\n  - name: application\n    project-type: npm\n    working-directory: .\ncontainers:\n  - name: application\n    container-file: Containerfile\n    context: .\n    platforms: linux/amd64\n    enable-scan: false\n    enable-slsa: false\n"}

	if !development {
		for _, path := range []string{".chglog/config-keepachangelog.yml", ".chglog/config-minimal.yml", ".chglog/CHANGELOG-keepachangelog.tpl", ".chglog/CHANGELOG-minimal.tpl"} {
			text, readErr := os.ReadFile(filepath.Join(config.RecipeDirectory, path)) //nolint:gosec // operator-supplied recipe directory joined with a fixed changelog file name.
			if readErr != nil {
				return nil, readErr
			}

			files[path] = string(text)
		}
	}
	// Snapshot planning must see a container-only declaration: npm's original
	// lifecycle runs in the checked-package job after the original source image.
	if development {
		files[".forge-ci/npm-application.yml"] = strings.SplitN(files[".forge-ci/npm-application.yml"], "containers:", 2)[1]
		files[".forge-ci/npm-application.yml"] = "containers:" + files[".forge-ci/npm-application.yml"]
	}

	return files, nil
}
