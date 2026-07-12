//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides a pre-execution safety guard for command and
// code-execution tools.
package safety

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

const (
	// DecisionAllow allows a tool invocation to execute.
	DecisionAllow Decision = "allow"
	// DecisionDeny blocks a tool invocation before execution.
	DecisionDeny Decision = "deny"
	// DecisionNeedsHumanReview asks a human to approve the invocation.
	DecisionNeedsHumanReview Decision = "needs_human_review"
	// DecisionAsk is a lighter approval-required decision for uncertain cases.
	DecisionAsk Decision = "ask"

	// RiskLow is informational risk.
	RiskLow RiskLevel = "low"
	// RiskMedium is review-worthy risk.
	RiskMedium RiskLevel = "medium"
	// RiskHigh is likely dangerous risk.
	RiskHigh RiskLevel = "high"
	// RiskCritical is immediately dangerous risk.
	RiskCritical RiskLevel = "critical"

	// BackendWorkspaceExec identifies workspace_exec style shell execution.
	BackendWorkspaceExec = "workspaceexec"
	// BackendHostExec identifies direct host shell execution.
	BackendHostExec = "hostexec"
	// BackendCodeExec identifies codeexec/codeexecutor execution.
	BackendCodeExec = "codeexec"
)

// Decision is the pre-execution safety decision.
type Decision string

// RiskLevel is the severity assigned to a scan report.
type RiskLevel string

// Policy controls how commands and scripts are scanned.
type Policy struct {
	AllowedCommands      []string `json:"allowed_commands,omitempty" yaml:"allowed_commands,omitempty"`
	DeniedCommands       []string `json:"denied_commands,omitempty" yaml:"denied_commands,omitempty"`
	DeniedPaths          []string `json:"denied_paths,omitempty" yaml:"denied_paths,omitempty"`
	NetworkAllowlist     []string `json:"network_allowlist,omitempty" yaml:"network_allowlist,omitempty"`
	MaxTimeoutSeconds    int      `json:"max_timeout_seconds,omitempty" yaml:"max_timeout_seconds,omitempty"`
	MaxOutputBytes       int64    `json:"max_output_bytes,omitempty" yaml:"max_output_bytes,omitempty"`
	EnvAllowlist         []string `json:"env_allowlist,omitempty" yaml:"env_allowlist,omitempty"`
	ReviewCommands       []string `json:"review_commands,omitempty" yaml:"review_commands,omitempty"`
	ReviewShellPipelines bool     `json:"review_shell_pipelines,omitempty" yaml:"review_shell_pipelines,omitempty"`
	DenyOnParseError     bool     `json:"deny_on_parse_error,omitempty" yaml:"deny_on_parse_error,omitempty"`

	reviewShellPipelinesSet bool
	denyOnParseErrorSet     bool
}

// DefaultPolicy returns a conservative policy suitable for workspaceexec,
// hostexec and codeexec wrappers.
func DefaultPolicy() Policy {
	return Policy{
		DeniedCommands: []string{
			"dd", "mkfs", "mount", "umount", "shutdown", "reboot",
			"halt", "poweroff", "sudo", "su", "doas",
		},
		DeniedPaths: []string{
			"/", "/bin", "/boot", "/dev", "/etc", "/lib", "/lib64",
			"/proc", "/root", "/sbin", "/sys", "/usr", "/var",
			"~/.ssh", ".ssh", ".env", ".npmrc", ".pypirc",
			"id_rsa", "id_ed25519", "credentials", "credential",
			"secrets", "secret",
		},
		NetworkAllowlist: []string{
			"api.github.com", "github.com", "proxy.golang.org",
			"sum.golang.org", "registry.npmjs.org", "pypi.org",
			"files.pythonhosted.org",
		},
		MaxTimeoutSeconds:    300,
		MaxOutputBytes:       4 * 1024 * 1024,
		ReviewShellPipelines: true,
		DenyOnParseError:     true,
		ReviewCommands: []string{
			"go install", "npm install", "npm ci", "pip install",
			"pip3 install", "apt install", "apt-get install",
			"brew install", "cargo install",
		},
		EnvAllowlist: []string{
			"PATH", "HOME", "TMPDIR", "TEMP", "TMP", "LANG", "LC_ALL",
			"CGO_ENABLED", "GOCACHE", "GOMODCACHE", "GOPATH",
		},
		reviewShellPipelinesSet: true,
		denyOnParseErrorSet:     true,
	}
}

// LoadPolicy loads a JSON or YAML policy file. Empty policy fields inherit
// their defaults so operators can override only the knobs they need.
func LoadPolicy(path string) (Policy, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Policy{}, err
	}
	var raw map[string]any
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		err = json.Unmarshal(b, &raw)
	case ".yaml", ".yml", "":
		err = yaml.Unmarshal(b, &raw)
	default:
		err = fmt.Errorf("unsupported policy extension %q", filepath.Ext(path))
	}
	if err != nil {
		return Policy{}, err
	}
	p := DefaultPolicy()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		err = json.Unmarshal(b, &p)
	default:
		err = yaml.Unmarshal(b, &p)
	}
	if err != nil {
		return Policy{}, err
	}
	if _, ok := raw["review_shell_pipelines"]; ok {
		p.reviewShellPipelinesSet = true
	}
	if _, ok := raw["deny_on_parse_error"]; ok {
		p.denyOnParseErrorSet = true
	}
	return p, nil
}

