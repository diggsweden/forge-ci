// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package livetest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
)

// A live campaign is the inputs of one commit, which `just campaign-prepare`
// writes to a directory, and lanes of cases that `just campaign-lane` runs
// against them. This file reads both as data and plans a lane; the script
// only runs the plan. docs/live-campaigns.md, Run a campaign, shows the use.

// CampaignEnvFile is the campaign's KEY=value file, in its directory.
const CampaignEnvFile = "campaign.env"

// campaignCommitKey names the commit a campaign was prepared from.
const campaignCommitKey = "CAMPAIGN_COMMIT"

// The providers a campaign has lanes on.
const (
	campaignForgejo = "forgejo"
	campaignGitLab  = "gitlab"
)

func isCampaignProvider(provider string) bool {
	return provider == campaignForgejo || provider == campaignGitLab
}

// campaignExported are the campaign.env keys a case's run reads from its
// environment, under the names the live tier reads, with every runtime image
// archive, GitLabJobImageArchiveEnv<TARGET>, which a gate's further job image
// takes by name; every other key is only an @NAME value.
var campaignExported = []string{
	"FORGE_CI_LIVE_SIGNER_IMAGE", "FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE", "FORGE_CI_LIVE_SIGNER_IMAGE_ARCHIVE",
	"FORGE_CI_LIVE_BUILDAH_JOB_IMAGE_ARCHIVE", "FORGE_CI_LIVE_KANIKO_JOB_IMAGE_ARCHIVE",
}

// campaignCaseNamed are the variables a case gets only from its own words: a
// recipe falls back to the caller's value when the case names none, so the
// lane clears the caller's first.
var campaignCaseNamed = []string{"FORGE_CI_LIVE_LINT_IMAGE_ARCHIVE"}

// campaignScenarioTarget is the runtime target of FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE,
// the job image of the GitLab scenarios.
const campaignScenarioTarget = "go"

// campaignRuntimeArchive is a cases file's reference to a runtime image
// archive of campaign.env: @FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE, the scenarios'
// go image, or @GitLabJobImageArchiveEnv<TARGET>.
var campaignRuntimeArchive = regexp.MustCompile(`@` + strings.TrimSuffix(GitLabJobImageArchiveEnv, "_") + `(?:_([A-Z][A-Z0-9]*))?\b`)

// The campaign.env keys of a nanolinter job image prepared with
// job-image=<flavor>:<image>, each followed by campaignFlavorKey(flavor): its
// reference, a Forgejo case's CI_JOB_IMAGE, and its archive, a GitLab case's
// lint-archive=.
const (
	campaignJobImagePrefix    = "JOB_IMAGE_"
	campaignLintArchivePrefix = "LINT_IMAGE_ARCHIVE_"
)

// campaignFlavorRef is a cases file's reference to a nanolinter job image of
// campaign.env, and its flavor's key.
var campaignFlavorRef = regexp.MustCompile(`@(?:` + campaignJobImagePrefix + `|` + campaignLintArchivePrefix + `)([A-Z][A-Z0-9_]*)\b`)

// campaignFlavor is a job-image= flavor, as campaign prepare accepts it.
var campaignFlavor = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// campaignFlavorKey is a flavor in campaign.env's keys: capitals, '-' as '_'.
func campaignFlavorKey(flavor string) string {
	return strings.ToUpper(strings.ReplaceAll(flavor, "-", "_"))
}

// campaignLintArchive is a GitLab consumer's setting of the nanolinter image
// archive its gate lints in, the trial's FORGE_CI_LIVE_LINT_IMAGE_ARCHIVE.
const campaignLintArchive = "lint-archive"

var (
	campaignKey     = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	campaignSetting = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	campaignDemo    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	campaignTest    = regexp.MustCompile(`^Test[A-Za-z0-9_]+$`)
)

// CampaignCase is one validated case: its text, and the just recipe with the
// arguments that run it.
type CampaignCase struct {
	Text string
	Args []string
}

// CampaignPlan is a lane ready to run: the variables cleared from the
// caller's environment, the environment every case gets, as KEY=value, and
// the cases in order. A case gets the campaign's images and what it names,
// never a caller's image left over from another run.
type CampaignPlan struct {
	Unset []string
	Env   []string
	Cases []CampaignCase
}

