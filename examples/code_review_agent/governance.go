//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	rootskill "trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	codeReviewSkillName = "code-review"

	commandCheckGoVersion   commandKind = "checkGoVersion"
	commandCheckGoTest      commandKind = "checkGoTest"
	commandCheckGoVet       commandKind = "checkGoVet"
	commandCheckStaticcheck commandKind = "checkStaticcheck"

	governanceDecisionAllow = "allow"
	governanceDecisionDeny  = "deny"
	governanceDecisionAsk   = "ask"
	governanceDecisionError = "error"

	ruleGovernanceCommandBlocked = "governance.command_blocked"
	ruleGovernancePermission     = "governance.permission_error"
	ruleSandboxPreflightFailed   = "sandbox.preflight_failed"
	ruleSandboxRunFailed         = "sandbox.run_failed"
	ruleSandboxRunSkipped        = "sandbox.run_skipped"

	toolNameSkillRun = "skill_run"

	defaultCommandTimeoutSeconds = 60
)

var commandEnvAllowlist = map[string]bool{
	"PATH":            true,
	"HOME":            true,
	"GOCACHE":         true,
	"GOMODCACHE":      true,
	"GOPATH":          true,
	"CGO_ENABLED":     true,
	"REVIEW_REPO_DIR": true,
}

type runtimeHooks struct {
	permissionPolicy tool.PermissionPolicy
	sandboxRunner    sandboxRunner
	skillRoot        string
	reviewStore      reviewStore
	taskID           string
}

type codeReviewSkill struct {
	Name   string
	Digest string
	Dir    string
	Root   string
}

type commandKind string

type commandSpec struct {
	Kind           commandKind
	Executable     string
	Args           []string
	Cwd            string
	Env            map[string]string
	Inputs         []inputMapping
	TimeoutSeconds int
}

type inputMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
	Mode string `json:"mode,omitempty"`
}

