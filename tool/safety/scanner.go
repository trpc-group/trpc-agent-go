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
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// DefaultScanner implements Scanner with the package policy rules.
type DefaultScanner struct {
	policy Policy
}

// NewDefaultScanner creates a scanner from policy.
func NewDefaultScanner(policy Policy) (*DefaultScanner, error) {
	p := policy.WithDefaults()
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &DefaultScanner{policy: p}, nil
}

// MustDefaultScanner creates a scanner and panics on invalid policy.
func MustDefaultScanner(policy Policy) *DefaultScanner {
	scanner, err := NewDefaultScanner(policy)
	if err != nil {
		panic(err)
	}
	return scanner
}

// Scan scans one request.
func (s *DefaultScanner) Scan(ctx context.Context, req ScanRequest) (Report, error) {
	start := time.Now()
	if s == nil {
		s = MustDefaultScanner(Policy{})
	}
	if req.Backend == "" {
		req.Backend = BackendUnknown
	}
	findings := s.scanRequest(ctx, req)
	report := buildReport(req, findings, time.Since(start))
	report.Command, report.Redacted = s.redactReportText(report.Command)
	if report.Evidence != "" {
		var redacted bool
		report.Evidence, redacted = s.redactReportText(report.Evidence)
		report.Redacted = report.Redacted || redacted
	}
	for i := range report.Findings {
		var redacted bool
		report.Findings[i].Evidence, redacted = s.redactReportText(report.Findings[i].Evidence)
		report.Findings[i].Redacted = report.Findings[i].Redacted || redacted
		report.Redacted = report.Redacted || report.Findings[i].Redacted
	}
	return report, nil
}

func (s *DefaultScanner) scanRequest(ctx context.Context, req ScanRequest) []Finding {
	select {
	case <-ctx.Done():
		return []Finding{{
			RuleID:         "scanner.context_cancelled",
			RiskLevel:      RiskMedium,
			Decision:       DecisionAsk,
			Evidence:       "context cancelled before scan completed",
			Recommendation: "retry the scan with an active context",
		}}
	default:
	}
	var findings []Finding
	findings = append(findings, s.scanSize(req)...)
	findings = append(findings, s.scanEnv(req.Env)...)
	findings = append(findings, s.scanCwd(req.Cwd)...)
	switch {
	case req.Command != "":
		findings = append(findings, s.scanCommand(req)...)
	case req.Code != "":
		findings = append(findings, s.scanCode(req)...)
	case len(req.Arguments) > 0:
		findings = append(findings, s.scanUnknownArguments(req)...)
	}
	if req.TimeoutSec > 0 && s.policy.MaxTimeoutSec > 0 &&
		req.TimeoutSec > s.policy.MaxTimeoutSec {
		decision := DecisionAsk
		if req.Backend == BackendHost {
			decision = DecisionDeny
		}
		findings = append(findings, Finding{
			RuleID:         "resource.long_running",
			RiskLevel:      RiskHigh,
			Decision:       decision,
			Evidence:       fmt.Sprintf("timeout_sec=%d exceeds max_timeout_sec=%d", req.TimeoutSec, s.policy.MaxTimeoutSec),
			Recommendation: "lower the timeout or require human approval",
		})
	}
	if req.Backend == BackendHost && req.TTY {
		findings = append(findings, Finding{
			RuleID:         "host.pty_session",
			RiskLevel:      RiskHigh,
			Decision:       DecisionAsk,
			Evidence:       "host command requested a PTY session",
			Recommendation: "review interactive host sessions before execution",
		})
	}
	if req.Backend == BackendHost && req.Background {
		findings = append(findings, Finding{
			RuleID:         "host.background_process",
			RiskLevel:      RiskHigh,
			Decision:       DecisionAsk,
			Evidence:       "host command requested background execution",
			Recommendation: "ensure the host process has cleanup and bounded lifetime",
		})
	}
	return findings
}

func (s *DefaultScanner) scanCwd(cwd string) []Finding {
	if strings.TrimSpace(cwd) == "" {
		return nil
	}
	for _, denied := range s.policy.DeniedPaths {
		if !sensitivePathMatch(cwd, denied) {
			continue
		}
		rule := "path.sensitive_credentials"
		if strings.Contains(strings.ToLower(denied), ".env") {
			rule = "path.secret_file"
		}
		return []Finding{{
			RuleID:         rule,
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       "<redacted>",
			Recommendation: "do not run tool calls from credential or secret directories",
			Redacted:       true,
		}}
	}
	return nil
}

