//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// defaultRules returns the rule set covering all seven risk categories.
func defaultRules(p *Policy) ([]Rule, error) {
	sensitivePatterns, err := compilePatterns(p.SensitivePatterns)
	if err != nil {
		return nil, fmt.Errorf("compile sensitive patterns: %w", err)
	}

	forbiddenPaths := expandPaths(p.ForbiddenPaths)
	networkWhitelist := toLowerSlice(p.NetworkWhitelist)

	return []Rule{
		&dangerousCommandRule{forbiddenPaths: forbiddenPaths},
		&networkEgressRule{whitelist: networkWhitelist},
		&shellBypassRule{},
		&hostExecRiskRule{policy: p.BackendPolicies.HostExec},
		&dependencyRule{allowed: p.DependencyPolicy.AllowedManagers, denied: p.DependencyPolicy.DeniedPackages},
		&resourceAbuseRule{maxSleep: p.ResourceLimits.AllowedSleepSeconds},
		&sensitiveLeakRule{patterns: sensitivePatterns, forbiddenPaths: forbiddenPaths},
		&codeExecDangerRule{},
	}, nil
}

// ----------------------------------------------------------------
// Rule 1: Dangerous command
//
// Detects: rm -rf and access to forbidden paths (credentials, system
// files).  This rule fires after shellsafe has already accepted the
// command structurally, so it catches semantic dangers that the
// allow/deny list alone might miss (e.g. "cat ~/.ssh/id_rsa" where
// "cat" is allowed but the path is forbidden).
// ----------------------------------------------------------------

type dangerousCommandRule struct {
	forbiddenPaths []string
}

func (r *dangerousCommandRule) ID() string   { return "dangerous_command" }
func (r *dangerousCommandRule) Name() string { return "Dangerous Command" }

func (r *dangerousCommandRule) Check(_ context.Context, req *ScanRequest) *Risk {
	cmd := req.Command
	lower := strings.ToLower(cmd)

	// rm -rf pattern (especially targeting root or system dirs).
	if strings.Contains(lower, "rm ") && strings.Contains(lower, "-rf") {
		return &Risk{
			RuleID:      r.ID(),
			RuleName:    r.Name(),
			Level:       RiskCritical,
			Evidence:    fmt.Sprintf("destructive 'rm -rf' detected: %s", truncate(cmd, 80)),
			Suggestion:  "specify precise paths and avoid recursive force delete",
			ShouldBlock: true,
		}
	}

	// Forbidden path access.  Check both the expanded path (e.g.
	// /Users/x/.ssh) and the raw tilde form (~/.ssh) so commands
	// using the tilde shorthand are caught too.
	for _, fp := range r.forbiddenPaths {
		if pathMatches(cmd, fp) || pathMatches(cmd, rawTildePath(fp)) {
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskCritical,
				Evidence:    fmt.Sprintf("access to forbidden path %q detected", fp),
				Suggestion:  "do not read or write credential, secret, or system files",
				ShouldBlock: true,
			}
		}
	}

	// /dev/zero and /dev/urandom — resource abuse via infinite streams.
	if strings.Contains(lower, "/dev/zero") || strings.Contains(lower, "/dev/urandom") {
		return &Risk{
			RuleID:      r.ID(),
			RuleName:    r.Name(),
			Level:       RiskHigh,
			Evidence:    fmt.Sprintf("access to device stream %q", "/dev/zero or /dev/urandom"),
			Suggestion:  "avoid reading from infinite device streams",
			ShouldBlock: true,
		}
	}

	return nil
}

// rawTildePath converts an expanded path back to its tilde form so
// commands using ~/.ssh are matched even when the forbidden path was
// expanded to /Users/x/.ssh.
func rawTildePath(expanded string) string {
	home := getHomeDir()
	if home != "" && strings.HasPrefix(expanded, home) {
		return "~" + expanded[len(home):]
	}
	return expanded
}

