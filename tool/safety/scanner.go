//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const maxReportedCommandBytes = 4096

// Scanner applies one immutable Policy to execution inputs. Scanner is safe
// for concurrent use.
type Scanner struct {
	policy Policy
}

// NewScanner validates and defensively copies policy.
func NewScanner(policy Policy) (*Scanner, error) {
	normalized, err := normalizePolicy(policy)
	if err != nil {
		return nil, err
	}
	return &Scanner{policy: normalized}, nil
}

// Scan evaluates an input without executing it. It always returns a complete
// report, including allow decisions.
func (s *Scanner) Scan(ctx context.Context, input Input) Report {
	start := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}

	input.Backend = resolvedBackend(input.ToolName, input.Backend)
	source := input.Command
	if source == "" {
		source = input.Script
	}
	reportCommand := source
	if len(input.Arguments) > 0 {
		reportCommand += " " + strings.Join(input.Arguments, " ")
	}
	reportCommand, commandRedacted, _ := redactText(
		reportCommand,
		maxReportedCommandBytes,
	)

	var findings []Finding
	if err := ctx.Err(); err != nil {
		findings = append(findings, scanCanceledFinding(err))
	}
	if size := inputByteSize(input); size > s.policy.Limits.MaxInputBytes {
		findings = append(findings, finding(
			DecisionDeny,
			RiskHigh,
			RuleInputLimit,
			fmt.Sprintf(
				"execution input is %d bytes and exceeds policy limit %d bytes",
				size,
				s.policy.Limits.MaxInputBytes,
			),
			"Reduce command, script, argument, or environment input size.",
		))
	}
	if len(findings) > 0 {
		report := buildReport(
			input,
			reportCommand,
			commandRedacted,
			findings,
		)
		report.DurationMicros = time.Since(start).Microseconds()
		return report
	}

	findings = append(findings, s.scanRequestProperties(input)...)
	sensitiveSource := source + " " + strings.Join(input.Arguments, " ")
	if containsSensitiveLiteral(sensitiveSource) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskCritical,
			RuleSensitiveLiteral,
			"command or script contains an inline credential-like value",
			"Pass secrets through an approved secret provider instead of command text.",
		))
		commandRedacted = true
	}
	for key, value := range input.Environment {
		if !containsSensitiveLiteral(value) {
			continue
		}
		findings = append(findings, finding(
			DecisionDeny,
			RiskCritical,
			RuleSensitiveLiteral,
			fmt.Sprintf(
				"environment variable %q contains a credential-like value",
				key,
			),
			"Inject secrets at the isolated runtime boundary and omit them from tool arguments.",
		))
		commandRedacted = true
	}

	switch {
	case strings.TrimSpace(input.Script) != "":
		findings = append(findings, s.scanScript(ctx, input)...)
	case strings.TrimSpace(input.Command) != "":
		findings = append(findings, s.scanCommand(input)...)
	default:
		findings = append(findings, finding(
			s.policy.Actions.Unparsable,
			RiskHigh,
			RuleShellUnparsable,
			"no command or script was provided",
			"Provide an explicit command or script for policy evaluation.",
		))
	}

	report := buildReport(input, reportCommand, commandRedacted, findings)
	report.DurationMicros = time.Since(start).Microseconds()
	return report
}

func (s *Scanner) scanRequestProperties(input Input) []Finding {
	var findings []Finding
	if input.TimeoutSecond > s.policy.Limits.MaxTimeoutSeconds {
		findings = append(findings, finding(
			DecisionDeny,
			RiskHigh,
			RuleTimeoutLimit,
			fmt.Sprintf(
				"requested timeout %d seconds exceeds policy limit %d seconds",
				input.TimeoutSecond,
				s.policy.Limits.MaxTimeoutSeconds,
			),
			"Use a bounded timeout within the configured policy limit.",
		))
	}
	if input.Concurrency > s.policy.Limits.MaxConcurrency {
		findings = append(findings, finding(
			DecisionDeny,
			RiskHigh,
			RuleConcurrencyLimit,
			fmt.Sprintf(
				"requested concurrency %d exceeds policy limit %d",
				input.Concurrency,
				s.policy.Limits.MaxConcurrency,
			),
			"Reduce concurrency or use a separately governed batch service.",
		))
	}
	if input.Backend == BackendHost && input.Background {
		findings = append(findings, finding(
			s.policy.Actions.HostBackground,
			RiskHigh,
			RuleHostBackground,
			"host execution requested a background process",
			"Run in the foreground or require approval and retain a killable session handle.",
		))
	}
	if input.Backend == BackendHost && input.TTY {
		findings = append(findings, finding(
			s.policy.Actions.HostTTY,
			RiskHigh,
			RuleHostTTY,
			"host execution requested a PTY session",
			"Require human review, a short timeout, output limits, and process-group cleanup.",
		))
	}

	allowedEnv := makeStringSet(s.policy.AllowedEnvironmentVariables, false)
	for key := range input.Environment {
		if _, ok := allowedEnv[key]; ok {
			continue
		}
		findings = append(findings, finding(
			DecisionDeny,
			RiskHigh,
			RuleEnvironmentVariable,
			fmt.Sprintf(
				"environment variable %q is not allowlisted", key,
			),
			"Remove the variable or add its name to the reviewed policy allowlist.",
		))
	}
	if input.WorkingDir != "" {
		if matched := s.matchForbiddenPath(input.WorkingDir); matched != "" {
			findings = append(findings, forbiddenPathFinding(matched))
		}
	}
	return findings
}

