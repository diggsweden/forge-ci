// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"gopkg.in/yaml.v3"
)

// IsolationConfig parameterises the static release-isolation checks. Job
// names and the signing-secret set differ per pipeline, so they are supplied by
// the caller rather than hard-coded.
type IsolationConfig struct {
	// SigningSecrets are the secret names no job may reference but the
	// prepare job, the sign job and a call of one of the layer's signer
	// workflows (e.g. RELEASE_GPG_PRIVATE_KEY, COSIGN_PRIVATE_KEY).
	SigningSecrets []string
	// SignJob is the reusable signing workflow call-site job. Empty disables the
	// Forgejo release-signing call-site checks, keeping the generic validator
	// backwards-compatible for workflows that only want the core release-isolation
	// checks.
	SignJob string
	// PrepareJob is the job that produces release-tag, release-sha and
	// artifacts-config. With SignJob set it must call the layer's
	// prepare-release.yml; without, a job built from steps must run its
	// signing-secret presence checks before the consumer checkout.
	PrepareJob string
	// DistDigestOutput is the output carrying the build-to-sign digest, of the
	// job the sign job takes dist-digest from. Empty disables the dist-digest
	// channel check.
	DistDigestOutput string
}

// IsolationViolation is one failed static invariant. Secret diagnostics identify
// the consuming job, secret and start of the containing source scalar (the anchor
// definition for an alias), not an exact expression-token location.
type IsolationViolation struct {
	Line int
	Msg  string
}

// CheckIsolation returns the release-isolation violations in a workflow:
//
//  1. a job referencing a signing secret, directly or through the workflow
//     env it inherits, other than the prepare job, the sign job and the calls
//     of the layer's signer workflows: keys live only in separately trusted
//     jobs, never in one that runs the project's code, whichever job that is
//     (a build, its tests, an image build or the hand-off);
//  2. any actions/checkout step that does not set persist-credentials: false
//     (a persisted token in a job that runs the project's code widens the
//     blast radius).
//
// It is pure — the caller supplies the raw YAML and owns IO and annotation — so
// it is exhaustively table-testable without a filesystem.
func CheckIsolation(workflowYAML []byte, cfg IsolationConfig) ([]IsolationViolation, error) {
	var doc struct {
		Jobs map[string]yaml.Node `yaml:"jobs"`
		Env  yaml.Node            `yaml:"env"`
	}

	if err := yaml.Unmarshal(workflowYAML, &doc); err != nil {
		return nil, fmt.Errorf("parse workflow yaml: %w", err)
	}

	violations, err := jobSecretViolations(doc.Jobs, cfg)
	if err != nil {
		return nil, err
	}

	inheritedViolations, err := workflowEnvSecretViolations(doc.Env, doc.Jobs, cfg)
	if err != nil {
		return nil, err
	}

	violations = append(violations, inheritedViolations...)

	prepareViolations, err := prepareOrderingViolations(doc.Jobs, cfg)
	if err != nil {
		return nil, err
	}

	violations = append(violations, prepareViolations...)
	violations = append(violations, signCallSiteViolations(doc.Jobs, cfg)...)
	violations = append(violations, prepareCallSiteViolations(doc.Jobs, cfg)...)
	violations = append(violations, releaseIdentityViolations(doc.Jobs, cfg)...)
	violations = append(violations, distDigestViolations(doc.Jobs, cfg)...)
	violations = append(violations, persistCredentialViolations(doc.Jobs)...)
	violations = append(violations, setupToolchainCacheViolations(doc.Jobs)...)

	return violations, nil
}

// signerWorkflows are the layer's reusable workflows that take signing
// secrets: a job that calls one of them is the separately trusted place a key
// may go. test/forgejoguard holds the list to the workflows whose secrets
// contract names one.
var signerWorkflows = []string{
	"prepare-release.yml", "sign-and-publish-release.yml", "sign-container-images.yml", "runtime-images.yml",
}

// SignerWorkflows returns the file names of the layer's workflows that take
// signing secrets, the calls of which may receive them.
func SignerWorkflows() []string { return slices.Clone(signerWorkflows) }

