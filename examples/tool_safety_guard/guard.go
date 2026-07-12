// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
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
	e = json.Unmarshal(data, &p)
	return p, e
}

type Guard struct{ policy Policy }

func NewGuard(p Policy) *Guard { return &Guard{policy: p} }

var secretRE = regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret)\s*[=:]\s*[^\s]+`)

func redact(s string) (string, bool) {
	out := secretRE.ReplaceAllStringFunc(s, func(v string) string {
		if i := strings.IndexAny(v, "=:"); i >= 0 {
			return v[:i+1] + "***REDACTED***"
		}
		return "***REDACTED***"
	})
	return out, out != s
}
func (g *Guard) Scan(req Request) ScanResult {
	started := time.Now()
	command, redacted := redact(strings.TrimSpace(req.Command))
	r := ScanResult{ToolName: req.ToolName, Command: command, Backend: req.Backend, Decision: "allow", RiskLevel: "low", RuleID: "ALLOW_POLICY", Evidence: "command satisfies configured policy", Recommendation: "execute only in the configured sandbox", Redacted: redacted}
	add := func(decision, risk, id, evidence, recommendation string) {
		ev, changed := redact(evidence)
		r.Redacted = r.Redacted || changed
		r.Findings = append(r.Findings, Finding{decision, risk, id, ev, recommendation})
	}
	lower := strings.ToLower(req.Command)
	tokens := strings.Fields(lower)
	base := ""
	if len(tokens) > 0 {
		base = strings.Trim(tokens[0], "'\"")
	}
	for _, cmd := range g.policy.DeniedCommands {
		if base == strings.ToLower(cmd) || regexp.MustCompile(`(^|[;&|]\s*)`+regexp.QuoteMeta(strings.ToLower(cmd))+`(\s|$)`).MatchString(lower) {
			add("deny", "critical", "DENIED_COMMAND", cmd, "remove the denied command")
		}
	}
	if regexp.MustCompile(`\brm\s+[^\n]*(?:-rf|-fr)|\b(?:mkfs|dd)\b`).MatchString(lower) {
		add("deny", "critical", "DESTRUCTIVE_COMMAND", req.Command, "do not run destructive filesystem commands")
	}
	for _, path := range g.policy.ForbiddenPaths {
		if strings.Contains(lower, strings.ToLower(path)) {
			add("deny", "critical", "FORBIDDEN_PATH", path, "remove access to the protected path")
		}
	}
	if regexp.MustCompile(`(?i)(cat|type|grep|less|head|tail)\s+[^\n]*(\.env|credentials|id_rsa|\.ssh)`).MatchString(req.Command) {
		add("deny", "critical", "SECRET_READ", req.Command, "use a secret provider instead of reading credential files")
	}
	if regexp.MustCompile(`\b(curl|wget|nc|netcat|ssh)\b`).MatchString(lower) {
		hosts := extractHosts(req.Command)
		if len(hosts) == 0 {
			add("ask", "high", "NETWORK_UNPARSED", req.Command, "provide a literal allowlisted HTTPS URL")
		}
		for _, host := range hosts {
			if !containsFold(g.policy.AllowedDomains, host) {
				add("deny", "critical", "NETWORK_NOT_ALLOWLISTED", host, "add a reviewed domain to allowed_domains or remove the request")
			}
		}
	}
	if regexp.MustCompile(`\b(?:sh|bash|cmd|powershell)\s+(?:-c|/c|-command)\b|\beval\b`).MatchString(lower) {
		add("ask", "high", "SHELL_WRAPPER", req.Command, "expand and review the wrapped command before execution")
	}
	if strings.ContainsAny(req.Command, "`$") || strings.Contains(req.Command, "|") || strings.ContainsAny(req.Command, ";><") {
		add("ask", "high", "SHELL_METACHAR", req.Command, "use argv execution without shell metacharacters")
	}
	if regexp.MustCompile(`\b(go\s+install|npm\s+install|pip\s+install|apt(?:-get)?\s+install)\b`).MatchString(lower) {
		add("ask", "high", "DEPENDENCY_CHANGE", req.Command, "pin and review dependencies in an isolated build environment")
	}
	if regexp.MustCompile(`\b(while\s+true|for\s*\(\s*;\s*;|yes\b|fork\s*bomb)`).MatchString(lower) {
		add("deny", "critical", "UNBOUNDED_EXECUTION", req.Command, "replace unbounded work with a bounded operation")
	}
	if m := regexp.MustCompile(`\bsleep\s+(\d+)`).FindStringSubmatch(lower); len(m) > 1 {
		n, _ := strconv.Atoi(m[1])
		if n > g.policy.MaxTimeoutSeconds {
			add("ask", "medium", "LONG_RUNNING", m[0], "reduce duration below the configured timeout")
		}
	}
	if req.TimeoutSeconds > g.policy.MaxTimeoutSeconds {
		add("deny", "high", "TIMEOUT_LIMIT", fmt.Sprint(req.TimeoutSeconds), "use a timeout within policy")
	}
	if req.MaxOutputBytes > g.policy.MaxOutputBytes {
		add("deny", "high", "OUTPUT_LIMIT", fmt.Sprint(req.MaxOutputBytes), "lower the maximum output size")
	}
	if req.Backend == "hostexec" && (req.PTY || strings.Contains(lower, "&")) {
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
func extractHosts(s string) []string {
	var out []string
	for _, word := range strings.Fields(s) {
		word = strings.Trim(word, "'\";,|<>")
		if u, e := url.Parse(word); e == nil && u.Hostname() != "" {
			out = append(out, u.Hostname())
		}
	}
	return out
}

type Executor func(context.Context, Request) (string, error)

func (g *Guard) Wrap(next Executor, audit func(AuditEvent) error) Executor {
	return func(ctx context.Context, req Request) (string, error) {
		result := g.Scan(req)
		event := AuditEvent{time.Now().UTC().Format(time.RFC3339Nano), req.ToolName, result.Decision, result.RiskLevel, result.RuleID, req.Backend, result.DurationMicros, result.Redacted, result.Blocked}
		if e := audit(event); e != nil {
			return "", e
		}
		if result.Blocked {
			return "", fmt.Errorf("tool execution blocked: %s (%s)", result.RuleID, result.Decision)
		}
		return next(ctx, req)
	}
}