// ReadCampaignEnv reads KEY=value lines, skipping blank lines and # comments.
// Keys are capitals, digits and underscores, each set once, to a value that
// is not empty. Nothing is evaluated.
func ReadCampaignEnv(path string) (map[string]string, error) {
	env := map[string]string{}

	err := eachLine(path, func(number int, line string) error {
		if line == "" || strings.HasPrefix(line, "#") {
			return nil
		}

		key, value, found := strings.Cut(line, "=")
		switch {
		case !found || !campaignKey.MatchString(key):
			return fmt.Errorf("%s:%d: want KEY=value, KEY in capitals, digits and underscores: %w", path, number, errs.ErrValidation)
		case value == "" || strings.Contains(value, "\r"):
			return fmt.Errorf("%s:%d: %s is empty or not one line: %w", path, number, key, errs.ErrValidation)
		}

		if _, twice := env[key]; twice {
			return fmt.Errorf("%s:%d: %s is set twice: %w", path, number, key, errs.ErrValidation)
		}

		env[key] = value

		return nil
	})
	if err != nil {
		return nil, err
	}

	if env[campaignCommitKey] == "" {
		return nil, fmt.Errorf("%s names no %s: %w", path, campaignCommitKey, errs.ErrValidation)
	}

	return env, nil
}

// PlanCampaignLane plans the lane of provider over the cases file: every case
// is parsed before any runs, so a typo costs no run. A campaign prepared for
// another commit than head is refused. Declared settings must match the caller's
// effective run facts, including per-case overrides; they never choose settings.
func PlanCampaignLane(dir, provider, casesPath, head string, run CampaignRun) (CampaignPlan, error) {
	if !filepath.IsAbs(dir) {
		return CampaignPlan{}, fmt.Errorf("the campaign directory must be absolute, got %q: %w", dir, errs.ErrValidation)
	}

	if !isCampaignProvider(provider) {
		return CampaignPlan{}, fmt.Errorf("the provider is forgejo or gitlab, got %q: %w", provider, errs.ErrValidation)
	}

	env, err := ReadCampaignEnv(filepath.Join(dir, CampaignEnvFile))
	if err != nil {
		return CampaignPlan{}, err
	}

	if env[campaignCommitKey] != head {
		return CampaignPlan{}, fmt.Errorf("the campaign was prepared for %s and this checkout is %s; check that commit out or prepare again: %w",
			env[campaignCommitKey], head, errs.ErrValidation)
	}

	file, err := readCampaignCases(casesPath)
	if err != nil {
		return CampaignPlan{}, err
	}

	if err := file.checkSettings(run.Settings); err != nil {
		return CampaignPlan{}, fmt.Errorf("%s: %w", casesPath, err)
	}

	plan := CampaignPlan{Unset: slices.Clone(campaignCaseNamed), Env: []string{"FORGE_CI_LIVE_PROVIDER=" + provider}}

	for _, key := range campaignExported {
		if value, found := env[key]; found {
			plan.Env = append(plan.Env, key+"="+value)
		} else {
			plan.Unset = append(plan.Unset, key)
		}
	}

	for _, key := range slices.Sorted(maps.Keys(env)) {
		if strings.HasPrefix(key, GitLabJobImageArchiveEnv) {
			plan.Env = append(plan.Env, key+"="+env[key])
		}
	}

	for _, line := range file.cases {
		args, caseErr := campaignCaseArgs(dir, provider, env, line.words)
		if caseErr != nil {
			return CampaignPlan{}, fmt.Errorf("%s:%d: %s: %w", casesPath, line.number, line.text, caseErr)
		}

		facts, caseErr := run.caseSettings(args)
		if caseErr == nil {
			caseErr = file.checkSettings(facts)
		}

		if caseErr != nil {
			return CampaignPlan{}, fmt.Errorf("%s:%d: case settings: %w", casesPath, line.number, caseErr)
		}

		plan.Cases = append(plan.Cases, CampaignCase{Text: line.text, Args: args})
	}

	return plan, nil
}

// campaignCaseArgs maps one case's words to the recipe that runs it.
func campaignCaseArgs(dir, provider string, env map[string]string, words []string) ([]string, error) {
	switch words[0] {
	case settingScenario:
		return campaignScenarioRecipe(provider, env, words)
	case "consumer":
	default:
		return nil, fmt.Errorf("a case is consumer or scenario: %w", errs.ErrValidation)
	}

	if len(words) < 3 {
		return nil, fmt.Errorf("want consumer <demo> <pull-request|release> [key=value ...]: %w", errs.ErrValidation)
	}

	if !campaignDemo.MatchString(words[1]) || !isDir(filepath.Join(dir, "demos", words[1])) {
		return nil, fmt.Errorf("no such demo in %s: %w", filepath.Join(dir, "demos"), errs.ErrValidation)
	}

	if words[2] != "pull-request" && words[2] != "release" {
		return nil, fmt.Errorf("a consumer event is pull-request or release: %w", errs.ErrValidation)
	}

	archives, settings, err := campaignSettings(provider, env, words[3:])
	if err != nil {
		return nil, err
	}

	if provider == campaignGitLab {
		if err := campaignAdmitGitLabConsumer(filepath.Join(dir, "demos", words[1]), words[2], words[3:], settings, env); err != nil {
			return nil, err
		}
	}

	return consumerRecipe(provider, words[2], filepath.Join(dir, "demos", words[1]), archives, settings)
}