func (p Policy) withDefaults() Policy {
	d := DefaultPolicy()
	if p.DeniedCommands == nil {
		p.DeniedCommands = d.DeniedCommands
	}
	if p.DeniedPaths == nil {
		p.DeniedPaths = d.DeniedPaths
	}
	if p.NetworkAllowlist == nil {
		p.NetworkAllowlist = d.NetworkAllowlist
	}
	if p.MaxTimeoutSeconds == 0 {
		p.MaxTimeoutSeconds = d.MaxTimeoutSeconds
	}
	if p.MaxOutputBytes == 0 {
		p.MaxOutputBytes = d.MaxOutputBytes
	}
	if p.EnvAllowlist == nil {
		p.EnvAllowlist = d.EnvAllowlist
	}
	if p.ReviewCommands == nil {
		p.ReviewCommands = d.ReviewCommands
	}
	if !p.reviewShellPipelinesSet && !p.ReviewShellPipelines {
		p.ReviewShellPipelines = d.ReviewShellPipelines
	}
	if !p.denyOnParseErrorSet && !p.DenyOnParseError {
		p.DenyOnParseError = d.DenyOnParseError
	}
	return p
}

// ToolMetadata captures the execution-relevant metadata used by the guard.
type ToolMetadata struct {
	ReadOnly        bool `json:"read_only,omitempty"`
	Destructive     bool `json:"destructive,omitempty"`
	ConcurrencySafe bool `json:"concurrency_safe,omitempty"`
	SearchOrRead    bool `json:"search_or_read,omitempty"`
	OpenWorld       bool `json:"open_world,omitempty"`
	MaxResultSize   int  `json:"max_result_size,omitempty"`
}

// CodeBlock is a script block supplied to a code execution tool.
type CodeBlock struct {
	Language string `json:"language,omitempty"`
	Code     string `json:"code,omitempty"`
}

// Request describes one pending tool execution.
type Request struct {
	ToolName       string            `json:"tool_name,omitempty"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	Metadata       ToolMetadata      `json:"metadata,omitempty"`
	Backend        string            `json:"backend,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int64             `json:"max_output_bytes,omitempty"`
	Background     bool              `json:"background,omitempty"`
	TTY            bool              `json:"tty,omitempty"`
	CodeBlocks     []CodeBlock       `json:"code_blocks,omitempty"`
}

// Finding is one matched safety rule.
type Finding struct {
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id"`
	Evidence       []string  `json:"evidence"`
	Recommendation string    `json:"recommendation"`
}

// Report is the structured pre-execution scan result.
type Report struct {
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id,omitempty"`
	Evidence       []string  `json:"evidence,omitempty"`
	Recommendation string    `json:"recommendation,omitempty"`
	ToolName       string    `json:"tool_name,omitempty"`
	Command        string    `json:"command,omitempty"`
	Backend        string    `json:"backend,omitempty"`
	Blocked        bool      `json:"blocked"`
	Redacted       bool      `json:"redacted"`
	DurationMillis int64     `json:"duration_ms"`
	SafeSummary    string    `json:"safe_summary,omitempty"`
	Findings       []Finding `json:"findings,omitempty"`
}

// AuditEvent is the JSONL-friendly monitoring event emitted for every scan.
type AuditEvent struct {
	Timestamp      string    `json:"timestamp"`
	ToolName       string    `json:"tool_name,omitempty"`
	Decision       Decision  `json:"decision"`
	RiskLevel      RiskLevel `json:"risk_level"`
	RuleID         string    `json:"rule_id,omitempty"`
	DurationMillis int64     `json:"duration_ms"`
	Redacted       bool      `json:"redacted"`
	Blocked        bool      `json:"blocked"`
	Backend        string    `json:"backend,omitempty"`
}

// SpanAttributes returns OpenTelemetry-ready attribute keys for callers that
// want to attach scan outcomes to an active span.
func (r Report) SpanAttributes() map[string]string {
	return map[string]string{
		"tool.safety.decision":   string(r.Decision),
		"tool.safety.risk_level": string(r.RiskLevel),
		"tool.safety.rule_id":    r.RuleID,
		"tool.safety.backend":    r.Backend,
	}
}

// AuditEvent returns the structured monitoring event for the report.
func (r Report) AuditEvent(now time.Time) AuditEvent {
	return AuditEvent{
		Timestamp:      now.UTC().Format(time.RFC3339Nano),
		ToolName:       r.ToolName,
		Decision:       r.Decision,
		RiskLevel:      r.RiskLevel,
		RuleID:         r.RuleID,
		DurationMillis: r.DurationMillis,
		Redacted:       r.Redacted,
		Blocked:        r.Blocked,
		Backend:        r.Backend,
	}
}

// WriteAuditJSONL writes one audit event as a JSONL record.
func WriteAuditJSONL(w io.Writer, report Report) error {
	if w == nil {
		return nil
	}
	b, err := json.Marshal(report.AuditEvent(time.Now()))
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if _, err := w.Write(b); err != nil {
		return err
	}
	return nil
}

// AppendAuditFile appends one report event to a JSONL audit file.
func AppendAuditFile(path string, report Report) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return WriteAuditJSONL(f, report)
}

// Scan evaluates a pending tool execution against the policy.
func Scan(req Request, policy Policy) Report {
	start := time.Now()
	policy = policy.withDefaults()
	s := scanner{policy: policy}
	report := s.scan(req)
	report.DurationMillis = time.Since(start).Milliseconds()
	return report
}

type scanner struct {
	policy Policy
}