func (s *DefaultScanner) scanSize(req ScanRequest) []Finding {
	var findings []Finding
	if s.policy.MaxCommandBytes > 0 && len(req.Command) > s.policy.MaxCommandBytes {
		findings = append(findings, Finding{
			RuleID:         "command.too_large",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       fmt.Sprintf("command has %d bytes", len(req.Command)),
			Recommendation: "review long generated commands manually",
		})
	}
	if s.policy.MaxScriptBytes > 0 && len(req.Code) > s.policy.MaxScriptBytes {
		findings = append(findings, Finding{
			RuleID:         "script.too_large",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       fmt.Sprintf("script has %d bytes", len(req.Code)),
			Recommendation: "review large scripts manually",
		})
	}
	return findings
}

func (s *DefaultScanner) scanEnv(env map[string]string) []Finding {
	var findings []Finding
	for key, value := range env {
		if looksSecretName(key) || containsSecret(value) {
			findings = append(findings, Finding{
				RuleID:         "secret.env_value",
				RiskLevel:      RiskCritical,
				Decision:       s.policy.SecretAction,
				Evidence:       key + "=<redacted>",
				Recommendation: "remove secrets from tool environment variables",
				Redacted:       true,
			})
		}
	}
	return findings
}

func (s *DefaultScanner) scanCommand(req ScanRequest) []Finding {
	command := strings.TrimSpace(req.Command)
	if command == "" {
		return nil
	}
	pipe, err := shellsafe.Parse(command)
	if err != nil {
		return []Finding{s.shellParseFinding(req, err)}
	}
	var findings []Finding
	if policyErr := shellsafe.PolicyFromLists(
		s.policy.AllowedCommands,
		s.policy.DeniedCommands,
	).Check(pipe); policyErr != nil {
		findings = append(findings, s.commandPolicyFinding(policyErr))
	}
	for _, argv := range pipe.Commands {
		findings = append(findings, s.scanArgv(req, argv)...)
	}
	if req.Stdin != "" {
		stdinReq := req
		stdinReq.Command = req.Stdin
		stdinFindings := s.scanTextForUnknownRisk(stdinReq, req.Stdin)
		for i := range stdinFindings {
			if req.Backend == BackendHost && stdinFindings[i].Decision == DecisionAsk {
				stdinFindings[i].Decision = DecisionDeny
			}
		}
		findings = append(findings, stdinFindings...)
	}
	return findings
}

func (s *DefaultScanner) shellParseFinding(req ScanRequest, err error) Finding {
	msg := err.Error()
	decision := s.policy.UnparseableShellAction
	if req.Backend == BackendHost {
		decision = s.policy.HostUnparseableAction
	}
	rule := "shell.unparseable"
	risk := RiskMedium
	if strings.Contains(msg, "$") ||
		strings.Contains(msg, "substitution") ||
		strings.Contains(msg, "expansion") ||
		strings.Contains(msg, "redirection") {
		rule = "shell.expansion"
		risk = RiskHigh
		decision = DecisionDeny
	}
	redacted := containsSecret(req.Command) ||
		s.commandMentionsDeniedPath(req.Command)
	return Finding{
		RuleID:         rule,
		RiskLevel:      risk,
		Decision:       decision,
		Evidence:       msg,
		Recommendation: "rewrite the command as a simple argv pipeline or require review",
		Redacted:       redacted,
	}
}

func (s *DefaultScanner) commandPolicyFinding(err error) Finding {
	msg := err.Error()
	rule := "command.policy"
	if strings.Contains(msg, "built-in policy") ||
		strings.Contains(msg, "shell wrapper") {
		rule = "shell.wrapper"
	}
	return Finding{
		RuleID:         rule,
		RiskLevel:      RiskHigh,
		Decision:       DecisionDeny,
		Evidence:       msg,
		Recommendation: "use a direct audited command instead of a denied wrapper or command",
	}
}