func campaignScenarioRecipe(provider string, env map[string]string, words []string) ([]string, error) {
	if special, handled, err := campaignGitLabScenarioArgs(provider, env, words); handled {
		return special, err
	}

	if len(words) != 2 || !campaignTest.MatchString(words[1]) {
		return nil, fmt.Errorf("want scenario <Go test name>: %w", errs.ErrValidation)
	}

	return []string{"test-live", "^" + words[1] + "$"}, nil
}

func campaignAdmitGitLabConsumer(checkout, event string, words, settings []string, env map[string]string) error {
	if event == TrialRelease {
		if _, err := ParseGitLabReleaseSettings(strings.Join(settings, "\n")); err != nil {
			return err
		}
	}

	gate, err := readCampaignGate(filepath.Join(checkout, GitLabGateFile), words)
	if err != nil {
		return err
	}

	return campaignHasGateImages(env, gate)
}

// readCampaignGate reads the GitLab gate at path of a consumer with the
// settings words, and refuses the consumer when its gate lints and it names
// no lint-archive=, or it names one for a gate that does not lint.
func readCampaignGate(path string, words []string) (demoGate, error) {
	gate, err := readDemoGate(path)
	if err != nil {
		return demoGate{}, err
	}

	named := slices.ContainsFunc(words, func(word string) bool { return strings.HasPrefix(word, campaignLintArchive+"=") })

	switch {
	case gate.lints && !named:
		return demoGate{}, fmt.Errorf("the demo's gate lints in $CI_LINT_IMAGE; name its nanolinter flavor's archive, %s=@%s<FLAVOR>: %w",
			campaignLintArchive, campaignLintArchivePrefix, errs.ErrValidation)
	case !gate.lints && named:
		return demoGate{}, fmt.Errorf("the demo's gate does not lint, so it takes no %s=: %w", campaignLintArchive, errs.ErrValidation)
	}

	return gate, nil
}

// campaignHasGateImages refuses a demo whose GitLab gate checks in a further
// job image campaign.env has no archive of.
func campaignHasGateImages(env map[string]string, gate demoGate) error {
	for _, name := range gate.extraJobs {
		if env[GitLabJobImageArchiveEnv+name] == "" {
			return fmt.Errorf("the demo's gate checks in $CI_JOB_IMAGE_%s and campaign.env has no %s%s; prepare the campaign with this cases file: %w",
				name, GitLabJobImageArchiveEnv, name, errs.ErrValidation)
		}
	}

	return nil
}

// campaignSettings validates and expands a consumer's settings. On GitLab the
// image archives are the recipe's arguments, not settings, and come apart.
func campaignSettings(provider string, env map[string]string, words []string) (map[string]string, []string, error) {
	archives := map[string]string{}

	var settings []string

	for _, word := range words {
		key, value, err := campaignSettingValue(env, word)
		if err != nil {
			return nil, nil, err
		}

		if provider != campaignGitLab || !slices.Contains([]string{"job-archive", "build-archive", "signer-archive", campaignLintArchive}, key) {
			settings = append(settings, key+"="+value)

			continue
		}

		if _, twice := archives[key]; twice {
			return nil, nil, fmt.Errorf("%s= is named twice: %w", key, errs.ErrValidation)
		}

		if value == "" {
			return nil, nil, fmt.Errorf("%s= names no archive: %w", key, errs.ErrValidation)
		}

		archives[key] = value
	}

	return archives, settings, nil
}