// mayHoldSigningSecrets reports whether a job is one of the separately
// trusted ones: the configured prepare and sign jobs, whose own rules apply
// below, and a cross-repository call of a signer workflow of the layer.
func mayHoldSigningSecrets(name string, job *yaml.Node, cfg IsolationConfig) bool {
	if name != "" && (name == cfg.PrepareJob || name == cfg.SignJob) {
		return true
	}

	uses := scalarValue(mappingValue(job, "uses"))
	if uses == "" || strings.HasPrefix(uses, "./") {
		return false
	}

	return slices.ContainsFunc(signerWorkflows, func(workflow string) bool {
		return strings.Contains(uses, "/.forgejo/workflows/"+workflow+"@")
	})
}

// workflowEnvSecretViolations reports a signing secret in the workflow env
// that a job which may not hold one inherits, without overriding the name.
func workflowEnvSecretViolations(env yaml.Node, jobs map[string]yaml.Node, cfg IsolationConfig) ([]IsolationViolation, error) {
	if env.Kind == 0 || len(cfg.SigningSecrets) == 0 {
		return nil, nil
	}

	var inherited map[string]yaml.Node
	if err := env.Decode(&inherited); err != nil {
		return nil, fmt.Errorf("decode workflow env: %w", err)
	}

	var violations []IsolationViolation

	for _, name := range sortedKeys(jobs) {
		job := jobs[name]
		if mayHoldSigningSecrets(name, &job, cfg) {
			continue
		}

		var overrides map[string]yaml.Node
		if jobEnv := mappingValue(&job, "env"); jobEnv != nil {
			if err := jobEnv.Decode(&overrides); err != nil {
				return nil, fmt.Errorf("decode job %q env: %w", name, err)
			}
		}

		for _, key := range sortedKeys(inherited) {
			if _, overridden := overrides[key]; overridden {
				continue
			}

			value := inherited[key]

			refs, err := findSecretRefs(&value, signingSecretPattern(cfg.SigningSecrets))
			if err != nil {
				return nil, fmt.Errorf("inspect workflow env %q: %w", key, err)
			}

			for _, ref := range refs {
				violations = append(violations, IsolationViolation{Line: ref.line, Msg: fmt.Sprintf("job %q inherits signing secret %s through workflow env %q%s", name, ref.describe(), key, signingSecretHome)})
			}
		}
	}

	return violations, nil
}

// signingSecretHome ends each signing-secret diagnostic with where a key may go.
const signingSecretHome = "; only the prepare job and the calls of the layer's signer workflows may receive signing keys, never a job that runs the project's code (release isolation)"

// jobSecretViolations flags every job that may not hold a signing secret and
// references one: a build, its tests, an image build and the hand-off all run
// the project's code, so none of them may reach a signing key.
func jobSecretViolations(jobs map[string]yaml.Node, cfg IsolationConfig) ([]IsolationViolation, error) {
	if len(cfg.SigningSecrets) == 0 {
		return nil, nil
	}

	var violations []IsolationViolation

	for _, name := range sortedKeys(jobs) {
		node := jobs[name]
		if mayHoldSigningSecrets(name, &node, cfg) {
			continue
		}

		refs, err := findSecretRefs(&node, signingSecretPattern(cfg.SigningSecrets))
		if err != nil {
			return nil, fmt.Errorf("inspect job %q: %w", name, err)
		}

		for _, ref := range refs {
			violations = append(violations, IsolationViolation{
				Line: ref.line,
				Msg:  fmt.Sprintf("job %q references signing secret %s%s", name, ref.describe(), signingSecretHome),
			})
		}

		// A reusable-workflow job that inherits the caller's secrets holds
		// every signing secret without naming one.
		if scalarValue(mappingValue(&node, "secrets")) == "inherit" {
			violations = append(violations, IsolationViolation{
				Line: node.Line,
				Msg:  fmt.Sprintf("job %q inherits every caller secret (secrets: inherit); pass it only the secrets it needs (release isolation)", name),
			})
		}
	}

	return violations, nil
}

