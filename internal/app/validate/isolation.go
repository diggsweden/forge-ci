// SPDX-FileCopyrightText: 2026 Digg - Agency for Digital Government
// SPDX-License-Identifier: EUPL-1.2 OR GPL-3.0-or-later

package validate

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/diggsweden/forge-ci/internal/clicolor"
	"github.com/diggsweden/forge-ci/internal/domain/errs"
	"github.com/diggsweden/forge-ci/internal/domain/git"
	"github.com/diggsweden/forge-ci/internal/domain/output"
	"github.com/diggsweden/forge-ci/internal/domain/validate"
	"gopkg.in/yaml.v3"
)

// IsolationInput drives Isolation.
type IsolationInput struct {
	// WorkflowsDir is the repository's workflow directory (relative to FS
	// when FS is set, else an OS path), for example .forgejo/workflows. The
	// repository root is the directory two levels above it. Its release
	// workflows are the ones that call the subject's release entry points;
	// the caller does not name them.
	WorkflowsDir string
	// SigningSecrets are the secret names no job may reference but a call of
	// a signer workflow of the layer.
	SigningSecrets []string
	// SinglePinSubject is the owner/repository of the engine (for example,
	// "example/provider-workflows"). The release workflows are the ones that
	// call its release entry points; every reference to it in the workflow
	// directory agrees on one commit and one forge, and the jobs that may
	// receive signing secrets call it. It is required.
	SinglePinSubject string
	// ServerURL is the base URL of the forge the workflows run on. A reference
	// to SinglePinSubject that writes a forge address must write this one, and
	// a reference without one is resolved by the forge. Empty means it is not
	// known: the references that write an address must then agree on one.
	ServerURL string
	// SinglePinInput names the input through which the subject's reusable
	// workflows receive their commit a second time. Every job in the workflow
	// directory that calls one of them must pass it as a literal commit, and
	// the value joins the pins that must agree. Empty checks uses: pins only.
	SinglePinInput string
	// LiteralInputs names inputs of the subject's reusable workflows that a
	// caller must write out: every job in the workflow directory that calls
	// one of them and passes such an input must pass a literal, never an
	// expression. Each value is NAME or NAME=SHAPE, where SHAPE (sha256,
	// image-digest, or enum:VALUE/VALUE) also holds the literal to that form.
	LiteralInputs []string
	// ContractRoot is the trusted checkout of SinglePinSubject at the pin
	// used by the caller. Its action/workflow declarations admit with: keys.
	// Empty leaves the with: keys unchecked.
	ContractRoot string
	// FS overrides filesystem access for tests. Nil -> the real OS filesystem.
	FS fs.FS
}