func (s *Scanner) scanScript(ctx context.Context, input Input) []Finding {
	var findings []Finding
	if looksLikeInfiniteLoop(input.Script) {
		findings = append(findings, finding(
			DecisionDeny,
			RiskHigh,
			RuleInfiniteLoop,
			"script contains a statically unbounded loop",
			"Use a bounded iteration count and an executor-enforced timeout.",
		))
	}

	language := strings.ToLower(strings.TrimSpace(input.Language))
	switch language {
	case "bash", "sh", "shell", "zsh", "":
		for lineNumber, line := range strings.Split(input.Script, "\n") {
			if err := ctx.Err(); err != nil {
				findings = append(findings, scanCanceledFinding(err))
				break
			}
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			lineInput := input
			lineInput.Command = line
			lineInput.Script = ""
			lineInput.Arguments = nil
			lineFindings := s.scanCommand(lineInput)
			for i := range lineFindings {
				lineFindings[i].Evidence = fmt.Sprintf(
					"line %d: %s", lineNumber+1, lineFindings[i].Evidence,
				)
			}
			findings = append(findings, lineFindings...)
		}
	default:
		if err := ctx.Err(); err != nil {
			findings = append(findings, scanCanceledFinding(err))
		} else {
			findings = append(findings, s.scanNonShellScript(input)...)
		}
	}
	return findings
}

func inputByteSize(input Input) int {
	size := len(input.Command) + len(input.Script) + len(input.WorkingDir)
	for _, arg := range input.Arguments {
		size += len(arg)
	}
	for key, value := range input.Environment {
		size += len(key) + len(value)
	}
	return size
}

func scanCanceledFinding(err error) Finding {
	return finding(
		DecisionDeny,
		RiskHigh,
		RuleScanCanceled,
		fmt.Sprintf("safety scan canceled: %v", err),
		"Retry with a live context and a bounded execution input.",
	)
}

func (s *Scanner) scanNonShellScript(input Input) []Finding {
	var findings []Finding
	lower := strings.ToLower(input.Script)
	for _, marker := range []string{
		"os.system(", "subprocess.", "runtime.exec(", "child_process.",
	} {
		if !strings.Contains(lower, marker) {
			continue
		}
		findings = append(findings, finding(
			s.policy.Actions.Unparsable,
			RiskHigh,
			RuleShellUnparsable,
			fmt.Sprintf(
				"%s script invokes an embedded command runner",
				input.Language,
			),
			"Move the operation to an explicitly scanned command tool or require human review.",
		))
		break
	}
	for _, token := range strings.Fields(input.Script) {
		if matched := s.matchForbiddenPath(token); matched != "" {
			findings = append(findings, forbiddenPathFinding(matched))
			break
		}
	}
	findings = append(findings, s.scanURLs(input.Script)...)
	return findings
}

func buildReport(
	input Input,
	command string,
	redacted bool,
	findings []Finding,
) Report {
	if len(findings) == 0 {
		return Report{
			Decision:       DecisionAllow,
			RiskLevel:      RiskLow,
			RuleID:         RuleAllow,
			Evidence:       "no configured safety rule matched",
			Recommendation: "Execute with runtime isolation, limits, and audit collection enabled.",
			ToolName:       input.ToolName,
			Command:        command,
			Backend:        input.Backend,
			Blocked:        false,
			Redacted:       redacted,
		}
	}

	primary := findings[0]
	for _, candidate := range findings[1:] {
		if findingPriority(candidate) > findingPriority(primary) {
			primary = candidate
		}
	}
	for _, item := range findings {
		redacted = redacted || item.Redacted
	}
	return Report{
		Decision:       primary.Decision,
		RiskLevel:      primary.RiskLevel,
		RuleID:         primary.RuleID,
		Evidence:       primary.Evidence,
		Recommendation: primary.Recommendation,
		ToolName:       input.ToolName,
		Command:        command,
		Backend:        input.Backend,
		Blocked:        primary.Decision != DecisionAllow,
		Redacted:       redacted,
		Findings:       findings,
	}
}

func finding(
	decision Decision,
	risk RiskLevel,
	ruleID string,
	evidence string,
	recommendation string,
) Finding {
	evidence, redacted, _ := redactText(evidence, maxReportedCommandBytes)
	return Finding{
		Decision:       decision,
		RiskLevel:      risk,
		RuleID:         ruleID,
		Evidence:       evidence,
		Recommendation: recommendation,
		Redacted:       redacted,
	}
}

func findingPriority(item Finding) int {
	return riskPriority(item.RiskLevel)*10 + decisionPriority(item.Decision)
}

func riskPriority(risk RiskLevel) int {
	switch risk {
	case RiskCritical:
		return 4
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	default:
		return 1
	}
}

func decisionPriority(decision Decision) int {
	switch decision {
	case DecisionDeny:
		return 3
	case DecisionAsk:
		return 2
	default:
		return 1
	}
}

func resolvedBackend(toolName string, backend Backend) Backend {
	if backend != "" && backend != BackendUnknown {
		return backend
	}
	switch toolName {
	case "workspace_exec", "workspace_write_stdin", "workspace_kill_session":
		return BackendWorkspace
	case "exec_command", "write_stdin", "kill_session":
		return BackendHost
	case "execute_code":
		return BackendCodeExecutor
	default:
		return BackendUnknown
	}
}

func makeStringSet(values []string, lower bool) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		if lower {
			value = strings.ToLower(value)
		}
		out[value] = struct{}{}
	}
	return out
}