func (s scanner) scan(req Request) Report {
	redactor := newRedactor()
	cmd := requestCommand(req)
	findings := make([]Finding, 0, 4)
	findings = append(findings, s.scanMetadata(req)...)
	findings = append(findings, s.scanEnv(req.Env)...)
	findings = append(findings, s.scanRequestEnvelope(req)...)
	if len(req.CodeBlocks) > 0 {
		for _, block := range req.CodeBlocks {
			findings = append(findings, s.scanCodeBlock(req, block)...)
		}
	} else {
		findings = append(findings, s.scanShell(req, cmd)...)
	}
	report := s.reportFromFindings(req, cmd, findings, redactor)
	report.Command = redactor.redact(report.Command)
	report.Evidence = redactList(redactor, report.Evidence)
	report.Recommendation = redactor.redact(report.Recommendation)
	report.SafeSummary = redactor.redact(report.SafeSummary)
	for i := range report.Findings {
		report.Findings[i].Evidence = redactList(redactor, report.Findings[i].Evidence)
		report.Findings[i].Recommendation = redactor.redact(report.Findings[i].Recommendation)
	}
	report.Redacted = redactor.changed
	return report
}

func (s scanner) scanRequestEnvelope(req Request) []Finding {
	var findings []Finding
	if s.pathDenied(req.Cwd) {
		findings = append(findings, newFinding(
			DecisionDeny,
			RiskCritical,
			"sensitive.cwd_access",
			[]string{fmt.Sprintf("working directory %q is denied", req.Cwd)},
			"Choose a workspace-relative working directory that does not "+
				"point at system paths, SSH material, credentials, or secrets.",
		))
	}
	if req.Backend == BackendHostExec && (req.Background || req.TTY) {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview,
			RiskHigh,
			"hostexec.long_session",
			[]string{"hostexec requested background or PTY execution"},
			"Require human approval for host PTY/background sessions and "+
				"ensure timeout, process-group cleanup, and output caps are enforced.",
		))
	}
	if req.Background {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview,
			RiskMedium,
			"process.background",
			[]string{"command may leave a background process behind"},
			"Run foreground commands with a bounded timeout, or record the "+
				"session id and cleanup plan.",
		))
	}
	if req.TimeoutSeconds > s.policy.MaxTimeoutSeconds {
		findings = append(findings, newFinding(
			DecisionDeny,
			RiskHigh,
			"resource.timeout_exceeded",
			[]string{fmt.Sprintf("timeout %ds exceeds policy max %ds",
				req.TimeoutSeconds, s.policy.MaxTimeoutSeconds)},
			"Use a shorter timeout or update the safety policy after review.",
		))
	}
	if req.MaxOutputBytes > s.policy.MaxOutputBytes {
		findings = append(findings, newFinding(
			DecisionDeny,
			RiskHigh,
			"resource.output_limit_exceeded",
			[]string{fmt.Sprintf("requested output cap %d exceeds policy max %d",
				req.MaxOutputBytes, s.policy.MaxOutputBytes)},
			"Lower the output cap or collect a bounded artifact instead.",
		))
	}
	return findings
}

func (s scanner) reportFromFindings(
	req Request,
	command string,
	findings []Finding,
	redactor *redactor,
) Report {
	best := Finding{
		Decision:       DecisionAllow,
		RiskLevel:      RiskLow,
		Recommendation: "Command matched no high-risk safety rules.",
	}
	for _, f := range findings {
		if findingBeats(f, best) {
			best = f
		}
	}
	decision := best.Decision
	blocked := decision == DecisionDeny ||
		decision == DecisionAsk ||
		decision == DecisionNeedsHumanReview
	summary := ""
	if decision == DecisionAllow {
		summary = safeSummary(req, command)
	}
	return Report{
		Decision:       decision,
		RiskLevel:      best.RiskLevel,
		RuleID:         best.RuleID,
		Evidence:       best.Evidence,
		Recommendation: best.Recommendation,
		ToolName:       req.ToolName,
		Command:        redactor.redact(command),
		Backend:        req.Backend,
		Blocked:        blocked,
		SafeSummary:    summary,
		Findings:       findings,
	}
}

func safeSummary(req Request, command string) string {
	if len(req.CodeBlocks) > 0 {
		return fmt.Sprintf(
			"%s scan allowed %d code block(s); no high-risk network, path, "+
				"shell, resource, dependency, or secret patterns matched.",
			req.Backend, len(req.CodeBlocks))
	}
	return fmt.Sprintf(
		"%s scan allowed command %q; no high-risk network, path, shell, "+
			"resource, dependency, or secret patterns matched.",
		req.Backend, command)
}

func requestCommand(req Request) string {
	parts := make([]string, 0, 1+len(req.Args))
	if strings.TrimSpace(req.Command) != "" {
		parts = append(parts, strings.TrimSpace(req.Command))
	}
	parts = append(parts, req.Args...)
	return strings.Join(parts, " ")
}

