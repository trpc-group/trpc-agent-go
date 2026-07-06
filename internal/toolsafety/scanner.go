//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolsafety

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// ScanInput is the data passed to Scanner.Scan.
type ScanInput struct {
	Command     string
	CommandArgs []string
	WorkDir     string
	EnvKeys     []string
	ToolName    string

	// Backend identifies the execution backend: "workspaceexec",
	// "hostexec", or "codeexec". Derived from ToolName when empty
	// (see DeriveBackend).
	Backend string

	TimeoutSec  int
	OutputBytes int64

	SessionID    string
	InvocationID string
}

// ScanResult wraps the report, audit event, and parsed state produced
// by a scan.
type ScanResult struct {
	Report     ScanReport
	Audit      AuditEvent
	ShellError error
}

// Scanner runs safety rules against a command input.
type Scanner struct {
	policy              *SafetyPolicy
	sensitiveRegexCache []*regexp.Regexp
}

// NewScanner creates a Scanner with the given policy. If policy is
// nil, a default policy is used (auto-deny critical and high).
// Sensitive-pattern regular expressions are precompiled at creation
// time so the Scan hot path does no regex compilation.
func NewScanner(policy *SafetyPolicy) *Scanner {
	if policy == nil {
		policy = &SafetyPolicy{
			Version:            "1.0",
			AutoDenyRiskLevels: []string{"critical", "high"},
		}
	}
	var cache []*regexp.Regexp
	for _, sp := range policy.SensitivePatterns {
		re, err := regexp.Compile(sp.Pattern)
		if err != nil {
			continue
		}
		cache = append(cache, re)
	}
	return &Scanner{
		policy:              policy,
		sensitiveRegexCache: cache,
	}
}

// DeriveBackend maps well-known tool names to their execution backend.
// This is used when the scan input's Backend field is empty (e.g. when
// SafetyGuard extracts from PermissionRequest.ToolName).
func DeriveBackend(toolName string) string {
	switch toolName {
	case "exec_command", "host_exec", "write_stdin", "kill_session":
		return "hostexec"
	case "workspace_exec", "workspace_write_stdin", "workspace_kill_session":
		return "workspaceexec"
	case "code_exec", "execute_code":
		return "codeexec"
	default:
		return "workspaceexec"
	}
}

