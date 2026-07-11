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
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

const (
	ruleDangerousDelete    = "TSG-CMD-001"
	ruleSensitivePath      = "TSG-PATH-001"
	ruleNetworkEgress      = "TSG-NET-001"
	ruleShellWrapper       = "TSG-SHELL-001"
	ruleShellExpansion     = "TSG-SHELL-002"
	ruleHostSession        = "TSG-HOST-001"
	ruleDependencyInstall  = "TSG-DEP-001"
	ruleResourceRuntime    = "TSG-RES-001"
	ruleResourceOutput     = "TSG-RES-002"
	ruleResourceConcurrent = "TSG-RES-003"
	ruleSecretLeakage      = "TSG-SECRET-001" // #nosec G101 -- rule id, not a credential.
	ruleParseError         = "TSG-PARSE-001"
	ruleEnvironment        = "TSG-ENV-001"
	rulePipeline           = "TSG-SHELL-003"
	ruleInteractiveStdin   = "TSG-SHELL-004"
	ruleUnknownBackend     = "TSG-BACKEND-001"
	ruleAuditFailure       = "TSG-AUDIT-001"
)

var (
	urlRe     = regexp.MustCompile(`(?i)\bhttps?://[^\s'"<>]+`)
	quotedRe  = regexp.MustCompile(`['"]([^'"]+)['"]`)
	sshPathRe = regexp.MustCompile(`(?i)(^|\s)/(home/[^/\s]+|root)/\.ssh(/|\s|$)`)
	awsPathRe = regexp.MustCompile(`(?i)(^|\s)/(home/[^/\s]+|root)/\.aws(/|\s|$)`)
	envFileRe = regexp.MustCompile(`(?i)(^|/)\.env([.\w-]*)(\s|$)`)
	secretRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\b(api[_-]?key|token|password|passwd|secret)\s*[:=]\s*"[^"]+"`),
		regexp.MustCompile(`(?i)\b(api[_-]?key|token|password|passwd|secret)\s*[:=]\s*'[^']+'`),
		regexp.MustCompile(`(?i)"(api[_-]?key|token|password|passwd|secret|authorization)"\s*:\s*"[^"]+"`),
		regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*bearer\s+[A-Za-z0-9._~+/=-]+`),
		regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{16,}`),
		regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9_]{20,}\b`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
		regexp.MustCompile(`(?i)\b(api[_-]?key|token|password|passwd|secret)\s*[:=]\s*['"]?[^'"\s]+`),
		regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)\b(AKIA[0-9A-Z]{16})\b`),
	}
)

// Option configures a Scanner.
type Option func(*Scanner)

// WithAuditSink records audit events after every scan.
func WithAuditSink(sink AuditSink) Option {
	return func(s *Scanner) {
		s.audit = sink
	}
}

// Scanner evaluates execution requests against a policy.
type Scanner struct {
	policy Policy
	audit  AuditSink
}

// NewScanner creates a scanner.
func NewScanner(policy Policy, opts ...Option) *Scanner {
	s := &Scanner{policy: clonePolicy(policy.Normalize())}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Policy returns the normalized policy used by this scanner.
func (s *Scanner) Policy() Policy {
	if s == nil {
		return DefaultPolicy()
	}
	return clonePolicy(s.policy)
}

// Scan evaluates a pending execution request.
func (s *Scanner) Scan(ctx context.Context, req Request) Report {
	start := time.Now()
	if s == nil {
		s = NewScanner(DefaultPolicy())
	}
	p := s.policy.Normalize()
	var findings []Finding

	if req.Backend == "" {
		req.Backend = BackendUnknown
	}
	if req.Backend == BackendUnknown && p.FailClosedOnUnsupportedBackend {
		findings = append(findings, finding(ruleUnknownBackend, "unsupported_backend", RiskMedium,
			"backend is unknown", "wire the safety guard to a supported execution tool",
			p.UnknownToolAction))
	}

	findings = append(findings, scanExecutableInputs(req, p)...)

	texts := requestTexts(req)
	findings = append(findings, scanRequestContext(req, texts, p)...)

	report := buildReport(req, findings, start, p)
	if s.audit != nil {
		if err := s.audit.WriteAudit(auditEventFromReport(report)); err != nil {
			report = reportWithAuditFailure(req, findings, start, p, err)
		}
	}
	_ = ctx
	return report
}

func clonePolicy(p Policy) Policy {
	p.AllowedCommands = slices.Clone(p.AllowedCommands)
	p.DeniedCommands = slices.Clone(p.DeniedCommands)
	p.AllowedDomains = slices.Clone(p.AllowedDomains)
	p.DeniedPaths = slices.Clone(p.DeniedPaths)
	p.EnvAllowlist = slices.Clone(p.EnvAllowlist)
	return p
}

func scanExecutableInputs(req Request, p Policy) []Finding {
	var findings []Finding
	if strings.TrimSpace(req.Command) != "" {
		findings = append(findings, scanCommand(req.Command, p)...)
	}
	if len(req.Args) > 0 {
		findings = append(findings, scanArgv(req.Args, p)...)
	}
	if shouldScanStdinAsCommand(req) {
		findings = append(findings, scanCommand(req.Stdin, p)...)
	}
	findings = append(findings, scanUnknownRawArgs(req.RawArgs, p)...)
	for _, block := range req.CodeBlocks {
		findings = append(findings, scanCodeBlock(block, p)...)
	}
	return findings
}

func scanRequestContext(req Request, texts []string, p Policy) []Finding {
	var findings []Finding
	findings = append(findings, scanEnvironment(req, p)...)
	findings = append(findings, scanSensitivePaths(texts, p)...)
	for _, text := range texts {
		findings = append(findings, scanNetworkText(text, p)...)
	}
	findings = append(findings, scanSecrets(texts, p)...)
	findings = append(findings, scanResourceHints(req, texts, p)...)
	findings = append(findings, scanHostExec(req, p)...)
	findings = append(findings, scanInteractiveStdin(req)...)
	return findings
}

// ScanOutput scans command output, logs or artifact text before they are
// persisted or exported. It complements Scan, which runs before execution.
func (s *Scanner) ScanOutput(ctx context.Context, req Request, output string) Report {
	start := time.Now()
	if s == nil {
		s = NewScanner(DefaultPolicy())
	}
	p := s.policy.Normalize()
	findings := scanSecrets([]string{output}, p)
	if len(findings) == 0 {
		findings = append(findings, scanSensitivePaths([]string{output}, p)...)
	}
	if req.Backend == "" {
		req.Backend = BackendUnknown
	}
	report := buildReport(req, findings, start, p)
	if s.audit != nil {
		if err := s.audit.WriteAudit(auditEventFromReport(report)); err != nil {
			report = reportWithAuditFailure(req, findings, start, p, err)
		}
	}
	_ = ctx
	return report
}

func reportWithAuditFailure(req Request, findings []Finding, start time.Time, p Policy, err error) Report {
	if p.AuditFailureMode != AuditFailClosed {
		return buildReport(req, findings, start, p)
	}
	findings = append(findings, finding(ruleAuditFailure, "audit_failure", RiskHigh,
		err.Error(), "restore audit sink availability before executing tools", DecisionDeny))
	return buildReport(req, findings, start, p)
}

func buildReport(req Request, findings []Finding, start time.Time, p Policy) Report {
	findings = dedupeFindings(findings)
	if findings == nil {
		findings = []Finding{}
	}
	decision := DecisionAllow
	risk := RiskLow
	for _, f := range findings {
		risk = maxRisk(risk, f.RiskLevel)
		decision = maxDecision(decision, f.Decision)
	}
	elapsed := time.Since(start)
	report := Report{
		ToolName:       req.ToolName,
		Backend:        req.Backend,
		Command:        redactIfNeeded(req.Command, p).text,
		Decision:       decision,
		RiskLevel:      risk,
		Blocked:        decision == DecisionDeny || decision == DecisionAsk,
		DurationMS:     elapsed.Milliseconds(),
		Findings:       findings,
		Recommendation: recommendationFor(decision),
		ScannedAt:      start.UTC(),
		Elapsed:        elapsed,
	}
	if p.RedactSensitiveEvidence {
		report = redactReport(report)
	}
	report = redactSensitivePathsReport(report, p)
	return report
}

func dedupeFindings(in []Finding) []Finding {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]Finding, 0, len(in))
	for _, f := range in {
		key := f.RuleID + "\x00" + f.Evidence + "\x00" + string(f.Decision)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	return out
}

func scanCommand(command string, p Policy) []Finding {
	var findings []Finding
	pipe, err := shellsafe.Parse(command)
	if err != nil {
		findings = append(findings, finding(ruleParseError, "shell_parse",
			RiskHigh, err.Error(),
			"rewrite command without shell expansion, redirection, wrappers, or substitutions",
			p.ParseErrorAction))
	}
	if err == nil {
		pol := shellsafe.PolicyFromLists(p.AllowedCommands, p.DeniedCommands)
		findings = append(findings, scanCommandSegments(pipe.Commands, p)...)
		findings = append(findings, scanNetworkCommandSegments(pipe.Commands, p)...)
		if err := pol.Check(pipe); err != nil {
			riskType, decision := commandPolicyFinding(err, p)
			findings = append(findings, finding(ruleShellWrapper, riskType, RiskHigh, err.Error(),
				"use an allowlisted command or an auditable workspace script",
				decision))
		}
		if p.ReviewShellPipelines && len(pipe.Commands) > 1 {
			findings = append(findings, finding(rulePipeline, "shell_pipeline",
				RiskMedium, command,
				"review multi-segment commands because pipes can hide data movement",
				DecisionAsk))
			if downloadsIntoShell(pipe.Commands) {
				findings = append(findings, finding(ruleShellWrapper,
					"shell_bypass", RiskHigh, command,
					"do not pipe downloaded content directly into an interpreter",
					p.ShellBypassAction))
			}
		}
	}
	findings = append(findings, scanShellBypassText(command, p)...)
	findings = append(findings, scanNetworkText(command, p)...)
	return findings
}

func scanCodeBlock(block CodeBlock, p Policy) []Finding {
	lang := strings.ToLower(strings.TrimSpace(block.Language))
	if lang == "bash" || lang == "sh" || lang == "shell" {
		return scanCommand(block.Code, p)
	}
	var findings []Finding
	texts := []string{block.Code}
	findings = append(findings, scanSensitivePaths(texts, p)...)
	findings = append(findings, scanSecrets(texts, p)...)
	findings = append(findings, scanNetworkText(block.Code, p)...)
	findings = append(findings, scanShellBypassText(block.Code, p)...)
	if strings.Contains(block.Code, "subprocess.") ||
		strings.Contains(block.Code, "os.system(") ||
		strings.Contains(block.Code, "os.popen(") ||
		strings.Contains(block.Code, "exec.Command(") {
		findings = append(findings, finding(ruleShellWrapper, "process_spawn",
			RiskHigh, trimEvidence(block.Code),
			"route command execution through the safety guarded tool path",
			p.ShellBypassAction))
	}
	for _, command := range extractQuotedCommands(block.Code) {
		findings = append(findings, scanCommand(command, p)...)
	}
	return findings
}

func scanArgv(argv []string, p Policy) []Finding {
	var clean []string
	for _, a := range argv {
		if strings.TrimSpace(a) != "" {
			clean = append(clean, a)
		}
	}
	if len(clean) == 0 {
		return nil
	}
	var findings []Finding
	findings = append(findings, scanCommandSegments([][]string{clean}, p)...)
	findings = append(findings, scanNetworkCommandSegments([][]string{clean}, p)...)
	pol := shellsafe.PolicyFromLists(p.AllowedCommands, p.DeniedCommands)
	if err := pol.Check(&shellsafe.Pipeline{Commands: [][]string{clean}}); err != nil {
		riskType, decision := commandPolicyFinding(err, p)
		findings = append(findings, finding(ruleShellWrapper,
			riskType, RiskHigh, err.Error(),
			"use an allowlisted command or an auditable workspace script",
			decision))
	}
	joined := strings.Join(clean, " ")
	findings = append(findings, scanNetworkText(joined, p)...)
	findings = append(findings, scanShellBypassText(joined, p)...)
	return findings
}

func commandPolicyFinding(err error, p Policy) (string, Decision) {
	if strings.Contains(err.Error(), "built-in policy") {
		return "shell_bypass", p.ShellBypassAction
	}
	return "command_policy", DecisionDeny
}

func scanUnknownRawArgs(raw string, p Policy) []Finding {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}
	var findings []Finding
	for _, field := range extractJSONFields("", v) {
		key := strings.ToLower(field.key)
		if isCommandLikeKey(key) {
			if len(field.values) > 0 {
				findings = append(findings, scanArgv(field.values, p)...)
				continue
			}
			val := strings.TrimSpace(field.value)
			if val == "" {
				continue
			}
			findings = append(findings, scanCommand(val, p)...)
			continue
		}
		if isCodeLikeKey(key) {
			val := strings.TrimSpace(field.value)
			if val == "" {
				continue
			}
			findings = append(findings, scanCodeBlock(CodeBlock{
				Language: languageFromKey(key),
				Code:     val,
			}, p)...)
		}
	}
	return findings
}

type jsonStringField struct {
	key    string
	value  string
	values []string
}

func extractJSONStrings(prefix string, v any) []jsonStringField {
	return extractJSONFields(prefix, v)
}

func extractJSONFields(prefix string, v any) []jsonStringField {
	switch x := v.(type) {
	case map[string]any:
		var out []jsonStringField
		for k, value := range x {
			key := k
			if prefix != "" {
				key = prefix + "." + k
			}
			out = append(out, extractJSONFields(key, value)...)
		}
		return out
	case []any:
		if isCommandLikeKey(strings.ToLower(prefix)) {
			values := stringArrayValue(x)
			if len(values) > 0 {
				return []jsonStringField{{key: prefix, values: values}}
			}
		}
		var out []jsonStringField
		for _, value := range x {
			out = append(out, extractJSONFields(prefix, value)...)
		}
		return out
	case string:
		return []jsonStringField{{key: prefix, value: x}}
	default:
		return nil
	}
}

func stringArrayValue(values []any) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		s, ok := v.(string)
		if !ok {
			return nil
		}
		out = append(out, s)
	}
	return out
}

func isCommandLikeKey(key string) bool {
	for _, part := range strings.Split(key, ".") {
		switch part {
		case "command", "cmd", "shell", "script", "args", "argv":
			return true
		}
	}
	return false
}

func isCodeLikeKey(key string) bool {
	for _, part := range strings.Split(key, ".") {
		switch part {
		case "code", "source", "program":
			return true
		}
	}
	return false
}

func languageFromKey(key string) string {
	if strings.Contains(key, "bash") || strings.Contains(key, "shell") {
		return "bash"
	}
	if strings.Contains(key, "python") {
		return "python"
	}
	return ""
}

func shouldScanStdinAsCommand(req Request) bool {
	if strings.TrimSpace(req.Stdin) == "" {
		return false
	}
	if req.Metadata["interactive_stdin"] == "true" {
		return strings.Contains(req.Stdin, "\n")
	}
	text := strings.ToLower(req.Command)
	return strings.Contains(text, " sh") ||
		strings.Contains(text, " bash") ||
		strings.HasPrefix(text, "sh") ||
		strings.HasPrefix(text, "bash") ||
		strings.Contains(text, "| sh") ||
		strings.Contains(text, "| bash") ||
		strings.Contains(text, "python -") ||
		strings.Contains(text, "python3 -")
}

func scanCommandSegments(cmds [][]string, p Policy) []Finding {
	var findings []Finding
	for _, argv := range cmds {
		if len(argv) == 0 {
			continue
		}
		cmd := commandBase(argv[0])
		args := strings.Join(argv[1:], " ")
		if isDangerousRecursiveDelete(cmd, argv) && p.DenyDangerousRecursiveDelete {
			findings = append(findings, finding(ruleDangerousDelete,
				"dangerous_command", RiskCritical, strings.Join(argv, " "),
				"remove recursive destructive deletes or narrow them to safe workspace paths",
				DecisionDeny))
		}
		if cmd == "find" && hasArg(argv, "-delete") && p.DenyDangerousRecursiveDelete {
			findings = append(findings, finding(ruleDangerousDelete,
				"dangerous_command", RiskCritical, strings.Join(argv, " "),
				"remove destructive find -delete operations or require review",
				DecisionDeny))
		}
		if wrapped := unwrapCommandRunner(cmd, argv); len(wrapped) > 0 {
			findings = append(findings, scanCommandSegments([][]string{wrapped}, p)...)
			findings = append(findings, scanNetworkText(strings.Join(wrapped, " "), p)...)
		}
		if isInlineInterpreter(cmd, argv) {
			findings = append(findings, finding(ruleShellWrapper, "process_spawn",
				RiskHigh, strings.Join(argv, " "),
				"move inline interpreter code into an auditable file or require review",
				p.ShellBypassAction))
		}
		if isDependencyInstall(cmd, argv) {
			findings = append(findings, finding(ruleDependencyInstall,
				"dependency_or_environment_change", RiskHigh, strings.Join(argv, " "),
				"preinstall dependencies in a controlled image or require approval",
				p.DependencyInstallAction))
		}
		if cmd == "sleep" && longSleep(argv, p.LongSleepSeconds) {
			findings = append(findings, finding(ruleResourceRuntime,
				"resource_abuse", RiskMedium, strings.Join(argv, " "),
				"keep tool executions short or raise max timeout explicitly",
				DecisionAsk))
		}
		if cmd == "yes" || strings.Contains(args, "while true") {
			findings = append(findings, finding(ruleResourceOutput,
				"resource_abuse", RiskHigh, strings.Join(argv, " "),
				"avoid unbounded output or loops before execution",
				DecisionAsk))
		}
	}
	return findings
}

func scanEnvironment(req Request, p Policy) []Finding {
	if len(req.Env) == 0 {
		return nil
	}
	allowed := make(map[string]struct{}, len(p.EnvAllowlist))
	for _, k := range p.EnvAllowlist {
		allowed[strings.ToUpper(strings.TrimSpace(k))] = struct{}{}
	}
	var findings []Finding
	for k, v := range req.Env {
		upper := strings.ToUpper(k)
		if _, ok := allowed[upper]; !ok {
			findings = append(findings, finding(ruleEnvironment,
				"environment_variable", RiskMedium, k,
				"only pass environment variables listed in env_allowlist",
				p.DisallowedEnvironmentAction))
		}
		if isDangerousEnvKey(upper) {
			findings = append(findings, finding(ruleEnvironment,
				"environment_variable", RiskHigh, k,
				"remove shell startup, dynamic linker, and search path override variables",
				DecisionDeny))
		}
		for _, f := range scanSecrets([]string{k + "=" + v}, p) {
			findings = append(findings, f)
		}
	}
	return findings
}

func scanSensitivePaths(texts []string, p Policy) []Finding {
	var findings []Finding
	for _, text := range texts {
		for _, denied := range p.DeniedPaths {
			needle := strings.TrimSpace(denied)
			if needle == "" {
				continue
			}
			if sensitivePathMatch(text, needle) {
				findings = append(findings, finding(ruleSensitivePath,
					"sensitive_path", RiskCritical, evidenceAround(text, denied),
					"do not access host credentials, dot-env files, or protected system paths",
					p.SensitivePathReadAction))
			}
		}
	}
	return findings
}

func sensitivePathMatch(text, denied string) bool {
	low := strings.ToLower(text)
	needle := strings.ToLower(denied)
	if needle == "~/.ssh" && sshPathRe.FindString(low) != "" {
		return true
	}
	if needle == "~/.aws" && awsPathRe.FindString(low) != "" {
		return true
	}
	if needle == ".env" && envFileRe.FindString(low) != "" {
		return true
	}
	if needle == "/" {
		fields := strings.Fields(low)
		for _, f := range fields {
			if strings.Trim(f, `"' ;|&`) == "/" {
				return true
			}
		}
		return false
	}
	if strings.HasPrefix(needle, "/") || strings.HasPrefix(needle, "~") {
		fields := strings.Fields(low)
		for _, f := range fields {
			f = strings.Trim(f, `"' ;|&`)
			if f == needle || strings.HasPrefix(f, needle+"/") {
				return true
			}
		}
		return false
	}
	return strings.Contains(low, needle)
}