func prepareOrderingViolations(jobs map[string]yaml.Node, cfg IsolationConfig) ([]IsolationViolation, error) {
	if cfg.PrepareJob == "" || len(cfg.SigningSecrets) == 0 {
		return nil, nil
	}

	node, ok := jobs[cfg.PrepareJob]
	if !ok {
		return nil, nil
	}

	steps := jobSteps(node)
	checkoutIndex := -1

	for i, step := range steps {
		uses := scalarValue(mappingValue(&step, "uses"))
		if usesAction(uses, "actions/checkout@") || usesAction(uses, "checkout-consumer") {
			checkoutIndex = i

			break
		}
	}

	if checkoutIndex < 0 {
		return nil, nil
	}

	var violations []IsolationViolation

	re := signingSecretPattern(cfg.SigningSecrets)
	for _, step := range steps[checkoutIndex:] {
		refs, err := findSecretRefs(&step, re)
		if err != nil {
			return nil, fmt.Errorf("inspect prepare job %q: %w", cfg.PrepareJob, err)
		}

		for _, ref := range refs {
			violations = append(violations, IsolationViolation{
				Line: ref.line,
				Msg:  fmt.Sprintf("prepare job %q references signing secret %s at/after checkout; signing-secret checks must run before consumer code is checked out", cfg.PrepareJob, ref.describe()),
			})
		}
	}

	return violations, nil
}

func signCallSiteViolations(jobs map[string]yaml.Node, cfg IsolationConfig) []IsolationViolation {
	if cfg.SignJob == "" {
		return nil
	}

	node, ok := jobs[cfg.SignJob]
	if !ok {
		return []IsolationViolation{{Line: 1, Msg: fmt.Sprintf("sign job %q is missing from workflow", cfg.SignJob)}}
	}

	uses := scalarValue(mappingValue(&node, "uses"))
	if uses == "" || strings.HasPrefix(uses, "./") {
		return []IsolationViolation{{
			Line: node.Line,
			Msg:  fmt.Sprintf("sign job %q must be a cross-repo workflow_call invocation", cfg.SignJob),
		}}
	}

	if len(cfg.SigningSecrets) == 0 {
		return nil
	}

	secretsNode := mappingValue(&node, "secrets")

	var violations []IsolationViolation

	for _, secret := range cfg.SigningSecrets {
		if secret == "" {
			continue
		}

		if !nodeContainsSecretRef(secretsNode, secret) {
			violations = append(violations, IsolationViolation{
				Line: node.Line,
				Msg:  fmt.Sprintf("sign job %q call site is missing signing secret %s", cfg.SignJob, secret),
			})
		}
	}

	return violations
}

// prepareCallSiteViolations reports a secret the sign job takes that the
// prepare job does not pass to prepare-release.yml. Prepare refuses the
// release before it pushes the tag when a secret the release uses is not
// configured, and a called workflow sees only the secrets its caller passes:
// without this check the omission shows only at the release. It reads only
// whether each is set, never its value.
func prepareCallSiteViolations(jobs map[string]yaml.Node, cfg IsolationConfig) []IsolationViolation {
	if cfg.SignJob == "" || cfg.PrepareJob == "" {
		return nil
	}

	prepare, ok := jobs[cfg.PrepareJob]
	if !ok {
		return nil
	}

	uses := scalarValue(mappingValue(&prepare, "uses"))
	if strings.HasPrefix(uses, "./") || !strings.Contains(uses, prepareReleaseWorkflow) {
		return nil
	}

	sign, ok := jobs[cfg.SignJob]
	if !ok {
		return nil
	}

	passed := mappingValue(&prepare, "secrets")
	if scalarValue(passed) == "inherit" {
		return nil
	}

	var violations []IsolationViolation

	for _, secret := range mappingKeys(mappingValue(&sign, "secrets")) {
		if mappingValue(passed, secret) == nil {
			violations = append(violations, IsolationViolation{
				Line: prepare.Line,
				Msg: fmt.Sprintf("prepare job %q call site is missing secret %s, which sign job %q takes: prepare-release.yml refuses the release before its tag when a secret the release uses is not configured, and it sees only what its caller passes",
					cfg.PrepareJob, secret, cfg.SignJob),
			})
		}
	}

	return violations
}

// releaseTagOutput and releaseSHAOutput are the prepare-job output names the
// release-identity check requires and pins the sign job's inputs to.
// artifactsConfigOutput is the declaration the prepare job planned the bump
// from: the sign job re-checks the release commit against the same one.
const (
	releaseTagOutput      = "release-tag"
	releaseSHAOutput      = "release-sha"
	artifactsConfigOutput = "artifacts-config"
)

// releaseIdentityOutputs are the prepare outputs the sign job must take.
var releaseIdentityOutputs = []string{releaseTagOutput, releaseSHAOutput, artifactsConfigOutput}