// Scan runs all safety rules against the input and returns a
// structured result. The caller should use result.Report.Decision to
// decide whether to allow, deny, or escalate.
//
// Scan always calls shellsafe.Parse first. When shellsafe rejects the
// command (structural unsafety), content rules are skipped and the
// result is an "ask" decision (we cannot safely parse, so we cannot
// safely allow). The caller receives an R3-SHELLSAFE-REJECT finding
// with the shellsafe error as evidence.
func (s *Scanner) Scan(ctx context.Context, input ScanInput) ScanResult {
	start := time.Now()

	raw := strings.TrimSpace(input.Command)
	backend := input.Backend
	if backend == "" {
		backend = DeriveBackend(input.ToolName)
	}

	// Step 1: shellsafe structural check.
	var parsed *parsedCommand
	var shellErr error
	if raw != "" {
		pipe, err := shellsafe.Parse(raw)
		if err != nil {
			shellErr = err
		} else if pipe != nil {
			parsed = &parsedCommand{segments: pipe.Commands}
		}
	}

	// Step 2: Build scan context.
	sctx := &scanContext{
		command:     raw,
		commandArgs: input.CommandArgs,
		workDir:     input.WorkDir,
		envKeys:     input.EnvKeys,
		backend:     backend,
		timeoutSec:  input.TimeoutSec,
		outputBytes: input.OutputBytes,
		parsed:      parsed,
		policy:      s.policy,
	}

	// Step 3: Run all rules + allowed_commands enforcement.
	var findings []RuleFinding
	var worst RiskLevel

	if shellErr != nil {
		findings = append(findings, RuleFinding{
			RuleID:    "R3-SHELLSAFE-REJECT",
			RiskLevel: RiskHigh,
			Category:  CatShellBypass,
			Evidence:  "shellsafe rejected command: " + shellErr.Error(),
			Recommendation: "The command contains shell constructs ($(), backticks, " +
				"redirections, shell wrappers, etc.) that shellsafe does not accept. " +
				"Rewrite the command as a simple executable with literal arguments.",
		})
		worst = RiskHigh
	} else {
		for _, r := range allRules {
			if finding := r.check(sctx); finding != nil {
				if finding.Recommendation == "" {
					finding.Recommendation = r.recommendText
				}
				findings = append(findings, *finding)
				if riskOrder(finding.RiskLevel) > riskOrder(worst) {
					worst = finding.RiskLevel
				}
			}
		}
		// Enforce allowed_commands when the list is non-empty.
		if f := checkAllowedCommands(sctx); f != nil {
			findings = append(findings, *f)
			if riskOrder(f.RiskLevel) > riskOrder(worst) {
				worst = f.RiskLevel
			}
		}
	}

	if len(findings) == 0 {
		worst = RiskNone
	}

	// Step 4: Determine decision.
	decision := DecisionAllow
	intercepted := false
	if shellErr != nil {
		decision = DecisionAsk
		intercepted = true
	} else if s.policy.IsAutoDeny(backend, worst) {
		decision = DecisionDeny
		intercepted = true
	} else if worst != RiskNone {
		decision = DecisionAsk
		intercepted = true
	}

	duration := time.Since(start).Milliseconds()

	// Step 5: Determine sanitization.
	sanitized := s.containsSensitivePattern(raw)

	// Step 6: Build report + audit event. When the command contains
	// secrets, store a hash instead of the raw text so audit logs
	// never persist plaintext credentials.
	cmdForOutput := raw
	if sanitized {
		cmdForOutput = redactCommand(raw)
	}

	ruleIDs := make([]string, len(findings))
	for i, f := range findings {
		ruleIDs[i] = f.RuleID
	}

	report := ScanReport{
		Decision:    decision,
		RiskLevel:   worst,
		ToolName:    input.ToolName,
		Backend:     backend,
		Command:     cmdForOutput,
		CommandArgs: input.CommandArgs,
		WorkDir:     input.WorkDir,
		EnvKeys:     input.EnvKeys,
		Findings:    findings,
		Intercepted: intercepted,
		DurationMs:  duration,
		Timestamp:   time.Now(),
	}

	audit := AuditEvent{
		Timestamp:    report.Timestamp,
		ToolName:     input.ToolName,
		Backend:      backend,
		Command:      cmdForOutput,
		Decision:     decision,
		RiskLevel:    worst,
		RuleIDs:      ruleIDs,
		Intercepted:  intercepted,
		DurationMs:   duration,
		Sanitized:    sanitized,
		SessionID:    input.SessionID,
		InvocationID: input.InvocationID,
	}

	return ScanResult{
		Report:     report,
		Audit:      audit,
		ShellError: shellErr,
	}
}

// containsSensitivePattern checks whether the command string contains
// any precompiled sensitive pattern. Regexes are compiled once at
// NewScanner time so this is cheap.
func (s *Scanner) containsSensitivePattern(cmd string) bool {
	if s == nil {
		return false
	}
	for _, re := range s.sensitiveRegexCache {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// redactCommand replaces a command containing secrets with a SHA-256
// hash so the audit trail can correlate events without storing the
// plaintext secret.
func redactCommand(cmd string) string {
	if cmd == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(cmd))
	return fmt.Sprintf("sha256:%x", sum)
}

// checkAllowedCommands enforces the policy's AllowedCommands list when
// non-empty. It runs after all other rules so content-level findings
// (dangerous delete, sensitive path, etc.) are still emitted even when
// the command is also not in the allowlist.
func checkAllowedCommands(ctx *scanContext) *RuleFinding {
	allowed := ctx.policy.AllowedCommands
	if len(allowed) == 0 {
		return nil
	}
	if ctx.parsed == nil {
		return nil
	}
	for _, seg := range ctx.parsed.segments {
		if len(seg) == 0 {
			continue
		}
		name := seg[0]
		matched := false
		for _, a := range allowed {
			if strings.EqualFold(a, name) {
				matched = true
				break
			}
		}
		if !matched {
			return &RuleFinding{
				RuleID:    "R1-ALLOWED-COMMAND",
				RiskLevel: RiskHigh,
				Category:  CatDangerousCmd,
				Evidence:  "command '" + name + "' is not in the allowed_commands list",
				Recommendation: "Add the command to allowed_commands in the " +
					"policy file, or use an alternative that is already allowed.",
			}
		}
	}
	return nil
}
