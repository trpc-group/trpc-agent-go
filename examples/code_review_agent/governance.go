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
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

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

	ruleGovernanceCommandBlocked   = "governance.command_blocked"
	ruleGovernancePermission       = "governance.permission_error"
	ruleSandboxPreflightFailed     = "sandbox.preflight_failed"
	ruleSandboxSnapshotUnavailable = "sandbox.snapshot_unavailable"
	ruleSandboxRunFailed           = "sandbox.run_failed"
	ruleSandboxRunSkipped          = "sandbox.run_skipped"

	toolNameSkillRun       = "skill_run"
	reviewRepoInputTarget  = "work/repo/"
	reviewRepoDirFromSkill = "../../work/repo"

	defaultCommandTimeoutSeconds = 60
	goTestSkippedReason          = "go test is disabled by default because the configured runtime has no verified outbound network boundary; pass --skip-go-test=false only for trusted code or a network-isolated sandbox"
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

var codeReviewSkillRequiredFiles = []string{
	"SKILL.md",
	"rules.md",
	path.Join("scripts", "run_checks.sh"),
}

//go:embed skills/code-review/SKILL.md skills/code-review/rules.md skills/code-review/scripts/run_checks.sh
var embeddedCodeReviewSkill embed.FS

type codeReviewSkillLoader func() (codeReviewSkill, error)

type sandboxRunnerFactory func(context.Context) (sandboxRunner, error)

type runtimeHooks struct {
	permissionPolicy       tool.PermissionPolicy
	sandboxRunner          sandboxRunner
	sandboxRunnerFactory   sandboxRunnerFactory
	skillLoader            codeReviewSkillLoader
	snapshotLimitsOverride *snapshotLimits
	reviewStore            reviewStore
	taskID                 string
	now                    func() time.Time
}

type codeReviewSkill struct {
	Name       string
	Digest     string
	Dir        string
	Repository rootskill.Repository
	cleanup    func() error
}

func (s codeReviewSkill) Close() error {
	if s.cleanup == nil {
		return nil
	}
	return s.cleanup()
}

type commandKind string

type commandSpec struct {
	Kind              commandKind
	Executable        string
	Args              []string
	Cwd               string
	Env               map[string]string
	Inputs            []inputMapping
	DiagnosticModules map[string]string
	TimeoutSeconds    int
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
	Matches             []ruleMatch
	ToolCalls           int
	Redactions          int
}

type sandboxRunner interface {
	RunSandboxCommand(ctx context.Context, spec commandSpec) sandboxRun
}