// prepareReleaseWorkflow is the reusable workflow the prepare job must call.
// Its outputs are the release identity and the declaration the bump was
// planned from; a prepare job of the caller's own would let the caller
// choose them.
const prepareReleaseWorkflow = "/.forgejo/workflows/prepare-release.yml@"

func releaseIdentityViolations(jobs map[string]yaml.Node, cfg IsolationConfig) []IsolationViolation {
	if cfg.SignJob == "" || cfg.PrepareJob == "" {
		return nil
	}

	prepare, ok := jobs[cfg.PrepareJob]
	if !ok {
		return nil
	}

	sign, ok := jobs[cfg.SignJob]
	if !ok {
		return nil
	}

	var violations []IsolationViolation

	uses := scalarValue(mappingValue(&prepare, "uses"))
	if strings.HasPrefix(uses, "./") || !strings.Contains(uses, prepareReleaseWorkflow) {
		violations = append(violations, IsolationViolation{
			Line: prepare.Line,
			Msg:  fmt.Sprintf("prepare job %q must call the layer's prepare-release.yml; its outputs are what the sign job is bound to", cfg.PrepareJob),
		})
	}

	for _, name := range releaseIdentityOutputs {
		violations = append(violations, boundInputViolations(&sign, cfg.SignJob, name, cfg.PrepareJob, name)...)
	}

	return violations
}

// boundInputViolations refuses a sign job that passes input as anything but
// the whole expression ${{ needs.<job>.outputs.<output> }} under its exact
// key. A value that only contains the reference can fall back to, or be
// built around, a value of the caller's choosing; a key that differs only in
// case is a second spelling a reader can miss.
func boundInputViolations(sign *yaml.Node, signJob, input, job, output string) []IsolationViolation {
	var violations []IsolationViolation

	with := mappingValue(sign, "with")
	for _, key := range mappingKeys(with) {
		if key != input && strings.EqualFold(key, input) {
			violations = append(violations, IsolationViolation{
				Line: sign.Line,
				Msg:  fmt.Sprintf("sign job %q passes %q; the input is spelled %s", signJob, key, input),
			})
		}
	}

	if !isNeedOutput(scalarValue(mappingValue(with, input)), job, output) {
		violations = append(violations, IsolationViolation{
			Line: sign.Line,
			Msg:  fmt.Sprintf("sign job %q must pass %s from needs.%s.outputs.%s, as that expression alone", signJob, input, job, output),
		})
	}

	return violations
}

// distDigestViolations follows the digest hand-off from the sign job back to
// the job it names: the sign job takes dist-digest as one bare
// ${{ needs.<job>.outputs.<DistDigestOutput> }} expression, and that job
// declares the output. The producer is read from the sign job, so the check
// follows whichever job hands dist/ over, a build or a hand-off after the
// image jobs.
func distDigestViolations(jobs map[string]yaml.Node, cfg IsolationConfig) []IsolationViolation {
	if cfg.SignJob == "" || cfg.DistDigestOutput == "" {
		return nil
	}

	sign, ok := jobs[cfg.SignJob]
	if !ok {
		return nil
	}

	producer := needsJob(scalarValue(mappingValue(mappingValue(&sign, "with"), "dist-digest")))
	if producer == "" {
		return boundInputViolations(&sign, cfg.SignJob, "dist-digest", "<job>", cfg.DistDigestOutput)
	}

	violations := boundInputViolations(&sign, cfg.SignJob, "dist-digest", producer, cfg.DistDigestOutput)

	build, ok := jobs[producer]
	if !ok {
		return append(violations, IsolationViolation{
			Line: sign.Line,
			Msg:  fmt.Sprintf("sign job %q takes dist-digest from job %q, which the workflow does not have", cfg.SignJob, producer),
		})
	}

	if mappingValue(mappingValue(&build, "outputs"), cfg.DistDigestOutput) == nil {
		violations = append(violations, IsolationViolation{
			Line: build.Line,
			Msg:  fmt.Sprintf("job %q, which the sign job takes dist-digest from, does not declare %q output", producer, cfg.DistDigestOutput),
		})
	}

	return violations
}

// needsJobPattern reads the job of one ${{ needs.<job>.outputs... }}
// expression; whether the rest is the bound output is boundInputViolations'.
var needsJobPattern = regexp.MustCompile(`^\$\{\{\s*needs\.([A-Za-z_][A-Za-z0-9_-]*)\.outputs\b`)

