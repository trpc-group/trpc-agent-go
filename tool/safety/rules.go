//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	// Secret leakage patterns.
	secretRegexes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)sk-[a-zA-Z0-9]{20,}`),
		regexp.MustCompile(`(?i)ghp_[a-zA-Z0-9]{36}`),
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		regexp.MustCompile(`-----BEGIN (RSA |EC |PGP |OPENSSH )?PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)(api[_-]?key|password|passwd|secret|token)\s*[:=]\s*["']?[a-zA-Z0-9_.\-]{8,}`),
	}

	// URL extractor for network outbound checks.
	urlRegex = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

// ScanRequest carries all contextual parameters needed for a tool safety check.
type ScanRequest struct {
	ToolName     string
	Command      string
	Args         []string
	Cwd          string
	Env          map[string]string
	ToolMetadata tool.ToolMetadata
	Backend      string
}

// Result holds the internal evaluation output of rules engine.
type Result struct {
	Decision       tool.PermissionAction
	RiskLevel      RiskLevel
	RuleID         string
	Evidence       string
	Recommendation string
	IsSanitized    bool
}

// EvaluateCommand runs the full suite of safety rule checks against a ScanRequest.
func EvaluateCommand(req *ScanRequest, policy *Policy) Result {
	if policy == nil {
		policy = DefaultPolicy()
	}

	cmdStr := strings.TrimSpace(req.Command)
	if cmdStr == "" && len(req.Args) > 0 {
		cmdStr = strings.Join(req.Args, " ")
	}

	// 1. Secret Leakage Check
	if res, matched := checkSecretLeakage(cmdStr); matched {
		return res
	}

	// 2. Resource Abuse Check (infinite loops, excessive sleep)
	if res, matched := checkResourceAbuse(cmdStr); matched {
		return res
	}

	// 3. Sensitive & Forbidden Paths Check
	if res, matched := checkForbiddenPaths(cmdStr, policy); matched {
		return res
	}

	// 4. Host Exec & Background Process Check
	if res, matched := checkHostExecAndBackground(cmdStr, req); matched {
		return res
	}

	// 5. Network Outbound Domain Whitelist Check
	if res, matched := checkNetworkOutbound(cmdStr, policy); matched {
		return res
	}

	// 6. Dependencies & Env Change / Ask Rules Check
	if res, matched := checkAskRulesAndDependencies(cmdStr, policy); matched {
		return res
	}

	// 7. Dangerous Commands & Shell Safe Parsing
	if res, matched := checkShellSafeAndDangerous(cmdStr, policy); matched {
		return res
	}

	return Result{
		Decision:       tool.PermissionActionAllow,
		RiskLevel:      RiskLevelNone,
		RuleID:         "RULE_ALLOW_PASSED",
		Evidence:       "Command passed all safety checks",
		Recommendation: "Allowed for execution",
	}
}

func checkSecretLeakage(cmd string) (Result, bool) {
	for _, re := range secretRegexes {
		if loc := re.FindStringIndex(cmd); loc != nil {
			snippet := cmd[loc[0]:loc[1]]
			if len(snippet) > 8 {
				snippet = snippet[:4] + "****" + snippet[len(snippet)-4:]
			} else {
				snippet = "****"
			}
			return Result{
				Decision:       tool.PermissionActionDeny,
				RiskLevel:      RiskLevelCritical,
				RuleID:         "RULE_SECRET_LEAKAGE",
				Evidence:       fmt.Sprintf("Potential credential or secret key detected in command: %s", snippet),
				Recommendation: "Do not pass hardcoded secrets or API keys in command arguments",
				IsSanitized:    true,
			}, true
		}
	}
	return Result{}, false
}