func findingBeats(a, b Finding) bool {
	if decisionRank(a.Decision) != decisionRank(b.Decision) {
		return decisionRank(a.Decision) > decisionRank(b.Decision)
	}
	return riskRank(a.RiskLevel) > riskRank(b.RiskLevel)
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

func riskRank(level RiskLevel) int {
	switch level {
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

func (s scanner) scanShell(req Request, command string) []Finding {
	var findings []Finding
	command = strings.TrimSpace(command)
	if command == "" {
		return []Finding{newFinding(
			DecisionDeny,
			RiskHigh,
			"command.empty",
			[]string{"command is empty"},
			"Provide an explicit command before invoking the tool.",
		)}
	}
	findings = append(findings, s.scanRawCommand(req, command)...)
	pipe, err := shellsafe.Parse(command)
	if err != nil {
		decision := DecisionAsk
		if s.policy.DenyOnParseError {
			decision = DecisionDeny
		}
		findings = append(findings, newFinding(
			decision,
			RiskHigh,
			"shellsafe.parse_error",
			[]string{err.Error()},
			"Rewrite the command without shell expansion, redirection, "+
				"subshells, background operators, or other unsupported shell syntax.",
		))
		return findings
	}
	findings = append(findings, s.scanParsedCommands(pipe)...)
	return findings
}

func (s scanner) scanRawCommand(req Request, command string) []Finding {
	var findings []Finding
	lower := strings.ToLower(command)
	findings = append(findings, s.scanSecretText(command, "command")...)
	if hasShellBypass(lower) {
		findings = append(findings, newFinding(
			DecisionDeny,
			RiskHigh,
			"shell.bypass",
			[]string{"command contains shell bypass syntax or wrapper"},
			"Avoid sh -c, bash -c, eval, backticks, $(), variable expansion, "+
				"redirections, and process substitutions in tool commands.",
		))
	}
	findings = append(findings, s.scanNetwork(command)...)
	if s.policy.ReviewShellPipelines && containsPipeline(command) {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview,
			RiskMedium,
			"shell.pipeline_review",
			[]string{"command contains a shell pipeline or command chain"},
			"Review multi-stage shell commands manually or replace them with "+
				"a small audited script.",
		))
	}
	if strings.Contains(lower, " &") || strings.HasSuffix(lower, "&") {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview,
			RiskMedium,
			"process.background",
			[]string{"command may leave a background process behind"},
			"Run foreground commands with a bounded timeout, or record the "+
				"session id and cleanup plan.",
		))
	}
	findings = append(findings, s.scanResourcePatterns(lower)...)
	return findings
}

func (s scanner) scanParsedCommands(pipe *shellsafe.Pipeline) []Finding {
	var findings []Finding
	for _, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		name := commandName(argv[0])
		full := strings.Join(argv, " ")
		findings = append(findings, s.scanDeniedCommand(name)...)
		if len(s.policy.AllowedCommands) > 0 && !s.commandAllowed(argv[0]) {
			findings = append(findings, newFinding(
				DecisionDeny,
				RiskMedium,
				"policy.command_not_allowed",
				[]string{fmt.Sprintf("command %q is not in allowed_commands", argv[0])},
				"Use an allowed command or update allowed_commands in the policy.",
			))
		}
		if containsEnvSplitString(argv) {
			findings = append(findings, newFinding(
				DecisionDeny,
				RiskCritical,
				"shell.env_split_string",
				[]string{"env split-string can hide an arbitrary command payload"},
				"Do not use env -S or --split-string in tool commands; pass the "+
					"command and arguments directly.",
			))
		}
		findings = append(findings, s.scanDangerousCommand(name, argv)...)
		findings = append(findings, s.scanReviewCommand(full)...)
		for _, effective := range commandChain(argv)[1:] {
			effectiveName := commandName(effective[0])
			findings = append(findings, s.scanDeniedCommand(effectiveName)...)
			findings = append(findings,
				s.scanDangerousCommand(effectiveName, effective)...)
			findings = append(findings,
				s.scanReviewCommand(strings.Join(effective, " "))...)
		}
		findings = append(findings, s.scanDeniedPaths(argv)...)
	}
	return findings
}

func (s scanner) scanDeniedCommand(name string) []Finding {
	if !s.commandDenied(name) {
		return nil
	}
	return []Finding{newFinding(
		DecisionDeny,
		RiskHigh,
		"policy.denied_command",
		[]string{fmt.Sprintf("command %q is denied", name)},
		"Remove the denied command or change the policy after review.",
	)}
}

func (s scanner) scanDangerousCommand(name string, argv []string) []Finding {
	var findings []Finding
	if name == "rm" && destructiveRM(argv) {
		findings = append(findings, newFinding(
			DecisionDeny,
			RiskCritical,
			"dangerous.rm_rf",
			[]string{strings.Join(argv, " ")},
			"Do not run recursive forced deletion through tool execution.",
		))
	}
	if name == "chmod" && containsArg(argv, "-R") {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview,
			RiskHigh,
			"dangerous.recursive_chmod",
			[]string{strings.Join(argv, " ")},
			"Review recursive permission changes before executing.",
		))
	}
	return findings
}

func destructiveRM(argv []string) bool {
	recursive := false
	force := false
	targetSystem := false
	for _, arg := range argv[1:] {
		if strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, "r") || strings.Contains(arg, "R") {
				recursive = true
			}
			if strings.Contains(arg, "f") {
				force = true
			}
			continue
		}
		if isSystemPath(arg) {
			targetSystem = true
		}
	}
	return recursive && (force || targetSystem)
}

func isSystemPath(path string) bool {
	p := strings.TrimSpace(strings.Trim(path, `"'`))
	if p == "" {
		return false
	}
	if p == "/" || p == "\\" {
		return true
	}
	p = strings.ReplaceAll(p, "\\", "/")
	system := []string{
		"/bin", "/boot", "/dev", "/etc", "/lib", "/lib64", "/proc",
		"/root", "/sbin", "/sys", "/usr", "/var",
		"c:/windows", "c:/program files", "c:/programdata",
	}
	lp := strings.ToLower(p)
	return slices.ContainsFunc(system, func(prefix string) bool {
		return lp == prefix || strings.HasPrefix(lp, prefix+"/")
	})
}

func (s scanner) scanReviewCommand(command string) []Finding {
	lower := strings.ToLower(strings.TrimSpace(command))
	for _, review := range s.policy.ReviewCommands {
		r := strings.ToLower(strings.TrimSpace(review))
		if r != "" && strings.HasPrefix(lower, r) {
			return []Finding{newFinding(
				DecisionNeedsHumanReview,
				RiskMedium,
				"dependency.environment_change",
				[]string{fmt.Sprintf("command starts with %q", review)},
				"Dependency installation or environment mutation should be "+
					"reviewed and pinned before execution.",
			)}
		}
	}
	return nil
}