// needsJob returns the job a needs-output expression reads, or "".
func needsJob(expr string) string {
	if match := needsJobPattern.FindStringSubmatch(expr); match != nil {
		return match[1]
	}

	return ""
}

// persistCredentialViolations flags every actions/checkout step that does not
// set persist-credentials to the literal false (boolean or string). Identifying
// checkout must not depend on successfully decoding its credential input.
func persistCredentialViolations(jobs map[string]yaml.Node) []IsolationViolation {
	var violations []IsolationViolation

	for _, name := range sortedKeys(jobs) {
		for _, step := range jobSteps(jobs[name]) {
			uses := scalarValue(mappingValue(&step, "uses"))
			if !usesAction(uses, "actions/checkout@") {
				continue
			}

			persist := mappingValue(mappingValue(&step, "with"), "persist-credentials")
			if scalarValue(persist) != "false" || (persist.ShortTag() != "!!bool" && persist.ShortTag() != "!!str") {
				violations = append(violations, IsolationViolation{
					Line: step.Line,
					Msg:  fmt.Sprintf("job %q: actions/checkout step must set persist-credentials: false (static literal required for release isolation)", name),
				})
			}
		}
	}

	return violations
}

func setupToolchainCacheViolations(jobs map[string]yaml.Node) []IsolationViolation {
	var violations []IsolationViolation

	for _, name := range sortedKeys(jobs) {
		for _, step := range jobSteps(jobs[name]) {
			uses := scalarValue(mappingValue(&step, "uses"))
			if !usesAction(uses, "setup-toolchain@") {
				continue
			}

			cacheNode := mappingValue(mappingValue(&step, "with"), "cache")
			if scalarValue(cacheNode) != "false" {
				violations = append(violations, IsolationViolation{
					Line: step.Line,
					Msg:  fmt.Sprintf("job %q: setup-toolchain step must set cache: false in release isolation mode", name),
				})
			}
		}
	}

	return violations
}

// usesAction reports whether a step's `uses:` names the action. Forges resolve
// repository names case-insensitively, so `Actions/Checkout@` is the same
// action as `actions/checkout@` and must not slip past a check by spelling.
func usesAction(uses, action string) bool {
	return strings.Contains(strings.ToLower(uses), action)
}

// signingSecretPattern indexes exact configured names for literal member matching.
func signingSecretPattern(secrets []string) map[string]bool {
	names := make(map[string]bool, len(secrets))
	for _, secret := range secrets {
		if secret != "" {
			names[secret] = true
		}
	}

	return names
}

func nodeContainsSecretRef(node *yaml.Node, secret string) bool {
	if node == nil {
		return false
	}

	re := signingSecretPattern([]string{secret})
	refs, err := findSecretRefs(node, re)

	return err == nil && len(refs) > 0
}

type signingSecretRef struct {
	line   int
	secret string // empty when the whole secrets context is referenced
}

// describe names the reference in a diagnostic: the secret, or the construct
// that reaches every secret at once.
func (r signingSecretRef) describe() string {
	if r.secret == "" {
		return "through the whole secrets context (toJSON(secrets) or a computed index)"
	}

	return fmt.Sprintf("%q", r.secret)
}

