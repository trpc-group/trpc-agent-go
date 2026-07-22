//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package governance fail-closes every sandbox check before execution.
package governance

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var resultNamePattern = regexp.MustCompile(`^result-[a-f0-9]{16}\.json$`)
var targetWorkspacePattern = regexp.MustCompile(`^/tmp/run/ws_cr-[a-f0-9]{16}_[0-9]+$`)

var fixedArgs = map[string][]string{
	"go-test": {"go", "test", "-mod=readonly", "./..."},
	"go-vet":  {"go", "vet", "-mod=readonly", "./..."},
}

var fixedEnv = map[string]string{
	"PATH": "/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin", "HOME": "/tmp/cr-target/home",
	"GOCACHE": "/tmp/cr-target/gocache", "GOMODCACHE": "/tmp/cr-target/gomodcache", "TMPDIR": "/tmp/cr-target/tmp",
	"GOMAXPROCS": "2",
}

var localEnvKeys = []string{"PATH", "HOME", "GOCACHE", "GOMODCACHE", "GOTMPDIR", "TMPDIR",
	"TMP", "TEMP", "GOMAXPROCS", "GOPROXY", "GOSUMDB", "GOENV", "GOWORK", "CR_REPO_DIR", "SYSTEMROOT"}

// CheckSpec is the complete immutable input to safety and permission checks.
type CheckSpec struct {
	ID, Runtime, RunnerPath, SkillRoot, Cwd, Artifact string
	RepoSource                                        string
	Argv                                              []string
	Env                                               map[string]string
	Timeout                                           time.Duration
	Network, Privileged, HostWrite                    bool
}

// Decision is persistable governance evidence.
type Decision struct {
	Stage, CheckID, Risk, Action, Reason string
	ArgsDigest                           string
	At                                   time.Time
}

// Recorder persists a decision before execution may continue.
type Recorder interface {
	SaveDecision(context.Context, Decision) error
}

// Authorizer enforces filter -> permission -> durable decision.
type Authorizer struct {
	Policy   tool.PermissionPolicy
	Recorder Recorder
}

type decisionEvidence struct{ stage, risk, action, reason string }

// Authorize returns nil only when execution is permitted.
func (a Authorizer) Authorize(ctx context.Context, spec CheckSpec) error {
	risk, err := validate(spec)
	if err != nil {
		return a.recordDenied(ctx, spec, decisionEvidence{"filter", "critical", "deny", err.Error()})
	}
	if err := a.save(ctx, decision(spec, decisionEvidence{"filter", risk, "allow", "fixed check passed safety filter"})); err != nil {
		return fmt.Errorf("persist filter decision: %w", err)
	}
	if a.Policy == nil {
		return a.recordDenied(ctx, spec, decisionEvidence{"permission", risk, "deny", "nil permission policy"})
	}
	arguments, err := json.Marshal(map[string]any{"check_id": spec.ID, "runtime": spec.Runtime, "argv": spec.Argv})
	if err != nil {
		return err
	}
	readOnly := reviewTool{declaration: &tool.Declaration{Name: "code_review_check", Description: "Run one fixed code review check", InputSchema: &tool.Schema{Type: "object"}}}
	request := &tool.PermissionRequest{Tool: readOnly, ToolName: "code_review_check", ToolCallID: spec.ID, Declaration: readOnly.Declaration(), Arguments: arguments}
	result, policyErr := a.Policy.CheckToolPermission(ctx, request)
	if policyErr != nil {
		return a.recordDenied(ctx, spec, decisionEvidence{"permission", risk, "deny", "permission policy error: " + policyErr.Error()})
	}
	if result.Action != tool.PermissionActionAllow {
		reason := result.Reason
		action := "deny"
		if result.Action == tool.PermissionActionAsk {
			action = string(tool.PermissionActionAsk)
		}
		if result.Action == "" {
			reason = "empty permission action"
		}
		if result.Action != "" && result.Action != tool.PermissionActionDeny && result.Action != tool.PermissionActionAsk {
			reason = fmt.Sprintf("unknown permission action %q", result.Action)
		}
		return a.recordDenied(ctx, spec, decisionEvidence{"permission", risk, action, reason})
	}
	if err := a.save(ctx, decision(spec, decisionEvidence{"permission", risk, "allow", result.Reason})); err != nil {
		return fmt.Errorf("persist permission decision: %w", err)
	}
	return nil
}

