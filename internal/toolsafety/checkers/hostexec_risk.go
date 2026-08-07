// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package checkers

import (
	"context"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/toolsafety"
)

// HostExecRiskChecker checks for PTY session risks, background processes,
// and privilege escalation on hostexec backend.
type HostExecRiskChecker struct {
}

// NewHostExecRiskChecker creates a HostExecRiskChecker.
func NewHostExecRiskChecker() *HostExecRiskChecker {
	return &HostExecRiskChecker{}
}

// ID returns the checker identifier.
func (c *HostExecRiskChecker) ID() string { return "hostexec_risk" }

// IsEnabled reports whether this checker is active.
func (c *HostExecRiskChecker) IsEnabled(policy *toolsafety.SafetyPolicy) bool {
	return true
}

// Check runs the host execution risk check.
func (c *HostExecRiskChecker) Check(ctx context.Context, req *toolsafety.ScanRequest) ([]toolsafety.RiskFinding, error) {
	var findings []toolsafety.RiskFinding

	if req == nil || req.Command == "" {
		return findings, nil
	}

	// Only relevant for hostexec backend.
	if req.Backend != "hostexec" {
		return findings, nil
	}

	cmd := strings.TrimSpace(req.Command)
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return findings, nil
	}

	// Check for background processes (shell & operator).
	if strings.Contains(cmd, " &") || strings.HasSuffix(cmd, "&") {
		findings = append(findings, toolsafety.RiskFinding{
			RuleID:         toolsafety.RuleBackgroundProcess,
			RiskLevel:      toolsafety.RiskLevelMedium,
			Evidence:       cmd,
			Recommendation: "Background process '&' detected on hostexec; ensure the process is managed or killed",
			SeverityScore:  6,
			MatchedPattern: "background_ampersand",
		})
	}

	// Check for nohup / disown.
	if strings.Contains(cmd, "nohup") || strings.Contains(cmd, "disown") {
		findings = append(findings, toolsafety.RiskFinding{
			RuleID:         toolsafety.RuleBackgroundProcess,
			RiskLevel:      toolsafety.RiskLevelMedium,
			Evidence:       cmd,
			Recommendation: "Detached process (nohup/disown) on hostexec may leave orphan processes",
			SeverityScore:  6,
			MatchedPattern: "detached_process",
		})
	}

	// Check for PTY session risk — interactive commands that hold a session.
	interactiveCommands := []string{"vim", "nano", "less", "more", "top", "htop", "tmux", "screen"}
	for _, ic := range interactiveCommands {
		if strings.EqualFold(fields[0], ic) || strings.HasSuffix(fields[0], "/"+ic) {
			findings = append(findings, toolsafety.RiskFinding{
				RuleID:         toolsafety.RuleHostExecPTY,
				RiskLevel:      toolsafety.RiskLevelMedium,
				Evidence:       cmd,
				Recommendation: "Interactive command " + ic + " on hostexec creates a PTY session with long-running risk",
				SeverityScore:  5,
				MatchedPattern: "interactive:" + ic,
			})
			break
		}
	}

	// Check for privilege escalation.
	privCommands := []string{"sudo", "su", "doas", "pkexec", "chroot"}
	for _, pc := range privCommands {
		if strings.EqualFold(fields[0], pc) || strings.HasSuffix(fields[0], "/"+pc) {
			findings = append(findings, toolsafety.RiskFinding{
				RuleID:         toolsafety.RulePrivilegeEscalation,
				RiskLevel:      toolsafety.RiskLevelCritical,
				Evidence:       cmd,
				Recommendation: "Privilege escalation via " + pc + " on hostexec is not allowed",
				SeverityScore:  10,
				MatchedPattern: "privilege:" + pc,
			})
			break
		}
	}

	// Check for long-running process risk.
	if req.TimeoutS <= 0 || req.TimeoutS > 300 {
		findings = append(findings, toolsafety.RiskFinding{
			RuleID:         toolsafety.RuleHostExecPTY,
			RiskLevel:      toolsafety.RiskLevelLow,
			Evidence:       "timeout: " + strings.TrimSpace(cmd),
			Recommendation: "Hostexec command without a bounded timeout may run indefinitely",
			SeverityScore:  4,
			MatchedPattern: "no_timeout",
		})
	}

	return findings, nil
}