func (s *DefaultScanner) scanArgv(req ScanRequest, argv []string) []Finding {
	if len(argv) == 0 {
		return nil
	}
	cmd := normalizeCommand(argv[0])
	var findings []Finding
	findings = append(findings, s.scanDangerousDelete(cmd, argv)...)
	findings = append(findings, s.scanSensitivePaths(req, argv)...)
	findings = append(findings, s.scanNetwork(cmd, argv)...)
	findings = append(findings, s.scanDependencyInstall(cmd, argv)...)
	findings = append(findings, s.scanResourceAbuse(cmd, argv)...)
	findings = append(findings, s.scanTextForUnknownRisk(req, strings.Join(argv, " "))...)
	return findings
}

func (s *DefaultScanner) scanDangerousDelete(cmd string, argv []string) []Finding {
	if cmd != "rm" && cmd != "rmdir" && cmd != "del" && cmd != "erase" &&
		cmd != "format" {
		return nil
	}
	joined := strings.Join(argv[1:], " ")
	if strings.Contains(joined, "-rf") || strings.Contains(joined, "-fr") ||
		strings.Contains(joined, "/") || strings.Contains(joined, "\\") {
		return []Finding{{
			RuleID:         "command.dangerous_delete",
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       strings.Join(argv, " "),
			Recommendation: "avoid recursive or system-path deletion in tool calls",
		}}
	}
	return []Finding{{
		RuleID:         "command.delete",
		RiskLevel:      RiskHigh,
		Decision:       DecisionAsk,
		Evidence:       strings.Join(argv, " "),
		Recommendation: "review destructive file deletion before execution",
	}}
}

func (s *DefaultScanner) scanSensitivePaths(req ScanRequest, argv []string) []Finding {
	var findings []Finding
	for _, arg := range argv[1:] {
		for _, denied := range s.policy.DeniedPaths {
			if sensitivePathMatch(arg, denied) || sensitivePathMatch(joinCwdPath(req.Cwd, arg), denied) {
				rule := "path.sensitive_credentials"
				if strings.Contains(strings.ToLower(denied), ".env") {
					rule = "path.secret_file"
				}
				findings = append(findings, Finding{
					RuleID:         rule,
					RiskLevel:      RiskCritical,
					Decision:       DecisionDeny,
					Evidence:       "<redacted>",
					Recommendation: "do not read credential or secret files through tools",
					Redacted:       true,
				})
			}
		}
	}
	return findings
}

func (s *DefaultScanner) scanNetwork(cmd string, argv []string) []Finding {
	if !isNetworkCommand(cmd) {
		return nil
	}
	text := strings.Join(argv, " ")
	hosts := extractHosts(text)
	if len(hosts) == 0 {
		decision := DecisionAsk
		if cmd == "nc" || cmd == "netcat" || cmd == "ssh" || cmd == "scp" {
			decision = DecisionDeny
		}
		return []Finding{{
			RuleID:         "network.external_tool",
			RiskLevel:      RiskHigh,
			Decision:       decision,
			Evidence:       text,
			Recommendation: "review network-capable commands and prefer allowlisted hosts",
		}}
	}
	var findings []Finding
	for _, host := range hosts {
		if s.hostAllowed(host) {
			continue
		}
		decision := DecisionAsk
		rule := "network.external_domain"
		if len(s.policy.NetworkAllowlist) > 0 {
			decision = DecisionDeny
			rule = "network.non_allowlisted_domain"
		}
		if isPrivateHost(host) {
			rule = "network.private_address"
			if decision == DecisionDeny {
				decision = DecisionAsk
			}
		}
		findings = append(findings, Finding{
			RuleID:         rule,
			RiskLevel:      RiskHigh,
			Decision:       decision,
			Evidence:       host,
			Recommendation: "add the host to network_allowlist or require human review",
		})
	}
	return findings
}

func (s *DefaultScanner) scanDependencyInstall(cmd string, argv []string) []Finding {
	if !isDependencyInstall(cmd, argv) {
		return nil
	}
	return []Finding{{
		RuleID:         "dependency.install",
		RiskLevel:      RiskHigh,
		Decision:       s.policy.DependencyInstallAction,
		Evidence:       strings.Join(argv, " "),
		Recommendation: "pin and review dependency installation before execution",
	}}
}