func validate(spec CheckSpec) (string, error) {
	expected, ok := fixedArgs[spec.ID]
	if !ok {
		return "critical", fmt.Errorf("unknown check ID %q", spec.ID)
	}
	if risk, err := validateExecution(spec, expected); err != nil {
		return risk, err
	}
	if err := validateRunner(spec); err != nil {
		return "critical", err
	}
	if spec.Runtime == "local" {
		return "high", nil
	}
	if spec.ID == "go-vet" {
		return "low", nil
	}
	return "medium", nil
}

func validateExecution(spec CheckSpec, expected []string) (string, error) {
	if err := validateRuntimeCapabilities(spec); err != nil {
		return "critical", err
	}
	if !equalStrings(spec.Argv, expected) {
		return "critical", errors.New("argv differs from fixed check manifest")
	}
	if risk, err := validateCheckBounds(spec); err != nil {
		return risk, err
	}
	if err := validateRuntimeEnvironment(spec); err != nil {
		return "critical", err
	}
	return "", nil
}

func validateRuntimeCapabilities(spec CheckSpec) error {
	if spec.Runtime != "container" && spec.Runtime != "fake" && spec.Runtime != "local" {
		return errors.New("runtime capability is not allowed")
	}
	if spec.Privileged || ((spec.Network || spec.HostWrite) && spec.Runtime != "local") {
		return errors.New("privilege or unexpected host capabilities are forbidden")
	}
	if spec.Runtime == "local" && (!spec.Network || !spec.HostWrite) {
		return errors.New("local runtime must declare network and host writes")
	}
	return nil
}

func validateCheckBounds(spec CheckSpec) (string, error) {
	if spec.Cwd != "repo" {
		return "high", errors.New("check cwd must be staged repo")
	}
	if spec.Timeout <= 0 || spec.Timeout > 90*time.Second {
		return "high", errors.New("check timeout is outside allowed range")
	}
	if !resultNamePattern.MatchString(spec.Artifact) {
		return "high", errors.New("artifact name is not opaque and exact")
	}
	return "", nil
}

func validateRuntimeEnvironment(spec CheckSpec) error {
	if spec.Runtime == "local" {
		return validateLocalEnvironment(spec.Env, spec.RepoSource)
	}
	return validateEnvironment(spec.Env)
}

func validateLocalEnvironment(env map[string]string, repoSource string) error {
	if len(env) != len(localEnvKeys) {
		return errors.New("local environment does not contain the exact whitelist")
	}
	for _, key := range localEnvKeys {
		if _, ok := env[key]; !ok {
			return fmt.Errorf("local environment is missing %q", key)
		}
	}
	if env["PATH"] == "" || env["GOMAXPROCS"] != "2" || env["GOPROXY"] != "off" ||
		env["GOSUMDB"] != "off" || env["GOENV"] != "off" || env["GOWORK"] != "off" {
		return errors.New("local environment safety controls differ from the trusted profile")
	}
	if repoSource == "" || filepath.Clean(env["CR_REPO_DIR"]) != filepath.Clean(repoSource) {
		return errors.New("local repository environment is not bound to the reviewed repository")
	}
	for _, key := range []string{"HOME", "GOCACHE", "GOMODCACHE", "GOTMPDIR", "TMPDIR", "TMP", "TEMP"} {
		if !filepath.IsAbs(env[key]) {
			return fmt.Errorf("local environment path %q is not absolute", key)
		}
	}
	return nil
}

