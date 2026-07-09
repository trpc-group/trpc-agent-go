// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// Scanner scans execution requests before tools run.
type Scanner struct {
	policy   Policy
	redactor *Redactor
	now      func() time.Time
}

// NewScanner creates a scanner from policy. Empty policy fields are filled
// with conservative defaults.
func NewScanner(policy Policy) (*Scanner, error) {
	p, err := policy.normalized()
	if err != nil {
		return nil, err
	}
	redactor, err := NewRedactor(p.Redaction)
	if err != nil {
		return nil, err
	}
	return &Scanner{policy: p, redactor: redactor, now: time.Now}, nil
}

// MustScanner returns a scanner or panics. It is intended for examples.
func MustScanner(policy Policy) *Scanner {
	sc, err := NewScanner(policy)
	if err != nil {
		panic(err)
	}
	return sc
}

// Scan scans req and never executes it.
func (s *Scanner) Scan(ctx context.Context, req ExecutionRequest) (Report, error) {
	if s == nil {
		return Report{}, fmt.Errorf("nil safety scanner")
	}
	select {
	case <-ctx.Done():
		return Report{}, ctx.Err()
	default:
	}
	start := s.now()
	req.Backend = normalizeBackend(req.Backend)
	if req.ToolName == "" {
		req.ToolName = "unknown"
	}
	if req.Timeout == 0 && req.TimeoutMS > 0 {
		req.Timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	commandSummary := firstNonBlank(req.Command, req.Script)
	findings := s.scanRequest(req)
	dur := float64(s.now().Sub(start).Microseconds()) / 1000.0
	return newReport(req, commandSummary, findings, dur, s.redactor), nil
}

func (s *Scanner) scanRequest(req ExecutionRequest) []Finding {
	var findings []Finding
	text := strings.Join([]string{req.Command, strings.Join(req.Args, " "), req.Script, req.Cwd}, "\n")
	if strings.TrimSpace(req.Command) != "" {
		findings = append(findings, s.scanCommand(req.Command)...)
	}
	if strings.TrimSpace(req.Script) != "" {
		findings = append(findings, s.scanScript(req.Script, req.Language)...)
	}
	findings = append(findings, s.scanRawText(text)...)
	findings = append(findings, s.scanEnv(req.Env)...)
	findings = append(findings, s.scanResources(req)...)
	findings = append(findings, s.scanBackend(req)...)
	findings = s.applyOverrides(findings)
	return dedupeFindings(findings)
}

func (s *Scanner) scanCommand(command string) []Finding {
	var findings []Finding
	if lim := s.policy.ResourceLimits.MaxCommandBytes; lim > 0 && len(command) > lim {
		findings = append(findings, finding(
			RuleResourceOutput, CategoryResource, RiskMedium, DecisionAsk,
			fmt.Sprintf("command length %d exceeds max_command_bytes %d", len(command), lim),
			"command",
			"Shorten the command or raise max_command_bytes in policy after review.",
		))
	}
	pipe, err := shellsafe.Parse(command)
	if err != nil {
		findings = append(findings, finding(
			RuleShellParseUnsafe, CategoryShellBypass, RiskHigh, s.policy.ParseErrorAction,
			"shellsafe rejected command: "+err.Error(),
			"command",
			"Rewrite as a simple command without shell expansion, redirection, subshells, or wrappers.",
		))
		return append(findings, s.scanRawText(command)...)
	}
	if lim := s.policy.ResourceLimits.MaxSegments; lim > 0 && len(pipe.Commands) > lim {
		findings = append(findings, finding(
			RuleResourceParallelism, CategoryResource, RiskMedium, DecisionAsk,
			fmt.Sprintf("command contains %d segments, max_segments is %d", len(pipe.Commands), lim),
			"command",
			"Reduce command fan-out or require human review.",
		))
	}
	for i, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		loc := fmt.Sprintf("segment[%d]", i)
		findings = append(findings, s.scanArgv(argv, loc)...)
	}
	if strings.Contains(command, "|") {
		findings = append(findings, finding(
			RuleShellBypassConstruct, CategoryShellBypass, RiskMedium, DecisionAsk,
			"pipeline operator requires review because it can hide data flow between commands",
			"command",
			"Use a direct command or review the full pipeline before execution.",
		))
	}
	return findings
}