// Isolation asserts the static release-isolation invariants of a
// repository's workflow directory. Its release workflows are found, not
// named: every workflow that calls one of the subject's release entry
// points. Their jobs, the local workflows they call and the local actions
// they use are held to the release rules (validate.CheckIsolation, local and
// subject actions only); every workflow of the directory is held to the
// signing-secret boundary, one subject pin on one forge and the literal and
// declared inputs. Violations are emitted as error annotations (GitHub:
// `::error file=…,line=…::`; elsewhere a plain `Error:` line) and the call
// returns a wrapped errs.ErrValidation.
func Isolation(out io.Writer, annot output.Annotator, in IsolationInput) error {
	literalInputs, err := subjectInputs(in)
	if err != nil {
		return err
	}

	files, err := workflowDirYAMLFiles(in)
	if err != nil {
		return err
	}

	releases, others := releaseWorkflows(files, in.SinglePinSubject)
	called, violations := calledLocalWorkflows(in, releases)

	ruleViolations, err := releaseRuleViolations(in, releases, notIn(others, called))
	if err != nil {
		return err
	}

	violations = append(violations, ruleViolations...)
	violations = append(violations, calledWorkflowViolations(in, called)...)

	actions, actionViolations := foreignActionViolations(in, slices.Concat(releases, called))
	violations = append(violations, actionViolations...)

	// The pin and address checks read every file the release runs, not only
	// the workflow directory: a local action can reference the engine too.
	pinViolations, addressNote := singlePinViolations(in, slices.Concat(files, notIn(slices.Concat(called, actions), files)))
	violations = append(violations, pinViolations...)
	violations = append(violations, literalInputViolations(in, literalInputs, files)...)
	violations = append(violations, declaredInputViolations(in, files)...)

	if len(releases) == 0 {
		violations = append(violations, fileViolation{File: in.WorkflowsDir, Msg: fmt.Sprintf(
			"no workflow under %s calls %s's %s: there is no release workflow to check",
			in.WorkflowsDir, in.SinglePinSubject, strings.Join(validate.ReleaseEntryWorkflows(), " or "))})
	}

	if addressNote != "" {
		_, _ = fmt.Fprintln(out, addressNote)
	}

	for _, v := range violations {
		annot.ErrorAt(output.Annotation{File: v.File, Line: v.Line}, "%s", v.Msg)
	}

	if len(violations) > 0 {
		return fmt.Errorf("isolation validation failed for %s: %w", in.WorkflowsDir, errs.ErrValidation)
	}

	if in.ContractRoot != "" {
		_, _ = fmt.Fprintf(out, "Action and workflow inputs checked against the supplied layer checkout %s\n", in.ContractRoot)
	}

	for _, file := range releases {
		_, _ = fmt.Fprintf(out, "Release workflow %s, which calls %s, checked\n", file.Path, in.SinglePinSubject)
	}

	for _, file := range called {
		_, _ = fmt.Fprintf(out, "Local workflow %s, which a release workflow calls, checked with it\n", file.Path)
	}

	_, _ = fmt.Fprintln(out, "Digest verification before signing is not checked here; the pinned signing workflow enforces the runtime verifier and ordering.")
	_, _ = fmt.Fprintf(out, "%s Static release-isolation checks passed in %s\n", clicolor.Check(out), in.WorkflowsDir)

	return nil
}

// releaseWorkflows splits the workflow directory into the release workflows,
// the ones with a job that calls one of the subject's release entry points,
// and the rest.
func releaseWorkflows(files []releaseFile, subject string) ([]releaseFile, []releaseFile) {
	var releases, others []releaseFile

	for _, file := range files {
		if callsReleaseEntry(file.Body, subject) {
			releases = append(releases, file)
		} else {
			others = append(others, file)
		}
	}

	return releases, others
}

// callsReleaseEntry reports whether a job of the workflow calls one of the
// subject's release entry points. A file that does not parse is a release
// workflow: the release rules then report it as malformed rather than the
// directory passing without it.
func callsReleaseEntry(body []byte, subject string) bool {
	var doc struct {
		Jobs map[string]struct {
			Uses yaml.Node `yaml:"uses"`
		} `yaml:"jobs"`
	}

	if err := yaml.Unmarshal(body, &doc); err != nil {
		return true
	}

	for _, job := range doc.Jobs {
		if uses, ok := resolvedScalar(&job.Uses); ok && validate.CallsReleaseEntry(uses) && referencesSubject(uses, subject) {
			return true
		}
	}

	return false
}

// notIn returns the files that are none of the others.
func notIn(files, others []releaseFile) []releaseFile {
	return slices.DeleteFunc(slices.Clone(files), func(file releaseFile) bool {
		return slices.ContainsFunc(others, func(other releaseFile) bool { return path.Clean(other.Path) == path.Clean(file.Path) })
	})
}