func validateEnvironment(env map[string]string) error {
	if len(env) != len(fixedEnv)+2 {
		return errors.New("environment does not contain the exact whitelist")
	}
	for key, expected := range fixedEnv {
		if env[key] != expected {
			return fmt.Errorf("environment value for %q differs from the fixed value", key)
		}
	}
	resultDir, repoDir := filepath.ToSlash(env["CR_RESULT_DIR"]), filepath.ToSlash(env["CR_REPO_DIR"])
	resultBase, repoBase := strings.TrimSuffix(resultDir, "/out"), strings.TrimSuffix(repoDir, "/work")
	if resultBase != repoBase || !targetWorkspacePattern.MatchString(resultBase) {
		return errors.New("workspace environment paths are not the trusted final pair")
	}
	return nil
}

func validateRunner(spec CheckSpec) error {
	runner, err := filepath.EvalSymlinks(spec.RunnerPath)
	if err != nil {
		return fmt.Errorf("resolve runner: %w", err)
	}
	skill, err := filepath.EvalSymlinks(spec.SkillRoot)
	if err != nil {
		return fmt.Errorf("resolve skill root: %w", err)
	}
	rel, err := filepath.Rel(skill, runner)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("runner escapes Skill root")
	}
	if filepath.ToSlash(rel) != "scripts/checkrunner/main.go" && filepath.ToSlash(rel) != "scripts/checkrunner/cr-checkrunner" && filepath.ToSlash(rel) != "scripts/checkrunner/cr-checkrunner.exe" {
		return errors.New("runner path is not trusted")
	}
	return nil
}

func (a Authorizer) recordDenied(ctx context.Context, spec CheckSpec, evidence decisionEvidence) error {
	evidence.reason = redact.String(evidence.reason)
	if err := a.save(ctx, decision(spec, evidence)); err != nil {
		return fmt.Errorf("governance denied (%s); persist denial: %w", evidence.reason, err)
	}
	return fmt.Errorf("governance denied: %s", evidence.reason)
}

func (a Authorizer) save(ctx context.Context, value Decision) error {
	if a.Recorder == nil {
		return errors.New("nil decision recorder")
	}
	value.Reason = redact.String(value.Reason)
	if err := a.Recorder.SaveDecision(ctx, value); err != nil {
		return errors.New(redact.String(err.Error()))
	}
	return nil
}

func decision(spec CheckSpec, evidence decisionEvidence) Decision {
	digest := sha256.Sum256([]byte(strings.Join(digestFields(spec), "\x00")))
	return Decision{Stage: evidence.stage, CheckID: spec.ID, Risk: evidence.risk, Action: evidence.action, Reason: evidence.reason, ArgsDigest: fmt.Sprintf("%x", digest), At: time.Now().UTC()}
}

func digestFields(spec CheckSpec) []string {
	fields := []string{spec.ID, spec.Runtime, spec.RunnerPath, spec.SkillRoot, spec.Cwd, spec.Artifact,
		spec.RepoSource, spec.Timeout.String(),
		fmt.Sprint(spec.Network), fmt.Sprint(spec.Privileged), fmt.Sprint(spec.HostWrite)}
	fields = append(fields, spec.Argv...)
	keys := make([]string, 0, len(spec.Env))
	for key := range spec.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fields = append(fields, key, spec.Env[key])
	}
	return fields
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type reviewTool struct{ declaration *tool.Declaration }

func (t reviewTool) Declaration() *tool.Declaration { return t.declaration }

// DefaultPolicy allows only fixed checks on validated runtimes. The CLI's
// explicit --allow-local gate is treated as prior approval for local fallback.
func DefaultPolicy(_ context.Context, request *tool.PermissionRequest) (tool.PermissionDecision, error) {
	if request == nil || request.ToolName != "code_review_check" {
		return tool.DenyPermission("unknown tool"), nil
	}
	var input struct {
		CheckID string `json:"check_id"`
		Runtime string `json:"runtime"`
	}
	if err := json.Unmarshal(request.Arguments, &input); err != nil {
		return tool.DenyPermission("invalid structured arguments"), nil
	}
	if _, ok := fixedArgs[input.CheckID]; !ok {
		return tool.AskPermission("custom check requires human review"), nil
	}
	if input.Runtime != "container" && input.Runtime != "fake" && input.Runtime != "local" {
		return tool.DenyPermission("unknown runtime"), nil
	}
	return tool.AllowPermission(), nil
}