func scanNetworkText(text string, p Policy) []Finding {
	var findings []Finding
	urlMatches := urlRe.FindAllString(text, -1)
	seenHosts := map[string]struct{}{}
	for _, raw := range urlMatches {
		host := hostFromURL(raw)
		if host != "" {
			seenHosts[host] = struct{}{}
		}
		if host == "" || domainAllowed(host, p.AllowedDomains) {
			continue
		}
		findings = append(findings, finding(ruleNetworkEgress,
			"network_egress", RiskCritical, raw,
			"add the domain to allowed_domains or remove the outbound network call",
			p.NonWhitelistedNetworkAction))
	}
	schemelessHosts := schemelessNetworkHosts(text)
	for _, host := range schemelessHosts {
		if _, ok := seenHosts[host]; ok {
			continue
		}
		if domainAllowed(host, p.AllowedDomains) {
			continue
		}
		findings = append(findings, finding(ruleNetworkEgress,
			"network_egress", RiskCritical, host,
			"add the domain to allowed_domains or remove the outbound network call",
			p.NonWhitelistedNetworkAction))
	}
	if hasNetworkCommand(text) && len(urlMatches) == 0 && len(schemelessHosts) == 0 {
		findings = append(findings, finding(ruleNetworkEgress,
			"network_egress", RiskHigh, trimEvidence(text),
			"review network-capable commands without an explicit allowlisted URL",
			DecisionAsk))
	}
	return findings
}

