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
	"fmt"
	"regexp"
	"strings"
)

// ScanResult holds the security verdict for a scanned command or script.
type ScanResult struct {
	Decision       string `json:"decision"` // "allow", "deny", "ask"
	RiskLevel      string `json:"risk_level"` // "critical", "high", "medium", "low", "none"
	RuleID         string `json:"rule_id"`
	Evidence       string `json:"evidence"`
	Recommendation string `json:"recommendation"`
	ToolName       string `json:"tool_name"`
	Command        string `json:"command"`
	Backend        string `json:"backend"`
	Intercepted    bool   `json:"intercepted"`
}

// Scanner performs static security scanning against commands and scripts.
type Scanner struct {
	policy *Policy
}

// NewScanner creates a new Scanner instance.
func NewScanner(policy *Policy) *Scanner {
	return &Scanner{policy: policy}
}

// ScanCommand scans a command or script string for security risks.
func (s *Scanner) ScanCommand(toolName, command, backend string) ScanResult {
	cmdTrimmed := strings.TrimSpace(command)
	res := ScanResult{
		Decision:       "allow",
		RiskLevel:      "none",
		RuleID:         "RULE_PASS",
		Evidence:       "No security risks detected",
		Recommendation: "Safe to execute",
		ToolName:       toolName,
		Command:        command,
		Backend:        backend,
		Intercepted:    false,
	}

	// 1. Dangerous Commands & Paths (rm -rf, .ssh, .env, credential files)
	if strings.Contains(cmdTrimmed, "rm -rf") || strings.Contains(cmdTrimmed, "rm -f /") {
		res.Decision = "deny"
		res.RiskLevel = "critical"
		res.RuleID = "RULE_DANGEROUS_DELETION"
		res.Evidence = "Recursive deletion command detected: " + cmdTrimmed
		res.Recommendation = "Blocked destructive file system deletion"
		res.Intercepted = true
		return res
	}

	for _, p := range s.policy.DeniedPaths {
		if strings.Contains(cmdTrimmed, p) {
			res.Decision = "deny"
			res.RiskLevel = "critical"
			res.RuleID = "RULE_DENIED_PATH_ACCESS"
			res.Evidence = fmt.Sprintf("Access to sensitive/denied path '%s' detected", p)
			res.Recommendation = "Block access to private credentials or system files"
			res.Intercepted = true
			return res
		}
	}

	// 2. Network Egress (curl, wget, nc, ssh to non-allowlisted domains)
	netRegex := regexp.MustCompile(`(?i)(curl|wget|nc|ssh|fetch)\s+(https?://)?([a-zA-Z0-9.-]+)`)
	if matches := netRegex.FindStringSubmatch(cmdTrimmed); len(matches) > 3 {
		domain := matches[3]
		allowed := false
		for _, d := range s.policy.AllowedDomains {
			if domain == d || strings.HasSuffix(domain, "."+d) {
				allowed = true
				break
			}
		}
		if !allowed {
			res.Decision = "deny"
			res.RiskLevel = "high"
			res.RuleID = "RULE_UNAPPROVED_NETWORK_EGRESS"
			res.Evidence = fmt.Sprintf("Egress request to unapproved domain '%s'", domain)
			res.Recommendation = "Restrict network connections to allowlisted domains"
			res.Intercepted = true
			return res
		}
	}

	// 3. Shell Bypass (sh -c, bash -c, eval, backticks, $())
	if strings.Contains(cmdTrimmed, "eval ") || strings.Contains(cmdTrimmed, "`") || strings.Contains(cmdTrimmed, "$(") || strings.Contains(cmdTrimmed, "sh -c") || strings.Contains(cmdTrimmed, "bash -c") {
		res.Decision = "deny"
		res.RiskLevel = "high"
		res.RuleID = "RULE_SHELL_BYPASS"
		res.Evidence = "Shell expansion or dynamic subshell wrapper detected"
		res.Recommendation = "Do not allow arbitrary subshell expansion wrapper execution"
		res.Intercepted = true
		return res
	}

	// 4. Host Exec & PTY Long Session / Privilege Escalation
	if backend == "hostexec" && (strings.Contains(cmdTrimmed, "sudo") || strings.Contains(cmdTrimmed, "su ") || strings.Contains(cmdTrimmed, "chmod +x")) {
		res.Decision = "ask"
		res.RiskLevel = "high"
		res.RuleID = "RULE_HOSTEXEC_PRIVILEGE_ESCALATION"
		res.Evidence = "Privilege escalation or permission change command on host"
		res.Recommendation = "Require human review before elevating host privileges"
		res.Intercepted = true
		return res
	}

	// 5. Dependency & Environment Mutation (npm install, pip install, go install)
	depRegex := regexp.MustCompile(`(?i)(go\s+install|npm\s+install|pip\s+install|apt\s+install)`)
	if depRegex.MatchString(cmdTrimmed) {
		res.Decision = "ask"
		res.RiskLevel = "medium"
		res.RuleID = "RULE_DEPENDENCY_MUTATION"
		res.Evidence = "Package installation / dependency mutation command detected"
		res.Recommendation = "Review untrusted external dependency installation"
		res.Intercepted = true
		return res
	}

	// 6. Resource Abuse (long sleep, large loops)
	if strings.Contains(cmdTrimmed, "sleep 100") || strings.Contains(cmdTrimmed, "while true") || strings.Contains(cmdTrimmed, "for (;;)") {
		res.Decision = "deny"
		res.RiskLevel = "medium"
		res.RuleID = "RULE_RESOURCE_ABUSE"
		res.Evidence = "Long sleep or infinite loop detected"
		res.Recommendation = "Enforce max timeout limits to prevent thread starvation"
		res.Intercepted = true
		return res
	}

	return res
}