func (s *DefaultScanner) scanResourceAbuse(cmd string, argv []string) []Finding {
	var findings []Finding
	if cmd == "sleep" && len(argv) > 1 {
		if n, err := strconv.Atoi(argv[1]); err == nil &&
			s.policy.MaxTimeoutSec > 0 && n > s.policy.MaxTimeoutSec {
			decision := DecisionAsk
			risk := RiskHigh
			if n > s.policy.MaxTimeoutSec*10 {
				decision = DecisionDeny
				risk = RiskCritical
			}
			findings = append(findings, Finding{
				RuleID:         "resource.long_running",
				RiskLevel:      risk,
				Decision:       decision,
				Evidence:       strings.Join(argv, " "),
				Recommendation: "use bounded execution time or require approval",
			})
		}
	}
	joined := strings.ToLower(strings.Join(argv, " "))
	if cmd == "yes" ||
		(strings.Contains(joined, "yes") &&
			strings.Contains(joined, "head")) {
		findings = append(findings, Finding{
			RuleID:         "resource.large_output",
			RiskLevel:      RiskHigh,
			Decision:       DecisionAsk,
			Evidence:       strings.Join(argv, " "),
			Recommendation: "cap output size before running high-volume commands",
		})
	}
	return findings
}

func (s *DefaultScanner) scanCode(req ScanRequest) []Finding {
	lang := strings.ToLower(strings.TrimSpace(req.Language))
	if lang == "" {
		return []Finding{{
			RuleID:         "codeexec.unsupported_language",
			RiskLevel:      RiskMedium,
			Decision:       DecisionAsk,
			Evidence:       "missing language",
			Recommendation: "review code blocks with missing language metadata",
		}}
	}
	var findings []Finding
	switch lang {
	case "bash", "sh", "shell":
		for _, line := range strings.Split(req.Code, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			lineReq := req
			lineReq.Command = line
			lineReq.Code = ""
			findings = append(findings, s.scanCommand(lineReq)...)
		}
	case "python":
		findings = append(findings, s.scanTextForUnknownRisk(req, req.Code)...)
		if strings.Contains(req.Code, "subprocess") ||
			strings.Contains(req.Code, "os.system") {
			findings = append(findings, Finding{
				RuleID:         "codeexec.subprocess",
				RiskLevel:      RiskHigh,
				Decision:       DecisionAsk,
				Evidence:       "python subprocess or os.system usage",
				Recommendation: "review subprocess execution inside code blocks",
			})
		}
	case "go", "javascript", "typescript", "node":
		findings = append(findings, s.scanTextForUnknownRisk(req, req.Code)...)
	default:
		findings = append(findings, Finding{
			RuleID:         "codeexec.unsupported_language",
			RiskLevel:      RiskMedium,
			Decision:       DecisionAsk,
			Evidence:       lang,
			Recommendation: "review unsupported code execution languages manually",
		})
	}
	return findings
}

func (s *DefaultScanner) scanUnknownArguments(req ScanRequest) []Finding {
	return s.scanTextForUnknownRisk(req, string(req.Arguments))
}

func (s *DefaultScanner) scanTextForUnknownRisk(req ScanRequest, text string) []Finding {
	var findings []Finding
	lower := strings.ToLower(text)
	if containsSecret(text) {
		findings = append(findings, Finding{
			RuleID:         "secret.inline_value",
			RiskLevel:      RiskCritical,
			Decision:       s.policy.SecretAction,
			Evidence:       "<redacted>",
			Recommendation: "remove secrets before executing or auditing tool calls",
			Redacted:       true,
		})
	}
	if strings.Contains(lower, "-----begin") &&
		strings.Contains(lower, "private key-----") {
		findings = append(findings, Finding{
			RuleID:         "secret.private_key",
			RiskLevel:      RiskCritical,
			Decision:       s.policy.SecretAction,
			Evidence:       "<redacted>",
			Recommendation: "never pass private keys through tool execution",
			Redacted:       true,
		})
	}
	if req.Backend == BackendUnknown &&
		(strings.Contains(lower, "download") || strings.Contains(lower, "curl ") ||
			strings.Contains(lower, "wget ") || strings.Contains(lower, "http://") ||
			strings.Contains(lower, "https://")) {
		findings = append(findings, Finding{
			RuleID:         "unknown.requires_review",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       "unknown tool contains downloader or URL-like content",
			Recommendation: "review unknown open-world tools before execution",
		})
	}
	if req.Backend == BackendUnknown && containsDangerousCommandText(lower) {
		findings = append(findings, Finding{
			RuleID:         "unknown.dangerous_command",
			RiskLevel:      RiskCritical,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       "unknown tool contains dangerous command-like content",
			Recommendation: "review unknown open-world tools before execution",
		})
	}
	if req.Backend == BackendUnknown && s.textMentionsDeniedPath(text) {
		findings = append(findings, Finding{
			RuleID:         "unknown.sensitive_path",
			RiskLevel:      RiskCritical,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       "<redacted>",
			Recommendation: "review unknown tools that reference credential or secret paths",
			Redacted:       true,
		})
	}
	return findings
}

