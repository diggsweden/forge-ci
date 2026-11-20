// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPlanConsumerBindsNestedBaseRegistryCredential(t *testing.T) {
	t.Parallel()
	workflows := map[string]string{
		"release.yml": `on: push
jobs:
  bases:
    uses: ./.forgejo/workflows/bases.yml
    secrets:
      BASE_IMAGE_REGISTRY_TOKEN: ${{ secrets.BASE_IMAGE_REGISTRY_TOKEN }}
`,
		"bases.yml": `on:
  workflow_call:
jobs:
  verify:
    uses: examplescope/forge-ci/.forgejo/workflows/verify-base-container-images.yml@` + consumerPinned + `
    secrets:
      RELEASE_REGISTRY_TOKEN: ${{ secrets.BASE_IMAGE_REGISTRY_TOKEN }}
`,
	}
	plan, err := sampleLayer().PlanConsumer(workflows, ConsumerFacts{Secrets: map[string]string{"RELEASE_REGISTRY_TOKEN": "lab-token"}})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"BASE_IMAGE_REGISTRY_TOKEN": "lab-token"}, plan.Secrets)
	require.Contains(t, plan.Render(workflows["bases.yml"]), "secrets.BASE_IMAGE_REGISTRY_TOKEN", "rendering must preserve consumer forwarding")
}

func TestConsumerSecretsFollowLocalScopes(t *testing.T) {
	t.Parallel()
	const entry = `on: push
jobs:
  bases:
    uses: ./.forgejo/workflows/bases.yml
    secrets:
      BASE_IMAGE_REGISTRY_TOKEN: ${{ secrets.BASE_PUBLISH_TOKEN }}
      PARENT_REGISTRY_READ_TOKEN: ${{ secrets.PRIVATE_PULL_TOKEN }}
`
	const bases = `on:
  workflow_call:
jobs:
  flavor:
    uses: ./.forgejo/workflows/stage.yml
    secrets:
      RELEASE_REGISTRY_TOKEN: ${{ secrets.BASE_IMAGE_REGISTRY_TOKEN }}
      PARENT_REGISTRY_READ_TOKEN: ${{ secrets.PARENT_REGISTRY_READ_TOKEN }}
`
	const stage = `on:
  workflow_call:
jobs:
  build:
    uses: owner/forge-ci/.forgejo/workflows/base-images.yml@pin
    secrets:
      RELEASE_REGISTRY_TOKEN: ${{ secrets.RELEASE_REGISTRY_TOKEN }}
      PARENT_REGISTRY_READ_TOKEN: ${{ secrets.PARENT_REGISTRY_READ_TOKEN }}
`
	for _, tc := range []struct {
		name, caller, middle string
		want                 map[string]string
		err                  string
	}{
		{"renamed at both boundaries", entry, bases, map[string]string{"BASE_PUBLISH_TOKEN": "lab-publication-token"}, ""},
		{"inherited into stage", entry, strings.Replace(bases, "    secrets:\n      RELEASE_REGISTRY_TOKEN: ${{ secrets.BASE_IMAGE_REGISTRY_TOKEN }}\n      PARENT_REGISTRY_READ_TOKEN: ${{ secrets.PARENT_REGISTRY_READ_TOKEN }}", "    secrets: inherit", 1), nil, "does not forward required secret RELEASE_REGISTRY_TOKEN"},
		{"missing forwarding", strings.Replace(entry, "      BASE_IMAGE_REGISTRY_TOKEN: ${{ secrets.BASE_PUBLISH_TOKEN }}\n", "", 1), bases, nil, "does not forward required secret"},
		{"unsupported expression", strings.Replace(entry, "secrets.BASE_PUBLISH_TOKEN", "secrets.BASE_PUBLISH_TOKEN || secrets.OTHER", 1), bases, nil, "must forward a single named secret"},
		{"inherit repository secrets", "on: push\njobs:\n  bases:\n    uses: ./.forgejo/workflows/bases.yml\n    secrets: inherit\n", bases, map[string]string{"BASE_IMAGE_REGISTRY_TOKEN": "lab-publication-token"}, ""},
		{"cycle", entry, "jobs:\n  loop:\n    uses: ./.forgejo/workflows/bases.yml\n", nil, "cycle"},
		{"missing workflow", entry, "jobs:\n  missing:\n    uses: ./.forgejo/workflows/missing.yml\n", nil, "is missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := ConsumerPlan{Pin: ConsumerPin{Workflows: "owner/forge-ci/.forgejo/workflows/"}, Secrets: map[string]string{}}
			workflows := map[string]string{"release.yml": tc.caller, "bases.yml": tc.middle, "stage.yml": stage}
			err := plan.bindWorkflowSecrets(workflows, map[string]string{"release.yml": tc.caller}, map[string]string{"RELEASE_REGISTRY_TOKEN": "lab-publication-token"})
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				require.NotContains(t, err.Error(), "lab-publication-token")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, plan.Secrets)
			require.NotContains(t, plan.Secrets, "PRIVATE_PULL_TOKEN", "publication authority must never substitute for a pull-only credential")
		})
	}
}

func TestPlanConsumerOnlyBindsCredentialsAtLayerBoundaries(t *testing.T) {
	t.Parallel()
	const workflow = `on: push
jobs:
  release:
    uses: examplescope/forge-ci/.forgejo/workflows/base-images.yml@` + consumerPinned + `
    secrets:
      RELEASE_REGISTRY_TOKEN: ${{ secrets.PUBLISH_TOKEN }}
  unrelated:
    uses: elsewhere/project/.github/workflows/other.yml@` + consumerPinned + `
    secrets:
      RELEASE_REGISTRY_TOKEN: ${{ secrets.UNRELATED_TOKEN }}
  actions:
    runs-on: ubuntu-latest
    steps:
      - uses: https://forge.example/examplescope/forge-ci/.forgejo/actions/record-release-image@` + consumerPinned + `
        env:
          RELEASE_REGISTRY_TOKEN: ${{ secrets.ACTION_TOKEN }}
      - uses: https://elsewhere.example/other/action@v1
        env:
          RELEASE_REGISTRY_TOKEN: ${{ secrets.UNRELATED_ACTION_TOKEN }}
      - run: echo no-credential-inference
        env:
          RELEASE_REGISTRY_TOKEN: ${{ secrets.SHELL_TOKEN }}
`
	plan, err := sampleLayer().PlanConsumer(map[string]string{"release.yml": workflow}, ConsumerFacts{Secrets: map[string]string{"RELEASE_REGISTRY_TOKEN": "lab-token"}})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"PUBLISH_TOKEN": "lab-token", "ACTION_TOKEN": "lab-token"}, plan.Secrets)
}
