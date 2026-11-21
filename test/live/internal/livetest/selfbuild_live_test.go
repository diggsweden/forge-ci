// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

//go:build live

package livetest

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	githubapi "github.com/google/go-github/v91/github"
	gitlabapi "gitlab.com/gitlab-org/api/client-go"

	"github.com/diggsweden/forge-ci/internal/domain/container"
	"github.com/diggsweden/forge-ci/internal/domain/provider"
)

// This is the repository's own native Go gate, not a consumer release or an
// image build. Source and workflow files are pushed unchanged to a new scratch
// repository; existing job-image bytes are reused and no release keys are set.
func TestForgeCI_SelfBuild(t *testing.T) {
	RegisterAdmittedScenario(t)

	root := os.Getenv("FORGE_CI_LIVE_CONSUMER")
	if root == "" {
		t.Skip("name the clean candidate checkout in FORGE_CI_LIVE_CONSUMER")
	}

	git := layerGit{}
	if err := git.requireCleanTop(t.Context(), root); err != nil {
		t.Fatal(err)
	}

	commit, err := git.run(t.Context(), root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("self-build candidate %s; source and workflows are not rewritten", commit)

	if os.Getenv("FORGE_CI_LIVE_PROVIDER") == "github" {
		selfBuildGitHub(t, root, commit)

		return
	}

	forge := provider.ForgeAPI(os.Getenv("FORGE_CI_LIVE_PROVIDER"))
	if forge != provider.ForgeForgejo && forge != provider.ForgeGitLab {
		t.Fatal("select exactly one self-build provider: forgejo, gitlab or github")
	}

	image := os.Getenv("FORGE_CI_LIVE_SELF_BUILD_IMAGE")
	if !container.ValidDigestReference(image) {
		t.Fatal("FORGE_CI_LIVE_SELF_BUILD_IMAGE must name an approved native Go runtime by digest")
	}

	if !Requires(t, forge, NeedsInRunner) {
		t.Skip("no admitted native runner")
	}

	target := Accept(t, forge)
	if RunnerArch(t, target) != "amd64" {
		t.Fatal("this bounded self-build trial requires a native amd64 runner")
	}

	consumer := OpenConsumer(t, target, root)
	Admitted(t)

	repo := NewBareScratchRepo(t, target, "self-build-"+strconv.FormatInt(time.Now().UnixNano(), 36), consumer.ObjectFormat, true)
	if forge == provider.ForgeGitLab {
		selfBuildGitLab(t, target, repo, consumer, image)

		return
	}

	labels, err := json.Marshal([]string{RunnerLabel(t, target)})
	if err != nil {
		t.Fatal(err)
	}

	head := consumer.PushSnapshot(t, repo)
	EnableActions(t, target, repo)
	SetRepoVariable(t, target, repo, "CI_RUNNER_LABEL", string(labels))
	SetRepoVariable(t, target, repo, "FORGE_CI_SELF_BUILD_IMAGE", image)
	DispatchConsumerWorkflow(t, target, repo, "self-pullrequest.yml", "main")
	run := waitForRunOf(t, target, repo, "self-pullrequest.yml", "workflow_dispatch", 35*time.Minute, "", 0)
	jobs := RunJobs(t, target, repo, run.ID)
	// A dispatch runs the Go gate alone; the optional black-box job is skipped.
	executed := 0

	for _, job := range jobs {
		if job.Status == "skipped" {
			continue
		}

		executed++

		t.Log(RetainJobLog(t, target, repo, job))

		if job.Name != "go" || job.Status != "success" || !job.Started() {
			t.Errorf("unexpected self-build job: %+v", job)
		}
	}

	if Branches(t, target, repo)["main"] != head || run.Status != "success" || executed != 1 {
		t.Fatalf("self-build %s at %s: run %+v, jobs %+v", repo, head, run, jobs)
	}
}

func selfBuildGitLab(t *testing.T, target Target, repo string, consumer Consumer, image string) {
	t.Helper()

	client, err := gitlabapi.NewClient(target.Token, gitlabapi.WithBaseURL(target.BaseURL()+"/api/v4/"), gitlabapi.WithHTTPClient(HTTPClient(t, target, time.Minute)))
	if err != nil {
		t.Fatal(err)
	}

	project := RepoSlug(target, repo)
	RetainGitLabJobLogsAfter(t, target, repo)
	head := consumer.PushSnapshot(t, repo)

	ctx, cancel := context.WithTimeout(Context(t), 35*time.Minute)
	defer cancel()

	pipeline, _, err := client.Pipelines.CreatePipeline(project, &gitlabapi.CreatePipelineOptions{
		Ref: new("main"), Variables: &[]*gitlabapi.PipelineVariableOptions{{
			Key: new("FORGE_CI_SELF_BUILD_IMAGE"), Value: new(image),
		}},
	}, gitlabapi.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}

	id := pipeline.ID
	finished := false

	t.Cleanup(func() {
		if finished {
			return
		}

		cleanup, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()

		_, _, err := client.Pipelines.CancelPipelineBuild(project, id, gitlabapi.WithContext(cleanup))
		if err != nil {
			t.Errorf("cancel unfinished self-build pipeline %d: %v", id, err)
		}
	})

	for {
		pipeline, err = PollGitLabPipeline(ctx, client, project, id)
		if err != nil {
			t.Fatal(err)
		}

		if pipeline.Status == "success" || pipeline.Status == "failed" || pipeline.Status == "canceled" || pipeline.Status == "skipped" {
			finished = true

			break
		}

		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Second):
		}
	}

	for _, line := range GitLabPipelineAccount(ctx, client, project, id) {
		t.Log(line)
	}

	jobs, _, err := client.Jobs.ListPipelineJobs(project, id, &gitlabapi.ListJobsOptions{PerPage: 100}, gitlabapi.WithContext(ctx))
	if err != nil || pipeline.SHA != head || pipeline.Status != "success" || len(jobs) != 1 || jobs[0].Name != "self-build" || jobs[0].Status != "success" {
		t.Fatalf("self-build %s at %s: pipeline %+v, jobs %+v, error %v", project, head, pipeline, jobs, err)
	}
}