func buildReport(req ScanRequest, findings []Finding, dur time.Duration) Report {
	report := Report{
		ToolName:       req.ToolName,
		ToolCallID:     req.ToolCallID,
		Backend:        req.Backend,
		Command:        req.Command,
		Decision:       DecisionAllow,
		RiskLevel:      RiskLow,
		RuleID:         "evaluation.none",
		Evidence:       "no findings",
		Recommendation: "no action required",
		Blocked:        false,
		Findings:       findings,
		Duration:       dur,
		DurationMS:     dur.Milliseconds(),
	}
	for _, f := range findings {
		if findingRank(f) > reportRank(report) {
			report.Decision = f.Decision
			report.RiskLevel = f.RiskLevel
			report.RuleID = f.RuleID
			report.Evidence = f.Evidence
			report.Recommendation = f.Recommendation
		}
		report.Redacted = report.Redacted || f.Redacted
	}
	report.Blocked = report.Decision == DecisionDeny ||
		report.Decision == DecisionAsk ||
		report.Decision == DecisionNeedsHumanReview
	return report
}

func findingRank(f Finding) int {
	return decisionRank(f.Decision)*10 + riskRank(f.RiskLevel)
}

func reportRank(r Report) int {
	return decisionRank(r.Decision)*10 + riskRank(r.RiskLevel)
}

func decisionRank(d Decision) int {
	switch d {
	case DecisionDeny:
		return 4
	case DecisionNeedsHumanReview:
		return 3
	case DecisionAsk:
		return 2
	case DecisionAllow:
		return 1
	default:
		return 0
	}
}

func riskRank(r RiskLevel) int {
	switch r {
	case RiskCritical:
		return 4
	case RiskHigh:
		return 3
	case RiskMedium:
		return 2
	case RiskLow:
		return 1
	default:
		return 0
	}
}

func normalizeCommand(cmd string) string {
	cmd = filepath.ToSlash(strings.TrimSpace(cmd))
	cmd = strings.TrimSuffix(filepath.Base(cmd), ".exe")
	return strings.ToLower(cmd)
}

func sensitivePathMatch(arg, denied string) bool {
	arg = strings.Trim(strings.TrimSpace(arg), `"'`)
	arg = strings.ReplaceAll(arg, "\\", "/")
	denied = strings.ReplaceAll(strings.TrimSpace(denied), "\\", "/")
	if denied == "" || arg == "" {
		return false
	}
	argLower := strings.ToLower(arg)
	deniedLower := strings.ToLower(denied)
	if strings.Contains(argLower, deniedLower) {
		return true
	}
	if strings.HasPrefix(deniedLower, "~/") &&
		strings.Contains(argLower, strings.TrimPrefix(deniedLower, "~/")) {
		return true
	}
	return false
}

func joinCwdPath(cwd, arg string) string {
	cwd = strings.Trim(strings.TrimSpace(cwd), `"'`)
	arg = strings.Trim(strings.TrimSpace(arg), `"'`)
	if cwd == "" || arg == "" || filepath.IsAbs(arg) ||
		strings.HasPrefix(arg, "~/") || strings.HasPrefix(arg, "~\\") {
		return arg
	}
	if strings.HasPrefix(arg, "-") {
		return arg
	}
	return filepath.ToSlash(filepath.Join(cwd, arg))
}

func isNetworkCommand(cmd string) bool {
	switch cmd {
	case "curl", "wget", "nc", "netcat", "ssh", "scp":
		return true
	default:
		return false
	}
}