// pathMatches checks whether cmd references a forbidden path. The
// forbidden path may be a glob pattern (* is treated as wildcard).
func pathMatches(cmd, forbidden string) bool {
	// Expand wildcards: "*.env" → check if cmd contains ".env".
	if strings.Contains(forbidden, "*") {
		stripped := strings.ReplaceAll(forbidden, "*", "")
		return strings.Contains(strings.ToLower(cmd), strings.ToLower(stripped))
	}
	return strings.Contains(strings.ToLower(cmd), strings.ToLower(forbidden))
}

// ----------------------------------------------------------------
// Rule 2: Network egress
//
// Detects network commands (curl, wget, nc, ssh, telnet, etc.)
// targeting hosts not in the whitelist.  Since shellsafe's implicit
// deny already blocks most of these tools, this rule primarily
// catches go get / go install / npm install patterns that reference
// non-whitelisted domains, as well as commands that embed URLs.
// ----------------------------------------------------------------

type networkEgressRule struct {
	whitelist []string
}

func (r *networkEgressRule) ID() string   { return "network_egress" }
func (r *networkEgressRule) Name() string { return "Network Egress" }

func (r *networkEgressRule) Check(_ context.Context, req *ScanRequest) *Risk {
	cmd := req.Command
	lower := strings.ToLower(cmd)

	// Extract URLs from the command.
	urls := extractURLs(lower)
	for _, u := range urls {
		host := extractHost(u)
		if host == "" {
			continue
		}
		if !isWhitelisted(host, r.whitelist) {
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskCritical,
				Evidence:    fmt.Sprintf("network egress to non-whitelisted host %q", host),
				Suggestion:  fmt.Sprintf("add %q to network_whitelist or use a whitelisted host", host),
				ShouldBlock: true,
			}
		}
	}

	// Detect network commands without URL (nc, ssh, telnet).
	networkCmds := []string{"nc ", "ncat ", "netcat ", "ssh ", "telnet ", "ftp "}
	for _, nc := range networkCmds {
		if strings.HasPrefix(lower, strings.TrimSpace(nc)) || strings.Contains(lower, " "+strings.TrimSpace(nc)) {
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskHigh,
				Evidence:    fmt.Sprintf("network command detected: %s", truncate(cmd, 80)),
				Suggestion:  "network commands require whitelisted hosts",
				ShouldBlock: true,
			}
		}
	}

	return nil
}

// extractURLs finds http(s) URLs in a command string.
func extractURLs(s string) []string {
	re := regexp.MustCompile(`https?://[^\s'"|;<>]+`)
	return re.FindAllString(s, -1)
}