func (s scanner) scanDeniedPaths(argv []string) []Finding {
	var findings []Finding
	for _, arg := range argv[1:] {
		for _, candidate := range pathCandidates(arg) {
			if s.pathDenied(candidate) {
				findings = append(findings, newFinding(
					DecisionDeny,
					RiskCritical,
					"sensitive.path_access",
					[]string{fmt.Sprintf("argument references denied path %q", candidate)},
					"Do not access SSH keys, .env files, credential stores, or "+
						"system directories from tool execution.",
				))
				break
			}
		}
	}
	return findings
}

func pathCandidates(arg string) []string {
	clean := strings.TrimSpace(strings.Trim(arg, `"'`))
	if clean == "" {
		return nil
	}
	candidates := []string{clean}
	if key, value, ok := strings.Cut(clean, "="); ok &&
		strings.HasPrefix(key, "-") && value != "" {
		candidates = append(candidates, strings.Trim(value, `"'`))
	}
	return candidates
}

func (s scanner) pathDenied(path string) bool {
	if path == "" {
		return false
	}
	normalized := normalizePathForMatch(path)
	for _, denied := range s.policy.DeniedPaths {
		d := normalizePathForMatch(denied)
		if d == "" {
			continue
		}
		if normalized == d ||
			strings.Contains(normalized, "/"+d+"/") ||
			strings.HasSuffix(normalized, "/"+d) ||
			strings.HasPrefix(normalized, d+"/") {
			return true
		}
		deniedBase := pathBase(d)
		if strings.HasPrefix(deniedBase, ".") &&
			strings.Contains(pathBase(normalized), deniedBase) {
			return true
		}
	}
	return false
}

func pathBase(path string) string {
	if index := strings.LastIndex(path, "/"); index >= 0 {
		return path[index+1:]
	}
	return path
}

func normalizePathForMatch(path string) string {
	p := strings.TrimSpace(strings.Trim(path, `"'`))
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "~/")
	p = strings.TrimPrefix(p, "./")
	p = strings.ToLower(p)
	if p == "/" {
		return p
	}
	return strings.Trim(p, "/")
}

func (s scanner) scanNetwork(command string) []Finding {
	var findings []Finding
	networkCommand := networkCommandRE.MatchString(command)
	if pipe, err := shellsafe.Parse(command); err == nil {
		for _, argv := range pipe.Commands {
			if containsNetworkCommand(argv) {
				networkCommand = true
			}
		}
	}
	targets := explicitURLRE.FindAllString(command, -1)
	if networkCommand {
		targets = extractURLs(command)
	}
	checkedHosts := make(map[string]struct{})
	for _, raw := range targets {
		host := hostOf(raw)
		if host == "" {
			continue
		}
		if _, ok := checkedHosts[host]; ok {
			continue
		}
		checkedHosts[host] = struct{}{}
		if !s.hostAllowed(host) {
			findings = append(findings, newFinding(
				DecisionDeny,
				RiskHigh,
				"network.non_whitelisted_domain",
				[]string{fmt.Sprintf("domain %q is not in network_allowlist", host)},
				"Use a whitelisted domain or update network_allowlist after review.",
			))
		}
	}
	if networkCommand && len(checkedHosts) == 0 {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview,
			RiskMedium,
			"network.unresolved_target",
			[]string{"network-capable command has no parseable URL target"},
			"Review the target manually or provide an explicit whitelisted URL.",
		))
	}
	return findings
}

func extractURLs(s string) []string {
	targets := explicitURLRE.FindAllString(s, -1)
	remaining := make([]string, 0, len(strings.Fields(s)))
	for _, field := range strings.Fields(s) {
		clean := strings.Trim(field, `"'`)
		if match := scpLikeNetworkTargetRE.FindStringSubmatch(clean); len(match) == 2 {
			targets = append(targets, match[1])
			continue
		}
		remaining = append(remaining, field)
	}
	return append(targets,
		schemeLessNetworkTargetRE.FindAllString(strings.Join(remaining, " "), -1)...)
}

