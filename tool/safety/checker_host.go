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
	"strings"
)

// hostChecker detects risks specific to host command execution:
// background processes, privilege escalation, and session-residual
// commands (tmux, screen).
//
// This checker only activates when the Backend is "hostexec".
type hostChecker struct {
	policy *Policy
}

func (c *hostChecker) Name() string { return "host" }

func (c *hostChecker) Check(ctx context.Context, req *ScanRequest) (*CheckResult, error) {
	if req.Backend != "hostexec" {
		return nil, nil
	}

	text := req.Command
	if len(req.Args) > 0 {
		text += " " + strings.Join(req.Args, " ")
	}
	textLower := strings.ToLower(text)

	// Background process detection: &, nohup, disown.
	if c.policy.HostExec.DenyBackgroundProcesses {
		if result := c.checkBackground(text, textLower); result != nil {
			return result, nil
		}
	}

	// Privilege escalation: sudo, su, doas, pkexec.
	if c.policy.HostExec.DenyPrivilegeEscalation {
		if result := c.checkPrivilegeEscalation(textLower); result != nil {
			return result, nil
		}
	}

	// Session residual: tmux, screen, script (detach + reattach).
	if result := c.checkSessionResidual(textLower); result != nil {
		return result, nil
	}

	return nil, nil
}

func (c *hostChecker) checkBackground(text, lower string) *CheckResult {
	// & operator: standalone & after a command.
	// We look for " &" or "&" at end of line/segment.
	if strings.Contains(lower, " & ") || strings.HasSuffix(strings.TrimSpace(lower), " &") {
		return &CheckResult{
			Decision:       DecisionAsk,
			RiskLevel:      RiskMedium,
			RuleID:         "HOST_BACKGROUND_PROC",
			Evidence:       text,
			Recommendation: "Background process detected. This may leave orphaned processes on the host. Use workspace isolation or confirm the process will be properly cleaned up.",
		}
	}
	// nohup, disown, setsid — backgrounding wrappers.
	for _, bg := range []string{"nohup", "disown"} {
		if strings.Contains(lower, bg) {
			return &CheckResult{
				Decision:       DecisionAsk,
				RiskLevel:      RiskMedium,
				RuleID:         "HOST_BACKGROUND_PROC",
				Evidence:       text,
				Recommendation: "Backgrounding command '" + bg + "' may leave orphaned processes. Consider wrapping in a systemd service or scheduled task instead.",
			}
		}
	}
	return nil
}

func (c *hostChecker) checkPrivilegeEscalation(lower string) *CheckResult {
	for _, pe := range []string{"sudo", "su ", "doas", "pkexec"} {
		// su must have a trailing space to avoid matching "sudo", "summary", etc.
		if strings.Contains(lower, pe) {
			return &CheckResult{
				Decision:       DecisionDeny,
				RiskLevel:      RiskCritical,
				RuleID:         "HOST_PRIVILEGE_ESC",
				Evidence:       pe,
				Recommendation: "Privilege escalation detected (" + pe + "). Run the command without privilege escalation, or configure a dedicated least-privilege execution environment.",
			}
		}
	}
	return nil
}

func (c *hostChecker) checkSessionResidual(lower string) *CheckResult {
	for _, sr := range []string{"tmux", "screen"} {
		if strings.Contains(lower, sr) {
			return &CheckResult{
				Decision:       DecisionAsk,
				RiskLevel:      RiskMedium,
				RuleID:         "HOST_SESSION_RESIDUAL",
				Evidence:       sr,
				Recommendation: "Session manager '" + sr + "' may leave persistent sessions on the host. Ensure sessions are cleaned up after use.",
			}
		}
	}
	return nil
}