// extractHost extracts the hostname from a URL string.
func extractHost(url string) string {
	// Strip scheme.
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	// Strip path.
	if i := strings.IndexAny(s, "/:"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

// isWhitelisted checks whether host matches any whitelist entry.
// Supports wildcard patterns like "*.github.com".
func isWhitelisted(host string, whitelist []string) bool {
	for _, w := range whitelist {
		if w == host {
			return true
		}
		if strings.HasPrefix(w, "*.") {
			suffix := w[1:] // ".github.com"
			if strings.HasSuffix(host, suffix) {
				return true
			}
		}
	}
	return false
}

// ----------------------------------------------------------------
// Rule 3: Shell bypass
//
// Detects attempts to bypass the shellsafe parser through shell
// wrappers.  Most wrappers (sh -c, bash -c, eval, exec) are already
// in shellsafe's implicit deny, so this rule catches cases that
// shellsafe's structural parser accepts but are still suspicious
// (e.g. commands that embed shell metacharacters in arguments).
// ----------------------------------------------------------------

type shellBypassRule struct{}

func (r *shellBypassRule) ID() string   { return "shell_bypass" }
func (r *shellBypassRule) Name() string { return "Shell Wrapper Bypass" }

func (r *shellBypassRule) Check(_ context.Context, req *ScanRequest) *Risk {
	// shellsafe already rejected $(), backticks, and redirections at
	// the parse stage.  If we reach here, the command parsed cleanly.
	// This rule is a secondary check for patterns that survived parse
	// but still look like bypass attempts.
	lower := strings.ToLower(req.Command)
	if strings.Contains(lower, "sh -c") || strings.Contains(lower, "bash -c") {
		return &Risk{
			RuleID:      r.ID(),
			RuleName:    r.Name(),
			Level:       RiskCritical,
			Evidence:    "shell wrapper with -c flag detected",
			Suggestion:  "execute commands directly without a shell wrapper",
			ShouldBlock: true,
		}
	}
	return nil
}

// ----------------------------------------------------------------
// Rule 4: Hostexec risk
//
// Detects risks specific to hostexec: privilege escalation (sudo/su
// are in shellsafe implicit deny, but we double-check), background
// processes, and long sessions.  Also enforces the
// RequireHumanReview policy for hostexec.
// ----------------------------------------------------------------

type hostExecRiskRule struct {
	policy BackendPolicy
}

func (r *hostExecRiskRule) ID() string   { return "hostexec_risk" }
func (r *hostExecRiskRule) Name() string { return "HostExec Session Risk" }

func (r *hostExecRiskRule) Check(_ context.Context, req *ScanRequest) *Risk {
	if req.Backend != BackendHostExec {
		return nil
	}

	// If hostexec requires human review, flag as ask.
	if r.policy.RequireHumanReview {
		return &Risk{
			RuleID:      r.ID(),
			RuleName:    r.Name(),
			Level:       RiskMedium,
			Evidence:    "hostexec backend requires human review for all commands",
			Suggestion:  "review the command before approving execution",
			ShouldBlock: false,
		}
	}

	// Background process detection.
	lower := strings.ToLower(req.Command)
	bgMarkers := []string{" &", "nohup ", "disown ", "setsid "}
	for _, marker := range bgMarkers {
		if strings.Contains(lower, marker) && !r.policy.AllowBackground {
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskHigh,
				Evidence:    fmt.Sprintf("background process marker %q detected", strings.TrimSpace(marker)),
				Suggestion:  "avoid background processes on hostexec or enable allow_background",
				ShouldBlock: true,
			}
		}
	}

	return nil
}

// ----------------------------------------------------------------
// Rule 5: Dependency install
//
// Detects package manager invocations (go install, npm install, pip
// install, apt install, etc.) and flags them for review.
// ----------------------------------------------------------------

type dependencyRule struct {
	allowed []string
	denied  []string
}

func (r *dependencyRule) ID() string   { return "dependency_install" }
func (r *dependencyRule) Name() string { return "Dependency Installation" }

func (r *dependencyRule) Check(_ context.Context, req *ScanRequest) *Risk {
	lower := strings.ToLower(req.Command)

	installPatterns := []string{
		"go install ", "go get ",
		"npm install ", "npm i ", "npm add ",
		"pip install ", "pip3 install ",
		"apt install ", "apt-get install ",
		"yum install ", "dnf install ",
		"brew install ",
	}

	for _, pat := range installPatterns {
		if strings.Contains(lower, pat) {
			// Check if the package manager is allowed.
			manager := strings.Fields(pat)[0]
			if !contains(r.allowed, manager) {
				return &Risk{
					RuleID:      r.ID(),
					RuleName:    r.Name(),
					Level:       RiskHigh,
					Evidence:    fmt.Sprintf("dependency installation via %q (not in allowed_managers)", manager),
					Suggestion:  fmt.Sprintf("add %q to allowed_managers or install manually", manager),
					ShouldBlock: true,
				}
			}
			// Manager is allowed — check denied packages before
			// returning the medium-risk "needs review" result.
			if len(r.denied) > 0 {
				pkgs := extractInstallPackages(lower, pat)
				for _, pkg := range pkgs {
					for _, denied := range r.denied {
						if strings.EqualFold(pkg, strings.ToLower(denied)) {
							return &Risk{
								RuleID:      r.ID(),
								RuleName:    r.Name(),
								Level:       RiskHigh,
								Evidence:    fmt.Sprintf("installation of denied package %q detected", denied),
								Suggestion:  fmt.Sprintf("remove %q from the install command or allow it explicitly", denied),
								ShouldBlock: true,
							}
						}
					}
				}
			}
			// Manager is allowed but still needs review.
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskMedium,
				Evidence:    fmt.Sprintf("dependency installation detected: %s", truncate(req.Command, 80)),
				Suggestion:  "review the package name and source before approving",
				ShouldBlock: false,
			}
		}
	}

	return nil
}