// consumerRecipe is the consumer recipe of provider and event, with its
// arguments. A GitLab merge request takes its lint archive as the recipe's
// optional last argument, a release as the setting lint-archive=, which the
// recipe takes out of the trial's settings.
func consumerRecipe(provider, event, checkout string, archives map[string]string, settings []string) ([]string, error) {
	lint, lints := archives[campaignLintArchive]
	images := len(archives)

	if lints {
		images--
	}

	switch {
	case provider == campaignForgejo:
		return append([]string{"test-live-forgejo-consumer", checkout, event}, settings...), nil
	case event == "pull-request":
		if images != 1 || archives["job-archive"] == "" || len(settings) != 0 {
			return nil, fmt.Errorf("a GitLab merge request takes job-archive= and lint-archive= alone: %w", errs.ErrValidation)
		}

		args := []string{"test-live-gitlab-consumer", checkout, archives["job-archive"]}
		if lints {
			args = append(args, lint)
		}

		return args, nil
	default:
		if images != 3 {
			return nil, fmt.Errorf("a GitLab release takes job-archive=, build-archive= and signer-archive=: %w", errs.ErrValidation)
		}

		args := []string{"test-live-gitlab-consumer-release", checkout, archives["job-archive"], archives["build-archive"], archives["signer-archive"]}
		if lints {
			args = append(args, campaignLintArchive+"="+lint)
		}

		return append(args, settings...), nil
	}
}

// campaignSettingValue validates one key=value setting and expands a value
// @NAME, NAME in capitals, to campaign.env's NAME. Other @ values, such as
// @layer-commit, are the trial's own and pass through.
func campaignSettingValue(env map[string]string, word string) (string, string, error) {
	key, value, found := strings.Cut(word, "=")
	if !found || !campaignSetting.MatchString(key) {
		return "", "", fmt.Errorf("want key=value, got %q: %w", word, errs.ErrValidation)
	}

	name, isRef := strings.CutPrefix(value, "@")
	if !isRef || name == "" || name[0] < 'A' || name[0] > 'Z' {
		return key, value, nil
	}

	expanded, found := env[name]
	if !found || !campaignKey.MatchString(name) {
		return "", "", fmt.Errorf("campaign.env has no %s: %w", name, errs.ErrValidation)
	}

	return key, expanded, nil
}

// CampaignRuntimeTargets lists the runtime targets a campaign prepares image
// archives of, from the repository at root, for the lanes it will run, each
// <forgejo|gitlab>=<cases file>: every archive a case names as
// @FORGE_CI_LIVE_JOB_IMAGE_ARCHIVE (go) or @GitLabJobImageArchiveEnv<TARGET>,
// and on GitLab the scenarios' go image and every $CI_JOB_IMAGE_<TARGET> the
// gate of a named demo checks in. Each must be a target of
// containers/runtime/images.json. Every nanolinter job image a case names
// must be of one of flavors, the job-image= flavors the campaign publishes,
// and a GitLab consumer names lint-archive= when its demo's gate lints.
func CampaignRuntimeTargets(root string, lanes, flavors []string) ([]string, error) {
	if len(lanes) == 0 {
		return nil, fmt.Errorf("name the lanes the campaign runs, <forgejo|gitlab>=<cases file>: %w", errs.ErrValidation)
	}

	prepared := map[string]bool{}

	for _, flavor := range flavors {
		if !campaignFlavor.MatchString(flavor) {
			return nil, fmt.Errorf("a job image flavor is lowercase letters, digits and hyphens, got %q: %w", flavor, errs.ErrValidation)
		}

		prepared[campaignFlavorKey(flavor)] = true
	}

	body, err := os.ReadFile(filepath.Join(root, "containers", "runtime", "images.json")) //nolint:gosec // This repository's roster, read as data.
	if err != nil {
		return nil, err
	}

	var roster []struct {
		Target string `json:"target"`
	}
	if err = json.Unmarshal(body, &roster); err != nil {
		return nil, fmt.Errorf("containers/runtime/images.json: %w", err)
	}

	targets := map[string]bool{}

	for _, lane := range lanes {
		provider, cases, _ := strings.Cut(lane, "=")
		if !isCampaignProvider(provider) || cases == "" {
			return nil, fmt.Errorf("want <forgejo|gitlab>=<cases file>, got %q: %w", lane, errs.ErrValidation)
		}

		file, err := readCampaignCases(cases)
		if err != nil {
			return nil, err
		}

		for _, line := range file.cases {
			if caseErr := campaignCaseTargets(root, provider, line.words, prepared, targets); caseErr != nil {
				return nil, fmt.Errorf("%s:%d: %w", cases, line.number, caseErr)
			}
		}
	}

	built := map[string]bool{}
	for _, image := range roster {
		built[image.Target] = true
	}

	for target := range targets {
		if !built[target] {
			return nil, fmt.Errorf("the campaign names the runtime target %q, which containers/runtime/images.json does not build: %w", target, errs.ErrValidation)
		}
	}

	return slices.Sorted(maps.Keys(targets)), nil
}

