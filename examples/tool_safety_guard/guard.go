// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Policy struct {
	AllowedCommands   []string `json:"allowed_commands"`
	DeniedCommands    []string `json:"denied_commands"`
	ForbiddenPaths    []string `json:"forbidden_paths"`
	AllowedDomains    []string `json:"allowed_domains"`
	MaxTimeoutSeconds int      `json:"max_timeout_seconds"`
	MaxOutputBytes    int      `json:"max_output_bytes"`
	AllowedEnvVars    []string `json:"allowed_env_vars"`
}
type Request struct {
	ToolName       string            `json:"tool_name"`
	Command        string            `json:"command"`
	Backend        string            `json:"backend"`
	WorkingDir     string            `json:"working_dir,omitempty"`
	Environment    map[string]string `json:"environment,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int               `json:"max_output_bytes,omitempty"`
	PTY            bool              `json:"pty,omitempty"`
	Background     bool              `json:"background,omitempty"`
}
type Finding struct {
	Decision       string `json:"decision"`
	RiskLevel      string `json:"risk_level"`
	RuleID         string `json:"rule_id"`
	Evidence       string `json:"evidence"`
	Recommendation string `json:"recommendation"`
}
type ScanResult struct {
	ToolName       string            `json:"tool_name"`
	Command        string            `json:"command"`
	Backend        string            `json:"backend"`
	Decision       string            `json:"decision"`
	RiskLevel      string            `json:"risk_level"`
	RuleID         string            `json:"rule_id"`
	Evidence       string            `json:"evidence"`
	Recommendation string            `json:"recommendation"`
	Blocked        bool              `json:"blocked"`
	Redacted       bool              `json:"redacted"`
	DurationMicros int64             `json:"duration_micros"`
	Findings       []Finding         `json:"findings"`
	SpanAttributes map[string]string `json:"span_attributes"`
}
type AuditEvent struct {
	Timestamp      string `json:"timestamp"`
	ToolName       string `json:"tool_name"`
	Decision       string `json:"decision"`
	RiskLevel      string `json:"risk_level"`
	RuleID         string `json:"rule_id"`
	Backend        string `json:"backend"`
	DurationMicros int64  `json:"duration_micros"`
	Redacted       bool   `json:"redacted"`
	Blocked        bool   `json:"blocked"`
}

func LoadPolicy(path string) (Policy, error) {
	var p Policy
	data, e := os.ReadFile(path)
	if e != nil {
		return p, e
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if e = decoder.Decode(&p); e != nil {
		return p, fmt.Errorf("decode policy: %w", e)
	}
	if e = decoder.Decode(&struct{}{}); e != io.EOF {
		if e == nil {
			return p, fmt.Errorf("decode policy: trailing JSON value")
		}
		return p, fmt.Errorf("decode policy trailing data: %w", e)
	}
	if e = validatePolicy(p); e != nil {
		return p, e
	}
	return p, nil
}

func validatePolicy(p Policy) error {
	if len(p.AllowedCommands) == 0 {
		return fmt.Errorf("validate policy: allowed_commands must not be empty")
	}
	if len(p.DeniedCommands) == 0 {
		return fmt.Errorf("validate policy: denied_commands must not be empty")
	}
	if len(p.ForbiddenPaths) == 0 {
		return fmt.Errorf("validate policy: forbidden_paths must not be empty")
	}
	if p.MaxTimeoutSeconds <= 0 {
		return fmt.Errorf("validate policy: max_timeout_seconds must be positive")
	}
	if p.MaxOutputBytes <= 0 {
		return fmt.Errorf("validate policy: max_output_bytes must be positive")
	}
	return nil
}

type deniedCommandPattern struct {
	command string
	pattern *regexp.Regexp
}

type Guard struct {
	policy         Policy
	deniedPatterns []deniedCommandPattern
}

func NewGuard(p Policy) *Guard {
	g := &Guard{policy: p}
	for _, command := range p.DeniedCommands {
		command = strings.ToLower(command)
		g.deniedPatterns = append(g.deniedPatterns, deniedCommandPattern{
			command: command,
			pattern: regexp.MustCompile(`(^|[;&|\n]\s*)` + regexp.QuoteMeta(command) + `(\s|$)`),
		})
	}
	return g
}

var (
	secretKeyPattern      = `(?:[a-z0-9]+[_-])*(?:api[_-]?key|access[_-]?token|auth[_-]?token|token|password|passwd|secret)(?:[_-][a-z0-9]+)*`
	encodedSeparator      = `%(?:25)*(?:3d|3a)`
	quotedSecretRE        = regexp.MustCompile(`(?i)(["']?` + secretKeyPattern + `["']?)(\s*(?:[=:]|` + encodedSeparator + `)\s*)("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')`)
	secretAssignmentRE    = regexp.MustCompile(`(?i)(["']?` + secretKeyPattern + `["']?)(\s*(?:[=:]|` + encodedSeparator + `)\s*)[^\s,;&"']+`)
	secretFlagRE          = regexp.MustCompile(`(?i)(--?` + secretKeyPattern + `(?:\s+|=))("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|[^\s,;&]+)`)
	authorizationPrefixRE = regexp.MustCompile(`(?i)\b(?:proxy-)?authorization["']?\s*:\s*`)
	credentialOptionRE    = regexp.MustCompile(`(?i)((?:^|\s)(?:--(?:user|proxy-user|oauth2-bearer)(?:\s+|=)|-u(?:\s+|=)?))("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|[^\s,;&]+)`)
	urlUserinfoRE         = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@]+@`)
	networkURLRE          = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s"'<>|]+`)
	networkOverrideRE     = regexp.MustCompile(`(?i)(^|\s)(?:(?:--connect-to|--resolve|--proxy|--preproxy|--unix-socket|--config|--insecure|--location|--location-trusted)(?:[=\s]|$)|-[a-z]*l[a-z]*(?:[=\s]|$)|-[a-z]*[xk][^\s]*)`)
	gitNetworkOverrideRE  = regexp.MustCompile(`(?i)(^|\s)(?:-c|--config-env)(?:[=\s]|$)|\b(?:https?\.proxy|url\.[^\s]+\.insteadof)=`)
	destructiveCommandRE  = regexp.MustCompile(`\brm\s+[^\n]*(?:-rf|-fr)|\b(?:mkfs|dd)\b`)
	secretReadRE          = regexp.MustCompile(`(?i)(cat|type|grep|less|head|tail)\s+[^\n]*(\.env|credentials|id_rsa|\.ssh)`)
	shellWrapperRE        = regexp.MustCompile(`\b(?:sh|bash|dash|zsh|ksh|cmd(?:\.exe)?|powershell(?:\.exe)?|pwsh(?:\.exe)?)\s+(?:-[a-z]*c|/c|-command)\b|\beval\b`)
	dependencyChangeRE    = regexp.MustCompile(`\b(go\s+install|npm\s+install|pip\s+install|apt(?:-get)?\s+install)\b`)
	unboundedExecutionRE  = regexp.MustCompile(`\b(while\s+true|for\s*\(\s*;\s*;|yes\b|fork\s*bomb)`)
	sleepRE               = regexp.MustCompile(`\bsleep\s+(\d+)`)
)

func redact(s string) (string, bool) {
	out := quotedSecretRE.ReplaceAllString(s, `${1}${2}***REDACTED***`)
	out = secretFlagRE.ReplaceAllString(out, `${1}***REDACTED***`)
	out = credentialOptionRE.ReplaceAllString(out, `${1}***REDACTED***`)
	out, _ = redactAuthorizationHeaders(out)
	out = secretAssignmentRE.ReplaceAllString(out, `${1}${2}***REDACTED***`)
	out = urlUserinfoRE.ReplaceAllString(out, `${1}***REDACTED***@`)
	return out, out != s
}

func redactAuthorizationHeaders(input string) (string, bool) {
	out := input
	changed := false
	searchFrom := 0
	for searchFrom < len(out) {
		match := authorizationPrefixRE.FindStringIndex(out[searchFrom:])
		if match == nil {
			break
		}
		headerStart := searchFrom + match[0]
		valueStart := searchFrom + match[1]
		quote := byte(0)
		for i := headerStart - 1; i >= 0; i-- {
			if out[i] == ' ' || out[i] == '\t' {
				continue
			}
			if out[i] == '\'' || out[i] == '"' {
				quote = out[i]
			}
			break
		}
		replacementStart := valueStart
		if quote != 0 && replacementStart < len(out) && out[replacementStart] == quote {
			replacementStart++
		}
		valueEnd := authorizationValueEnd(out, replacementStart, quote)
		out = out[:replacementStart] + "***REDACTED***" + out[valueEnd:]
		searchFrom = replacementStart + len("***REDACTED***")
		changed = true
	}
	return out, changed
}

func authorizationValueEnd(input string, start int, quote byte) int {
	if quote != 0 {
		for i := start; i < len(input); i++ {
			if input[i] == quote && (i == start || input[i-1] != '\\') {
				return i
			}
		}
		return len(input)
	}
	for i := start; i < len(input); i++ {
		if input[i] == '\r' || input[i] == '\n' {
			return i
		}
		if input[i] != ' ' && input[i] != '\t' {
			continue
		}
		j := i
		for j < len(input) && (input[j] == ' ' || input[j] == '\t') {
			j++
		}
		if j < len(input) && input[j] == '-' {
			return i
		}
		if match := networkURLRE.FindStringIndex(input[j:]); match != nil && match[0] == 0 {
			return i
		}
	}
	return len(input)
}
func (g *Guard) Scan(req Request) ScanResult {
	started := time.Now()
	command, redacted := redact(strings.TrimSpace(req.Command))
	r := ScanResult{ToolName: req.ToolName, Command: command, Backend: req.Backend, Decision: "allow", RiskLevel: "low", RuleID: "ALLOW_POLICY", Evidence: "command satisfies configured policy", Recommendation: "execute only in the configured sandbox", Redacted: redacted}
	add := func(decision, risk, id, evidence, recommendation string) {
		ev, changed := redact(evidence)
		r.Redacted = r.Redacted || changed
		r.Findings = append(r.Findings, Finding{Decision: decision, RiskLevel: risk, RuleID: id, Evidence: ev, Recommendation: recommendation})
	}
	lower := strings.ToLower(req.Command)
	tokens := strings.Fields(lower)
	base := ""
	if len(tokens) > 0 {
		base = executableName(tokens[0])
	}
	for _, denied := range g.deniedPatterns {
		if base == denied.command || denied.pattern.MatchString(lower) {
			add("deny", "critical", "DENIED_COMMAND", denied.command, "remove the denied command")
		}
	}
	if len(tokens) > 0 && strings.ContainsAny(strings.Trim(tokens[0], "'\""), `/\`) {
		add("ask", "high", "EXPLICIT_EXECUTABLE_PATH", tokens[0], "use a reviewed bare command resolved from the executor's trusted PATH")
	}
	if destructiveCommandRE.MatchString(lower) {
		add("deny", "critical", "DESTRUCTIVE_COMMAND", req.Command, "do not run destructive filesystem commands")
	}
	workingDir := strings.ToLower(filepath.ToSlash(filepath.Clean(req.WorkingDir)))
	normalizedCommand := strings.ToLower(strings.ReplaceAll(req.Command, `\`, "/"))
	for _, path := range g.policy.ForbiddenPaths {
		normalizedPath := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
		if strings.Contains(normalizedCommand, normalizedPath) {
			add("deny", "critical", "FORBIDDEN_PATH", path, "remove access to the protected path")
		}
		if req.WorkingDir != "" && strings.Contains(workingDir, normalizedPath) {
			add("deny", "critical", "FORBIDDEN_WORKING_DIR", req.WorkingDir, "choose a working directory outside protected paths")
		}
	}
	if secretReadRE.MatchString(req.Command) {
		add("deny", "critical", "SECRET_READ", req.Command, "use a secret provider instead of reading credential files")
	}
	if isNetworkCommand(tokens) {
		targets := extractNetworkTargets(req.Command)
		if len(targets) == 0 {
			add("ask", "high", "NETWORK_UNPARSED", req.Command, "provide a literal allowlisted HTTPS URL")
		}
		if networkOverrideRE.MatchString(lower) || (base == "git" && gitNetworkOverrideRE.MatchString(lower)) {
			add("ask", "high", "NETWORK_OVERRIDE", req.Command, "remove network destination overrides and use a literal allowlisted HTTPS URL")
		}
		for _, target := range targets {
			if !strings.EqualFold(target.Scheme, "https") {
				add("ask", "high", "NETWORK_SCHEME", target.Scheme, "use an HTTPS URL for reviewed network access")
			}
			if !containsFold(g.policy.AllowedDomains, target.Hostname()) {
				add("deny", "critical", "NETWORK_NOT_ALLOWLISTED", target.Hostname(), "add a reviewed domain to allowed_domains or remove the request")
			}
		}
	}
	if isShellWrapper(tokens) || shellWrapperRE.MatchString(lower) {
		add("ask", "high", "SHELL_WRAPPER", req.Command, "expand and review the wrapped command before execution")
	}
	if strings.ContainsAny(req.Command, "`$|;&><\r\n") {
		add("ask", "high", "SHELL_METACHAR", req.Command, "use argv execution without shell metacharacters")
	}
	if dependencyChangeRE.MatchString(lower) {
		add("ask", "high", "DEPENDENCY_CHANGE", req.Command, "pin and review dependencies in an isolated build environment")
	}
	if unboundedExecutionRE.MatchString(lower) {
		add("deny", "critical", "UNBOUNDED_EXECUTION", req.Command, "replace unbounded work with a bounded operation")
	}
	if m := sleepRE.FindStringSubmatch(lower); len(m) > 1 {
		n, _ := strconv.Atoi(m[1])
		if n > g.policy.MaxTimeoutSeconds {
			add("ask", "medium", "LONG_RUNNING", m[0], "reduce duration below the configured timeout")
		}
	}
	if req.TimeoutSeconds <= 0 || req.TimeoutSeconds > g.policy.MaxTimeoutSeconds {
		add("deny", "high", "TIMEOUT_LIMIT", fmt.Sprint(req.TimeoutSeconds), "use a timeout within policy")
	}
	if req.MaxOutputBytes <= 0 || req.MaxOutputBytes > g.policy.MaxOutputBytes {
		add("deny", "high", "OUTPUT_LIMIT", fmt.Sprint(req.MaxOutputBytes), "lower the maximum output size")
	}
	if req.Backend != "workspaceexec" && req.Backend != "hostexec" && req.Backend != "codeexec" {
		add("deny", "high", "BACKEND_NOT_ALLOWED", req.Backend, "use workspaceexec, hostexec, or codeexec")
	}
	if req.Backend == "hostexec" && (req.PTY || req.Background || strings.Contains(lower, "&")) {
		add("ask", "high", "HOST_SESSION", req.Command, "prefer workspaceexec; require approval and process-tree cleanup")
	}
	for key := range req.Environment {
		if !containsFold(g.policy.AllowedEnvVars, key) {
			add("ask", "medium", "ENV_NOT_ALLOWLISTED", key, "remove the environment variable or add it after review")
		}
	}
	if len(tokens) == 0 {
		add("deny", "high", "EMPTY_COMMAND", "empty command", "provide an explicit argv command")
	} else if !containsFold(g.policy.AllowedCommands, base) && len(r.Findings) == 0 {
		add("ask", "medium", "COMMAND_NOT_ALLOWLISTED", base, "add the command to allowed_commands after review")
	}
	for _, f := range r.Findings {
		if rank(f.Decision) > rank(r.Decision) {
			r.Decision, r.RiskLevel, r.RuleID, r.Evidence, r.Recommendation = f.Decision, f.RiskLevel, f.RuleID, f.Evidence, f.Recommendation
		}
	}
	r.Blocked = r.Decision != "allow"
	r.DurationMicros = time.Since(started).Microseconds()
	r.SpanAttributes = map[string]string{"tool.safety.decision": r.Decision, "tool.safety.risk_level": r.RiskLevel, "tool.safety.rule_id": r.RuleID, "tool.safety.backend": r.Backend}
	return r
}

func isNetworkCommand(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	base := executableName(tokens[0])
	switch base {
	case "curl", "wget", "nc", "netcat", "ssh":
		return true
	case "git":
		for _, token := range tokens[1:] {
			switch strings.Trim(token, "'\"") {
			case "clone", "fetch", "pull", "push", "ls-remote", "submodule":
				return true
			}
		}
	case "go":
		for i, token := range tokens[1:] {
			token = strings.Trim(token, "'\"")
			if token == "get" || token == "install" {
				return true
			}
			if token == "mod" && i+2 < len(tokens) && strings.Trim(tokens[i+2], "'\"") == "download" {
				return true
			}
		}
	}
	return false
}

func isShellWrapper(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	switch executableName(tokens[0]) {
	case "sh", "bash", "dash", "zsh", "ksh", "cmd", "powershell", "pwsh", "eval":
		return true
	}
	return false
}

func executableName(token string) string {
	token = strings.Trim(token, "'\"")
	if index := strings.LastIndexAny(token, `/\`); index >= 0 {
		token = token[index+1:]
	}
	return strings.TrimSuffix(token, ".exe")
}
func rank(v string) int {
	switch v {
	case "deny":
		return 3
	case "ask", "needs_human_review":
		return 2
	case "allow":
		return 1
	}
	return 0
}
func containsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
func extractNetworkTargets(s string) []*url.URL {
	var out []*url.URL
	for _, match := range networkURLRE.FindAllString(s, -1) {
		match = strings.TrimRight(match, ".,;)]}")
		if target, err := url.Parse(match); err == nil && target.Hostname() != "" {
			out = append(out, target)
		}
	}
	return out
}

type Executor func(context.Context, Request) (string, error)

func (g *Guard) Wrap(next Executor, audit func(AuditEvent) error) Executor {
	return func(ctx context.Context, req Request) (string, error) {
		result := g.Scan(req)
		event := AuditEvent{
			Timestamp:      time.Now().UTC().Format(time.RFC3339Nano),
			ToolName:       req.ToolName,
			Decision:       result.Decision,
			RiskLevel:      result.RiskLevel,
			RuleID:         result.RuleID,
			Backend:        req.Backend,
			DurationMicros: result.DurationMicros,
			Redacted:       result.Redacted,
			Blocked:        result.Blocked,
		}
		if e := audit(event); e != nil {
			return "", e
		}
		if result.Blocked {
			return "", fmt.Errorf("tool execution blocked: %s (%s)", result.RuleID, result.Decision)
		}
		return next(ctx, req)
	}
}