type fakeSandboxRunner struct {
	runs map[commandKind]sandboxRun
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
	loader := hooks.skillLoader
	if loader == nil {
		loader = loadCodeReviewSkill
	}
	meta, err := loader()
	if err != nil {
		return governanceResult{}, fmt.Errorf("load code-review skill: %w", err)
	}
	defer func() { _ = meta.Close() }()
	result := governanceResult{
		SkillName:   meta.Name,
		SkillDigest: meta.Digest,
	}

	plannedInput := input
	shouldRunRepoChecks := shouldRunRepositoryChecks(input, parsed)
	if requiresGoRepositoryValidation(parsed) && !shouldRunRepoChecks {
		result.addGovernanceWarning(
			"sandbox",
			commandSpec{Kind: commandCheckGoTest},
			ruleSandboxSnapshotUnavailable,
			"Repository validation is unavailable",
			"Go source or module metadata changes require a complete repository snapshot",
		)
	}
	if shouldRunRepoChecks && plannedInput.sandboxRepoRoot == "" {
		if len(input.repoFiles) > 0 {
			result.addGovernanceWarning(
				"sandbox",
				commandSpec{Kind: commandCheckGoTest},
				ruleSandboxSnapshotUnavailable,
				"Repository checks require a complete snapshot",
				"scoped --files reviews do not upload the complete module needed for repository checks",
			)
		} else {
			snapshotCtx, cancel := context.WithTimeout(ctx, gitDiffTimeout)
			limits := defaultSandboxSnapshotLimits()
			if hooks.snapshotLimitsOverride != nil {
				limits = *hooks.snapshotLimitsOverride
			}
			snapshot, snapshotErr := prepareSandboxRepoSnapshot(
				snapshotCtx,
				input.repoRoot,
				nil,
				limits,
			)
			manifestReady := false
			if snapshotErr == nil {
				var manifest sandboxModuleManifest
				manifest, snapshotErr = prepareAffectedModuleManifest(snapshotCtx, snapshot.Root, parsed)
				if snapshotErr == nil && len(manifest.ModulesByToken) > 0 {
					plannedInput.sandboxDiagnosticModules = cloneStringMap(manifest.ModulesByToken)
					manifestReady = true
				}
			}
			cancel()
			if snapshotErr != nil {
				if snapshot.Root != "" {
					_ = os.RemoveAll(snapshot.Root)
				}
				result.addGovernanceWarning(
					"sandbox",
					commandSpec{Kind: commandCheckGoTest},
					ruleSandboxSnapshotUnavailable,
					"Repository snapshot is unavailable",
					snapshotErr.Error(),
				)
			} else if manifestReady {
				plannedInput.sandboxRepoRoot = snapshot.Root
				defer os.RemoveAll(snapshot.Root)
			} else if snapshot.Root != "" {
				_ = os.RemoveAll(snapshot.Root)
			}
		}
	}

	specs := planCommands(cfg, plannedInput, parsed)
	result.CommandsPlanned = len(specs)

	policy := hooks.permissionPolicy
	if policy == nil {
		policy = tool.PermissionPolicyFunc(nil)
	}
	runnerFactory := hooks.sandboxRunnerFactory
	if runnerFactory == nil && hooks.sandboxRunner == nil {
		runnerFactory = func(runCtx context.Context) (sandboxRunner, error) {
			return newConfiguredSandboxRunner(runCtx, cfg, meta, plannedInput)
		}
	}
	var preflightFailed bool
	var preflightReason string
	for _, spec := range specs {
		if spec.Kind == commandCheckGoTest && cfg.skipGoTest {
			run := intentionallySkippedSandboxRun(
				cfg.effectiveRuntime,
				spec,
				goTestSkippedReason,
			)
			result.addSandboxRun(run)
			result.addSandboxWarning(spec, run)
			continue
		}
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
			if spec.Kind == commandCheckGoVersion {
				preflightFailed = true
				preflightReason = filterDecision.Reason
			}
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
			if spec.Kind == commandCheckGoVersion {
				preflightFailed = true
				preflightReason = permissionDecision.Reason
			}
			continue
		}

		result.CommandsAllowed++
		if preflightFailed && spec.Kind != commandCheckGoVersion {
			run := skippedSandboxRun(cfg.effectiveRuntime, spec, preflightReason)
			result.addSandboxRun(run)
			result.addSandboxWarning(spec, run)
			continue
		}

		runner := hooks.sandboxRunner
		ownsRunner := false
		if runner == nil {
			var createErr error
			runner, createErr = runnerFactory(ctx)
			if createErr != nil || runner == nil {
				if createErr == nil {
					createErr = fmt.Errorf("sandbox runner factory returned nil")
				}
				reason := fmt.Sprintf("create isolated sandbox: %v", createErr)
				run := skippedSandboxRun(cfg.effectiveRuntime, spec, reason)
				result.addSandboxRun(run)
				result.addSandboxWarning(spec, run)
				if spec.Kind == commandCheckGoVersion {
					preflightFailed = true
					preflightReason = reason
					result.addGovernanceWarning(
						"sandbox",
						spec,
						ruleSandboxPreflightFailed,
						"Sandbox runtime preflight failed",
						reason,
					)
				}
				continue
			}
			ownsRunner = true
		}

		result.ToolCalls++
		run := runner.RunSandboxCommand(ctx, spec)
		if ownsRunner {
			if closer, ok := runner.(interface{ Close() error }); ok {
				if err := closer.Close(); err != nil {
					run.Warnings = append(
						run.Warnings,
						fmt.Sprintf("sandbox cleanup failed: %v", err),
					)
				}
			}
		}
		run = result.addSandboxRun(run)
		if sandboxRunNeedsWarning(run) {
			diagnostics := parseSandboxDiagnostics(spec, run, parsed)
			result.Matches = append(result.Matches, diagnostics.Matches...)
			if sandboxDiagnosticsNeedGenericWarning(spec, run, diagnostics) {
				result.addSandboxWarning(spec, run)
			}
		}
		if spec.Kind == commandCheckGoVersion && sandboxRunFailed(run) {
			preflightFailed = true
			preflightReason = sandboxRunFailureReason(run)
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

func (r *governanceResult) addSandboxRun(run sandboxRun) sandboxRun {
	sanitized, redactions := sanitizeSandboxRun(run)
	r.Redactions += redactions
	r.SandboxRuns = append(r.SandboxRuns, sanitized)
	return sanitized
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
		sandboxWarningReason(spec, run),
	)
}

func sandboxWarningReason(spec commandSpec, run sandboxRun) string {
	if spec.Kind == commandCheckGoTest {
		return sandboxRunStatusReason(run)
	}
	return sandboxRunFailureReason(run)
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
	r.Matches = append(r.Matches, governanceWarning(stage, spec, ruleID, title, redacted.Text))
}

func loadCodeReviewSkill() (codeReviewSkill, error) {
	expectedDigest, err := digestEmbeddedCodeReviewSkill()
	if err != nil {
		return codeReviewSkill{}, err
	}
	root, err := materializeEmbeddedCodeReviewSkill()
	if err != nil {
		return codeReviewSkill{}, err
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(root)
		}
	}()

	loaded, err := loadCodeReviewSkillFromRoot(root)
	if err != nil {
		return codeReviewSkill{}, err
	}
	if loaded.Digest != expectedDigest {
		return codeReviewSkill{}, fmt.Errorf(
			"materialized code-review skill digest %q does not match embedded digest %q",
			loaded.Digest,
			expectedDigest,
		)
	}
	loaded.cleanup = func() error { return os.RemoveAll(root) }
	cleanupOnError = false
	return loaded, nil
}

