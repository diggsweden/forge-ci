// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

const campaignHead = "1111111111111111111111111111111111111111"

// newCampaign writes a campaign directory with demos hello-go-cli and
// hello-maven, campaign.env and a cases file, and returns the directory and
// the cases file's path.
func newCampaign(t *testing.T, env, cases string) (string, string) {
	t.Helper()

	dir := t.TempDir()
	for _, demo := range []string{"hello-go-cli", "hello-maven"} {
		if err := os.MkdirAll(filepath.Join(dir, "demos", demo), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	casesPath := filepath.Join(t.TempDir(), "lane.cases")
	for path, body := range map[string]string{filepath.Join(dir, CampaignEnvFile): env, casesPath: cases} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return dir, casesPath
}

func TestPlanCampaignLane_ExpandsAndMapsEveryCaseInOrder(t *testing.T) {
	t.Parallel()

	dir, cases := newCampaign(t, "# prepared\nCAMPAIGN_COMMIT="+campaignHead+"\n\nFORGE_CI_LIVE_SIGNER_IMAGE=lab/signer@sha256:aa\n"+
		"KANIKO_JOB_IMAGE=lab/kaniko@sha256:bb\nJOB_IMAGE_GO=lab/nanolinter-go@sha256:cc=d\n", `# a lane
consumer hello-go-cli release CI_JOB_IMAGE=@JOB_IMAGE_GO CI_IMAGE_BUILDER_JOB_IMAGE=@KANIKO_JOB_IMAGE version=@layer-commit

   scenario TestSecond
	# indented comment
scenario TestThird
`)

	plan, err := PlanCampaignLane(dir, "forgejo", cases, campaignHead)
	if err != nil {
		t.Fatal(err)
	}

	// Only the keys the live tier reads are exported; the provider is the lane's.
	if want := []string{"FORGE_CI_LIVE_PROVIDER=forgejo", "FORGE_CI_LIVE_SIGNER_IMAGE=lab/signer@sha256:aa"}; !slices.Equal(plan.Env, want) {
		t.Errorf("env %q, want %q", plan.Env, want)
	}

	want := []CampaignCase{
		{
			Text: "consumer hello-go-cli release CI_JOB_IMAGE=@JOB_IMAGE_GO CI_IMAGE_BUILDER_JOB_IMAGE=@KANIKO_JOB_IMAGE version=@layer-commit",
			Args: []string{
				"test-live-forgejo-consumer", filepath.Join(dir, "demos", "hello-go-cli"), "release",
				"CI_JOB_IMAGE=lab/nanolinter-go@sha256:cc=d", "CI_IMAGE_BUILDER_JOB_IMAGE=lab/kaniko@sha256:bb", "version=@layer-commit",
			},
		},
		{Text: "scenario TestSecond", Args: []string{"test-live", "^TestSecond$"}},
		{Text: "scenario TestThird", Args: []string{"test-live", "^TestThird$"}},
	}
	if !slices.EqualFunc(plan.Cases, want, func(a, b CampaignCase) bool { return a.Text == b.Text && slices.Equal(a.Args, b.Args) }) {
		t.Fatalf("cases\n%q\nwant\n%q", plan.Cases, want)
	}
}

func TestPlanCampaignLane_MapsAGitLabConsumerToItsRecipes(t *testing.T) {
	t.Parallel()

	dir, cases := newCampaign(t, "CAMPAIGN_COMMIT="+campaignHead+"\nFORGE_CI_LIVE_SIGNER_IMAGE_ARCHIVE=/c/signer.tar\nJAVA_JOB_IMAGE_ARCHIVE=/c/java.tar\n",
		"consumer hello-maven pull-request job-archive=@JAVA_JOB_IMAGE_ARCHIVE\n"+
			"consumer hello-maven release job-archive=@JAVA_JOB_IMAGE_ARCHIVE build-archive=/b.tar signer-archive=@FORGE_CI_LIVE_SIGNER_IMAGE_ARCHIVE version-bump=maven\n")

	plan, err := PlanCampaignLane(dir, "gitlab", cases, campaignHead)
	if err != nil {
		t.Fatal(err)
	}

	checkout := filepath.Join(dir, "demos", "hello-maven")
	if len(plan.Cases) != 2 ||
		!slices.Equal(plan.Cases[0].Args, []string{"test-live-gitlab-consumer", checkout, "/c/java.tar"}) ||
		!slices.Equal(plan.Cases[1].Args, []string{"test-live-gitlab-consumer-release", checkout, "/c/java.tar", "/b.tar", "/c/signer.tar", "version-bump=maven"}) {
		t.Fatalf("cases %q", plan.Cases)
	}
}

// A lane is refused whole: a campaign of another commit, a campaign.env or a
// cases file it cannot read as data, or a case it does not know.
func TestPlanCampaignLane_RefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

	good := "CAMPAIGN_COMMIT=" + campaignHead + "\nJOB_IMAGE_GO=lab/go\n"

	for name, tc := range map[string]struct{ provider, env, cases, want string }{
		"another commit":           {"forgejo", "CAMPAIGN_COMMIT=0000\n", "scenario TestA\n", "prepared for 0000 and this checkout is " + campaignHead},
		"no commit":                {"forgejo", "JOB_IMAGE_GO=lab/go\n", "scenario TestA\n", "names no CAMPAIGN_COMMIT"},
		"a lowercase env key":      {"forgejo", good + "job_image=lab/go\n", "scenario TestA\n", "want KEY=value"},
		"an env line without =":    {"forgejo", good + "JOB_IMAGE_JAVA\n", "scenario TestA\n", "want KEY=value"},
		"a shell line in env":      {"forgejo", good + "export X=1\n", "scenario TestA\n", "want KEY=value"},
		"an empty env value":       {"forgejo", good + "JOB_IMAGE_JAVA=\n", "scenario TestA\n", "JOB_IMAGE_JAVA is empty"},
		"an env key set twice":     {"forgejo", good + "JOB_IMAGE_GO=lab/other\n", "scenario TestA\n", "JOB_IMAGE_GO is set twice"},
		"an unknown provider":      {"github", good, "scenario TestA\n", "forgejo or gitlab"},
		"an unknown kind":          {"forgejo", good, "run rm -rf /\n", "consumer or scenario"},
		"a free regex":             {"forgejo", good, "scenario TestA|TestB\n", "want scenario"},
		"a regex anchor":           {"forgejo", good, "scenario ^TestA$\n", "want scenario"},
		"a non-test name":          {"forgejo", good, "scenario BenchmarkA\n", "want scenario"},
		"two scenario names":       {"forgejo", good, "scenario TestA TestB\n", "want scenario"},
		"an unknown demo":          {"forgejo", good, "consumer hello-nothing release\n", "no such demo"},
		"a demo path":              {"forgejo", good, "consumer ../demos/hello-go-cli release\n", "no such demo"},
		"no event":                 {"forgejo", good, "consumer hello-go-cli\n", "want consumer <demo>"},
		"an unknown event":         {"forgejo", good, "consumer hello-go-cli dev-release\n", "pull-request or release"},
		"a setting without =":      {"forgejo", good, "consumer hello-go-cli release CI_JOB_IMAGE\n", "want key=value"},
		"a bad setting key":        {"forgejo", good, "consumer hello-go-cli release $(id)=x\n", "want key=value"},
		"a missing campaign value": {"forgejo", good, "consumer hello-go-cli release CI_JOB_IMAGE=@JOB_IMAGE_RUST\n", "campaign.env has no JOB_IMAGE_RUST"},
		"a bad campaign name":      {"forgejo", good, "consumer hello-go-cli release CI_JOB_IMAGE=@JOB-IMAGE\n", "campaign.env has no JOB-IMAGE"},
		"a GitLab MR without job":  {"gitlab", good, "consumer hello-go-cli pull-request\n", "job-archive= alone"},
		"a GitLab MR setting":      {"gitlab", good, "consumer hello-go-cli pull-request job-archive=/a.tar version=1.0.0\n", "job-archive= alone"},
		"a GitLab release short":   {"gitlab", good, "consumer hello-go-cli release job-archive=/a.tar\n", "build-archive="},
		"a GitLab archive twice":   {"gitlab", good, "consumer hello-go-cli pull-request job-archive=/a.tar job-archive=/b.tar\n", "job-archive= is named twice"},
		"a NUL byte":               {"forgejo", good, "scenario TestA\x00\n", "a NUL byte"},
		"no case":                  {"forgejo", good, "# nothing\n\n", "names no case"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// A good first case shows the lane is refused whole.
			dir, cases := newCampaign(t, tc.env, "scenario TestFirst\n"+tc.cases)
			if name == "no case" {
				dir, cases = newCampaign(t, tc.env, tc.cases)
			}

			_, err := PlanCampaignLane(dir, tc.provider, cases, campaignHead)
			if !errors.Is(err, errs.ErrValidation) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want a validation error naming %q", err, tc.want)
			}
		})
	}

	if _, err := PlanCampaignLane("relative/campaign", "forgejo", "lane.cases", campaignHead); !errors.Is(err, errs.ErrValidation) {
		t.Fatalf("a relative campaign directory: %v", err)
	}
}

func TestWriteCampaignPlan_IsNULTerminatedFields(t *testing.T) {
	t.Parallel()

	var out strings.Builder

	err := WriteCampaignPlan(&out, CampaignPlan{
		Env:   []string{"FORGE_CI_LIVE_PROVIDER=gitlab"},
		Cases: []CampaignCase{{Text: "scenario TestA", Args: []string{"test-live", "^TestA$"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if want := "env\x00FORGE_CI_LIVE_PROVIDER=gitlab\x00case\x00scenario TestA\x002\x00test-live\x00^TestA$\x00"; out.String() != want {
		t.Fatalf("plan %q, want %q", out.String(), want)
	}
}
