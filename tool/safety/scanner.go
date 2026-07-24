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
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

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
	var findings []Finding
	if !req.Backend.Valid() {
		unsupported := req.Backend
		req.Backend = BackendUnknown
		findings = []Finding{{
			RuleID:         "backend.unsupported",
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       fmt.Sprintf("unsupported backend %q", unsupported),
			Recommendation: "use a supported safety backend before tool execution",
		}}
	} else {
		findings = s.scanRequest(ctx, req)
	}
	report := buildReport(req, findings, time.Since(start))
	report.Command, report.Redacted = s.redactReportText(report.Command)
	if report.Evidence != "" {
		var redacted bool
		report.Evidence, redacted = s.redactReportText(report.Evidence)
		report.Redacted = report.Redacted || redacted
	}
	if report.Recommendation != "" {
		var redacted bool
		report.Recommendation, redacted = s.redactReportText(report.Recommendation)
		report.Redacted = report.Redacted || redacted
	}
	for i := range report.Findings {
		var redacted bool
		report.Findings[i].Evidence, redacted = s.redactReportText(report.Findings[i].Evidence)
		report.Findings[i].Redacted = report.Findings[i].Redacted || redacted
		report.Redacted = report.Redacted || report.Findings[i].Redacted
		report.Findings[i].Recommendation, redacted = s.redactReportText(report.Findings[i].Recommendation)
		report.Findings[i].Redacted = report.Findings[i].Redacted || redacted
		report.Redacted = report.Redacted || redacted
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
	findings = append(findings, s.scanOutputLimit(req)...)
	switch {
	case req.Command != "":
		findings = append(findings, s.scanCommand(req)...)
	case len(req.Args) > 0:
		findings = append(findings, s.scanArgvRequest(req)...)
	case req.Code != "":
		findings = append(findings, s.scanCode(req)...)
	case len(req.RawArguments) > 0:
		findings = append(findings, s.scanUnknownArguments(req)...)
	}
	if req.Stdin != "" && req.Command == "" {
		findings = append(findings, Finding{
			RuleID:         "stdin.session_fragment",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       "non-empty stdin without a complete submitted command",
			Recommendation: "scan complete submitted session lines or require review",
		})
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
		if isProcessControlEnv(key) {
			findings = append(findings, Finding{
				RuleID:         "env.process_control",
				RiskLevel:      RiskHigh,
				Decision:       DecisionDeny,
				Evidence:       key,
				Recommendation: "remove process-control environment variables before execution",
			})
		}
		if len(s.policy.EnvAllowlist) > 0 && !s.envAllowed(key) {
			decision := DecisionAsk
			risk := RiskMedium
			if isProcessControlEnv(key) {
				risk = RiskHigh
			}
			findings = append(findings, Finding{
				RuleID:         "env.not_allowlisted",
				RiskLevel:      risk,
				Decision:       decision,
				Evidence:       key,
				Recommendation: "add the variable to env_allowlist or remove it from the tool environment",
			})
		}
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
		stdinFindings := s.scanStdin(req)
		for i := range stdinFindings {
			if req.Backend == BackendHost && stdinFindings[i].Decision == DecisionAsk {
				stdinFindings[i].Decision = DecisionDeny
			}
		}
		findings = append(findings, stdinFindings...)
	}
	return findings
}

func (s *DefaultScanner) scanStdin(req ScanRequest) []Finding {
	pipe, err := shellsafe.Parse(req.Stdin)
	if err == nil {
		var findings []Finding
		for _, argv := range pipe.Commands {
			// Stdin is data for the outer command. Do not apply the outer
			// command allowlist to each data line, but retain argv-level
			// security checks for commands that are explicitly submitted.
			findings = append(findings, s.scanArgv(req, argv)...)
		}
		return findings
	}
	textReq := req
	textReq.Command = ""
	textReq.Stdin = ""
	return s.scanTextForUnknownRisk(textReq, req.Stdin)
}