// releaseRuleViolations holds each release workflow to the release rules,
// and every other workflow of the directory to the signing-secret boundary:
// a key reaches the engine's signers alone, whichever workflow names it.
func releaseRuleViolations(in IsolationInput, releases, others []releaseFile) ([]fileViolation, error) {
	cfg := validate.IsolationConfig{SigningSecrets: in.SigningSecrets}

	var violations []fileViolation

	for _, file := range releases {
		found, err := validate.CheckIsolation(file.Body, cfg)
		if err != nil {
			return nil, fmt.Errorf("check isolation %s: %w: %w", file.Path, err, errs.ErrMalformedInput)
		}

		violations = append(violations, inFile(file.Path, found)...)
	}

	for _, file := range others {
		found, err := validate.CheckSigningSecrets(file.Body, cfg)
		if err != nil {
			return nil, fmt.Errorf("check signing secrets %s: %w: %w", file.Path, err, errs.ErrMalformedInput)
		}

		violations = append(violations, inFile(file.Path, found)...)
	}

	return violations, nil
}

// inFile places the violations of one file at that file.
func inFile(file string, found []validate.IsolationViolation) []fileViolation {
	violations := make([]fileViolation, 0, len(found))
	for _, v := range found {
		violations = append(violations, fileViolation{File: file, Line: v.Line, Msg: v.Msg})
	}

	return violations
}

// perFile runs a check over each file and places its violations there.
func perFile(files []releaseFile, check func([]byte) []validate.IsolationViolation) []fileViolation {
	violations := make([]fileViolation, 0, len(files))
	for _, file := range files {
		violations = append(violations, inFile(file.Path, check(file.Body))...)
	}

	return violations
}

// subjectInputs refuses a call without a subject, and returns the parsed
// literal inputs.
func subjectInputs(in IsolationInput) ([]literalInput, error) {
	if in.SinglePinSubject == "" {
		return nil, fmt.Errorf("--single-pin-subject names the engine whose release entry points find the release workflows, and none is given: %w", errs.ErrUsage)
	}

	if in.WorkflowsDir == "" {
		return nil, fmt.Errorf("--workflows-dir names the workflow directory to check, and none is given: %w", errs.ErrUsage)
	}

	return parseLiteralInputs(in.LiteralInputs)
}

// singlePinViolations holds the workflow directory to one engine: every
// reference to the subject at one commit and one forge address, and every job
// that may receive a signing secret calling the subject. The string is a note
// for the reader when the address could not be compared with the forge's own.
func singlePinViolations(in IsolationInput, files []releaseFile) ([]fileViolation, string) {
	shas, addresses := map[string]struct{}{}, map[string]struct{}{}

	var violations, engineViolations, unpinned []fileViolation

	for _, file := range files {
		refs, err := yamlUsesRefs(file.Body)
		if err != nil {
			violations = append(violations, fileViolation{File: file.Path, Line: 1, Msg: err.Error()})

			continue
		}

		for _, ref := range refs {
			address, _, ok := subjectReference(ref.value, in.SinglePinSubject)
			if !ok {
				continue
			}

			if address != "" {
				addresses[address] = struct{}{}
			}

			sha := pinnedSubjectSHA(ref.value, in.SinglePinSubject)
			if sha == "" {
				unpinned = append(unpinned, fileViolation{File: file.Path, Line: ref.line, Msg: fmt.Sprintf(
					"%s references %s at %s, which is not a full commit: every reference to the engine is pinned to one full commit SHA",
					ref.value, in.SinglePinSubject, subjectRef(ref.value))})

				continue
			}

			shas[sha] = struct{}{}
		}

		engineViolations = append(engineViolations, inFile(file.Path, signerCallViolations(file.Body, in.SinglePinSubject))...)

		inputPins, inputViolations := subjectCallInputPins(file.Body, in.SinglePinSubject, in.SinglePinInput)
		violations = append(violations, inFile(file.Path, inputViolations)...)

		for _, sha := range inputPins {
			shas[sha] = struct{}{}
		}
	}

	if len(violations) > 0 {
		return violations, ""
	}

	// A subject that matches nothing is not a workflow directory with one
	// consistent pin -- it is a check that ran over nothing and said so in the
	// same words. A stale or misspelled subject silently disabled this entirely.
	if len(shas) == 0 && len(unpinned) == 0 {
		return []fileViolation{{
			File: in.WorkflowsDir,
			Msg: fmt.Sprintf(
				"no %s pin found under workflow directory: the single-pin subject matches nothing, so pin consistency was not checked",
				in.SinglePinSubject),
		}}, ""
	}

	addressViolations, note := subjectAddressViolations(in, slices.Sorted(maps.Keys(addresses)))
	engineViolations = slices.Concat(unpinned, engineViolations, addressViolations)

	if len(shas) > 1 {
		engineViolations = append(engineViolations, fileViolation{
			File: in.WorkflowsDir,
			Msg: fmt.Sprintf("mixed %s pins under workflow directory: %s",
				in.SinglePinSubject, strings.Join(slices.Sorted(maps.Keys(shas)), ", ")),
		})
	}

	return engineViolations, note
}