func loadCodeReviewSkillFromRoot(root string) (codeReviewSkill, error) {
	repo, err := rootskill.NewFSRepository(root)
	if err != nil {
		return codeReviewSkill{}, err
	}
	loaded, err := repo.Get(codeReviewSkillName)
	if err != nil {
		return codeReviewSkill{}, err
	}
	name := loaded.Summary.Name
	if name != codeReviewSkillName {
		return codeReviewSkill{}, fmt.Errorf(
			"skill name %q does not match %q",
			name,
			codeReviewSkillName,
		)
	}
	dir, err := repo.Path(codeReviewSkillName)
	if err != nil {
		return codeReviewSkill{}, err
	}
	digest, err := digestCodeReviewSkill(dir)
	if err != nil {
		return codeReviewSkill{}, err
	}
	return codeReviewSkill{
		Name:       name,
		Digest:     digest,
		Dir:        dir,
		Repository: repo,
	}, nil
}

func materializeEmbeddedCodeReviewSkill() (string, error) {
	root, err := os.MkdirTemp("", "code-review-skill-*")
	if err != nil {
		return "", fmt.Errorf("create embedded skill root: %w", err)
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(root)
		}
	}()
	for _, rel := range codeReviewSkillRequiredFiles {
		embeddedPath := path.Join("skills", codeReviewSkillName, rel)
		data, err := embeddedCodeReviewSkill.ReadFile(embeddedPath)
		if err != nil {
			return "", fmt.Errorf("read embedded skill file %q: %w", rel, err)
		}
		dst := filepath.Join(root, codeReviewSkillName, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return "", fmt.Errorf("create embedded skill directory: %w", err)
		}
		if err := os.WriteFile(dst, data, 0o600); err != nil {
			return "", fmt.Errorf("write embedded skill file %q: %w", rel, err)
		}
	}
	cleanupOnError = false
	return root, nil
}