// findSecretRefs reports distinct literal secret names per scalar, not token
// occurrences. yaml.v3 resolves effective mapping values before traversal, so
// direct keys and earlier merge sources shadow defaults just as in env decoding.
func findSecretRefs(node *yaml.Node, names map[string]bool) ([]signingSecretRef, error) { //nolint:cyclop,gocognit // Keep bounded traversal, cycle state and effective-value diagnostics in one walk.
	var refs []signingSecretRef

	active, seen := map[*yaml.Node]bool{}, map[*yaml.Node]bool{}

	var walk func(*yaml.Node, int) error

	walk = func(node *yaml.Node, depth int) error {
		if node == nil {
			return nil
		}

		if depth >= aliasDepthLimit || (node.Kind == yaml.AliasNode && resolveAlias(node) == nil) {
			return fmt.Errorf("workflow alias/nesting limit %d exceeded at line %d: %w", aliasDepthLimit, node.Line, errs.ErrMalformedInput)
		}

		node = resolveAlias(node)
		if active[node] {
			return fmt.Errorf("cyclic workflow alias at line %d: %w", node.Line, errs.ErrMalformedInput)
		}

		if seen[node] {
			return nil
		}

		active[node], seen[node] = true, true
		defer delete(active, node)

		if node.Kind == yaml.ScalarNode {
			matched := map[string]bool{}

			for _, ref := range expressionReferences(node.Value, "secrets", false) {
				// toJSON(secrets), secrets[format(...)] and a bare `secrets`
				// reach every secret without naming one. They are a
				// reference to each configured secret, reported once.
				secret := ""
				if len(ref.members) > 0 {
					secret = ref.members[0]
				}

				if (secret == "" || names[secret]) && !matched[secret] {
					refs = append(refs, signingSecretRef{line: node.Line, secret: secret})
					matched[secret] = true
				}
			}

			return nil
		}

		children := node.Content
		if node.Kind == yaml.MappingNode {
			var values map[string]yaml.Node
			if err := node.Decode(&values); err != nil {
				return fmt.Errorf("decode workflow mapping at line %d: %w", node.Line, err)
			}

			children = make([]*yaml.Node, 0, len(values))
			for _, key := range sortedKeys(values) {
				value := values[key]
				children = append(children, &value)
			}
		}

		for _, child := range children {
			if err := walk(child, depth+1); err != nil {
				return err
			}
		}

		return nil
	}
	if err := walk(node, 0); err != nil {
		return nil, err
	}

	return refs, nil
}

// aliasDepthLimit bounds alias chains and secret-walk nesting. Recursive aliases
// and excessive nesting must not turn a partial inspection into a passing check.
const aliasDepthLimit = 100

// resolveAlias follows an alias node to the content it names.
//
// The checks in this file walk yaml.Node trees by hand, and a hand-walk
// sees an alias as a childless leaf: the anchored content is never
// visited. That made the same workflow pass or fail depending on how it
// was spelled, in the fail-open direction -- moving a build job's env
// behind an anchor hid its signing secrets from the release-isolation check.
//
// Anchors are not exotic here. This repository's own consumer-facing
// workflow_call files use them for shared `if:` conditions, so refusing
// them outright was not an option; every reader has to resolve instead.
func resolveAlias(node *yaml.Node) *yaml.Node {
	for depth := 0; node != nil && node.Kind == yaml.AliasNode; depth++ {
		if depth >= aliasDepthLimit {
			return nil
		}

		node = node.Alias
	}

	return node
}

func jobSteps(job yaml.Node) []yaml.Node {
	stepsNode := resolveAlias(mappingValue(&job, "steps"))
	if stepsNode == nil || stepsNode.Kind != yaml.SequenceNode {
		return nil
	}

	steps := make([]yaml.Node, 0, len(stepsNode.Content))

	for _, step := range stepsNode.Content {
		if resolved := resolveAlias(step); resolved != nil {
			steps = append(steps, *resolved)
		}
	}

	return steps
}

// mappingValue uses the same YAML merge precedence as the secret walk. Decoding
// also bounds recursive merge aliases rather than recursively hand-walking them.
func mappingValue(node *yaml.Node, key string) *yaml.Node {
	node = resolveAlias(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}

	var values map[string]yaml.Node
	if err := node.Decode(&values); err != nil {
		return nil
	}

	value, ok := values[key]
	if !ok {
		return nil
	}

	return resolveAlias(&value)
}

// mappingKeys are a mapping's keys after merges, as mappingValue sees them.
func mappingKeys(node *yaml.Node) []string {
	node = resolveAlias(node)
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}

	var values map[string]yaml.Node
	if err := node.Decode(&values); err != nil {
		return nil
	}

	return sortedKeys(values)
}

func scalarValue(node *yaml.Node) string {
	node = resolveAlias(node)
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}

	return node.Value
}

// isNeedOutput reports whether expr is exactly one expression reading
// needs.<job>.outputs.<output>, dotted or bracketed, and nothing else.
func isNeedOutput(expr, job, output string) bool {
	name := regexp.QuoteMeta(output)
	pattern := `^\$\{\{\s*needs\.` + regexp.QuoteMeta(job) +
		`\.outputs(?:\.` + name + `|\['` + name + `'\]|\["` + name + `"\])\s*\}\}$`

	return regexp.MustCompile(pattern).MatchString(expr)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}