func (s *Scanner) scanArgv(argv []string, loc string) []Finding {
	var findings []Finding
	cmd := normalizeCommandName(argv[0])
	if isShellWrapper(cmd) {
		findings = append(findings, finding(
			RuleShellWrapper, CategoryShellBypass, RiskHigh, DecisionDeny,
			"shell wrapper command: "+cmd,
			loc,
			"Do not wrap commands in a shell; provide a directly parseable command instead.",
		))
	}
	if inList(cmd, s.policy.DeniedCommands) {
		findings = append(findings, finding(
			ruleForDeniedCommand(cmd), categoryForCommand(cmd), RiskHigh, DecisionDeny,
			"denied command: "+cmd,
			loc,
			"Use an allowed, auditable command or request human approval.",
		))
	}
	if len(s.policy.AllowedCommands) > 0 && !inList(cmd, s.policy.AllowedCommands) {
		findings = append(findings, finding(
			RuleHumanReview, CategoryPolicy, RiskMedium, s.policy.DefaultAction,
			"command is not in allowed_commands: "+cmd,
			loc,
			"Add the command to policy only after reviewing its safety boundary.",
		))
	}
	findings = append(findings, s.scanDangerousArgv(argv, loc)...)
	findings = append(findings, s.scanNetworkArgv(argv, loc)...)
	findings = append(findings, s.scanDependencyArgv(argv, loc)...)
	findings = append(findings, s.scanResourceArgv(argv, loc)...)
	findings = append(findings, s.scanForbiddenPaths(argv, loc)...)
	return findings
}

func isShellWrapper(cmd string) bool {
	switch cmd {
	case "sh", "bash", "zsh", "dash", "ash", "fish", "pwsh", "powershell", "cmd", "eval":
		return true
	default:
		return false
	}
}

func (s *Scanner) scanScript(script, language string) []Finding {
	var findings []Finding
	lang := strings.ToLower(strings.TrimSpace(language))
	if isShellLikeLanguage(lang) {
		action := s.policy.BackendRules.CodeExec.BashAction
		if action == "" {
			action = DecisionAsk
		}
		findings = append(findings, finding(
			RuleHumanReview, CategoryShellBypass, RiskMedium, action,
			"code execution contains shell language block",
			"script.language",
			"Review shell code before execution or use a more constrained language.",
		))
	}
	allowCommandScan := shouldScanScriptCommands(lang)
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		loc := fmt.Sprintf("script.line[%d]", i+1)
		findings = append(findings, s.scanRawTextAt(trimmed, loc)...)
		if allowCommandScan && looksLikeCommand(trimmed) {
			for _, f := range s.scanCommand(trimmed) {
				if f.Location == "" || f.Location == "command" {
					f.Location = loc
				}
				findings = append(findings, f)
			}
		}
	}
	return findings
}

func (s *Scanner) scanRawText(text string) []Finding {
	return s.scanRawTextAt(text, "text")
}

func (s *Scanner) scanRawTextAt(text, loc string) []Finding {
	var findings []Finding
	if text == "" {
		return findings
	}
	lower := strings.ToLower(text)
	for _, token := range []string{"`", "$(", "${", " 2>", "&>", "eval ", "sh -c", "bash -c"} {
		if strings.Contains(lower, strings.ToLower(token)) {
			findings = append(findings, finding(
				RuleShellBypassConstruct, CategoryShellBypass, RiskHigh, DecisionDeny,
				"shell bypass construct detected: "+token,
				loc,
				"Remove shell expansion, wrappers, and redirections before execution.",
			))
			break
		}
	}
	if hasSecret(text) {
		findings = append(findings, finding(
			RuleSecretLeak, CategorySecretLeak, RiskHigh, DecisionDeny,
			text,
			loc,
			"Remove secrets from commands, outputs, logs, and artifacts.",
		))
	}
	return findings
}

func (s *Scanner) scanDangerousArgv(argv []string, loc string) []Finding {
	var findings []Finding
	cmd := normalizeCommandName(argv[0])
	if cmd == "rm" && hasAnyFlag(argv[1:], "-rf", "-fr", "-r", "-R", "--recursive") {
		findings = append(findings, finding(
			RuleDangerousDelete, CategoryDangerousCommand, RiskCritical, DecisionDeny,
			"recursive delete command: "+strings.Join(argv, " "),
			loc,
			"Do not run recursive deletion through tools; delete reviewed workspace-relative paths manually.",
		))
	}
	if cmd == "dd" || cmd == "mkfs" {
		findings = append(findings, finding(
			RuleDangerousOverwrite, CategoryDangerousCommand, RiskCritical, DecisionDeny,
			"destructive overwrite/system command: "+cmd,
			loc,
			"Block disk or filesystem overwrite operations.",
		))
	}
	if cmd == "sudo" || cmd == "su" || cmd == "doas" {
		findings = append(findings, finding(
			RuleHostPrivilege, CategoryHostExec, RiskCritical, DecisionDeny,
			"privilege escalation command: "+cmd,
			loc,
			"Do not run privileged commands from agent tools.",
		))
	}
	return findings
}