func scanShellBypassText(text string, p Policy) []Finding {
	var findings []Finding
	lower := strings.ToLower(text)
	patterns := []string{"sh -c", "bash -c", "eval ", "`", "$(", " > ", ">>", " 2>", "<("}
	for _, pat := range patterns {
		if strings.Contains(lower, pat) || strings.Contains(text, pat) {
			findings = append(findings, finding(ruleShellExpansion,
				"shell_bypass", RiskHigh, evidenceAround(text, pat),
				"avoid shell wrappers, command substitution, eval, and redirection",
				p.ShellBypassAction))
			break
		}
	}
	return findings
}

func scanResourceHints(req Request, texts []string, p Policy) []Finding {
	var findings []Finding
	if req.TimeoutSec > p.MaxTimeoutSec {
		findings = append(findings, finding(ruleResourceRuntime,
			"resource_abuse", RiskMedium,
			fmt.Sprintf("timeout_sec=%d exceeds max_timeout_sec=%d", req.TimeoutSec, p.MaxTimeoutSec),
			"lower timeout_sec or update policy after review",
			DecisionAsk))
	}
	if req.MaxOutputBytes > p.MaxOutputBytes {
		findings = append(findings, finding(ruleResourceOutput,
			"resource_abuse", RiskMedium,
			fmt.Sprintf("max_output_bytes=%d exceeds max_output_bytes=%d", req.MaxOutputBytes, p.MaxOutputBytes),
			"lower output size or update policy after review",
			DecisionAsk))
	}
	for _, text := range texts {
		low := strings.ToLower(text)
		if strings.Contains(low, "while true") || strings.Contains(low, "for ;;") ||
			strings.Contains(low, ":(){ :|:& };:") {
			findings = append(findings, finding(ruleResourceRuntime,
				"resource_abuse", RiskHigh, trimEvidence(text),
				"avoid unbounded loops and fork bombs in tool execution",
				DecisionAsk))
		}
		if strings.Contains(low, "xargs -p") ||
			strings.Contains(low, "parallel ") ||
			strings.Contains(low, "-parallel ") ||
			strings.Contains(low, "go test -parallel") ||
			strings.Contains(low, "ants.newpool") {
			findings = append(findings, finding(ruleResourceConcurrent,
				"resource_abuse", RiskMedium, trimEvidence(text),
				"review high-concurrency execution and set explicit resource limits",
				DecisionAsk))
		}
	}
	return findings
}