func checkShellSafeAndDangerous(cmd string, policy *Policy) (Result, bool) {
	// Parse command using shellsafe
	pipe, err := shellsafe.Parse(cmd)
	if err != nil {
		return Result{
			Decision:       tool.PermissionActionDeny,
			RiskLevel:      RiskLevelHigh,
			RuleID:         "RULE_SHELL_SYNTAX_REJECT",
			Evidence:       fmt.Sprintf("Command failed shell safety parser: %v", err),
			Recommendation: "Use simple, explicit commands without complex shell wrapper or expansion tricks",
		}, true
	}

	// Build shellsafe policy from denied/allowed commands
	safePolicy := shellsafe.PolicyFromLists(policy.AllowedCommands, policy.DeniedCommands)
	if safePolicy.Active() {
		if err := safePolicy.Check(pipe); err != nil {
			return Result{
				Decision:       tool.PermissionActionDeny,
				RiskLevel:      RiskLevelHigh,
				RuleID:         "RULE_DENIED_COMMAND",
				Evidence:       err.Error(),
				Recommendation: "Ensure command is in allowed_commands and does not match denied_commands",
			}, true
		}
	}

	// Additional explicit dangerous pattern checks
	lowerCmd := strings.ToLower(cmd)
	dangerousSubstrings := []string{
		"rm -rf", "rm -r -f", "rm -f -r",
		"mkfs", "dd if=", "> /dev/sd", "> /dev/nvme",
		"sudo ", "su -", "chmod 777", "chmod -r 777",
	}
	for _, danger := range dangerousSubstrings {
		if strings.Contains(lowerCmd, danger) {
			return Result{
				Decision:       tool.PermissionActionDeny,
				RiskLevel:      RiskLevelCritical,
				RuleID:         "RULE_DANGEROUS_COMMAND",
				Evidence:       fmt.Sprintf("Command contains dangerous string pattern %q", danger),
				Recommendation: "Destructive system modification commands are blocked by policy",
			}, true
		}
	}

	return Result{}, false
}

func checkForbiddenPaths(cmd string, policy *Policy) (Result, bool) {
	lowerCmd := strings.ToLower(cmd)
	words := strings.Fields(lowerCmd)

	for _, forbidden := range policy.ForbiddenPaths {
		pattern := strings.ToLower(forbidden)
		matched := false

		if strings.Contains(pattern, "*") {
			// Glob pattern
			for _, w := range words {
				if m, _ := filepath.Match(pattern, filepath.Base(w)); m {
					matched = true
					break
				}
			}
		} else {
			// Per-token matching for non-glob paths
			for _, w := range words {
				cleanWord := strings.Trim(w, "'\"(),")
				if cleanWord == pattern || strings.HasPrefix(cleanWord, pattern+"/") || strings.HasPrefix(cleanWord, pattern+"\\") {
					matched = true
					break
				}
			}
			if !matched && strings.Contains(lowerCmd, pattern) {
				matched = true
			}
		}

		if matched {
			return Result{
				Decision:       tool.PermissionActionDeny,
				RiskLevel:      RiskLevelHigh,
				RuleID:         "RULE_FORBIDDEN_PATH",
				Evidence:       fmt.Sprintf("Command accesses forbidden path or file pattern %q", forbidden),
				Recommendation: "Access to sensitive system paths and credentials is restricted",
			}, true
		}
	}
	return Result{}, false
}

func checkNetworkOutbound(cmd string, policy *Policy) (Result, bool) {
	lowerCmd := strings.ToLower(cmd)
	hasNetTool := strings.Contains(lowerCmd, "curl") ||
		strings.Contains(lowerCmd, "wget") ||
		strings.Contains(lowerCmd, "nc ") ||
		strings.Contains(lowerCmd, "ssh ")

	if !hasNetTool {
		return Result{}, false
	}

	urls := urlRegex.FindAllString(cmd, -1)
	if len(urls) == 0 {
		// Net tool without explicit URL (or non-HTTP target like nc/ssh) -> check whitelist domains in args
		return checkDomainInArgs(cmd, policy)
	}

	for _, rawURL := range urls {
		u, err := url.Parse(rawURL)
		if err != nil {
			return Result{
				Decision:       tool.PermissionActionDeny,
				RiskLevel:      RiskLevelHigh,
				RuleID:         "RULE_MALFORMED_URL",
				Evidence:       fmt.Sprintf("Failed to parse URL %q", rawURL),
				Recommendation: "Ensure network URLs are well-formed",
			}, true
		}

		host := strings.ToLower(u.Hostname())
		if !isDomainAllowed(host, policy.NetworkWhitelist) {
			return Result{
				Decision:       tool.PermissionActionDeny,
				RiskLevel:      RiskLevelHigh,
				RuleID:         "RULE_NETWORK_NON_WHITELIST",
				Evidence:       fmt.Sprintf("Outbound request to domain %q is not in network_whitelist", host),
				Recommendation: "Add host to network_whitelist in policy if this external connection is legitimate",
			}, true
		}
	}

	return Result{}, false
}

func checkDomainInArgs(cmd string, policy *Policy) (Result, bool) {
	words := strings.Fields(cmd)
	for _, w := range words {
		clean := strings.Trim(w, "'\"")
		if strings.Contains(clean, ".") && !strings.HasPrefix(clean, "-") {
			if isDomainAllowed(clean, policy.NetworkWhitelist) {
				return Result{}, false
			}
		}
	}
	return Result{
		Decision:       tool.PermissionActionDeny,
		RiskLevel:      RiskLevelHigh,
		RuleID:         "RULE_NETWORK_NON_WHITELIST",
		Evidence:       fmt.Sprintf("Network connection command %q uses non-whitelisted target", cmd),
		Recommendation: "Only connections to whitelisted domains are permitted",
	}, true
}