func hostOf(raw string) string {
	raw = strings.TrimSpace(strings.Trim(raw, `"'`))
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func isNetworkCommand(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	switch commandName(argv[0]) {
	case "curl", "wget", "nc", "netcat", "ssh", "scp", "rsync":
		return true
	case "git":
		return isGitNetworkOperation(argv)
	default:
		return false
	}
}

func isGitNetworkOperation(argv []string) bool {
	for i := 1; i < len(argv); i++ {
		arg := strings.ToLower(argv[i])
		if gitGlobalOptionHasValue(arg) {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch arg {
		case "clone", "fetch", "pull", "push", "ls-remote", "submodule":
			return true
		case "remote":
			return slices.Contains(argv[i+1:], "update")
		default:
			return false
		}
	}
	return false
}

func gitGlobalOptionHasValue(arg string) bool {
	switch arg {
	case "-c", "-C", "--git-dir", "--work-tree", "--namespace", "--config-env":
		return true
	default:
		return false
	}
}

func containsNetworkCommand(argv []string) bool {
	for _, command := range commandChain(argv) {
		if isNetworkCommand(command) {
			return true
		}
	}
	return false
}

func commandChain(argv []string) [][]string {
	var commands [][]string
	for len(argv) > 0 {
		commands = append(commands, argv)
		next := unwrapCommand(argv)
		if len(next) == 0 || len(next) >= len(argv) {
			return commands
		}
		argv = next
	}
	return commands
}

func unwrapCommand(argv []string) []string {
	name := commandName(argv[0])
	switch name {
	case "env":
		return unwrapEnvCommand(argv)
	case "command", "nohup":
		return unwrapOptionCommand(argv, false)
	case "timeout":
		return unwrapTimeoutCommand(argv)
	case "nice":
		return unwrapOptionCommand(argv, true)
	}
	return argv
}

func unwrapEnvCommand(argv []string) []string {
	if envSplitStringRequested(argv) {
		return nil
	}
	for i := 1; i < len(argv); {
		arg := argv[i]
		if arg == "-u" || arg == "--unset" || arg == "-C" || arg == "--chdir" {
			i += 2
			continue
		}
		if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
			i++
			continue
		}
		return argv[i:]
	}
	return nil
}

func containsEnvSplitString(argv []string) bool {
	for _, command := range commandChain(argv) {
		if commandName(command[0]) == "env" && envSplitStringRequested(command) {
			return true
		}
	}
	return false
}

func envSplitStringRequested(argv []string) bool {
	for i := 1; i < len(argv); {
		arg := argv[i]
		if arg == "--" {
			return false
		}
		if isEnvSplitStringOption(arg) {
			return true
		}
		if arg == "-u" || arg == "--unset" || arg == "-C" || arg == "--chdir" {
			i += 2
			continue
		}
		if strings.HasPrefix(arg, "-") || strings.Contains(arg, "=") {
			i++
			continue
		}
		return false
	}
	return false
}

func isEnvSplitStringOption(arg string) bool {
	if arg == "--split-string" || strings.HasPrefix(arg, "--split-string=") {
		return true
	}
	if !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}
	for _, option := range arg[1:] {
		switch option {
		case 'S':
			return true
		case 'u', 'C':
			return false
		case 'i', '0', 'v':
			continue
		default:
			return false
		}
	}
	return false
}

func unwrapOptionCommand(argv []string, optionHasValue bool) []string {
	for i := 1; i < len(argv); {
		arg := argv[i]
		if optionHasValue && (arg == "-n" || arg == "--adjustment") {
			i += 2
			continue
		}
		if strings.HasPrefix(arg, "-") {
			i++
			continue
		}
		return argv[i:]
	}
	return nil
}

func unwrapTimeoutCommand(argv []string) []string {
	for i := 1; i < len(argv); {
		arg := argv[i]
		if arg == "-s" || arg == "--signal" || arg == "-k" || arg == "--kill-after" {
			i += 2
			continue
		}
		if strings.HasPrefix(arg, "-") {
			i++
			continue
		}
		i++ // Skip the required duration.
		if i < len(argv) {
			return argv[i:]
		}
		return nil
	}
	return nil
}

func (s scanner) hostAllowed(host string) bool {
	if host == "" {
		return false
	}
	for _, allowed := range s.policy.NetworkAllowlist {
		a := strings.ToLower(strings.TrimSpace(allowed))
		if a == "" {
			continue
		}
		if host == a || strings.HasSuffix(host, "."+a) {
			return true
		}
	}
	return false
}

func (s scanner) scanResourcePatterns(lower string) []Finding {
	var findings []Finding
	if longSleep(lower) {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview,
			RiskMedium,
			"resource.long_sleep",
			[]string{"command contains a long sleep"},
			"Use a shorter sleep or a bounded wait condition.",
		))
	}
	if infiniteLoop(lower) {
		findings = append(findings, newFinding(
			DecisionDeny,
			RiskHigh,
			"resource.infinite_loop",
			[]string{"command appears to contain an infinite loop"},
			"Replace the unbounded loop with a bounded command and timeout.",
		))
	}
	if largeOutput(lower, s.policy.MaxOutputBytes) {
		findings = append(findings, newFinding(
			DecisionDeny,
			RiskHigh,
			"resource.large_output",
			[]string{"command appears to generate output above the policy limit"},
			"Limit output size, write bounded artifacts, or raise the policy "+
				"limit after review.",
		))
	}
	if highConcurrency(lower) {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview,
			RiskHigh,
			"resource.high_concurrency",
			[]string{"command appears to start many concurrent workers"},
			"Use a bounded, reviewed concurrency level before executing.",
		))
	}
	return findings
}

func longSleep(lower string) bool {
	m := longSleepRE.FindStringSubmatch(lower)
	if len(m) != 2 {
		return false
	}
	n, _ := strconv.Atoi(m[1])
	return n > 300
}

func infiniteLoop(lower string) bool {
	patterns := []string{
		"while true", "while(true)", "for ;;", "for(;;)",
		"while 1", "while(1)",
	}
	return slices.ContainsFunc(patterns, func(p string) bool {
		return strings.Contains(lower, p)
	})
}

func largeOutput(lower string, limit int64) bool {
	if m := headBytesRE.FindStringSubmatch(lower); len(m) == 2 {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		return n > limit
	}
	if strings.Contains(lower, "yes ") || strings.HasPrefix(lower, "yes") {
		return true
	}
	return printRepeatRE.MatchString(lower)
}

func highConcurrency(lower string) bool {
	if m := xargsParallelRE.FindStringSubmatch(lower); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		return n > 8
	}
	if m := parallelJobsRE.FindStringSubmatch(lower); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		return n > 8
	}
	return strings.Contains(lower, "gnu parallel")
}

func (s scanner) scanMetadata(req Request) []Finding {
	if req.Metadata.Destructive {
		return []Finding{newFinding(
			DecisionNeedsHumanReview,
			RiskMedium,
			"tool.metadata_destructive",
			[]string{"tool metadata marks the tool as destructive"},
			"Require review for destructive tools unless a narrower policy "+
				"allows this exact operation.",
		)}
	}
	return nil
}