func (s *DefaultScanner) scanArgvRequest(req ScanRequest) []Finding {
	if len(req.Args) == 0 {
		return nil
	}
	var findings []Finding
	if policyErr := shellsafe.PolicyFromLists(
		s.policy.AllowedCommands,
		s.policy.DeniedCommands,
	).Check(&shellsafe.Pipeline{Commands: [][]string{req.Args}}); policyErr != nil {
		findings = append(findings, s.commandPolicyFinding(policyErr))
	}
	findings = append(findings, s.scanArgv(req, req.Args)...)
	return findings
}

func (s *DefaultScanner) shellParseFinding(req ScanRequest, err error) Finding {
	msg := err.Error()
	decision := s.policy.UnparsableShellAction
	if req.Backend == BackendHost {
		decision = s.policy.HostUnparsableAction
	}
	rule := "shell.unparsable"
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
	if deleteArgsAreDangerous(argv[1:]) {
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
	text := strings.Join(argv, " ")
	hosts := extractNetworkHosts(cmd, argv)
	if !isNetworkCommand(cmd) && len(hosts) == 0 {
		return nil
	}
	if hasNetworkDestinationOverride(cmd, argv) {
		decision := DecisionAsk
		if len(s.policy.NetworkAllowlist) > 0 {
			decision = DecisionDeny
		}
		return []Finding{{
			RuleID:         "network.destination_override",
			RiskLevel:      RiskHigh,
			Decision:       decision,
			Evidence:       "curl destination override requires an explicit network review",
			Recommendation: "remove curl destination-routing options or review the effective destination",
		}}
	}
	if len(hosts) == 0 {
		decision := DecisionAsk
		if len(s.policy.NetworkAllowlist) > 0 {
			decision = DecisionDeny
		}
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
		if isHighRiskNetworkCommand(cmd) {
			decision = DecisionDeny
			rule = "network.external_tool"
		}
		if len(s.policy.NetworkAllowlist) > 0 {
			decision = DecisionDeny
			rule = "network.non_allowlisted_domain"
		}
		if isPrivateHost(host) {
			rule = "network.private_address"
			if decision != DecisionDeny {
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
		if n, ok := parseSleepSeconds(argv[1]); ok &&
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
	if cmd == "yes" ||
		(hasExactArg(argv, "yes") && hasExactArg(argv, "head")) {
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

func (s *DefaultScanner) scanOutputLimit(req ScanRequest) []Finding {
	if s.policy.MaxOutputBytes <= 0 || len(req.Metadata) == 0 {
		return nil
	}
	value, ok := metadataInt64(req.Metadata, "max_result_size", "max_output_bytes", "max_output_size")
	if !ok || value <= s.policy.MaxOutputBytes {
		return nil
	}
	return []Finding{{
		RuleID:         "resource.output_limit",
		RiskLevel:      RiskHigh,
		Decision:       DecisionAsk,
		Evidence:       fmt.Sprintf("requested output size %d exceeds max_output_bytes=%d", value, s.policy.MaxOutputBytes),
		Recommendation: "lower the requested output size or require approval",
	}}
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
		findings = append(findings, s.scanCodeResourceAbuse(lang, req.Code)...)
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
		findings = append(findings, s.scanCodeResourceAbuse(lang, req.Code)...)
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

var (
	pythonInfiniteLoopPattern = regexp.MustCompile(`(?mi)\bwhile\s+(?:true|1)\s*:`)
	goInfiniteLoopPattern     = regexp.MustCompile(`(?mi)\bfor\s*\{`)
	jsInfiniteLoopPattern     = regexp.MustCompile(`(?mi)\bwhile\s*\(\s*(?:true|1)\s*\)|\bfor\s*\(\s*;\s*;\s*\)`)
	codeSleepPattern          = regexp.MustCompile(`(?i)(?:time\.)?sleep\s*\(\s*([0-9]+)\s*(seconds?|secs?|minutes?|mins?|hours?|hrs?|days?|d)?\s*\)`)
	goSleepPattern            = regexp.MustCompile(`(?i)time\.sleep\s*\(\s*([0-9]+)\s*\*\s*time\.(second|minute|hour|day)s?\s*\)`)
	jsSleepPattern            = regexp.MustCompile(`(?i)settimeout\s*\([^,]+,\s*([0-9]+)\s*\)`)
)

func (s *DefaultScanner) scanCodeResourceAbuse(lang, code string) []Finding {
	if hasObviousInfiniteLoop(lang, code) {
		return []Finding{{
			RuleID:         "resource.long_running",
			RiskLevel:      RiskCritical,
			Decision:       DecisionDeny,
			Evidence:       fmt.Sprintf("%s code contains an obvious infinite loop", lang),
			Recommendation: "bound code execution with a terminating condition or timeout",
		}}
	}
	seconds, ok := codeSleepSeconds(lang, code)
	if !ok || s.policy.MaxTimeoutSec <= 0 || seconds <= s.policy.MaxTimeoutSec {
		return nil
	}
	decision := DecisionAsk
	risk := RiskHigh
	if seconds > s.policy.MaxTimeoutSec*10 {
		decision = DecisionDeny
		risk = RiskCritical
	}
	return []Finding{{
		RuleID:         "resource.long_running",
		RiskLevel:      risk,
		Decision:       decision,
		Evidence:       fmt.Sprintf("%s code sleeps for %d seconds", lang, seconds),
		Recommendation: "use bounded execution time or require approval",
	}}
}

func hasObviousInfiniteLoop(lang, code string) bool {
	switch lang {
	case "python":
		return pythonInfiniteLoopPattern.MatchString(code)
	case "go":
		return goInfiniteLoopPattern.MatchString(code)
	case "javascript", "typescript", "node":
		return jsInfiniteLoopPattern.MatchString(code)
	default:
		return false
	}
}

func codeSleepSeconds(lang, code string) (int, bool) {
	if lang == "go" {
		if match := goSleepPattern.FindStringSubmatch(code); len(match) == 3 {
			value, err := strconv.Atoi(match[1])
			if err != nil {
				return 0, false
			}
			multipliers := map[string]int{
				"second": 1,
				"minute": 60,
				"hour":   60 * 60,
				"day":    24 * 60 * 60,
			}
			return value * multipliers[strings.ToLower(match[2])], true
		}
	}
	if lang == "javascript" || lang == "typescript" || lang == "node" {
		if match := jsSleepPattern.FindStringSubmatch(code); len(match) == 2 {
			value, err := strconv.Atoi(match[1])
			if err != nil {
				return 0, false
			}
			return value / 1000, true
		}
	}
	if match := codeSleepPattern.FindStringSubmatch(code); len(match) == 3 {
		value, err := strconv.Atoi(match[1])
		if err != nil {
			return 0, false
		}
		multipliers := map[string]int{
			"":        1,
			"second":  1,
			"seconds": 1,
			"sec":     1,
			"secs":    1,
			"minute":  60,
			"minutes": 60,
			"min":     60,
			"mins":    60,
			"hour":    60 * 60,
			"hours":   60 * 60,
			"hr":      60 * 60,
			"hrs":     60 * 60,
			"day":     24 * 60 * 60,
			"days":    24 * 60 * 60,
			"d":       24 * 60 * 60,
		}
		return value * multipliers[strings.ToLower(match[2])], true
	}
	return 0, false
}

func (s *DefaultScanner) scanUnknownArguments(req ScanRequest) []Finding {
	if s.policy.MaxCommandBytes > 0 && len(req.RawArguments) > s.policy.MaxCommandBytes {
		return []Finding{{
			RuleID:         "unknown.bounded_scan",
			RiskLevel:      RiskHigh,
			Decision:       DecisionNeedsHumanReview,
			Evidence:       fmt.Sprintf("raw arguments have %d bytes, exceeds max_command_bytes=%d", len(req.RawArguments), s.policy.MaxCommandBytes),
			Recommendation: "review large unknown tool arguments manually before execution",
		}}
	}
	rawFindings := s.scanTextForUnknownRisk(req, string(req.RawArguments))
	var decoded any
	if err := json.Unmarshal(req.RawArguments, &decoded); err != nil {
		return rawFindings
	}
	findings := append(rawFindings, s.scanDecodedUnknownArguments(req, decoded)...)
	return dedupeFindings(findings)
}

func (s *DefaultScanner) scanDecodedUnknownArguments(req ScanRequest, value any) []Finding {
	switch v := value.(type) {
	case string:
		return s.scanTextForUnknownRisk(req, v)
	case []any:
		var findings []Finding
		for _, item := range v {
			findings = append(findings, s.scanDecodedUnknownArguments(req, item)...)
		}
		return findings
	case map[string]any:
		var findings []Finding
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			findings = append(findings, s.scanTextForUnknownRisk(req, key)...)
			item := v[key]
			findings = append(findings, s.scanDecodedUnknownArguments(req, item)...)
		}
		return findings
	default:
		return nil
	}
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
	directTextInput := req.Command == ""
	if containsDownloaderOrURL(lower) {
		if req.Backend == BackendUnknown {
			findings = append(findings, Finding{
				RuleID:         "unknown.requires_review",
				RiskLevel:      RiskHigh,
				Decision:       DecisionNeedsHumanReview,
				Evidence:       "unknown tool contains downloader or URL-like content",
				Recommendation: "review unknown open-world tools before execution",
			})
		} else if directTextInput && (req.Backend == BackendHost || req.Backend == BackendCodeExec) {
			findings = append(findings, s.scanTextNetwork(text)...)
		}
	}
	if containsDangerousCommandText(lower) {
		switch req.Backend {
		case BackendUnknown:
			findings = append(findings, Finding{
				RuleID:         "unknown.dangerous_command",
				RiskLevel:      RiskCritical,
				Decision:       DecisionNeedsHumanReview,
				Evidence:       "unknown tool contains dangerous command-like content",
				Recommendation: "review unknown open-world tools before execution",
			})
		case BackendHost, BackendCodeExec:
			if !directTextInput {
				break
			}
			findings = append(findings, Finding{
				RuleID:         "command.dangerous_text",
				RiskLevel:      RiskCritical,
				Decision:       DecisionAsk,
				Evidence:       "text contains dangerous command-like content",
				Recommendation: "review generated stdin or code before execution",
			})
		}
	}
	if s.textMentionsDeniedPath(text) {
		if req.Backend == BackendUnknown {
			findings = append(findings, Finding{
				RuleID:         "unknown.sensitive_path",
				RiskLevel:      RiskCritical,
				Decision:       DecisionNeedsHumanReview,
				Evidence:       "<redacted>",
				Recommendation: "review unknown tools that reference credential or secret paths",
				Redacted:       true,
			})
		} else if directTextInput && (req.Backend == BackendHost || req.Backend == BackendCodeExec) {
			findings = append(findings, Finding{
				RuleID:         "path.sensitive_credentials",
				RiskLevel:      RiskCritical,
				Decision:       DecisionDeny,
				Evidence:       "<redacted>",
				Recommendation: "do not pass credential or secret paths through execution inputs",
				Redacted:       true,
			})
		}
	}
	return findings
}

func (s *DefaultScanner) scanTextNetwork(text string) []Finding {
	hosts := extractHosts(text)
	if len(hosts) == 0 {
		return []Finding{{
			RuleID:         "network.external_tool",
			RiskLevel:      RiskHigh,
			Decision:       DecisionAsk,
			Evidence:       "text contains network-capable command",
			Recommendation: "review generated network access before execution",
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
	if strings.TrimSpace(arg) == "" || strings.TrimSpace(denied) == "" {
		return false
	}
	deniedNeedles := sensitivePathNeedles(denied)
	for _, candidate := range sensitivePathCandidates(arg) {
		candidateLower := strings.ToLower(candidate)
		for _, deniedNeedle := range deniedNeedles {
			if deniedNeedle != "" && strings.Contains(candidateLower, deniedNeedle) {
				return true
			}
		}
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

func hasNetworkDestinationOverride(cmd string, argv []string) bool {
	if cmd != "curl" {
		return false
	}
	for i := 1; i < len(argv); i++ {
		arg := strings.ToLower(strings.TrimSpace(argv[i]))
		switch {
		case arg == "--resolve", arg == "--connect-to":
			return i+1 < len(argv) && strings.TrimSpace(argv[i+1]) != ""
		case strings.HasPrefix(arg, "--resolve="):
			return strings.TrimSpace(strings.TrimPrefix(arg, "--resolve=")) != ""
		case strings.HasPrefix(arg, "--connect-to="):
			return strings.TrimSpace(strings.TrimPrefix(arg, "--connect-to=")) != ""
		}
	}
	return false
}

func isHighRiskNetworkCommand(cmd string) bool {
	switch cmd {
	case "nc", "netcat", "ssh", "scp":
		return true
	default:
		return false
	}
}

var urlLikePattern = regexp.MustCompile(`(?i)\b(?:https?|ftp)://[^\s"'<>]+`)

func extractHosts(text string) []string {
	seen := make(map[string]struct{})
	var hosts []string
	for _, raw := range urlLikePattern.FindAllString(text, -1) {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			continue
		}
		hosts = appendHost(hosts, seen, u.Hostname())
	}
	return hosts
}

func extractNetworkHosts(cmd string, argv []string) []string {
	seen := make(map[string]struct{})
	var hosts []string
	hosts = appendHosts(hosts, seen, extractHosts(strings.Join(argv, " "))...)
	for _, arg := range argv[1:] {
		host, ok := networkArgHost(cmd, arg)
		if !ok {
			continue
		}
		hosts = appendHost(hosts, seen, host)
	}
	return hosts
}

func networkArgHost(cmd, arg string) (string, bool) {
	arg = strings.Trim(strings.TrimSpace(arg), `"'`)
	if arg == "" || strings.HasPrefix(arg, "-") {
		return "", false
	}
	if strings.Contains(arg, "://") {
		u, err := url.Parse(arg)
		if err != nil || u.Hostname() == "" {
			return "", false
		}
		return u.Hostname(), true
	}
	if !isNetworkCommand(cmd) && cmd != "git" {
		return "", false
	}
	switch cmd {
	case "curl", "wget":
		if host, _, ok := strings.Cut(arg, "/"); ok {
			arg = host
		}
		if host, _, ok := strings.Cut(arg, ":"); ok {
			arg = host
		}
	case "ssh", "scp":
		if userHost, _, ok := strings.Cut(arg, ":"); ok {
			arg = userHost
		}
		if _, host, ok := strings.Cut(arg, "@"); ok {
			arg = host
		}
	case "nc", "netcat":
	}
	arg = strings.TrimSuffix(arg, ".")
	if !looksLikeHost(arg) {
		return "", false
	}
	return arg, true
}

func appendHosts(hosts []string, seen map[string]struct{}, values ...string) []string {
	for _, host := range values {
		hosts = appendHost(hosts, seen, host)
	}
	return hosts
}

func appendHost(hosts []string, seen map[string]struct{}, host string) []string {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return hosts
	}
	if _, ok := seen[host]; ok {
		return hosts
	}
	seen[host] = struct{}{}
	return append(hosts, host)
}

func looksLikeHost(s string) bool {
	if s == "localhost" || net.ParseIP(s) != nil {
		return true
	}
	if strings.ContainsAny(s, ":/\\") {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
				return false
			}
		}
	}
	return true
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

func (s *DefaultScanner) envAllowed(key string) bool {
	for _, allowed := range s.policy.EnvAllowlist {
		if key == allowed || strings.EqualFold(key, allowed) {
			return true
		}
	}
	return false
}

func isProcessControlEnv(key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	if key == "PATH" || strings.HasPrefix(key, "LD_") {
		return true
	}
	switch key {
	case "BASH_ENV", "ENV", "CDPATH", "GLOBIGNORE", "PROMPT_COMMAND",
		"PYTHONPATH", "PYTHONHOME", "PYTHONSTARTUP", "NODE_PATH", "NODE_OPTIONS",
		"RUBYLIB", "RUBYOPT", "PERL5LIB", "PERL5OPT", "DYLD_INSERT_LIBRARIES",
		"DYLD_LIBRARY_PATH", "DYLD_FRAMEWORK_PATH", "DYLD_FALLBACK_LIBRARY_PATH",
		"PATHEXT":
		return true
	default:
		return false
	}
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
	return redactReportTextWithDeniedPaths(text, append([]string(nil), s.policy.DeniedPaths...))
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
	out = redactNormalizedSensitiveTokens(out, denied)
	return out
}

func redactNormalizedSensitiveTokens(text, denied string) string {
	needles := sensitivePathNeedles(denied)
	if len(needles) == 0 {
		return text
	}
	var b strings.Builder
	start := 0
	for _, span := range sensitiveTokenSpans(text) {
		b.WriteString(text[start:span[0]])
		token := text[span[0]:span[1]]
		redacted, ok := redactSensitiveToken(token, needles)
		if ok {
			b.WriteString(redacted)
		} else {
			b.WriteString(token)
		}
		start = span[1]
	}
	b.WriteString(text[start:])
	return b.String()
}

func redactSensitiveToken(token string, deniedNeedles []string) (string, bool) {
	candidates := []struct {
		text  string
		start int
		end   int
	}{
		{text: token, start: 0, end: len(token)},
	}
	if idx := strings.Index(token, "="); idx >= 0 && idx+1 < len(token) {
		candidates = append(candidates, struct {
			text  string
			start int
			end   int
		}{
			text:  token[idx+1:],
			start: idx + 1,
			end:   len(token),
		})
	}
	for _, candidate := range candidates {
		for _, normalized := range sensitivePathCandidates(candidate.text) {
			lower := strings.ToLower(normalized)
			for _, deniedNeedle := range deniedNeedles {
				if deniedNeedle != "" && strings.Contains(lower, deniedNeedle) {
					return token[:candidate.start] + "<redacted>" + token[candidate.end:], true
				}
			}
		}
	}
	return token, false
}

func sensitivePathNeedles(denied string) []string {
	var needles []string
	add := func(value string) {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			return
		}
		for _, existing := range needles {
			if existing == value {
				return
			}
		}
		needles = append(needles, value)
	}
	raw := slashNormalizedPathText(denied)
	add(raw)
	add(normalizePathForMatch(raw))
	if strings.HasPrefix(raw, "~/") {
		add(strings.TrimPrefix(raw, "~/"))
	}
	normalized := normalizePathForMatch(raw)
	if strings.HasPrefix(normalized, "~/") {
		add(strings.TrimPrefix(normalized, "~/"))
	}
	return needles
}

func sensitivePathCandidates(text string) []string {
	base := slashNormalizedPathText(text)
	if base == "" {
		return nil
	}
	var candidates []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range candidates {
			if existing == value {
				return
			}
		}
		candidates = append(candidates, value)
	}
	add(base)
	if isPathLikeCandidate(base) {
		add(normalizePathForMatch(base))
	}
	if looksFreeFormText(base) {
		for _, span := range sensitiveTokenSpans(base) {
			token := base[span[0]:span[1]]
			add(token)
			if idx := strings.Index(token, "="); idx >= 0 && idx+1 < len(token) {
				token = token[idx+1:]
				add(token)
			}
			if isPathLikeCandidate(token) {
				add(normalizePathForMatch(token))
			}
		}
	}
	return candidates
}

func slashNormalizedPathText(text string) string {
	return strings.ReplaceAll(strings.Trim(strings.TrimSpace(text), `"'`), "\\", "/")
}

func normalizePathForMatch(value string) string {
	value = slashNormalizedPathText(value)
	if value == "" {
		return ""
	}
	switch {
	case strings.HasPrefix(value, "~/"):
		return "~/" + strings.TrimPrefix(path.Clean("/"+strings.TrimPrefix(value, "~/")), "/")
	case strings.HasPrefix(value, "/"):
		return path.Clean(value)
	case len(value) >= 3 && value[1] == ':' && value[2] == '/':
		return value[:2] + path.Clean(value[2:])
	default:
		return path.Clean(value)
	}
}

func isPathLikeCandidate(value string) bool {
	if value == "" || strings.ContainsAny(value, "\r\n\t ") {
		return false
	}
	return strings.ContainsAny(value, `/\`) ||
		strings.HasPrefix(value, "~") ||
		strings.HasPrefix(value, ".")
}

func looksFreeFormText(value string) bool {
	return strings.ContainsAny(value, "\r\n\t ") ||
		strings.ContainsAny(value, "|&;()[]{}<>,")
}

func sensitiveTokenSpans(text string) [][2]int {
	var spans [][2]int
	start := -1
	for i, r := range text {
		if isSensitiveTokenSeparator(r) {
			if start >= 0 {
				spans = append(spans, [2]int{start, i})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		spans = append(spans, [2]int{start, len(text)})
	}
	return spans
}

func isSensitiveTokenSeparator(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(`|&;()[]{}<>,`, r)
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

func dedupeFindings(findings []Finding) []Finding {
	seen := make(map[string]struct{}, len(findings))
	out := make([]Finding, 0, len(findings))
	for _, finding := range findings {
		key := strings.Join([]string{
			finding.RuleID,
			string(finding.RiskLevel),
			string(finding.Decision),
			finding.Evidence,
			finding.Recommendation,
			strconv.FormatBool(finding.Redacted),
		}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, finding)
	}
	return out
}

var dangerousCommandTextSubstrings = []string{
	"rm -rf",
	"rm -fr",
	"sudo ",
	"curl ",
	"wget ",
	" nc ",
	"netcat ",
	"ssh ",
	"scp ",
	"format ",
}

var dangerousCommandTextTokens = map[string]struct{}{
	"rm":     {},
	"sudo":   {},
	"curl":   {},
	"wget":   {},
	"nc":     {},
	"netcat": {},
	"ssh":    {},
	"scp":    {},
	"format": {},
}

func containsDangerousCommandText(lower string) bool {
	return containsAnySubstring(lower, dangerousCommandTextSubstrings) ||
		containsDangerousCommandToken(lower)
}

func containsDownloaderOrURL(lower string) bool {
	return strings.Contains(lower, "download") ||
		strings.Contains(lower, "curl ") ||
		strings.Contains(lower, "wget ") ||
		strings.Contains(lower, "http://") ||
		strings.Contains(lower, "https://")
}

func metadataInt64(metadata map[string]any, keys ...string) (int64, bool) {
	for _, key := range keys {
		value, ok := metadata[key]
		if !ok {
			continue
		}
		switch v := value.(type) {
		case int:
			return int64(v), true
		case int8:
			return int64(v), true
		case int16:
			return int64(v), true
		case int32:
			return int64(v), true
		case int64:
			return v, true
		case uint:
			return metadataUnsignedInt64(uint64(v))
		case uint8:
			return metadataUnsignedInt64(uint64(v))
		case uint16:
			return metadataUnsignedInt64(uint64(v))
		case uint32:
			return metadataUnsignedInt64(uint64(v))
		case uint64:
			return metadataUnsignedInt64(v)
		case float64:
			return int64(v), true
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

func metadataUnsignedInt64(v uint64) (int64, bool) {
	n, err := strconv.ParseInt(strconv.FormatUint(v, 10), 10, 64)
	return n, err == nil
}

func deleteArgsAreDangerous(args []string) bool {
	for _, arg := range args {
		if deleteFlagIsRecursive(arg) || deleteTargetIsSystemPath(arg) {
			return true
		}
	}
	return false
}

func deleteFlagIsRecursive(arg string) bool {
	arg = strings.ToLower(strings.TrimSpace(arg))
	if arg == "--recursive" {
		return true
	}
	if strings.HasPrefix(arg, "--") || !strings.HasPrefix(arg, "-") {
		return false
	}
	flags := strings.TrimLeft(arg, "-")
	if flags == "" {
		return false
	}
	for _, r := range flags {
		if !strings.ContainsRune("dfirpv", r) {
			return false
		}
	}
	return strings.ContainsRune(flags, 'r')
}

func deleteTargetIsSystemPath(arg string) bool {
	arg = strings.Trim(strings.TrimSpace(arg), `"'`)
	if arg == "" || strings.HasPrefix(arg, "-") {
		return false
	}
	slashed := strings.ReplaceAll(filepath.ToSlash(arg), `\`, "/")
	lower := strings.ToLower(slashed)
	if lower == "." || lower == ".." || strings.HasPrefix(lower, "~/") {
		return true
	}
	if filepath.IsAbs(arg) || strings.HasPrefix(lower, "/") {
		return true
	}
	if len(lower) >= 3 && lower[1] == ':' && lower[2] == '/' {
		return true
	}
	return false
}

func parseSleepSeconds(raw string) (int, bool) {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(raw); err == nil {
		return n, true
	}
	if strings.HasSuffix(raw, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(raw, "d"))
		if err != nil {
			return 0, false
		}
		return n * 24 * 60 * 60, true
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, false
	}
	return int(d.Seconds()), true
}

func hasExactArg(argv []string, want string) bool {
	for _, arg := range argv {
		if normalizeCommand(arg) == want {
			return true
		}
	}
	return false
}

func containsAnySubstring(text string, substrings []string) bool {
	for _, substring := range substrings {
		if strings.Contains(text, substring) {
			return true
		}
	}
	return false
}

func containsDangerousCommandToken(lower string) bool {
	for _, token := range strings.FieldsFunc(lower, isCommandTextSeparator) {
		if _, ok := dangerousCommandTextTokens[token]; ok {
			return true
		}
	}
	return false
}

func isCommandTextSeparator(r rune) bool {
	switch r {
	case '_', '-', '.', '/', '\\':
		return false
	default:
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}
}

func isDependencyInstall(cmd string, argv []string) bool {
	switch cmd {
	case "python", "python3", "py":
		for i := 1; i+1 < len(argv); i++ {
			if strings.ToLower(argv[i]) != "-m" {
				continue
			}
			module := strings.ToLower(argv[i+1])
			if module != "pip" && module != "pip3" {
				return false
			}
			return hasDependencyAction(argv[i+2:], "install", "add")
		}
		return false
	case "npm", "pnpm", "yarn", "pip", "pip3", "pipx", "apt", "apt-get", "brew":
		return hasDependencyAction(argv[1:], "install", "add")
	case "go":
		return hasDependencyAction(argv[1:], "install", "get")
	default:
		return false
	}
}

func hasDependencyAction(args []string, actions ...string) bool {
	for i := 0; i < len(args); i++ {
		arg := strings.ToLower(strings.TrimSpace(args[i]))
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if dependencyOptionTakesValue(arg) && !strings.Contains(arg, "=") {
				i++
			}
			continue
		}
		for _, action := range actions {
			if arg == action {
				return true
			}
		}
		return false
	}
	return false
}

func dependencyOptionTakesValue(option string) bool {
	if strings.Contains(option, "=") {
		return false
	}
	switch option {
	case "-c", "-e", "-f", "-g", "-i", "-o", "-p", "-r", "-t", "-w",
		"--cache", "--cache-dir", "--config-settings", "--constraint", "--cwd",
		"--directory", "--extra-index-url", "--file", "--filter", "--index-url",
		"--log-file", "--loglevel", "--prefix", "--python", "--registry", "--root",
		"--target", "--userconfig", "--workspace", "--workspace-root":
		return true
	default:
		return false
	}
}
