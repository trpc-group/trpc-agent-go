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
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// defaultRules returns the rule set covering all seven risk categories.
func defaultRules(p *Policy) ([]Rule, error) {
	sensitivePatterns, err := compilePatterns(p.SensitivePatterns)
	if err != nil {
		return nil, fmt.Errorf("compile sensitive patterns: %w", err)
	}

	forbiddenPaths, err := buildForbiddenPaths(p.ForbiddenPaths)
	if err != nil {
		return nil, fmt.Errorf("build forbidden paths: %w", err)
	}
	networkWhitelist := toLowerSlice(p.NetworkWhitelist)

	return []Rule{
		&dangerousCommandRule{forbiddenPaths: forbiddenPaths},
		&networkEgressRule{whitelist: networkWhitelist},
		&shellBypassRule{},
		&hostExecRiskRule{policies: p.BackendPolicies},
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
	forbiddenPaths []forbiddenPath
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

	// Forbidden path access.  Matching operates on the parsed argv with
	// each path argument normalized (quote-stripping, filepath.Clean and
	// tilde expansion), never on raw substrings of the command text, so
	// shell-equivalent spellings like /etc/"shadow", /etc//shadow and
	// /etc/../etc/shadow are all caught by a configured /etc/shadow.
	if fp := matchForbidden(req, r.forbiddenPaths); fp != "" {
		return &Risk{
			RuleID:      r.ID(),
			RuleName:    r.Name(),
			Level:       RiskCritical,
			Evidence:    fmt.Sprintf("access to forbidden path %q detected", fp),
			Suggestion:  "do not read or write credential, secret, or system files",
			ShouldBlock: true,
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

	// Extract URLs from the command.  This covers any command, including
	// interpreters that embed a URL in an argument (python, ruby, ...).
	urls := extractURLs(lower)
	for _, u := range urls {
		host := extractHost(u)
		if host == "" {
			continue
		}
		if !isWhitelisted(host, r.whitelist) {
			return egressRisk(r, host)
		}
	}

	// Extract scheme-less host arguments from known network-capable
	// commands (curl, wget) and package managers (go get / go install,
	// pip install, npm install).  A scheme-less host is matched against
	// the whitelist just like a URL host, so an explicitly whitelisted
	// host passes here instead of falling through to the policy default.
	for _, host := range extractSchemelessHosts(cmd, req.Backend) {
		if !isWhitelisted(host, r.whitelist) {
			return egressRisk(r, host)
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

// egressRisk is the risk returned when a command targets a host that is
// not in the network whitelist.
func egressRisk(r *networkEgressRule, host string) *Risk {
	return &Risk{
		RuleID:      r.ID(),
		RuleName:    r.Name(),
		Level:       RiskCritical,
		Evidence:    fmt.Sprintf("network egress to non-whitelisted host %q", host),
		Suggestion:  fmt.Sprintf("add %q to network_whitelist or use a whitelisted host", host),
		ShouldBlock: true,
	}
}

// extractSchemelessHosts returns the lower-cased hosts that cmd targets
// through a network-capable command without a URL scheme.  It inspects
// the parsed argv so quote-stripping matches the rest of the rule set.
//
// Only commands that consume a host / package target positionally are
// inspected, so an arbitrary command's ordinary arguments (file names,
// local paths) are never mistaken for a host.  A target is treated as a
// host only when its first path segment looks like a domain (contains a
// dot), which prevents a bare package name such as "pip install requests"
// from being flagged as egress.
func extractSchemelessHosts(cmd string, backend Backend) []string {
	args := commandArgs(cmd, backend)
	if len(args) == 0 {
		return nil
	}
	targetIdx := hostTargetIndex(args)
	if targetIdx < 0 {
		return nil
	}
	var hosts []string
	for _, arg := range args[targetIdx:] {
		if arg == "" || strings.HasPrefix(arg, "-") {
			continue
		}
		if h := hostFromTarget(arg); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// hostTargetIndex returns the argv index at which host-bearing targets
// begin for the leading command, or -1 when the command is not a
// recognized network client or package manager.  The leading command is
// matched on its basename so "/usr/bin/curl ..." is recognized the same
// as "curl ...".
func hostTargetIndex(args []string) int {
	cmd := basename(strings.ToLower(args[0]))
	switch cmd {
	case "curl", "wget":
		return 1
	case "go":
		if len(args) >= 2 {
			switch strings.ToLower(args[1]) {
			case "get", "install":
				return 2
			}
		}
	case "pip", "pip3", "pipenv":
		if len(args) >= 2 && strings.ToLower(args[1]) == "install" {
			return 2
		}
	case "npm", "pnpm", "yarn":
		if len(args) >= 2 {
			switch strings.ToLower(args[1]) {
			case "install", "i", "add":
				return 2
			}
		}
	}
	return -1
}

// hostFromTarget extracts a hostname from a target argument, handling
// both scheme-full URLs and scheme-less hosts.  It returns "" when the
// target does not carry a recognizable host, so ordinary package names
// and local arguments are not treated as egress targets.
func hostFromTarget(arg string) string {
	arg = strings.ToLower(arg)
	// Scheme-full URL: delegate to the existing host extractor.
	if strings.HasPrefix(arg, "http://") || strings.HasPrefix(arg, "https://") {
		return extractHost(arg)
	}
	// Scheme-less: take the first path segment, dropping any version
	// specifier (@v1.0), userinfo, or port that follows.
	host := arg
	if i := strings.IndexAny(host, "/:@"); i >= 0 {
		host = host[:i]
	}
	if host == "" {
		return ""
	}
	// Only a segment that looks like a domain is a host candidate; a
	// bare word such as "requests" is not.
	if !strings.Contains(host, ".") {
		return ""
	}
	return host
}

// basename returns the final path element of name, lower-cased.
func basename(name string) string {
	if i := strings.LastIndexAny(name, "/\\"); i >= 0 {
		return name[i+1:]
	}
	return name
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
// Detects risks specific to the hostexec and workspaceexec backends:
// background processes and long-running sessions.  Also enforces the
// RequireHumanReview policy for hostexec.
//
// Background detection is driven by the structured "background" boolean
// that both backends expose in their argument shape.  A request carrying
// background: true is denied when allow_background is false, regardless of
// whether the command text contains a background marker.  This closes the
// bypass where background: true plus a command with no textual marker
// slipped past allow_background: false.  The textual markers ("&",
// "nohup", "disown", "setsid") remain a fallback for the direct
// ScanCommand path, where no structured argument is available.
//
// Background denies take precedence over the require_human_review ask, so
// allow_background: false remains enforceable under the default policy,
// where hostexec defaults to require_human_review: true.
// ----------------------------------------------------------------

type hostExecRiskRule struct {
	policies BackendPolicies
}

func (r *hostExecRiskRule) ID() string   { return "hostexec_risk" }
func (r *hostExecRiskRule) Name() string { return "HostExec Session Risk" }

// policyFor returns the per-backend policy that applies to req.Backend.
// Both hostexec and workspaceexec are governed by their own BackendPolicy;
// the rule selects the matching one so allow_background and
// require_human_review are honored per backend.
func (r *hostExecRiskRule) policyFor(req *ScanRequest) BackendPolicy {
	if req.Backend == BackendWorkspaceExec {
		return r.policies.WorkspaceExec
	}
	return r.policies.HostExec
}

func (r *hostExecRiskRule) Check(_ context.Context, req *ScanRequest) *Risk {
	if req.Backend != BackendHostExec && req.Backend != BackendWorkspaceExec {
		return nil
	}
	policy := r.policyFor(req)

	// Background risks are hard denies and take precedence over the
	// softer ask from RequireHumanReview, so allow_background: false
	// stays enforceable even when the backend policy also requires
	// human review.  Checking RequireHumanReview first would mask the
	// background deny under the default policy (hostexec defaults to
	// require_human_review: true).
	if !policy.AllowBackground {
		// Structured "background" flag: authoritative.  The adapter
		// reads it from the tool's JSON arguments, so a
		// background:true request with a clean command string is
		// still caught here.
		if req.Background {
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskHigh,
				Evidence:    "background process requested via 'background': true",
				Suggestion:  "avoid background processes or enable allow_background",
				ShouldBlock: true,
			}
		}

		// Textual fallback for the direct ScanCommand path (no
		// structured argument): a command that textually requests
		// background execution is still a background risk.
		lower := strings.ToLower(req.Command)
		bgMarkers := []string{" &", "nohup ", "disown ", "setsid "}
		for _, marker := range bgMarkers {
			if strings.Contains(lower, marker) {
				return &Risk{
					RuleID:      r.ID(),
					RuleName:    r.Name(),
					Level:       RiskHigh,
					Evidence:    fmt.Sprintf("background process marker %q detected", strings.TrimSpace(marker)),
					Suggestion:  "avoid background processes or enable allow_background",
					ShouldBlock: true,
				}
			}
		}
	}

	// If the backend requires human review, flag as ask.  This is
	// checked after the background denies so a deny is never downgraded
	// to an ask when both policies apply.
	if policy.RequireHumanReview {
		return &Risk{
			RuleID:      r.ID(),
			RuleName:    r.Name(),
			Level:       RiskMedium,
			Evidence:    "hostexec backend requires human review for all commands",
			Suggestion:  "review the command before approving execution",
			ShouldBlock: false,
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
	// Match on the parsed argv (executable basename + subcommand) rather
	// than a raw single-space substring of the command text, so
	// shell-equivalent spellings like "pip  install" (double space) and
	// "pip\tinstall" (tab) are detected the same as "pip install".  This
	// also guards against over-broad detection: "echo pip install ..."
	// has echo as its executable and is not an install.
	args := commandArgs(req.Command, req.Backend)
	if len(args) < 2 {
		return nil
	}
	manager := basename(strings.ToLower(args[0]))
	if !isInstallSubcommand(manager, strings.ToLower(args[1])) {
		return nil
	}

	// Check if the package manager is allowed.
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
	// Manager is allowed — check denied packages before returning the
	// medium-risk "needs review" result.
	if len(r.denied) > 0 {
		for _, pkg := range installPackages(args[2:]) {
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
	forbiddenPaths []forbiddenPath
}

func (r *sensitiveLeakRule) ID() string   { return "sensitive_leak" }
func (r *sensitiveLeakRule) Name() string { return "Sensitive Information Leak" }

func (r *sensitiveLeakRule) Check(_ context.Context, req *ScanRequest) *Risk {
	// Check command against sensitive patterns.  The evidence is a
	// generic description rather than the matched bytes so the secret
	// itself is never echoed into the report, the recommendation, or the
	// model-visible permission decision.  We only need to know a secret
	// was present, not what it was.
	for _, re := range r.patterns {
		if re.MatchString(req.Command) {
			return &Risk{
				RuleID:      r.ID(),
				RuleName:    r.Name(),
				Level:       RiskHigh,
				Evidence:    "matched sensitive pattern (e.g. API key, token, or password)",
				Suggestion:  "do not embed secrets in commands; use environment variables or config files",
				ShouldBlock: true,
			}
		}
	}

	// Check for credential file access (for codeexec where shellsafe
	// is not applied, and as a secondary check for shell backends).
	// Matching uses the same normalized-argv semantics as the
	// dangerous_command rule.
	if fp := matchForbidden(req, r.forbiddenPaths); fp != "" {
		return &Risk{
			RuleID:      r.ID(),
			RuleName:    r.Name(),
			Level:       RiskCritical,
			Evidence:    fmt.Sprintf("access to sensitive path %q", fp),
			Suggestion:  "do not read credential or secret files",
			ShouldBlock: true,
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

// forbiddenPath is a compiled forbidden-path entry.  Non-glob entries are
// compared against normalized command arguments as exact paths or as a
// parent directory; glob entries (those containing '*') are matched against
// the whole normalized argument, where '*' spans path separators so
// "*.env" matches "config/app.env" and "/secret/*/key" matches
// "/secret/user/key".
type forbiddenPath struct {
	pattern string         // normalized path or glob pattern
	glob    *regexp.Regexp // compiled glob matcher, nil when pattern has no '*'
}

// buildForbiddenPaths expands "~" and cleans each configured forbidden path,
// compiling any glob patterns up front so the per-request scan only performs
// cheap regexp matches.
func buildForbiddenPaths(paths []string) ([]forbiddenPath, error) {
	fps := make([]forbiddenPath, 0, len(paths))
	for _, p := range paths {
		norm := normalizePath(p)
		fp := forbiddenPath{pattern: norm}
		if strings.Contains(norm, "*") {
			re, err := globToRegexp(norm)
			if err != nil {
				return nil, fmt.Errorf("compile forbidden path %q: %w", p, err)
			}
			fp.glob = re
		}
		fps = append(fps, fp)
	}
	return fps, nil
}

// matchForbidden returns the pattern of the first forbidden path that the
// request's command references, or "" if none.  Matching operates on the
// parsed argv (quotes stripped by shellsafe) with each path argument
// normalized via normalizePath, never on raw substrings of the command text.
func matchForbidden(req *ScanRequest, forbidden []forbiddenPath) string {
	args := commandArgs(req.Command, req.Backend)
	for _, fp := range forbidden {
		for _, arg := range args {
			if matchForbiddenArg(normalizePath(arg), fp) {
				return fp.pattern
			}
		}
	}
	return ""
}

// matchForbiddenArg reports whether the normalized argument references the
// forbidden path entry.  Non-glob entries match the argument itself or any
// path beneath it at a directory boundary; glob entries match the whole
// argument.
func matchForbiddenArg(arg string, fp forbiddenPath) bool {
	if fp.glob != nil {
		return fp.glob.MatchString(arg)
	}
	return arg == fp.pattern || isUnderPath(arg, fp.pattern)
}

// isUnderPath reports whether path equals dir or sits beneath it at a
// directory boundary (dir itself, dir/sub, dir/sub/file, ...).  A sibling
// like "/etc/shadow.safe" is deliberately not beneath "/etc/shadow".
func isUnderPath(path, dir string) bool {
	if path == dir {
		return true
	}
	return strings.HasPrefix(path, dir+string(filepath.Separator))
}

// globToRegexp converts a glob pattern to an anchored regexp in which '*'
// matches any sequence of characters, including path separators.
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteByte('^')
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '.', '+', '(', ')', '[', ']', '{', '}', '^', '$', '|', '\\', '?':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('$')
	return regexp.Compile(b.String())
}

// commandArgs returns the path-relevant argument tokens of a command.  For
// shell backends it uses shellsafe's argv, which already strips quotes; for
// codeexec it falls back to tokenizing the source text so string literals
// referencing a forbidden path are still inspected.
func commandArgs(command string, backend Backend) []string {
	if backend == BackendCodeExec {
		return tokenizeCode(command)
	}
	pipe, err := shellsafe.Parse(command)
	if err != nil {
		return nil
	}
	var args []string
	for _, seg := range pipe.Commands {
		args = append(args, seg...)
	}
	return args
}

// tokenizeCode splits source text into tokens on whitespace and the
// characters that commonly delimit string literals and call arguments, so a
// reference like open('/etc/passwd') yields the "/etc/passwd" token.
func tokenizeCode(src string) []string {
	return strings.FieldsFunc(src, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' ||
			r == '\'' || r == '"' || r == '(' || r == ')' || r == ','
	})
}

// normalizePath expands a leading "~" and cleans the path so
// "/etc//shadow" and "/etc/../etc/shadow" both normalize to "/etc/shadow".
func normalizePath(p string) string {
	return filepath.Clean(expandTilde(p))
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

// installSubcommands maps each package manager to the subcommands that
// trigger a dependency installation.  Matching is done on the parsed
// argv so non-canonical whitespace does not defeat detection.
var installSubcommands = map[string][]string{
	"go":      {"get", "install"},
	"npm":     {"install", "i", "add"},
	"pip":     {"install"},
	"pip3":    {"install"},
	"apt":     {"install"},
	"apt-get": {"install"},
	"yum":     {"install"},
	"dnf":     {"install"},
	"brew":    {"install"},
}

// isInstallSubcommand reports whether sub is an install subcommand of
// the given package manager.
func isInstallSubcommand(manager, sub string) bool {
	for _, s := range installSubcommands[manager] {
		if s == sub {
			return true
		}
	}
	return false
}

// installPackages returns the package name tokens found in args (the
// argv after the manager subcommand), stripping version specifiers
// (==, >=, @, etc.) and skipping flags (tokens starting with -).
func installPackages(args []string) []string {
	var pkgs []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if pkg := stripVersionSpecifier(arg); pkg != "" {
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