// campaignCaseTargets adds the runtime targets one case of provider needs,
// and refuses a nanolinter job image of a flavor not prepared.
func campaignCaseTargets(root, provider string, words []string, prepared, targets map[string]bool) error {
	if len(words) == 0 || strings.HasPrefix(words[0], "#") {
		return nil
	}

	for _, word := range words[1:] {
		for _, match := range campaignRuntimeArchive.FindAllStringSubmatch(word, -1) {
			if match[1] == "" {
				targets[campaignScenarioTarget] = true
			} else {
				targets[strings.ToLower(match[1])] = true
			}
		}

		if err := campaignFlavorsPrepared(word, prepared); err != nil {
			return err
		}
	}

	switch {
	case provider != campaignGitLab:
	case words[0] == "scenario":
		targets[campaignScenarioTarget] = true
	case words[0] == "consumer" && len(words) > 1:
		if !campaignDemo.MatchString(words[1]) {
			return fmt.Errorf("not a demo name: %q: %w", words[1], errs.ErrValidation)
		}

		gate, err := readCampaignGate(filepath.Join(root, "test", "live", "demos", words[1], "consumer", filepath.FromSlash(GitLabGateFile)), words[2:])
		if err != nil {
			return err
		}

		for _, name := range gate.extraJobs {
			targets[strings.ToLower(name)] = true
		}
	}

	return nil
}

// campaignFlavorsPrepared refuses a case's word that names a nanolinter job
// image of a flavor not prepared.
func campaignFlavorsPrepared(word string, prepared map[string]bool) error {
	for _, match := range campaignFlavorRef.FindAllStringSubmatch(word, -1) {
		if !prepared[match[1]] {
			return fmt.Errorf("%s names a nanolinter job image the campaign does not publish; add job-image=%s:<local image>: %w",
				word, strings.ToLower(strings.ReplaceAll(match[1], "_", "-")), errs.ErrValidation)
		}
	}

	return nil
}

// demoGate is what a demo's GitLab gate runs in beside $CI_JOB_IMAGE: the
// NAME of each $CI_JOB_IMAGE_<NAME> it checks in, and whether it lints in
// $CI_LINT_IMAGE.
type demoGate struct {
	extraJobs []string
	lints     bool
}

// readDemoGate reads the demo's GitLab gate at path; a demo without one runs
// in nothing further.
func readDemoGate(path string) (demoGate, error) {
	body, err := os.ReadFile(path) //nolint:gosec // A demo of this repository, read as data.
	if errors.Is(err, fs.ErrNotExist) {
		return demoGate{}, nil
	}

	if err != nil {
		return demoGate{}, err
	}

	var gate demoGate
	if gate.extraJobs, err = gitlabGateExtraJobImages(body); err == nil {
		gate.lints, err = gitlabGateLints(body)
	}

	if err != nil {
		return demoGate{}, fmt.Errorf("%s: %w", path, err)
	}

	return gate, nil
}

// WriteCampaignPlan writes plan for the lane script, which reads it without
// evaluating it: NUL-terminated fields, "unset" and one name per variable
// cleared, "env" and one KEY=value per variable, then per case "case", its
// text, its argument count and its arguments.
func WriteCampaignPlan(out io.Writer, plan CampaignPlan) error {
	var fields []string
	for _, name := range plan.Unset {
		fields = append(fields, "unset", name)
	}

	for _, assignment := range plan.Env {
		fields = append(fields, "env", assignment)
	}

	for _, c := range plan.Cases {
		fields = append(fields, "case", c.Text, strconv.Itoa(len(c.Args)))
		fields = append(fields, c.Args...)
	}

	for _, field := range fields {
		if _, err := io.WriteString(out, field+"\x00"); err != nil {
			return err
		}
	}

	return nil
}

// eachLine calls visit with each line of the file at path, numbered from 1.
func eachLine(path string, visit func(int, string) error) error {
	file, err := os.Open(path) //nolint:gosec // Operator-named campaign input, read as data.
	if err != nil {
		return err
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for number := 1; scanner.Scan(); number++ {
		if strings.ContainsRune(scanner.Text(), 0) {
			return fmt.Errorf("%s:%d: a NUL byte: %w", path, number, errs.ErrValidation)
		}

		if err := visit(number, scanner.Text()); err != nil {
			return err
		}
	}

	return scanner.Err()
}

func isDir(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.IsDir()
}