func isDomainAllowed(host string, whitelist []string) bool {
	host = strings.ToLower(host)
	for _, domain := range whitelist {
		d := strings.ToLower(domain)
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func checkHostExecAndBackground(cmd string, req *ScanRequest) (Result, bool) {
	// Background process execution
	if strings.HasSuffix(strings.TrimSpace(cmd), "&") {
		return Result{
			Decision:       tool.PermissionActionDeny,
			RiskLevel:      RiskLevelHigh,
			RuleID:         "RULE_BACKGROUND_PROCESS",
			Evidence:       "Background execution operator '&' detected",
			Recommendation: "Background process creation is restricted to prevent orphaned processes",
		}, true
	}

	// HostExec backend special checks
	if strings.EqualFold(req.Backend, BackendHostExec) {
		lowerCmd := strings.ToLower(cmd)
		if strings.Contains(lowerCmd, "top") || strings.Contains(lowerCmd, "htop") || strings.Contains(lowerCmd, "tail -f") {
			return Result{
				Decision:       tool.PermissionActionAsk,
				RiskLevel:      RiskLevelMedium,
				RuleID:         "RULE_HOSTEXEC_LONG_SESSION",
				Evidence:       "Interactive or long-running command detected under hostexec",
				Recommendation: "Requires human review before launching persistent terminal sessions",
			}, true
		}
	}

	return Result{}, false
}

func checkAskRulesAndDependencies(cmd string, policy *Policy) (Result, bool) {
	lowerCmd := strings.ToLower(cmd)
	for _, askRule := range policy.AskRules {
		if strings.Contains(lowerCmd, strings.ToLower(askRule)) {
			return Result{
				Decision:       tool.PermissionActionAsk,
				RiskLevel:      RiskLevelMedium,
				RuleID:         "RULE_REQUIRES_HUMAN_APPROVAL",
				Evidence:       fmt.Sprintf("Command matches approval rule %q", askRule),
				Recommendation: "Dependency installation or system package changes require human approval",
			}, true
		}
	}
	return Result{}, false
}

func checkResourceAbuse(cmd string) (Result, bool) {
	lowerCmd := strings.ToLower(cmd)

	// Infinite loop check
	if strings.Contains(lowerCmd, "while true") || strings.Contains(lowerCmd, "while :") || strings.Contains(lowerCmd, "for ((;;))") {
		return Result{
			Decision:       tool.PermissionActionDeny,
			RiskLevel:      RiskLevelHigh,
			RuleID:         "RULE_INFINITE_LOOP",
			Evidence:       "Infinite loop structure detected in command",
			Recommendation: "Command contains non-terminating loops which may exhaust resources",
		}, true
	}

	// Excessive sleep check (e.g. sleep 3600, sleep 1h, sleep 10m)
	if strings.HasPrefix(lowerCmd, "sleep ") {
		parts := strings.Fields(lowerCmd)
		if len(parts) >= 2 {
			val := parts[1]
			var totalSecs float64
			if dur, err := time.ParseDuration(val); err == nil {
				totalSecs = dur.Seconds()
			} else if secs, err := strconv.Atoi(val); err == nil {
				totalSecs = float64(secs)
			} else if strings.HasSuffix(val, "m") {
				if m, err := strconv.Atoi(strings.TrimSuffix(val, "m")); err == nil {
					totalSecs = float64(m * 60)
				}
			} else if strings.HasSuffix(val, "h") {
				if h, err := strconv.Atoi(strings.TrimSuffix(val, "h")); err == nil {
					totalSecs = float64(h * 3600)
				}
			} else if strings.HasSuffix(val, "d") {
				if d, err := strconv.Atoi(strings.TrimSuffix(val, "d")); err == nil {
					totalSecs = float64(d * 86400)
				}
			}

			if totalSecs > 300 {
				return Result{
					Decision:       tool.PermissionActionDeny,
					RiskLevel:      RiskLevelHigh,
					RuleID:         "RULE_EXCESSIVE_SLEEP",
					Evidence:       fmt.Sprintf("Sleep duration of %.0f seconds exceeds 300s limit", totalSecs),
					Recommendation: "Reduce sleep duration to avoid hanging tool execution",
				}, true
			}
		}
	}

	return Result{}, false
}