type governanceDecision struct {
	Command  string `json:"command"`
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

type sandboxRun struct {
	Runtime    string   `json:"runtime"`
	Command    string   `json:"command"`
	ExitCode   int      `json:"exit_code"`
	Stdout     string   `json:"stdout,omitempty"`
	Stderr     string   `json:"stderr,omitempty"`
	TimedOut   bool     `json:"timed_out"`
	DurationMS int64    `json:"duration_ms"`
	Error      string   `json:"error,omitempty"`
	Skipped    bool     `json:"skipped,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

type governanceResult struct {
	SkillName           string
	SkillDigest         string
	CommandsPlanned     int
	CommandsAllowed     int
	CommandsBlocked     int
	PermissionBlocks    int
	FilterDecisions     []governanceDecision
	PermissionDecisions []governanceDecision
	SandboxRuns         []sandboxRun
	Warnings            []ruleMatch
	Redactions          int
}

type sandboxRunner interface {
	RunSandboxCommand(ctx context.Context, spec commandSpec) sandboxRun
}

type fakeSandboxRunner struct {
	fixture string
}

type skillRunPermissionArguments struct {
	Skill   string            `json:"skill"`
	Command string            `json:"command"`
	Cwd     string            `json:"cwd,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Inputs  []inputMapping    `json:"inputs,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
}

func runGovernance(
	ctx context.Context,
	cfg config,
	input reviewInput,
	parsed parsedDiff,
	hooks runtimeHooks,
) (governanceResult, error) {
	meta, err := loadCodeReviewSkill(hooks.skillRoot)
	if err != nil {
		return governanceResult{}, fmt.Errorf("load code-review skill: %w", err)
	}
	result := governanceResult{
		SkillName:   meta.Name,
		SkillDigest: meta.Digest,
	}

	specs := planCommands(cfg, input, parsed)
	result.CommandsPlanned = len(specs)

	policy := hooks.permissionPolicy
	if policy == nil {
		policy = tool.PermissionPolicyFunc(nil)
	}
	runner := hooks.sandboxRunner
	ownsRunner := false
	var preflightFailed bool
	var preflightReason string
	if runner == nil {
		var createErr error
		runner, createErr = newConfiguredSandboxRunner(ctx, cfg, meta)
		ownsRunner = createErr == nil
		if createErr != nil {
			preflightFailed = true
			preflightReason = createErr.Error()
			result.addGovernanceWarning(
				"sandbox",
				commandSpec{Kind: commandCheckGoVersion},
				ruleSandboxPreflightFailed,
				"Sandbox runtime preflight failed",
				preflightReason,
			)
		}
	}
	for _, spec := range specs {
		filterDecision := gateCommand(spec)
		result.addFilterDecision(filterDecision)
		if filterDecision.Decision != governanceDecisionAllow {
			result.CommandsBlocked++
			result.addGovernanceWarning(
				"filter",
				spec,
				ruleGovernanceCommandBlocked,
				"Command was blocked by the command gate",
				filterDecision.Reason,
			)
			continue
		}

		permissionDecision := checkCommandPermission(ctx, policy, meta.Name, spec)
		result.addPermissionDecision(permissionDecision)
		if permissionDecision.Decision != governanceDecisionAllow {
			result.PermissionBlocks++
			result.addGovernanceWarning(
				"permission",
				spec,
				ruleGovernancePermission,
				"Command requires permission review",
				permissionDecision.Reason,
			)
			continue
		}

		result.CommandsAllowed++
		if runner == nil || preflightFailed && spec.Kind != commandCheckGoVersion {
			run := skippedSandboxRun(cfg.effectiveRuntime, spec, preflightReason)
			result.addSandboxRun(run)
			result.addSandboxWarning(spec, run)
			continue
		}

		run := runner.RunSandboxCommand(ctx, spec)
		result.addSandboxRun(run)
		if sandboxRunNeedsWarning(run) {
			result.addSandboxWarning(spec, run)
		}
		if spec.Kind == commandCheckGoVersion && sandboxRunFailed(run) {
			preflightFailed = true
			preflightReason = sandboxRunFailureReason(run)
		}
	}
	if ownsRunner {
		closer, ok := runner.(interface{ Close() error })
		if ok {
			if err := closer.Close(); err != nil {
				result.addGovernanceWarning(
					"sandbox",
					commandSpec{Kind: commandKind("runtimeClose")},
					ruleSandboxRunFailed,
					"Sandbox runtime cleanup failed",
					err.Error(),
				)
			}
		}
	}
	return result, nil
}

func (r *governanceResult) addFilterDecision(decision governanceDecision) {
	sanitized, redactions := sanitizeGovernanceDecision(decision)
	r.Redactions += redactions
	r.FilterDecisions = append(r.FilterDecisions, sanitized)
}

func (r *governanceResult) addPermissionDecision(decision governanceDecision) {
	sanitized, redactions := sanitizeGovernanceDecision(decision)
	r.Redactions += redactions
	r.PermissionDecisions = append(r.PermissionDecisions, sanitized)
}

func (r *governanceResult) addSandboxRun(run sandboxRun) {
	sanitized, redactions := sanitizeSandboxRun(run)
	r.Redactions += redactions
	r.SandboxRuns = append(r.SandboxRuns, sanitized)
}

func (r *governanceResult) addSandboxWarning(spec commandSpec, run sandboxRun) {
	ruleID := ruleSandboxRunFailed
	title := "Sandbox command failed"
	if run.Skipped {
		ruleID = ruleSandboxRunSkipped
		title = "Sandbox command was skipped"
	}
	r.addGovernanceWarning(
		"sandbox",
		spec,
		ruleID,
		title,
		sandboxRunFailureReason(run),
	)
}

func (r *governanceResult) addGovernanceWarning(
	stage string,
	spec commandSpec,
	ruleID string,
	title string,
	reason string,
) {
	redacted := redactText(reason)
	r.Redactions += redacted.Count
	r.Warnings = append(r.Warnings, governanceWarning(stage, spec, ruleID, title, redacted.Text))
}

func loadCodeReviewSkill(rootOverride string) (codeReviewSkill, error) {
	var lastErr error
	for _, root := range skillRootCandidates(rootOverride) {
		repo, err := rootskill.NewFSRepository(root)
		if err != nil {
			lastErr = err
			continue
		}
		loaded, err := repo.Get(codeReviewSkillName)
		if err != nil {
			lastErr = err
			continue
		}
		dir, err := repo.Path(codeReviewSkillName)
		if err != nil {
			lastErr = err
			continue
		}
		digest, err := digestCodeReviewSkill(dir)
		if err != nil {
			lastErr = err
			continue
		}
		name := strings.TrimSpace(loaded.Summary.Name)
		if name == "" {
			name = codeReviewSkillName
		}
		return codeReviewSkill{Name: name, Digest: digest, Dir: dir, Root: root}, nil
	}
	if lastErr != nil {
		return codeReviewSkill{}, lastErr
	}
	return codeReviewSkill{}, fmt.Errorf("skill %q not found", codeReviewSkillName)
}

func skillRootCandidates(rootOverride string) []string {
	if strings.TrimSpace(rootOverride) != "" {
		return []string{rootOverride}
	}
	candidates := []string{
		filepath.Join("skills"),
		filepath.Join("code_review_agent", "skills"),
	}
	_, file, _, ok := runtime.Caller(0)
	if ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(file), "skills"))
	}
	return candidates
}