func selfBuildGitHub(t *testing.T, root, commit string) {
	t.Helper()

	org, err := strconv.ParseInt(os.Getenv("FORGE_CI_LIVE_GITHUB_ORG_ID"), 10, 64)
	if err != nil || org <= 0 {
		t.Fatal("name the authorised numeric organization in FORGE_CI_LIVE_GITHUB_ORG_ID")
	}

	trial := OpenGitHubTrial(t, org)
	Admitted(t)

	repo := trial.NewConsumerRepository("forge-ci-self-build", false)
	trial.SetVariable(repo, "FORGE_CI_PRERELEASE_BRANCH", "disabled-self-build-publication")
	trial.SetVariable(repo, "FORGE_CI_COSIGN_TRANSPARENCY", "none")
	// A non-prerelease branch skips the runtime publisher's build jobs. The
	// dispatch reads the exact committed workflow, without building any image.
	const branch = "self-build"
	trial.gitRemote(root, "push", trial.remote(repo), "HEAD:refs/heads/"+branch)

	_, _, err = trial.api.Repositories.Edit(t.Context(), trial.org, repo, &githubapi.Repository{DefaultBranch: new(branch)})
	if err != nil {
		t.Fatal(err)
	}

	const file = "self-pullrequest.yml"
	trial.waitWorkflows(repo, filepath.Join(root, ".github", "workflows"), file)

	_, _, err = trial.api.Actions.CreateWorkflowDispatchEventByFileName(t.Context(), trial.org, repo, file, githubapi.CreateWorkflowDispatchEventRequest{Ref: branch})
	if err != nil {
		t.Fatal(err)
	}

	run := trial.WaitRun(repo, file, "workflow_dispatch", commit, 35*time.Minute)
	trial.ReportRun(repo, run)

	jobs, err := trial.listRunJobs(t.Context(), repo, run)
	if err != nil {
		t.Fatal(err)
	}

	executed := 0

	for _, job := range jobs.Jobs {
		if job.GetConclusion() != "skipped" {
			executed++

			if job.GetName() != "Go (build, test, lint)" || job.GetConclusion() != "success" {
				t.Errorf("unexpected executed job: %s, %s", job.GetName(), job.GetConclusion())
			}
		}
	}

	if run.GetConclusion() != "success" || executed != 1 {
		t.Fatalf("self-build run %d: %s; executed %d jobs", run.GetID(), run.GetConclusion(), executed)
	}
}