func (s scanner) scanEnv(env map[string]string) []Finding {
	var findings []Finding
	for k, v := range env {
		if len(s.policy.EnvAllowlist) > 0 && !s.envAllowed(k) {
			findings = append(findings, newFinding(
				DecisionNeedsHumanReview,
				RiskMedium,
				"environment.non_whitelisted_variable",
				[]string{fmt.Sprintf("environment variable %q is not allowlisted", k)},
				"Pass only environment variables listed in env_allowlist or "+
					"update the policy after review.",
			))
		}
		findings = append(findings, s.scanSecretText(k+"="+v, "environment")...)
	}
	return findings
}

func (s scanner) envAllowed(key string) bool {
	for _, allowed := range s.policy.EnvAllowlist {
		if strings.EqualFold(strings.TrimSpace(allowed), key) {
			return true
		}
	}
	return false
}

func (s scanner) scanSecretText(text, source string) []Finding {
	if !looksSensitive(text) {
		return nil
	}
	return []Finding{newFinding(
		DecisionDeny,
		RiskCritical,
		"sensitive.secret_leak",
		[]string{fmt.Sprintf("%s contains a likely secret", source)},
		"Remove API keys, tokens, passwords, private keys, and credential "+
			"material from command arguments, logs, artifacts, and audit events.",
	)}
}

func (s scanner) scanCodeBlock(req Request, block CodeBlock) []Finding {
	lang := strings.ToLower(strings.TrimSpace(block.Language))
	code := strings.TrimSpace(block.Code)
	var findings []Finding
	findings = append(findings, s.scanSecretText(code, "code block")...)
	if lang == "bash" || lang == "sh" || lang == "shell" || lang == "" {
		shellReq := req
		shellReq.ToolName = "code_block"
		shellReq.Command = code
		shellReq.Backend = BackendCodeExec
		shellReq.CodeBlocks = nil
		if strings.ContainsAny(code, "\r\n") {
			code = normalizeShellScript(code)
		}
		findings = append(findings, s.scanShell(shellReq, code)...)
		return findings
	}
	lower := strings.ToLower(code)
	if strings.Contains(lower, "os.system") ||
		strings.Contains(lower, "subprocess.") ||
		strings.Contains(lower, "exec(") {
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview,
			RiskMedium,
			"codeexec.host_command_bridge",
			[]string{fmt.Sprintf("%s code can launch shell commands", lang)},
			"Review code that bridges from code execution into shell execution.",
		))
	}
	findings = append(findings, s.scanNetwork(code)...)
	findings = append(findings, s.scanResourcePatterns(lower)...)
	return findings
}

func hasShellBypass(lower string) bool {
	patterns := []string{
		"sh -c", "bash -c", "zsh -c", "eval ", "`", "$(",
		"${", "exec ",
	}
	if slices.ContainsFunc(patterns, func(p string) bool {
		return strings.Contains(lower, p)
	}) {
		return true
	}
	return containsUnquotedRedirect(lower)
}

func containsUnquotedRedirect(command string) bool {
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(command); i++ {
		char := command[i]
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' && !inSingle {
			escaped = true
			continue
		}
		switch char {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '>', '<':
			if !inSingle && !inDouble {
				return true
			}
		}
	}
	return false
}

func normalizeShellScript(script string) string {
	var normalized strings.Builder
	normalized.Grow(len(script) + strings.Count(script, "\n")*2)
	inSingle := false
	inDouble := false
	for i := 0; i < len(script); i++ {
		char := script[i]
		if end, ok := shellLineContinuationEnd(script, i); ok {
			normalized.WriteByte(' ')
			i = end
			continue
		}
		if char == '\\' && !inSingle && i+1 < len(script) {
			normalized.WriteByte(char)
			normalized.WriteByte(script[i+1])
			i++
			continue
		}
		if char == '\r' || char == '\n' {
			i = shellNewlineEnd(script, i)
			writeScriptNewline(&normalized, script, i+1, inSingle || inDouble)
			continue
		}
		if char == '\'' && !inDouble {
			inSingle = !inSingle
		}
		if char == '"' && !inSingle {
			inDouble = !inDouble
		}
		normalized.WriteByte(char)
	}
	return strings.TrimSpace(normalized.String())
}

func shellLineContinuationEnd(script string, index int) (int, bool) {
	if script[index] != '\\' || index+1 >= len(script) {
		return index, false
	}
	next := script[index+1]
	if next != '\n' && next != '\r' {
		return index, false
	}
	end := index + 1
	if next == '\r' && end+1 < len(script) && script[end+1] == '\n' {
		end++
	}
	return end, true
}

func shellNewlineEnd(script string, index int) int {
	if script[index] == '\r' && index+1 < len(script) && script[index+1] == '\n' {
		return index + 1
	}
	return index
}

func writeScriptNewline(
	normalized *strings.Builder,
	script string,
	next int,
	quoted bool,
) {
	if quoted {
		normalized.WriteByte(' ')
		return
	}
	writeScriptSeparator(normalized, script, next)
}

func writeScriptSeparator(normalized *strings.Builder, script string, next int) {
	current := normalized.String()
	last := len(current) - 1
	for last >= 0 && (current[last] == ' ' || current[last] == '\t') {
		last--
	}
	if last < 0 || current[last] == ';' || current[last] == '|' ||
		current[last] == '&' {
		return
	}
	for next < len(script) && (script[next] == ' ' || script[next] == '\t' ||
		script[next] == '\r' || script[next] == '\n') {
		next++
	}
	if next >= len(script) || script[next] == ';' || script[next] == '|' ||
		script[next] == '&' {
		return
	}
	normalized.WriteString(" ; ")
}