func scanHostExec(req Request, p Policy) []Finding {
	if req.Backend != BackendHostExec {
		return nil
	}
	var findings []Finding
	if req.TTY {
		findings = append(findings, finding(ruleHostSession,
			"host_execution", RiskHigh, "tty=true",
			"host TTY sessions require human review because they can persist state",
			p.HostExecTTYAction))
	}
	if req.Background {
		findings = append(findings, finding(ruleHostSession,
			"host_execution", RiskHigh, "background=true",
			"background host processes require review and cleanup guarantees",
			p.BackgroundAction))
	}
	return findings
}

func scanInteractiveStdin(req Request) []Finding {
	if strings.TrimSpace(req.Stdin) == "" ||
		req.Metadata["interactive_stdin"] != "true" {
		return nil
	}
	return []Finding{finding(ruleInteractiveStdin,
		"shell_bypass", RiskMedium, "interactive stdin write",
		"review interactive stdin writes because commands can be split across chunks",
		DecisionAsk)}
}

func scanSecrets(texts []string, p Policy) []Finding {
	if !p.DenySecretLeakage {
		return nil
	}
	var findings []Finding
	for _, text := range texts {
		for _, re := range secretRes {
			if match := re.FindString(text); match != "" {
				findings = append(findings, finding(ruleSecretLeakage,
					"sensitive_information_leakage", RiskCritical, match,
					"remove secrets from command arguments, env, logs, artifacts, and audit data",
					DecisionDeny))
			}
		}
	}
	return findings
}