func digestCodeReviewSkill(skillDir string) (string, error) {
	required := []string{
		"SKILL.md",
		"rules.md",
		filepath.Join("scripts", "run_checks.sh"),
	}
	hash := sha256.New()
	for _, rel := range required {
		data, err := os.ReadFile(filepath.Join(skillDir, rel))
		if err != nil {
			return "", err
		}
		hash.Write([]byte(filepath.ToSlash(rel)))
		hash.Write([]byte{0})
		hash.Write(data)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func planCommands(cfg config, input reviewInput, parsed parsedDiff) []commandSpec {
	inputs := commandInputs(input)
	env := commandEnv(input)
	commands := []commandSpec{
		newCommandSpec(commandCheckGoVersion, inputs, env),
	}
	if hasReviewableGoChange(parsed) {
		commands = append(commands,
			newCommandSpec(commandCheckGoTest, inputs, env),
			newCommandSpec(commandCheckGoVet, inputs, env),
		)
		if cfg.enableStaticcheck {
			commands = append(commands, newCommandSpec(commandCheckStaticcheck, inputs, env))
		}
	}
	return commands
}

func newCommandSpec(kind commandKind, inputs []inputMapping, env map[string]string) commandSpec {
	spec := commandSpec{
		Kind:           kind,
		Cwd:            ".",
		Env:            cloneStringMap(env),
		Inputs:         cloneInputMappings(inputs),
		TimeoutSeconds: defaultCommandTimeoutSeconds,
	}
	switch kind {
	case commandCheckGoVersion:
		spec.Executable = "go"
		spec.Args = []string{"version"}
	case commandCheckGoTest:
		spec.Executable = "bash"
		spec.Args = []string{"scripts/run_checks.sh", "test"}
	case commandCheckGoVet:
		spec.Executable = "bash"
		spec.Args = []string{"scripts/run_checks.sh", "vet"}
	case commandCheckStaticcheck:
		spec.Executable = "bash"
		spec.Args = []string{"scripts/run_checks.sh", "staticcheck"}
	default:
		spec.Executable = ""
	}
	return spec
}

func commandInputs(input reviewInput) []inputMapping {
	if input.kind != inputKindRepoPath || strings.TrimSpace(input.repoRoot) == "" {
		return nil
	}
	repoRoot, err := filepath.Abs(input.repoRoot)
	if err != nil {
		repoRoot = input.repoRoot
	}
	return []inputMapping{{
		From: "host://" + filepath.ToSlash(repoRoot),
		To:   "work/repo",
		Mode: "copy",
	}}
}

func commandEnv(input reviewInput) map[string]string {
	if input.kind != inputKindRepoPath || strings.TrimSpace(input.repoRoot) == "" {
		return nil
	}
	return map[string]string{"REVIEW_REPO_DIR": "../../work/repo"}
}

func hasReviewableGoChange(parsed parsedDiff) bool {
	for _, file := range parsed.Files {
		if file.isGoFile() && !file.IsDeleted && !file.IsBinary {
			return true
		}
	}
	return false
}

func gateCommand(spec commandSpec) governanceDecision {
	if !isKnownCommandKind(spec.Kind) {
		return denyFilterDecision(spec.Kind, "unknown command kind")
	}
	expected := newCommandSpec(spec.Kind, nil, nil)
	if spec.Executable != expected.Executable || !stringSlicesEqual(spec.Args, expected.Args) {
		return denyFilterDecision(spec.Kind, "command executable or arguments do not match the allowlist")
	}
	if err := validateCommandCWD(spec.Cwd); err != nil {
		return denyFilterDecision(spec.Kind, err.Error())
	}
	if err := validateCommandEnv(spec.Env); err != nil {
		return denyFilterDecision(spec.Kind, err.Error())
	}
	if err := validateCommandInputs(spec.Inputs); err != nil {
		return denyFilterDecision(spec.Kind, err.Error())
	}
	return governanceDecision{Command: string(spec.Kind), Decision: governanceDecisionAllow}
}

func isKnownCommandKind(kind commandKind) bool {
	switch kind {
	case commandCheckGoVersion, commandCheckGoTest, commandCheckGoVet, commandCheckStaticcheck:
		return true
	default:
		return false
	}
}

func denyFilterDecision(kind commandKind, reason string) governanceDecision {
	return governanceDecision{
		Command:  string(kind),
		Decision: governanceDecisionDeny,
		Reason:   reason,
	}
}

func validateCommandCWD(cwd string) error {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || cwd == "." {
		return nil
	}
	normalized := strings.ReplaceAll(cwd, "\\", "/")
	if strings.ContainsRune(normalized, '\x00') {
		return fmt.Errorf("cwd contains a NUL byte")
	}
	if strings.HasPrefix(normalized, "/") || strings.HasPrefix(normalized, "//") ||
		hasWindowsDrive(normalized) {
		return fmt.Errorf("cwd must be workspace-relative")
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("cwd escapes the workspace")
	}
	return fmt.Errorf("cwd %q is not allowed", cwd)
}

func validateCommandEnv(env map[string]string) error {
	for key, value := range env {
		if !commandEnvAllowlist[key] {
			return fmt.Errorf("env key %q is not allowed", key)
		}
		if strings.ContainsRune(value, '\x00') || strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("env value for %q contains an unsafe character", key)
		}
	}
	return nil
}

func validateCommandInputs(inputs []inputMapping) error {
	if len(inputs) == 0 {
		return nil
	}
	if len(inputs) != 1 {
		return fmt.Errorf("only one repository input mapping is allowed")
	}
	input := inputs[0]
	if input.To != "work/repo" {
		return fmt.Errorf("input target %q is not allowed", input.To)
	}
	if input.Mode != "" && input.Mode != "copy" {
		return fmt.Errorf("input mode %q is not allowed", input.Mode)
	}
	if !strings.HasPrefix(input.From, "host://") {
		return fmt.Errorf("input source must be a host repository mapping")
	}
	hostPath := filepath.FromSlash(strings.TrimPrefix(input.From, "host://"))
	if strings.ContainsRune(hostPath, '\x00') || strings.ContainsAny(hostPath, "\r\n") {
		return fmt.Errorf("input source contains an unsafe character")
	}
	if !filepath.IsAbs(hostPath) {
		return fmt.Errorf("input source must be absolute")
	}
	return nil
}

func checkCommandPermission(
	ctx context.Context,
	policy tool.PermissionPolicy,
	skillName string,
	spec commandSpec,
) governanceDecision {
	args, err := permissionArguments(skillName, spec)
	if err != nil {
		return governanceDecision{
			Command:  string(spec.Kind),
			Decision: governanceDecisionError,
			Reason:   err.Error(),
		}
	}
	decision, err := policy.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName:  toolNameSkillRun,
		Arguments: args,
	})
	if err != nil {
		return governanceDecision{
			Command:  string(spec.Kind),
			Decision: governanceDecisionError,
			Reason:   err.Error(),
		}
	}
	normalized, err := tool.NormalizePermissionDecision(decision)
	if err != nil {
		return governanceDecision{
			Command:  string(spec.Kind),
			Decision: governanceDecisionError,
			Reason:   err.Error(),
		}
	}
	return governanceDecision{
		Command:  string(spec.Kind),
		Decision: string(normalized.Action),
		Reason:   normalized.Reason,
	}
}