// ----------------------------------------------------------------
// Rule 6: Resource abuse
//
// Detects: long sleep, infinite loops (while true, for(;;)), and
// other patterns that could consume resources.
// ----------------------------------------------------------------

type resourceAbuseRule struct {
	maxSleep int
}

func (r *resourceAbuseRule) ID() string   { return "resource_abuse" }
func (r *resourceAbuseRule) Name() string { return "Resource Abuse" }

func (r *resourceAbuseRule) Check(_ context.Context, req *ScanRequest) *Risk {
	lower := strings.ToLower(req.Command)

	// Sleep duration check.  A maxSleep of 0 disables this check.
	if r.maxSleep > 0 {
		if dur := extractSleepSeconds(lower); dur > r.maxSleep {
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskHigh,
				Evidence:    fmt.Sprintf("sleep duration %ds exceeds allowed_sleep_seconds %d", dur, r.maxSleep),
				Suggestion:  "reduce the sleep duration or increase allowed_sleep_seconds",
				ShouldBlock: true,
			}
		}
	}

	// Infinite loop patterns.
	if strings.Contains(lower, "while true") || strings.Contains(lower, "for(;;)") || strings.Contains(lower, "for (;;)") {
		return &Risk{
			RuleID:      r.ID(),
			RuleName:    r.Name(),
			Level:       RiskHigh,
			Evidence:    "infinite loop pattern detected",
			Suggestion:  "add a termination condition or timeout",
			ShouldBlock: true,
		}
	}

	// /dev/zero (also caught by dangerous_command, but we check here
	// for codeexec backend where shellsafe is not applied).
	if strings.Contains(lower, "/dev/zero") {
		return &Risk{
			RuleID:      r.ID(),
			RuleName:    r.Name(),
			Level:       RiskHigh,
			Evidence:    "access to /dev/zero (infinite stream)",
			Suggestion:  "avoid reading from /dev/zero",
			ShouldBlock: true,
		}
	}

	return nil
}

// ----------------------------------------------------------------
// Rule 7: Sensitive leak
//
// Detects: API keys, tokens, passwords, private keys in commands,
// and attempts to read credential files.
// ----------------------------------------------------------------

type sensitiveLeakRule struct {
	patterns       []*regexp.Regexp
	forbiddenPaths []string
}

func (r *sensitiveLeakRule) ID() string   { return "sensitive_leak" }
func (r *sensitiveLeakRule) Name() string { return "Sensitive Information Leak" }

func (r *sensitiveLeakRule) Check(_ context.Context, req *ScanRequest) *Risk {
	// Check command against sensitive patterns.
	for _, re := range r.patterns {
		if match := re.FindString(req.Command); match != "" {
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskHigh,
				Evidence:    fmt.Sprintf("sensitive pattern matched: %s", truncate(match, 40)),
				Suggestion:  "do not embed secrets in commands; use environment variables or config files",
				ShouldBlock: true,
			}
		}
	}

	// Check for credential file access (for codeexec where shellsafe
	// is not applied, and as a secondary check for shell backends).
	lower := strings.ToLower(req.Command)
	for _, fp := range r.forbiddenPaths {
		if pathMatches(lower, fp) || pathMatches(lower, rawTildePath(fp)) {
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskCritical,
				Evidence:    fmt.Sprintf("access to sensitive path %q", fp),
				Suggestion:  "do not read credential or secret files",
				ShouldBlock: true,
			}
		}
	}

	return nil
}