func finding(ruleID, riskType string, risk RiskLevel, evidence, rec string, d Decision) Finding {
	return Finding{
		RuleID:         ruleID,
		RiskType:       riskType,
		RiskLevel:      risk,
		Evidence:       trimEvidence(evidence),
		Recommendation: rec,
		Decision:       normalizeDecision(d, DecisionAsk),
	}
}

func requestTexts(req Request) []string {
	texts := []string{req.Command, req.Cwd, req.Stdin, req.RawArgs}
	for k, v := range req.Env {
		texts = append(texts, k+"="+v)
	}
	for _, a := range req.Args {
		texts = append(texts, a)
	}
	for _, b := range req.CodeBlocks {
		texts = append(texts, b.Language, b.Code)
	}
	for k, v := range req.Metadata {
		texts = append(texts, k+"="+v)
	}
	return texts
}

func isDangerousRecursiveDelete(cmd string, argv []string) bool {
	if cmd != "rm" {
		return false
	}
	recursive := false
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") && strings.Contains(a, "r") {
			recursive = true
		}
	}
	if !recursive {
		return false
	}
	for _, a := range argv[1:] {
		if strings.HasPrefix(a, "-") {
			continue
		}
		clean := path.Clean(strings.TrimSpace(a))
		if clean == "." || clean == "/" || clean == "~" ||
			strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") {
			return true
		}
	}
	return false
}