func finding(ruleID string, cat Category, risk RiskLevel, action Decision, evidence, loc, rec string) Finding {
	return Finding{
		RuleID:         ruleID,
		Category:       cat,
		RiskLevel:      risk,
		Action:         action,
		Evidence:       evidence,
		Location:       loc,
		Recommendation: rec,
	}
}

func normalizeCommandName(s string) string {
	base := filepath.Base(strings.TrimSpace(s))
	base = strings.TrimSuffix(base, ".exe")
	base = strings.TrimSuffix(base, ".cmd")
	base = strings.TrimSuffix(base, ".bat")
	return strings.ToLower(base)
}

func inList(cmd string, list []string) bool {
	for _, item := range list {
		if normalizeCommandName(item) == cmd || strings.EqualFold(strings.TrimSpace(item), cmd) {
			return true
		}
	}
	return false
}

func hasAnyFlag(args []string, flags ...string) bool {
	for _, arg := range args {
		for _, flag := range flags {
			if arg == flag {
				return true
			}
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") {
			short := strings.TrimLeft(arg, "-")
			if isRecursiveForceShortFlag(short) {
				return true
			}
		}
	}
	return false
}

func isRecursiveForceShortFlag(short string) bool {
	if short == "" {
		return false
	}
	hasRecursive := false
	hasForce := false
	for _, r := range short {
		switch r {
		case 'r', 'R':
			hasRecursive = true
		case 'f', 'F':
			hasForce = true
		default:
			return false
		}
	}
	return hasRecursive && hasForce
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func looksLikeCommand(line string) bool {
	if strings.ContainsAny(line, "|;&<>`$") {
		return true
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return false
	}
	switch normalizeCommandName(fields[0]) {
	case "rm", "cat", "curl", "wget", "nc", "ssh", "go", "npm", "pip", "pip3", "apt", "apt-get", "sleep", "yes", "while", "for", "sh", "bash", "eval":
		return true
	default:
		return false
	}
}

func isShellLikeLanguage(lang string) bool {
	for _, part := range strings.Split(lang, ",") {
		switch strings.ToLower(strings.TrimSpace(part)) {
		case "bash", "sh", "shell", "zsh", "dash":
			return true
		}
	}
	return false
}

func shouldScanScriptCommands(lang string) bool {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return true
	}
	return isShellLikeLanguage(lang)
}

func ruleForDeniedCommand(cmd string) string {
	switch cmd {
	case "curl", "wget", "nc", "netcat", "ssh", "scp":
		return RuleNetworkDeniedDomain
	case "rm", "rmdir", "dd", "mkfs":
		return RuleDangerousDelete
	case "sudo", "su", "doas":
		return RuleHostPrivilege
	default:
		return RuleHumanReview
	}
}

func categoryForCommand(cmd string) Category {
	switch cmd {
	case "curl", "wget", "nc", "netcat", "ssh", "scp":
		return CategoryNetwork
	case "sudo", "su", "doas":
		return CategoryHostExec
	default:
		return CategoryDangerousCommand
	}
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|token|password|passwd|secret|credential)\s*[:=]\s*['"]?[^'"\s]+`),
	regexp.MustCompile(`(?i)authorization:\s*bearer\s+[A-Za-z0-9._~+/\-=]+`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

func hasSecret(s string) bool {
	for _, re := range secretPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func parseIntArg(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil
}

func (s *Scanner) applyOverrides(findings []Finding) []Finding {
	if len(s.policy.Rules) == 0 {
		return findings
	}
	out := append([]Finding(nil), findings...)
	for i := range out {
		override, ok := s.policy.Rules[out[i].RuleID]
		if !ok {
			continue
		}
		if override.Action != "" {
			out[i].Action = override.Action
		}
		if override.RiskLevel != "" {
			out[i].RiskLevel = override.RiskLevel
		}
	}
	return out
}

func dedupeFindings(in []Finding) []Finding {
	out := make([]Finding, 0, len(in))
	seen := map[string]struct{}{}
	for _, f := range in {
		key := f.RuleID + "\x00" + f.Location + "\x00" + f.Evidence
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	return out
}
