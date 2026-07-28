// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"path/filepath"
	"strings"
)

func (s *Scanner) scanBackend(req ExecutionRequest) []Finding {
	var findings []Finding
	switch req.Backend {
	case BackendHostExec:
		findings = append(findings, finding(
			RuleHostDefault, CategoryHostExec, RiskMedium,
			s.policy.BackendRules.HostExec.DefaultAction,
			"hostexec request is subject to the host execution default action",
			"backend",
			"Require explicit approval for host execution unless policy allows it.",
		))
		if req.TTY {
			action := DecisionAsk
			if s.policy.BackendRules.HostExec.DenyTTY {
				action = DecisionDeny
			}
			findings = append(findings, finding(
				RuleHostPTY, CategoryHostExec, RiskHigh, action,
				"hostexec requested PTY/TTY interactive session",
				"backend.tty",
				"Host PTY sessions can persist and should require explicit approval.",
			))
		}
		if req.Background {
			action := s.policy.BackendRules.HostExec.BackgroundAction
			if action == "" {
				action = DecisionAsk
			}
			findings = append(findings, finding(
				RuleHostBackground, CategoryHostExec, RiskHigh, action,
				"hostexec requested background session",
				"backend.background",
				"Review process lifetime and cleanup before starting host background commands.",
			))
		}
	case BackendWorkspaceExec:
		if s.policy.BackendRules.WorkspaceExec.RequireWorkspaceRelativeCwd && req.Cwd != "" &&
			(isAbsolutePathAnyPlatform(req.Cwd) || strings.HasPrefix(req.Cwd, "~") ||
				strings.HasPrefix(filepath.ToSlash(req.Cwd), "..")) {
			findings = append(findings, finding(RuleForbiddenPath, CategoryPolicy, RiskCritical, DecisionDeny,
				"workspace cwd must be relative: "+req.Cwd, "cwd", "Use a path relative to the workspace root."))
		}
		if req.TTY && s.policy.BackendRules.WorkspaceExec.DenyTTY {
			findings = append(findings, finding(
				RuleHostPTY, CategoryHostExec, RiskMedium, DecisionDeny,
				"workspace_exec requested PTY/TTY but policy denies it",
				"backend.tty",
				"Disable TTY or change policy after review.",
			))
		}
		if req.Background {
			action := s.policy.BackendRules.WorkspaceExec.BackgroundAction
			if action == "" {
				action = DecisionAsk
			}
			findings = append(findings, finding(
				RuleHostBackground, CategoryResource, RiskMedium, action,
				"workspace_exec requested background session",
				"backend.background",
				"Confirm session cleanup and output limits before background execution.",
			))
		}
	case BackendMCP, BackendSkill, BackendUnknown:
		findings = append(findings, finding(
			RuleHumanReview, CategoryPolicy, RiskMedium, s.policy.DefaultAction,
			"execution backend has no registered semantic scanner: "+string(req.Backend),
			"backend",
			"Register a supported backend or require explicit approval for this tool.",
		))
	}
	return findings
}

func isAbsolutePathAnyPlatform(path string) bool {
	path = strings.TrimSpace(path)
	if filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) ||
		strings.HasPrefix(path, "//") {
		return true
	}
	return len(path) >= 3 &&
		((path[0] >= 'a' && path[0] <= 'z') ||
			(path[0] >= 'A' && path[0] <= 'Z')) &&
		path[1] == ':' && (path[2] == '/' || path[2] == '\\')
}