func isDependencyInstall(cmd string, argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	sub := argv[1]
	switch cmd {
	case "go", "npm", "npx", "pip", "pip3", "apt", "apt-get", "yum", "dnf", "brew":
		return sub == "install" || sub == "add" || sub == "get"
	default:
		return false
	}
}

func isInlineInterpreter(cmd string, argv []string) bool {
	if len(argv) < 2 {
		return false
	}
	switch cmd {
	case "python", "python3", "node", "perl", "ruby", "php":
		return hasArg(argv, "-c") || hasArg(argv, "-e")
	default:
		return false
	}
}

func downloadsIntoShell(cmds [][]string) bool {
	if len(cmds) < 2 {
		return false
	}
	for i := 0; i < len(cmds)-1; i++ {
		from := commandBase(cmds[i][0])
		to := commandBase(cmds[i+1][0])
		if (from == "curl" || from == "wget") &&
			(to == "sh" || to == "bash" || to == "python" || to == "python3") {
			return true
		}
	}
	return false
}

func unwrapCommandRunner(cmd string, argv []string) []string {
	if len(argv) < 2 {
		return nil
	}
	switch cmd {
	case "env":
		i := 1
		for i < len(argv) {
			a := argv[i]
			if strings.Contains(a, "=") && !strings.HasPrefix(a, "-") {
				i++
				continue
			}
			if strings.HasPrefix(a, "-") {
				i++
				continue
			}
			break
		}
		if i < len(argv) {
			return argv[i:]
		}
	case "command", "builtin", "exec", "nohup", "time", "timeout", "nice", "xargs":
		for i := 1; i < len(argv); i++ {
			if strings.HasPrefix(argv[i], "-") {
				continue
			}
			if cmd == "timeout" && i == 1 {
				continue
			}
			return argv[i:]
		}
	case "busybox":
		return argv[1:]
	}
	return nil
}

func hasArg(argv []string, want string) bool {
	for _, a := range argv[1:] {
		if a == want {
			return true
		}
	}
	return false
}

func extractQuotedCommands(code string) []string {
	if !strings.Contains(code, "os.system(") &&
		!strings.Contains(code, "os.popen(") &&
		!strings.Contains(code, "subprocess.") {
		return nil
	}
	var out []string
	for _, match := range quotedRe.FindAllStringSubmatch(code, -1) {
		if len(match) < 2 {
			continue
		}
		s := strings.TrimSpace(match[1])
		if looksLikeShellCommand(s) {
			out = append(out, s)
		}
	}
	return out
}

func looksLikeShellCommand(s string) bool {
	if strings.ContainsAny(s, "|;&<>$`") || strings.Contains(s, "://") {
		return true
	}
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return false
	}
	switch commandBase(fields[0]) {
	case "rm", "cat", "curl", "wget", "nc", "ssh", "scp", "sudo", "go", "npm", "pip", "pip3", "apt", "apt-get", "sleep", "yes", "find":
		return true
	default:
		return false
	}
}

func longSleep(argv []string, limit int) bool {
	for _, a := range argv[1:] {
		n, ok := sleepSeconds(a)
		if ok && n > limit {
			return true
		}
	}
	return false
}