func containsPipeline(command string) bool {
	inSingle := false
	inDouble := false
	for i := 0; i < len(command); i++ {
		switch command[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '|', ';':
			if !inSingle && !inDouble {
				return true
			}
		case '&':
			if !inSingle && !inDouble && i+1 < len(command) && command[i+1] == '&' {
				return true
			}
		}
	}
	return false
}

func (s scanner) commandDenied(name string) bool {
	for _, denied := range s.policy.DeniedCommands {
		if strings.EqualFold(commandName(denied), name) {
			return true
		}
	}
	return false
}

func (s scanner) commandAllowed(rawName string) bool {
	candidate := commandToken(rawName)
	for _, allowed := range s.policy.AllowedCommands {
		allowedToken := commandToken(allowed)
		if candidate == allowedToken ||
			(runtime.GOOS == "windows" && strings.EqualFold(candidate, allowedToken)) {
			return true
		}
	}
	return false
}

func commandToken(name string) string {
	name = strings.TrimSpace(strings.Trim(name, `"'`))
	return strings.ReplaceAll(name, "\\", "/")
}

func commandName(name string) string {
	name = strings.TrimSpace(strings.Trim(name, `"'`))
	name = strings.ReplaceAll(name, "\\", "/")
	base := filepath.Base(name)
	base = strings.TrimSuffix(base, ".exe")
	base = strings.TrimSuffix(base, ".cmd")
	base = strings.TrimSuffix(base, ".bat")
	base = strings.TrimSuffix(base, ".com")
	return strings.ToLower(base)
}

func containsArg(argv []string, want string) bool {
	return slices.ContainsFunc(argv, func(arg string) bool {
		return arg == want
	})
}

func newFinding(
	decision Decision,
	risk RiskLevel,
	ruleID string,
	evidence []string,
	recommendation string,
) Finding {
	return Finding{
		Decision:       decision,
		RiskLevel:      risk,
		RuleID:         ruleID,
		Evidence:       evidence,
		Recommendation: recommendation,
	}
}

var (
	networkCommandRE = regexp.MustCompile(
		`(?i)(?:^|[;&|]\s*)["']?(?:(?:[a-z]:)?[^\s"';&|]*[/\\])?(?:curl|wget|nc|netcat|ssh|scp|rsync)(?:\.exe|\.cmd|\.bat|\.com)?["']?(?:\s|$)`)
	explicitURLRE = regexp.MustCompile(
		`(?i)https?://[A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]+`)
	schemeLessNetworkTargetRE = regexp.MustCompile(
		`(?i)\b(?:(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}|(?:[0-9]{1,3}\.){3}[0-9]{1,3}|localhost)(?::[0-9]{1,5})?(?:/[A-Za-z0-9._~:/?#\[\]@!$&'()*+,;=%-]*)?`)
	scpLikeNetworkTargetRE = regexp.MustCompile(
		`(?i)^(?:[a-z0-9._-]+@)?((?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}|(?:[0-9]{1,3}\.){3}[0-9]{1,3}|localhost):.+$`)
	longSleepRE   = regexp.MustCompile(`\bsleep\s+([0-9]+)`)
	headBytesRE   = regexp.MustCompile(`\bhead\s+-c\s+([0-9]+)`)
	printRepeatRE = regexp.MustCompile(
		`print\s*\([^)]*\*\s*([0-9]{7,})`)
	xargsParallelRE = regexp.MustCompile(
		`\bxargs\b[^;&|]*\s-p\s*([0-9]+)`)
	parallelJobsRE = regexp.MustCompile(
		`\bparallel\b[^;&|]*(?:-j|--jobs)\s*([0-9]+)`)
	secretNameRE = regexp.MustCompile(
		`(?i)(api[_-]?key|token|password|passwd|secret|private[_-]?key|credential)`)
	secretValueRE = regexp.MustCompile(
		`(?i)(sk-[A-Za-z0-9_-]{12,}|ghp_[A-Za-z0-9_]{12,}|xox[baprs]-[A-Za-z0-9-]{10,}|-----BEGIN [A-Z ]*PRIVATE KEY-----)`)
	secretNameValueRE = regexp.MustCompile(
		`(?i)(api[_-]?key|token|password|passwd|secret|private[_-]?key|credential)=([^ \t\n\r;&|]+)`)
)

func looksSensitive(text string) bool {
	return secretValueRE.MatchString(text) ||
		(secretNameRE.MatchString(text) && strings.Contains(text, "="))
}

type redactor struct {
	changed bool
}

func newRedactor() *redactor { return &redactor{} }

func (r *redactor) redact(s string) string {
	orig := s
	s = secretValueRE.ReplaceAllString(s, "[REDACTED_SECRET]")
	s = secretNameValueRE.ReplaceAllString(s, "$1=[REDACTED_SECRET]")
	if s != orig {
		r.changed = true
	}
	return s
}

func redactList(r *redactor, in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = r.redact(s)
	}
	return out
}

// ValidateReport checks that a report has the fields required by the safety
// contract. It is mainly useful for examples and tests.
func ValidateReport(report Report) error {
	if report.Decision == "" {
		return errors.New("missing decision")
	}
	if report.RiskLevel == "" {
		return errors.New("missing risk_level")
	}
	if report.Decision != DecisionAllow && report.RuleID == "" {
		return errors.New("missing rule_id")
	}
	if report.Decision != DecisionAllow && len(report.Evidence) == 0 {
		return errors.New("missing evidence")
	}
	if report.Recommendation == "" {
		return errors.New("missing recommendation")
	}
	return nil
}