func permissionArguments(skillName string, spec commandSpec) ([]byte, error) {
	cwd := strings.TrimSpace(spec.Cwd)
	if cwd == "." {
		cwd = ""
	}
	return json.Marshal(skillRunPermissionArguments{
		Skill:   skillName,
		Command: spec.commandString(),
		Cwd:     cwd,
		Env:     cloneStringMap(spec.Env),
		Inputs:  cloneInputMappings(spec.Inputs),
		Timeout: spec.TimeoutSeconds,
	})
}

func (s commandSpec) commandString() string {
	parts := make([]string, 0, len(s.Args)+1)
	parts = append(parts, s.Executable)
	parts = append(parts, s.Args...)
	return strings.Join(parts, " ")
}

func governanceWarning(
	stage string,
	spec commandSpec,
	ruleID string,
	title string,
	reason string,
) ruleMatch {
	if strings.TrimSpace(reason) == "" {
		reason = "no reason provided"
	}
	return ruleMatch{
		Severity:       "low",
		Category:       "governance",
		File:           fmt.Sprintf("<governance>/%s/%s", stage, spec.Kind),
		Line:           0,
		Title:          title,
		Evidence:       reason,
		Recommendation: "Review the command governance decision before re-running the sandbox command.",
		Confidence:     confidenceWarning,
		Source:         stage,
		RuleID:         ruleID,
	}
}