func sleepSeconds(arg string) (int, bool) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 0, false
	}
	multiplier := 1
	last := arg[len(arg)-1]
	switch last {
	case 's', 'S':
		arg = arg[:len(arg)-1]
	case 'm', 'M':
		arg = arg[:len(arg)-1]
		multiplier = 60
	case 'h', 'H':
		arg = arg[:len(arg)-1]
		multiplier = 60 * 60
	case 'd', 'D':
		arg = arg[:len(arg)-1]
		multiplier = 24 * 60 * 60
	}
	n, err := strconv.Atoi(arg)
	if err != nil || n < 0 {
		return 0, false
	}
	if multiplier != 0 && n > int(^uint(0)>>1)/multiplier {
		return 0, false
	}
	return n * multiplier, true
}

func commandBase(cmd string) string {
	base := path.Base(strings.ReplaceAll(cmd, "\\", "/"))
	return strings.ToLower(strings.TrimSuffix(base, ".exe"))
}

func hasNetworkCommand(text string) bool {
	fields := strings.Fields(strings.ToLower(text))
	for _, f := range fields {
		f = strings.Trim(f, ";|&")
		switch f {
		case "curl", "wget", "nc", "netcat", "ssh", "scp", "sftp":
			return true
		}
	}
	return false
}

func scanNetworkCommandSegments(cmds [][]string, p Policy) []Finding {
	var findings []Finding
	for _, argv := range cmds {
		if len(argv) == 0 {
			continue
		}
		cmd := commandBase(argv[0])
		if cmd != "ssh" && cmd != "scp" && cmd != "sftp" {
			continue
		}
		for _, host := range sshLikeHosts(cmd, argv[1:]) {
			if domainAllowed(host, p.AllowedDomains) {
				continue
			}
			findings = append(findings, finding(ruleNetworkEgress,
				"network_egress", RiskCritical, host,
				"add the domain to allowed_domains or remove the outbound network call",
				p.NonWhitelistedNetworkAction))
		}
	}
	return findings
}

func schemelessNetworkHosts(text string) []string {
	fields := strings.Fields(text)
	var out []string
	for i, f := range fields {
		cmd := commandBase(strings.Trim(f, ";|&"))
		if !isNetworkCommandName(cmd) {
			continue
		}
		for _, arg := range fields[i+1:] {
			arg = strings.Trim(arg, `"' ;|&`)
			if arg == "" {
				continue
			}
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if cmd == "curl" || cmd == "wget" {
				if !looksLikeNetworkOperand(arg) {
					continue
				}
			}
			if cmd == "ssh" || cmd == "scp" || cmd == "sftp" {
				out = append(out, sshLikeHosts(cmd, fields[i+1:])...)
				break
			}
			if cmd == "nc" || cmd == "netcat" {
				if host := hostFromSchemelessTarget(arg); host != "" {
					out = append(out, host)
				}
				break
			}
			if strings.Contains(arg, "://") {
				break
			}
			if host := hostFromSchemelessTarget(arg); host != "" {
				out = append(out, host)
			}
			break
		}
	}
	return uniqueStrings(out)
}

func sshLikeHosts(cmd string, args []string) []string {
	var out []string
	optionsEnded := false
	pendingOption := ""
	for _, raw := range args {
		arg := strings.Trim(strings.TrimSpace(raw), `"'`)
		if shellSeparatorToken(arg) {
			break
		}
		arg = strings.Trim(arg, ";|&")
		if arg == "" {
			continue
		}
		if pendingOption != "" {
			out = append(out, hostsFromSSHLikeOptionOperand(cmd, pendingOption, arg)...)
			pendingOption = ""
			continue
		}
		if !optionsEnded && arg == "--" {
			optionsEnded = true
			continue
		}
		if !optionsEnded && strings.HasPrefix(arg, "-") {
			opt := strings.TrimLeft(arg, "-")
			for i := 0; i < len(opt); i++ {
				if !sshLikeOptionNeedsOperand(cmd, opt[i]) {
					continue
				}
				if operand := opt[i+1:]; operand != "" {
					out = append(out, hostsFromSSHLikeOptionOperand(cmd, opt[i:i+1], operand)...)
				} else {
					pendingOption = opt[i : i+1]
				}
				break
			}
			continue
		}
		if host := hostFromSSHLikeTargetForCommand(cmd, arg); host != "" {
			out = append(out, host)
			if cmd != "scp" {
				break
			}
		}
	}
	return uniqueStrings(out)
}

func hostsFromSSHLikeOptionOperand(cmd, opt, operand string) []string {
	opt = strings.TrimLeft(opt, "-")
	if opt == "" {
		return nil
	}
	switch opt[0] {
	case 'J':
		return hostsFromProxyJump(operand)
	case 'L', 'R':
		return hostsFromSSHForward(operand)
	case 'o':
		return hostsFromSSHConfigOption(operand)
	case 'W':
		if host := hostFromSSHLikeTarget(operand); host != "" {
			return []string{host}
		}
		return nil
	default:
		return nil
	}
}

func hostsFromSSHConfigOption(option string) []string {
	name, value, ok := strings.Cut(option, "=")
	if !ok {
		fields := strings.Fields(option)
		if len(fields) < 2 {
			return nil
		}
		name = fields[0]
		value = strings.Join(fields[1:], " ")
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "hostname":
		if host := hostFromSSHLikeTarget(value); host != "" {
			return []string{host}
		}
		return nil
	case "proxycommand":
		return schemelessNetworkHosts(value)
	case "proxyjump":
		return hostsFromProxyJump(value)
	case "localforward", "remoteforward":
		return hostsFromSSHForward(value)
	default:
		return nil
	}
}