// ----------------------------------------------------------------
// Rule 8: CodeExec danger
//
// Detects dangerous patterns in source code submitted to the
// codeexec backend (os.system, subprocess, exec, eval in Python;
// rm, curl, wget in bash).
// ----------------------------------------------------------------

type codeExecDangerRule struct{}

func (r *codeExecDangerRule) ID() string   { return "code_exec_danger" }
func (r *codeExecDangerRule) Name() string { return "Code Execution Danger" }

func (r *codeExecDangerRule) Check(_ context.Context, req *ScanRequest) *Risk {
	if req.Backend != BackendCodeExec {
		return nil
	}

	lower := strings.ToLower(req.Command)

	// Dangerous Python patterns.
	dangerPatterns := []string{
		"os.system(", "os.popen(", "subprocess.run(", "subprocess.call(",
		"subprocess.popen(", "os.exec", "os.remove(", "os.unlink(",
		"shutil.rmtree(",
	}
	for _, pat := range dangerPatterns {
		if strings.Contains(lower, pat) {
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskCritical,
				Evidence:    fmt.Sprintf("dangerous code pattern %q detected", pat),
				Suggestion:  "avoid executing shell commands or deleting files from code",
				ShouldBlock: true,
			}
		}
	}

	// Dangerous shell patterns in code.
	shellDangers := []string{"rm -rf", "rm -r /", "curl ", "wget ", "nc ", "ssh "}
	for _, pat := range shellDangers {
		if strings.Contains(lower, pat) {
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskCritical,
				Evidence:    fmt.Sprintf("dangerous shell pattern %q in code", pat),
				Suggestion:  "remove shell commands from code or use safe APIs",
				ShouldBlock: true,
			}
		}
	}

	return nil
}

// ----------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------

func compilePatterns(patterns []string) ([]*regexp.Regexp, error) {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("pattern %q: %w", p, err)
		}
		compiled = append(compiled, re)
	}
	return compiled, nil
}

func expandPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		out = append(out, expandTilde(p))
	}
	return out
}

func expandTilde(p string) string {
	return strings.ReplaceAll(p, "~", getHomeDir())
}

func getHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/root"
	}
	return home
}

func toLowerSlice(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strings.ToLower(s))
	}
	return out
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// extractInstallPackages returns the package name tokens found in cmd
// after the install pattern, stripping version specifiers (==, >=, @,
// etc.) and skipping flags (tokens starting with -).
func extractInstallPackages(cmd, pattern string) []string {
	idx := strings.Index(cmd, pattern)
	if idx < 0 {
		return nil
	}
	rest := cmd[idx+len(pattern):]
	var pkgs []string
	for _, tok := range strings.Fields(rest) {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		pkg := stripVersionSpecifier(tok)
		if pkg != "" {
			pkgs = append(pkgs, pkg)
		}
	}
	return pkgs
}

// stripVersionSpecifier removes version qualifiers from a package
// token (e.g. "pkg==1.0" → "pkg") and lower-cases the result.
func stripVersionSpecifier(pkg string) string {
	for _, sep := range []string{"==", ">=", "<=", "~=", "@", ">", "<"} {
		if idx := strings.Index(pkg, sep); idx >= 0 {
			pkg = pkg[:idx]
		}
	}
	return strings.ToLower(pkg)
}

// extractSleepSeconds parses "sleep N" (or "sleep Ns", "sleep Nm",
// "sleep Nh") from cmd and returns the duration in seconds.  Returns
// 0 if no sleep command is found.
func extractSleepSeconds(cmd string) int {
	re := regexp.MustCompile(`(?:^|\s)sleep\s+(\d+)([smhd]?)`)
	m := re.FindStringSubmatch(cmd)
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	switch m[2] {
	case "m":
		n *= 60
	case "h":
		n *= 3600
	case "d":
		n *= 86400
	}
	return n
}