// subjectRef is the ref a uses: value names after its last @, or says that
// it names none.
func subjectRef(uses string) string {
	if _, ref, found := strings.CutLast(uses, "@"); found {
		return "@" + ref
	}

	return "no ref"
}

// subjectAddressViolations refuses a reference to the subject on another
// forge. A reference that writes an address is fetched from that address,
// whatever forge runs the workflow, so the same owner/repository on a foreign
// host is another engine. Without the forge's own URL, off a runner, only the
// agreement of the written addresses can be checked, and the note says so.
func subjectAddressViolations(in IsolationInput, addresses []string) ([]fileViolation, string) {
	server := strings.TrimRight(in.ServerURL, "/")
	if server == "" {
		switch len(addresses) {
		case 0:
			return nil, ""
		case 1:
			return nil, fmt.Sprintf("The forge's server URL is not known here: the address %s of the %s references was not compared with it.",
				addresses[0], in.SinglePinSubject)
		}

		return []fileViolation{{
			File: in.WorkflowsDir,
			Msg: fmt.Sprintf("%s is referenced at more than one forge address under workflow directory: %s",
				in.SinglePinSubject, strings.Join(addresses, ", ")),
		}}, ""
	}

	var violations []fileViolation

	for _, address := range addresses {
		if !strings.EqualFold(address, server) {
			violations = append(violations, fileViolation{
				File: in.WorkflowsDir,
				Msg: fmt.Sprintf("%s is referenced at %s, and this forge is %s: a reference that writes a forge address must write the forge's own",
					in.SinglePinSubject, address, server),
			})
		}
	}

	return violations, ""
}

// signerCallViolations refuses a job that calls a workflow named like one of
// the layer's signer workflows in another repository than the subject. The
// secret check lets such a call receive signing secrets by the workflow's
// name alone, so the name must be the subject's.
func signerCallViolations(body []byte, subject string) []validate.IsolationViolation {
	var doc struct {
		Jobs map[string]struct {
			Uses yaml.Node `yaml:"uses"`
		} `yaml:"jobs"`
	}

	if err := yaml.Unmarshal(body, &doc); err != nil {
		return []validate.IsolationViolation{{Line: 1, Msg: fmt.Sprintf("parse workflow YAML: %v", err)}}
	}

	var violations []validate.IsolationViolation

	for _, name := range slices.Sorted(maps.Keys(doc.Jobs)) {
		job := doc.Jobs[name]

		uses, ok := resolvedScalar(&job.Uses)
		if !ok || !validate.CallsSignerWorkflow(uses) || referencesSubject(uses, subject) {
			continue
		}

		violations = append(violations, validate.IsolationViolation{
			Line: job.Uses.Line,
			Msg:  fmt.Sprintf("job %q calls %s, which is not %s: a job that may receive signing secrets must call the single-pin subject", name, uses, subject),
		})
	}

	return violations
}