func digestEmbeddedCodeReviewSkill() (string, error) {
	hash := sha256.New()
	for _, rel := range codeReviewSkillRequiredFiles {
		data, err := embeddedCodeReviewSkill.ReadFile(path.Join("skills", codeReviewSkillName, rel))
		if err != nil {
			return "", fmt.Errorf("read embedded skill file %q: %w", rel, err)
		}
		writeCodeReviewSkillDigestEntry(hash, rel, data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func digestCodeReviewSkill(skillDir string) (string, error) {
	hash := sha256.New()
	for _, rel := range codeReviewSkillRequiredFiles {
		data, err := os.ReadFile(filepath.Join(skillDir, filepath.FromSlash(rel)))
		if err != nil {
			return "", err
		}
		writeCodeReviewSkillDigestEntry(hash, rel, data)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeCodeReviewSkillDigestEntry(hash interface{ Write([]byte) (int, error) }, rel string, data []byte) {
	_, _ = hash.Write([]byte(path.Clean(filepath.ToSlash(rel))))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(data)
	_, _ = hash.Write([]byte{0})
}

func planCommands(cfg config, input reviewInput, parsed parsedDiff) []commandSpec {
	commands := []commandSpec{
		newCommandSpec(commandCheckGoVersion, nil, nil),
	}
	if !shouldRunRepositoryChecks(input, parsed) || input.sandboxRepoRoot == "" ||
		len(input.sandboxDiagnosticModules) == 0 {
		return commands
	}

	inputs := commandInputs(input)
	env := commandEnv(input)
	testSpec := newCommandSpec(commandCheckGoTest, inputs, env)
	testSpec.DiagnosticModules = cloneStringMap(input.sandboxDiagnosticModules)
	vetSpec := newCommandSpec(commandCheckGoVet, inputs, env)
	vetSpec.DiagnosticModules = cloneStringMap(input.sandboxDiagnosticModules)
	commands = append(commands, testSpec, vetSpec)
	if cfg.enableStaticcheck {
		staticcheckSpec := newCommandSpec(commandCheckStaticcheck, inputs, env)
		staticcheckSpec.DiagnosticModules = cloneStringMap(input.sandboxDiagnosticModules)
		commands = append(commands, staticcheckSpec)
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
	if input.kind != inputKindRepoPath || input.sandboxRepoRoot == "" {
		return nil
	}
	repoRoot, err := filepath.Abs(input.sandboxRepoRoot)
	if err != nil {
		repoRoot = input.sandboxRepoRoot
	}
	return []inputMapping{{
		From: "host://" + filepath.ToSlash(repoRoot),
		To:   reviewRepoInputTarget,
		Mode: "copy",
	}}
}

func commandEnv(input reviewInput) map[string]string {
	if input.kind != inputKindRepoPath || input.sandboxRepoRoot == "" {
		return nil
	}
	return map[string]string{"REVIEW_REPO_DIR": reviewRepoDirFromSkill}
}

func shouldRunRepositoryChecks(input reviewInput, parsed parsedDiff) bool {
	return input.kind == inputKindRepoPath &&
		input.repoRoot != "" &&
		hasRepositoryValidationChange(parsed)
}

func hasRepositoryValidationChange(parsed parsedDiff) bool {
	return len(repositoryValidationTargets(parsed)) > 0
}

func requiresGoRepositoryValidation(parsed parsedDiff) bool {
	for _, file := range parsed.Files {
		oldIsGo := isGoSourcePath(file.OldPath)
		newIsGo := isGoSourcePath(file.NewPath)
		if isGoRepositoryMetadataPath(file.OldPath) ||
			isGoRepositoryMetadataPath(file.NewPath) {
			return true
		}
		if (file.IsBinary && (oldIsGo || newIsGo)) ||
			(file.IsDeleted && oldIsGo) ||
			(file.IsRename && (oldIsGo || newIsGo)) {
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
	if err := validateCommandDiagnosticModules(spec.Kind, spec.DiagnosticModules); err != nil {
		return denyFilterDecision(spec.Kind, err.Error())
	}
	return governanceDecision{Command: string(spec.Kind), Decision: governanceDecisionAllow}
}

func validateCommandDiagnosticModules(kind commandKind, modules map[string]string) error {
	if kind == commandCheckGoVersion {
		if len(modules) != 0 {
			return fmt.Errorf("go version command must not carry diagnostic modules")
		}
		return nil
	}
	if len(modules) == 0 {
		return fmt.Errorf("repository command requires diagnostic modules")
	}
	seenModules := make(map[string]bool, len(modules))
	for token, module := range modules {
		if !isValidSandboxModuleToken(token) {
			return fmt.Errorf("diagnostic module token is invalid")
		}
		if !isSafeSandboxModulePath(module) {
			return fmt.Errorf("diagnostic module path %q is invalid", module)
		}
		if seenModules[module] {
			return fmt.Errorf("diagnostic module path %q is duplicated", module)
		}
		seenModules[module] = true
	}
	return nil
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
	if input.To != reviewRepoInputTarget {
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

func newFakeSandboxRunner(fixture *fixtureItem) fakeSandboxRunner {
	runner := fakeSandboxRunner{runs: map[commandKind]sandboxRun{}}
	if fixture == nil {
		return runner
	}
	for kind, configured := range map[commandKind]*fixtureSandboxRun{
		commandCheckGoVersion:   fixture.FakeSandbox.GoVersion,
		commandCheckGoTest:      fixture.FakeSandbox.Test,
		commandCheckGoVet:       fixture.FakeSandbox.Vet,
		commandCheckStaticcheck: fixture.FakeSandbox.Staticcheck,
	} {
		if configured == nil {
			continue
		}
		runner.runs[kind] = sandboxRun{
			Runtime:    runtimeFake,
			Command:    string(kind),
			ExitCode:   configured.ExitCode,
			Stdout:     configured.Stdout,
			Stderr:     configured.Stderr,
			TimedOut:   configured.TimedOut,
			DurationMS: configured.DurationMS,
			Error:      configured.Error,
			Skipped:    configured.Skipped,
			Warnings:   append([]string(nil), configured.Warnings...),
		}
	}
	return runner
}

func (r fakeSandboxRunner) RunSandboxCommand(_ context.Context, spec commandSpec) sandboxRun {
	if configured, ok := r.runs[spec.Kind]; ok {
		configured.Runtime = runtimeFake
		configured.Command = string(spec.Kind)
		configured.Warnings = append([]string(nil), configured.Warnings...)
		return configured
	}
	run := sandboxRun{
		Runtime:    runtimeFake,
		Command:    string(spec.Kind),
		ExitCode:   0,
		TimedOut:   false,
		DurationMS: 1,
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