func sanitizeGovernanceDecision(decision governanceDecision) (governanceDecision, int) {
	redacted := redactText(decision.Reason)
	decision.Reason = redacted.Text
	return decision, redacted.Count
}

func sanitizeSandboxRun(run sandboxRun) (sandboxRun, int) {
	stdout := redactText(run.Stdout)
	stderr := redactText(run.Stderr)
	errText := redactText(run.Error)
	run.Stdout = stdout.Text
	run.Stderr = stderr.Text
	run.Error = errText.Text
	total := stdout.Count + stderr.Count + errText.Count
	for i := range run.Warnings {
		warning := redactText(run.Warnings[i])
		run.Warnings[i] = warning.Text
		total += warning.Count
	}
	return run, total
}

func (r fakeSandboxRunner) RunSandboxCommand(_ context.Context, spec commandSpec) sandboxRun {
	run := sandboxRun{
		Runtime:    runtimeFake,
		Command:    string(spec.Kind),
		ExitCode:   0,
		TimedOut:   false,
		DurationMS: 1,
	}
	if r.fixture == "sandbox_failure" && spec.Kind == commandCheckGoVersion {
		run.ExitCode = 127
		run.Stderr = "go: command not found"
		return run
	}
	switch spec.Kind {
	case commandCheckGoVersion:
		run.Stdout = "go version fake"
	case commandCheckGoTest:
		run.Stdout = "ok"
	case commandCheckGoVet:
		run.Stdout = "ok"
	case commandCheckStaticcheck:
		run.Stdout = "ok"
	default:
		run.ExitCode = -1
		run.Stderr = "unknown command"
	}
	return run
}

func cloneInputMappings(inputs []inputMapping) []inputMapping {
	if len(inputs) == 0 {
		return nil
	}
	cloned := make([]inputMapping, len(inputs))
	copy(cloned, inputs)
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func stringSlicesEqual(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