var urlLikePattern = regexp.MustCompile(`(?i)\b(?:https?|ftp)://[^\s"'<>]+`)

func extractHosts(text string) []string {
	var hosts []string
	for _, raw := range urlLikePattern.FindAllString(text, -1) {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			continue
		}
		hosts = append(hosts, strings.ToLower(u.Hostname()))
	}
	return hosts
}

func (s *DefaultScanner) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, pattern := range s.policy.NetworkAllowlist {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		pattern = strings.TrimSuffix(pattern, ".")
		if pattern == "" {
			continue
		}
		if strings.HasPrefix(pattern, ".") {
			if strings.HasSuffix(host, pattern) ||
				host == strings.TrimPrefix(pattern, ".") {
				return true
			}
			continue
		}
		if host == pattern {
			return true
		}
	}
	return false
}

func isPrivateHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func (s *DefaultScanner) commandMentionsDeniedPath(command string) bool {
	for _, denied := range s.policy.DeniedPaths {
		if sensitivePathMatch(command, denied) {
			return true
		}
	}
	return false
}

func (s *DefaultScanner) textMentionsDeniedPath(text string) bool {
	for _, denied := range s.policy.DeniedPaths {
		if sensitivePathMatch(text, denied) {
			return true
		}
	}
	return false
}

func (s *DefaultScanner) redactReportText(text string) (string, bool) {
	out, redacted := redactString(text)
	deniedPaths := append([]string(nil), s.policy.DeniedPaths...)
	sort.SliceStable(deniedPaths, func(i, j int) bool {
		return len(deniedPaths[i]) > len(deniedPaths[j])
	})
	for _, denied := range deniedPaths {
		if strings.TrimSpace(denied) == "" {
			continue
		}
		next := redactSensitivePath(out, denied)
		if next != out {
			redacted = true
			out = next
		}
	}
	return out, redacted
}

func redactSensitivePath(text, denied string) string {
	needle := strings.Trim(strings.TrimSpace(denied), `"'`)
	if needle == "" {
		return text
	}
	out := replaceFold(text, needle, "<redacted>")
	slashed := strings.ReplaceAll(needle, "\\", "/")
	if slashed != needle {
		out = replaceFold(out, slashed, "<redacted>")
	}
	backslashed := strings.ReplaceAll(needle, "/", "\\")
	if backslashed != needle {
		out = replaceFold(out, backslashed, "<redacted>")
	}
	if strings.HasPrefix(slashed, "~/") {
		trimmed := strings.TrimPrefix(slashed, "~/")
		out = replaceFold(out, trimmed, "<redacted>")
	}
	return out
}

func replaceFold(s, old, new string) string {
	if old == "" {
		return s
	}
	lower := strings.ToLower(s)
	oldLower := strings.ToLower(old)
	var b strings.Builder
	for {
		idx := strings.Index(lower, oldLower)
		if idx < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:idx])
		b.WriteString(new)
		cut := idx + len(old)
		s = s[cut:]
		lower = lower[cut:]
	}
}

func containsDangerousCommandText(lower string) bool {
	if strings.Contains(lower, "rm -rf") ||
		strings.Contains(lower, "rm -fr") ||
		strings.Contains(lower, "sudo ") ||
		strings.Contains(lower, "curl ") ||
		strings.Contains(lower, "wget ") ||
		strings.Contains(lower, " nc ") ||
		strings.Contains(lower, "netcat ") ||
		strings.Contains(lower, "ssh ") ||
		strings.Contains(lower, "scp ") ||
		strings.Contains(lower, "format ") {
		return true
	}
	for _, token := range strings.FieldsFunc(lower, func(r rune) bool {
		return !(r == '_' || r == '-' || r == '.' ||
			r == '/' || r == '\\' ||
			(r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9'))
	}) {
		switch token {
		case "rm", "sudo", "curl", "wget", "nc", "netcat", "ssh", "scp", "format":
			return true
		}
	}
	return false
}

func isDependencyInstall(cmd string, argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	sub := strings.ToLower(argv[1])
	switch cmd {
	case "npm", "pnpm", "yarn", "pip", "pip3", "apt", "apt-get", "brew":
		return sub == "install" || sub == "add"
	case "go":
		return sub == "install" || sub == "get"
	default:
		return false
	}
}