func hostsFromSSHForward(value string) []string {
	value = strings.TrimSpace(value)
	fields := strings.Fields(value)
	if len(fields) > 1 {
		value = strings.Join(fields, ":")
	}
	parts := strings.Split(value, ":")
	if len(parts) < 3 {
		return nil
	}
	if host := hostFromSSHLikeTarget(parts[len(parts)-2]); host != "" {
		return []string{host}
	}
	return nil
}

func hostsFromProxyJump(value string) []string {
	var out []string
	for _, target := range strings.Split(value, ",") {
		target = strings.TrimSpace(target)
		if strings.EqualFold(target, "none") {
			continue
		}
		if host := hostFromSSHLikeTarget(target); host != "" {
			out = append(out, host)
		}
	}
	return uniqueStrings(out)
}

func shellSeparatorToken(arg string) bool {
	if arg == "" {
		return false
	}
	return strings.Trim(arg, ";|&") == ""
}

func sshLikeOptionNeedsOperand(cmd string, opt byte) bool {
	if opt == 0 {
		return false
	}
	switch cmd {
	case "ssh":
		return strings.ContainsRune("BbcDEeFIiJLlmOoPpQRSWw", rune(opt))
	case "scp":
		return strings.ContainsRune("cDFiJloPSX", rune(opt))
	case "sftp":
		return strings.ContainsRune("BbcDFiJloPRS", rune(opt))
	default:
		return false
	}
}

func hostFromSSHLikeTargetForCommand(cmd, target string) string {
	if cmd == "scp" && !strings.Contains(target, ":") && !strings.Contains(target, "@") {
		return ""
	}
	return hostFromSSHLikeTarget(target)
}

func looksLikeNetworkOperand(arg string) bool {
	if strings.Contains(arg, "://") {
		return true
	}
	return hostFromSchemelessTarget(arg) != ""
}

func isNetworkCommandName(cmd string) bool {
	switch cmd {
	case "curl", "wget", "nc", "netcat", "ssh", "scp", "sftp":
		return true
	default:
		return false
	}
}

func hostFromSSHLikeTarget(target string) string {
	if strings.Contains(target, ":") && !strings.Contains(target, "/") {
		target = strings.SplitN(target, ":", 2)[0]
	}
	if at := strings.LastIndex(target, "@"); at >= 0 {
		target = target[at+1:]
	}
	return hostFromSchemelessTarget(target)
}

func hostFromSchemelessTarget(target string) string {
	target = strings.TrimSpace(target)
	target = strings.TrimPrefix(target, "//")
	target = strings.SplitN(target, "/", 2)[0]
	target = strings.SplitN(target, "?", 2)[0]
	target = strings.SplitN(target, "#", 2)[0]
	target = strings.Trim(target, "[]")
	if h, _, ok := strings.Cut(target, ":"); ok {
		target = h
	}
	target = strings.ToLower(strings.TrimSuffix(target, "."))
	if !looksLikeHost(target) {
		return ""
	}
	return target
}

func looksLikeHost(host string) bool {
	if host == "" || strings.ContainsAny(host, `/\`) {
		return false
	}
	if strings.Contains(host, ".") {
		return true
	}
	return strings.EqualFold(host, "localhost")
}

func uniqueStrings(in []string) []string {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func hostFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func domainAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range allowed {
		d = strings.ToLower(strings.Trim(strings.TrimSpace(d), "."))
		if d == "" {
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func isDangerousEnvKey(key string) bool {
	return slices.Contains([]string{
		"LD_PRELOAD", "LD_LIBRARY_PATH", "DYLD_INSERT_LIBRARIES",
		"BASH_ENV", "ENV", "SHELLOPTS", "PATH",
	}, key)
}

func maxDecision(a, b Decision) Decision {
	if decisionRank(b) > decisionRank(a) {
		return b
	}
	return a
}

func maxRisk(a, b RiskLevel) RiskLevel {
	if riskRank(b) > riskRank(a) {
		return b
	}
	return a
}

func decisionRank(d Decision) int {
	switch d {
	case DecisionDeny:
		return 3
	case DecisionAsk:
		return 2
	default:
		return 1
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
	default:
		return 1
	}
}

func recommendationFor(d Decision) string {
	switch d {
	case DecisionDeny:
		return "blocked before execution; remove high-risk behavior or update policy after review"
	case DecisionAsk:
		return "requires human review before execution"
	default:
		return "allowed by the configured safety policy"
	}
}

func trimEvidence(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 180 {
		return s
	}
	return s[:177] + "..."
}

func evidenceAround(text, needle string) string {
	if needle == "" {
		return trimEvidence(text)
	}
	idx := strings.Index(strings.ToLower(text), strings.ToLower(needle))
	if idx < 0 {
		return trimEvidence(text)
	}
	start := idx - 40
	if start < 0 {
		start = 0
	}
	end := idx + len(needle) + 40
	if end > len(text) {
		end = len(text)
	}
	return trimEvidence(text[start:end])
}