// subjectCallInputPins returns the commit each job passes as input to a
// reusable workflow of subject, and a violation for every such job that does
// not pass a literal commit.
//
// The name is the caller's, not a guess from the subject. Deriving it from the
// subject's repository name ("<repo>-helpers-sha") skipped this half of the
// check whenever the serving repository was not named like the input: for
// every fork that renamed the repository, and for a lab snapshot. A job that
// calls the subject without the input is refused for the same reason: an
// absent or computed value is a second pin nothing compared.
func subjectCallInputPins(body []byte, subject, input string) ([]string, []validate.IsolationViolation) {
	if input == "" {
		return nil, nil
	}

	var doc struct {
		Jobs map[string]struct {
			Uses yaml.Node            `yaml:"uses"`
			With map[string]yaml.Node `yaml:"with"`
		} `yaml:"jobs"`
	}

	if err := yaml.Unmarshal(body, &doc); err != nil {
		return nil, []validate.IsolationViolation{{Line: 1, Msg: fmt.Sprintf("parse workflow YAML: %v", err)}}
	}

	names := make([]string, 0, len(doc.Jobs))
	for name := range doc.Jobs {
		names = append(names, name)
	}

	slices.Sort(names)

	var (
		pins       []string
		violations []validate.IsolationViolation
	)

	for _, name := range names {
		job := doc.Jobs[name]

		uses, ok := resolvedScalar(&job.Uses)
		if !ok || !referencesSubject(uses, subject) {
			continue
		}

		value := job.With[input]
		if sha, ok := resolvedScalar(&value); ok && git.ValidCommitSHA(sha) {
			pins = append(pins, sha)

			continue
		}

		violations = append(violations, validate.IsolationViolation{
			Line: job.Uses.Line,
			Msg: fmt.Sprintf("job %q calls %s without a literal commit in its %s input: pass the commit of its uses: pin, written out",
				name, subject, input),
		})
	}

	return pins, violations
}

// workflowDirYAMLFiles reads every workflow file of the directory, by name.
func workflowDirYAMLFiles(in IsolationInput) ([]releaseFile, error) {
	dir := cleanDir(in.WorkflowsDir)

	var (
		entries []fs.DirEntry
		err     error
	)

	if in.FS != nil {
		entries, err = fs.ReadDir(in.FS, dir)
	} else {
		entries, err = os.ReadDir(filepath.FromSlash(dir))
	}

	if err != nil {
		return nil, workflowReadError(dir, err)
	}

	var files []releaseFile

	for _, entry := range entries {
		if entry.IsDir() || !isYAMLFile(entry.Name()) {
			continue
		}

		name := path.Join(dir, entry.Name())

		data, err := readFile(in, name)
		if err != nil {
			return nil, workflowReadError(name, err)
		}

		files = append(files, releaseFile{Path: name, Body: data})
	}

	return files, nil
}

// readRepositoryFile reads a file through FS when set, else the OS. A file
// that does not exist is not an error: the second result is false.
func readRepositoryFile(in IsolationInput, name string) ([]byte, bool, error) {
	data, err := readFile(in, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("read %s: %w", name, err)
	}

	return data, true, nil
}

// readFile reads a file through FS when set, else the OS.
func readFile(in IsolationInput, name string) ([]byte, error) {
	if in.FS != nil {
		return fs.ReadFile(in.FS, name) //nolint:wrapcheck // the callers say which file
	}

	return os.ReadFile(filepath.FromSlash(name)) //nolint:wrapcheck // the callers say which file
}

// cleanDir is the workflow directory as a clean slash path.
func cleanDir(dir string) string {
	return path.Clean(filepath.ToSlash(dir))
}

// repositoryRoot is the directory two levels above the workflow directory,
// where the local actions and workflows the release uses are resolved.
func repositoryRoot(in IsolationInput) string {
	return path.Dir(path.Dir(cleanDir(in.WorkflowsDir)))
}

func isYAMLFile(name string) bool {
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}
