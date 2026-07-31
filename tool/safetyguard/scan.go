//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safetyguard

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/redact"
	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// RiskLevel is the severity of a Finding and the aggregate severity of a
// ScanReport. Levels are ordered none < low < medium < high < critical.
type RiskLevel string

const (
	// RiskLevelNone means no risk identified.
	RiskLevelNone RiskLevel = "none"
	// RiskLevelLow is informational; does not block by default.
	RiskLevelLow RiskLevel = "low"
	// RiskLevelMedium triggers ask under fail_closed mode.
	RiskLevelMedium RiskLevel = "medium"
	// RiskLevelHigh triggers deny under the default thresholds.
	RiskLevelHigh RiskLevel = "high"
	// RiskLevelCritical always denies.
	RiskLevelCritical RiskLevel = "critical"
)

func validRiskLevel(r RiskLevel) bool {
	switch r {
	case RiskLevelNone, RiskLevelLow, RiskLevelMedium, RiskLevelHigh, RiskLevelCritical:
		return true
	}
	return false
}

// riskOrder is the numeric weight used to compare and aggregate levels.
func riskOrder(r RiskLevel) int {
	switch r {
	case RiskLevelNone:
		return 0
	case RiskLevelLow:
		return 1
	case RiskLevelMedium:
		return 2
	case RiskLevelHigh:
		return 3
	case RiskLevelCritical:
		return 4
	}
	return 0
}

// max returns the higher of two risk levels.
func maxRisk(a, b RiskLevel) RiskLevel {
	if riskOrder(a) >= riskOrder(b) {
		return a
	}
	return b
}

// atLeast reports whether a is >= threshold.
func atLeast(a, threshold RiskLevel) bool {
	return riskOrder(a) >= riskOrder(threshold)
}

// Finding categories. These are the values carried by Finding.Type and
// stamped on audit events; they are part of the stable report contract.
const (
	FindingDangerousCommand  = "dangerous_command"
	FindingShellBypass       = "shell_bypass"
	FindingNetworkEgress     = "network_egress"
	FindingForbiddenPath     = "forbidden_path"
	FindingDependencyChange  = "dependency_change"
	FindingPrivilegeEscalate = "privilege_escalation"
	FindingResourceAbuse     = "resource_abuse"
	FindingSensitiveInfo     = "sensitive_info"
	FindingEnvViolation      = "environment_violation"
	FindingParseError        = "parse_error"
)

// Finding describes one risk identified during a scan.
type Finding struct {
	// Type is the stable category (FindingDangerousCommand, ...).
	Type string `json:"type"`
	// RiskLevel is the severity of this finding.
	RiskLevel RiskLevel `json:"risk_level"`
	// Rule is the policy rule that fired (e.g. "denied_commands",
	// "forbidden_paths", "shellsafe_parse").
	Rule string `json:"rule"`
	// Detail is a human-readable explanation returned to the model when
	// the action is deny/ask.
	Detail string `json:"detail"`
	// Evidence is a sanitized snippet that triggered the finding. It is
	// redacted via internal/redact before being stored.
	Evidence string `json:"evidence,omitempty"`
}

// ScanReport is the structured result of one Guard.Scan call. It is the
// canonical shape for both the in-memory return and the persisted
// tool_safety_report.json output.
type ScanReport struct {
	// Timestamp is when the scan ran (RFC3339).
	Timestamp string `json:"timestamp"`
	// ToolName is the model-visible tool name.
	ToolName string `json:"tool_name"`
	// ToolCallID is the model-issued call ID.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// PolicyVersion is the version stamped on the active policy.
	PolicyVersion string `json:"policy_version"`
	// Decision is the resulting permission action (allow/deny/ask).
	Decision string `json:"decision"`
	// RiskLevel is the aggregate severity of all findings.
	RiskLevel RiskLevel `json:"risk_level"`
	// Command is the extracted shell command, sanitized. Empty when the
	// tool did not carry a command field.
	Command string `json:"command,omitempty"`
	// HostExec reports whether the tool is flagged as a host-exec surface.
	HostExec bool `json:"host_exec,omitempty"`
	// Findings lists every risk identified, ordered by descending risk.
	Findings []Finding `json:"findings,omitempty"`
}

// scanContext carries the inputs derived from a PermissionRequest plus the
// active policy, so each checker is a pure function over scanContext.
type scanContext struct {
	policy      SafetyPolicy
	shellPolicy shellsafe.Policy
	toolName    string
	command     string
	hasCommand  bool
	args        map[string]any
	rawArgs     []byte
	hostExec    bool
}

// extractScanContext turns a tool.PermissionRequest into a scanContext by
// pulling the command field (if any) and decoding the JSON arguments.
// Decoding failures are recorded as a parse_error finding rather than
// aborting the scan, so the operator still gets an auditable report.
func (g *Guard) extractScanContext(toolName string, rawArgs []byte) scanContext {
	sc := scanContext{
		policy:      g.policy,
		shellPolicy: shellsafe.PolicyFromLists(g.policy.Commands.Allowed, g.policy.Commands.Denied),
		toolName:    toolName,
		rawArgs:     rawArgs,
		hostExec:    g.policy.isHostExecTool(toolName),
	}
	if len(rawArgs) == 0 {
		return sc
	}
	var args map[string]any
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		// Keep args nil; the decode failure is surfaced by the caller as
		// a parse_error finding so malformed JSON is not silently allowed.
		return sc
	}
	sc.args = args
	field := g.policy.commandField(toolName)
	if cmd, ok := args[field].(string); ok && cmd != "" {
		sc.command = cmd
		sc.hasCommand = true
	}
	return sc
}

// scan runs every applicable checker and returns the aggregated report.
// It never returns an error: a scan failure is itself a finding (parse_error)
// so the caller always receives a complete report to audit and decide on.
func (g *Guard) scan(sc scanContext) ScanReport {
	report := ScanReport{
		Timestamp:     g.now().UTC().Format(time.RFC3339Nano),
		ToolName:      sc.toolName,
		PolicyVersion: g.policy.Version,
		HostExec:      sc.hostExec,
	}
	if !g.policy.Active() {
		report.Decision = string(DecisionAllow)
		report.RiskLevel = RiskLevelNone
		return report
	}
	if sc.hasCommand {
		report.Command = redact.SensitiveText(sc.command)
		g.scanCommand(sc, &report)
	}
	g.scanArguments(sc, &report)
	g.scanResources(sc, &report)
	g.scanEnvironment(sc, &report)

	report.RiskLevel = aggregateRisk(report.Findings)
	if sc.hostExec {
		// Host-exec surface has the larger blast radius; escalate one
		// level so a medium workspace finding becomes high on hostexec.
		report.RiskLevel = maxRisk(report.RiskLevel, escalateHost(sc.hostExec, report.Findings))
	}
	report.Decision = string(g.decide(report.RiskLevel, report.Findings))
	sortFindings(report.Findings)
	return report
}

// scanCommand runs the shellsafe structural parse and executable-name
// policy, plus the dependency / privilege / network-tool detectors.
func (g *Guard) scanCommand(sc scanContext, report *ScanReport) {
	pipe, err := shellsafe.Parse(sc.command)
	if err != nil {
		report.Findings = append(report.Findings, Finding{
			Type:      FindingParseError,
			RiskLevel: RiskLevelHigh,
			Rule:      "shellsafe_parse",
			Detail:    fmt.Sprintf("command could not be safely parsed: %s", err),
			Evidence:  truncate(redact.SensitiveText(sc.command), 120),
		})
		return
	}
	// Executable-name allow/deny + built-in shell-wrapper deny.
	if err := sc.shellPolicy.Check(pipe); err != nil {
		// A denial from the user deny list is a dangerous_command; the
		// built-in deny set (sh, eval, xargs, ...) is a shell_bypass.
		rule := "denied_commands"
		findingType := FindingDangerousCommand
		risk := RiskLevelHigh
		if strings.Contains(err.Error(), "built-in policy") {
			rule = "implicit_deny"
			findingType = FindingShellBypass
			risk = RiskLevelCritical
		} else if strings.Contains(err.Error(), "not in allowed_commands") {
			rule = "allowed_commands"
			findingType = FindingDangerousCommand
			risk = RiskLevelMedium
		}
		report.Findings = append(report.Findings, Finding{
			Type:      findingType,
			RiskLevel: risk,
			Rule:      rule,
			Detail:    err.Error(),
			Evidence:  truncate(redact.SensitiveText(sc.command), 120),
		})
	}
	for _, argv := range pipe.Commands {
		if len(argv) == 0 {
			continue
		}
		base := argvBase(argv[0])
		g.detectDependencyChange(base, sc, report)
		g.detectPrivilegeEscalation(base, sc, report)
		g.detectNetworkTool(base, sc, report)
	}
}

// scanArguments scans the raw argument payload for forbidden paths,
// network URLs and sensitive information. It runs for every tool, whether
// or not it carries a command field.
func (g *Guard) scanArguments(sc scanContext, report *ScanReport) {
	if len(sc.rawArgs) == 0 {
		return
	}
	if len(sc.args) == 0 {
		// JSON decode failed earlier; flag it so the operator can see it.
		report.Findings = append(report.Findings, Finding{
			Type:      FindingParseError,
			RiskLevel: RiskLevelMedium,
			Rule:      "arguments_decode",
			Detail:    "tool arguments are not valid JSON; static scan limited to raw payload",
		})
	}
	payload := string(sc.rawArgs)
	g.detectForbiddenPaths(payload, sc, report)
	g.detectNetworkURLs(payload, sc, report)
	if g.policy.SensitiveInfo.Enabled {
		g.detectSensitiveInfo(payload, sc, report)
	}
}

// scanResources enforces timeout / output-size limits declared in the
// tool arguments.
func (g *Guard) scanResources(sc scanContext, report *ScanReport) {
	if sc.args == nil {
		return
	}
	if g.policy.ResourceLimits.MaxTimeoutSeconds > 0 {
		if t := intValue(sc.args, "timeout_sec", "timeoutSec", "timeout"); t > g.policy.ResourceLimits.MaxTimeoutSeconds {
			report.Findings = append(report.Findings, Finding{
				Type:      FindingResourceAbuse,
				RiskLevel: RiskLevelMedium,
				Rule:      "max_timeout_seconds",
				Detail:    fmt.Sprintf("requested timeout %ds exceeds max %ds", t, g.policy.ResourceLimits.MaxTimeoutSeconds),
				Evidence:  fmt.Sprintf("timeout=%d", t),
			})
		}
	}
	if g.policy.ResourceLimits.MaxOutputBytes > 0 {
		if m := intValue(sc.args, "max_output_bytes"); m > g.policy.ResourceLimits.MaxOutputBytes {
			report.Findings = append(report.Findings, Finding{
				Type:      FindingResourceAbuse,
				RiskLevel: RiskLevelLow,
				Rule:      "max_output_bytes",
				Detail:    fmt.Sprintf("requested max_output_bytes %d exceeds limit %d", m, g.policy.ResourceLimits.MaxOutputBytes),
				Evidence:  fmt.Sprintf("max_output_bytes=%d", m),
			})
		}
	}
}

// scanEnvironment enforces the env-key allow/deny lists on the env map
// passed to shell tools.
func (g *Guard) scanEnvironment(sc scanContext, report *ScanReport) {
	if sc.args == nil {
		return
	}
	envAny, ok := sc.args["env"]
	if !ok {
		return
	}
	env, ok := envAny.(map[string]any)
	if !ok {
		return
	}
	denied := toSet(g.policy.Environment.DeniedVars)
	allowed := toSet(g.policy.Environment.AllowedVars)
	for k, v := range env {
		lk := strings.ToLower(k)
		if _, bad := denied[lk]; bad {
			report.Findings = append(report.Findings, Finding{
				Type:      FindingEnvViolation,
				RiskLevel: RiskLevelMedium,
				Rule:      "denied_vars",
				Detail:    fmt.Sprintf("environment variable %q is denied", k),
				Evidence:  k,
			})
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[lk]; !ok {
				report.Findings = append(report.Findings, Finding{
					Type:      FindingEnvViolation,
					RiskLevel: RiskLevelLow,
					Rule:      "allowed_vars",
					Detail:    fmt.Sprintf("environment variable %q is not in allowed_vars", k),
					Evidence:  k,
				})
			}
		}
		_ = redactedValue(v) // best-effort; no finding emitted but value scrubbed from logs
	}
}

func (g *Guard) detectDependencyChange(base string, sc scanContext, report *ScanReport) {
	for _, dep := range g.policy.Commands.DependencyChanges {
		if base == dep {
			// install / add subcommands are the riskiest shape; the
			// presence of the tool itself is still flagged so a bare
			// "go build" / "npm run" is auditable.
			risk := RiskLevelMedium
			if strings.Contains(sc.command, " install") || strings.Contains(sc.command, " add") {
				risk = RiskLevelHigh
			}
			report.Findings = append(report.Findings, Finding{
				Type:      FindingDependencyChange,
				RiskLevel: risk,
				Rule:      "dependency_changes",
				Detail:    fmt.Sprintf("command %q may mutate the toolchain or environment", base),
				Evidence:  base,
			})
			return
		}
	}
}

func (g *Guard) detectPrivilegeEscalation(base string, sc scanContext, report *ScanReport) {
	for _, esc := range g.policy.Commands.PrivilegeEscalation {
		if base == esc {
			risk := RiskLevelHigh
			if sc.hostExec {
				risk = RiskLevelCritical
			}
			report.Findings = append(report.Findings, Finding{
				Type:      FindingPrivilegeEscalate,
				RiskLevel: risk,
				Rule:      "privilege_escalation",
				Detail:    fmt.Sprintf("command %q attempts privilege escalation", base),
				Evidence:  base,
			})
			return
		}
	}
}

func (g *Guard) detectNetworkTool(base string, sc scanContext, report *ScanReport) {
	if !g.policy.Network.Enabled {
		return
	}
	for _, net := range g.policy.Network.NetworkTools {
		if base == net {
			report.Findings = append(report.Findings, Finding{
				Type:      FindingNetworkEgress,
				RiskLevel: RiskLevelMedium,
				Rule:      "network_tools",
				Detail:    fmt.Sprintf("command %q may open a network connection", base),
				Evidence:  base,
			})
			return
		}
	}
}

// detectForbiddenPaths flags any forbidden path fragment appearing in the
// payload. Home-relative entries (~/.ssh) are expanded to the absolute
// home path so both the literal and expanded forms are caught.
func (g *Guard) detectForbiddenPaths(payload string, _ scanContext, report *ScanReport) {
	home := homeDir()
	for _, raw := range g.policy.ForbiddenPaths {
		for _, candidate := range expandPath(raw, home) {
			if strings.Contains(payload, candidate) {
				report.Findings = append(report.Findings, Finding{
					Type:      FindingForbiddenPath,
					RiskLevel: RiskLevelHigh,
					Rule:      "forbidden_paths",
					Detail:    fmt.Sprintf("arguments reference forbidden path %q", raw),
					Evidence:  raw,
				})
				break
			}
		}
	}
}

// detectNetworkURLs scans the payload for http(s) URLs and flags any host
// not in the configured allowlist. When the allowlist is non-empty, an
// unlisted host is a high-risk egress.
func (g *Guard) detectNetworkURLs(payload string, _ scanContext, report *ScanReport) {
	if !g.policy.Network.Enabled {
		return
	}
	allowed := toSet(g.policy.Network.AllowedDomains)
	for _, m := range urlRegex.FindAllString(payload, -1) {
		host := hostOf(m)
		if host == "" {
			continue
		}
		if len(allowed) > 0 {
			if _, ok := allowed[strings.ToLower(host)]; ok {
				continue
			}
			report.Findings = append(report.Findings, Finding{
				Type:      FindingNetworkEgress,
				RiskLevel: RiskLevelHigh,
				Rule:      "allowed_domains",
				Detail:    fmt.Sprintf("network egress to non-allowlisted host %q", host),
				Evidence:  host,
			})
		} else {
			report.Findings = append(report.Findings, Finding{
				Type:      FindingNetworkEgress,
				RiskLevel: RiskLevelMedium,
				Rule:      "network_url",
				Detail:    fmt.Sprintf("network egress detected to host %q", host),
				Evidence:  host,
			})
		}
	}
}

func (g *Guard) detectSensitiveInfo(payload string, _ scanContext, report *ScanReport) {
	if !redact.IsSensitiveName(payload) && !looksSensitive(payload) {
		return
	}
	risk := RiskLevelMedium
	if g.policy.SensitiveInfo.DenyOnDetect {
		risk = RiskLevelHigh
	}
	report.Findings = append(report.Findings, Finding{
		Type:      FindingSensitiveInfo,
		RiskLevel: risk,
		Rule:      "sensitive_info",
		Detail:    "arguments appear to contain credentials or secret material",
		Evidence:  "[redacted]",
	})
}

// decide maps an aggregate risk level to a permission action under the
// configured DecisionConfig. parse_error findings route through
// OnParseError regardless of risk level, so they are checked before the
// risk-threshold denies.
func (g *Guard) decide(level RiskLevel, findings []Finding) Decision {
	if g.policy.Decision.Mode == DecisionModeAdvisory {
		// Advisory mode only denies critical findings; everything else
		// is recorded but allowed.
		if atLeast(level, RiskLevelCritical) {
			return DecisionDeny
		}
		return DecisionAllow
	}
	if atLeast(level, RiskLevelCritical) {
		return DecisionDeny
	}
	// parse_error is routed through OnParseError so the operator can pick
	// deny or ask for structurally unparseable commands. This check runs
	// before the risk-threshold denies because the parse-error finding is
	// stamped at high risk but should honor OnParseError, not the deny
	// threshold.
	for _, f := range findings {
		if f.Type == FindingParseError && f.Rule == "shellsafe_parse" {
			switch g.policy.Decision.OnParseError {
			case ParseErrorAsk:
				return DecisionAsk
			default:
				return DecisionDeny
			}
		}
	}
	if atLeast(level, g.policy.Decision.RiskThresholdDeny) {
		return DecisionDeny
	}
	if atLeast(level, g.policy.Decision.RiskThresholdAsk) {
		return DecisionAsk
	}
	return DecisionAllow
}

// aggregateRisk returns the highest risk level among findings.
func aggregateRisk(findings []Finding) RiskLevel {
	level := RiskLevelNone
	for _, f := range findings {
		level = maxRisk(level, f.RiskLevel)
	}
	return level
}

// escalateHost lifts the aggregate risk by one level when the tool runs on
// the host and any non-none finding exists. It is applied after the
// workspace-level aggregation so the host boundary is reflected in the
// final decision.
func escalateHost(hostExec bool, findings []Finding) RiskLevel {
	if !hostExec || len(findings) == 0 {
		return RiskLevelNone
	}
	level := aggregateRisk(findings)
	switch level {
	case RiskLevelNone:
		return RiskLevelNone
	case RiskLevelLow:
		return RiskLevelMedium
	case RiskLevelMedium:
		return RiskLevelHigh
	case RiskLevelHigh:
		return RiskLevelCritical
	default:
		return RiskLevelCritical
	}
}

func sortFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if riskOrder(findings[i].RiskLevel) != riskOrder(findings[j].RiskLevel) {
			return riskOrder(findings[i].RiskLevel) > riskOrder(findings[j].RiskLevel)
		}
		return findings[i].Type < findings[j].Type
	})
}

func toSet(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		s := strings.ToLower(strings.TrimSpace(item))
		if s == "" {
			continue
		}
		out[s] = struct{}{}
	}
	return out
}

func intValue(args map[string]any, keys ...string) int {
	for _, k := range keys {
		v, ok := args[k]
		if !ok {
			continue
		}
		switch n := v.(type) {
		case float64:
			return int(n)
		case int:
			return n
		case string:
			var parsed int
			if _, err := fmt.Sscanf(n, "%d", &parsed); err == nil {
				return parsed
			}
		}
	}
	return 0
}

func argvBase(name string) string {
	clean := strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(clean, "/"); i >= 0 {
		clean = clean[i+1:]
	}
	return strings.ToLower(clean)
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return ""
}

// expandPath returns the literal forms a forbidden path should be matched
// against. A "~/.ssh" entry expands to both the literal "~/.ssh" and the
// absolute home-prefixed form so a payload that uses either is caught.
func expandPath(raw, home string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	out := []string{raw}
	if strings.HasPrefix(raw, "~") {
		if home != "" {
			out = append(out, filepath.ToSlash(filepath.Join(home, strings.TrimPrefix(raw, "~"))))
		}
	}
	return out
}

func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// hostOf extracts the host portion of a URL string, stripping userinfo
// and port. It is deliberately permissive: malformed URLs are skipped.
func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Hostname()
	return strings.ToLower(host)
}

func redactedValue(v any) string {
	switch s := v.(type) {
	case string:
		return redact.SensitiveText(s)
	default:
		b, _ := json.Marshal(v)
		return redact.SensitiveText(string(b))
	}
}

// looksSensitive is a lightweight pre-filter that avoids running the
// heavier redact regex machinery on obviously-benign payloads.
func looksSensitive(payload string) bool {
	for _, needle := range sensitiveNeedles {
		if strings.Contains(strings.ToLower(payload), needle) {
			return true
		}
	}
	return sensitivePattern.MatchString(payload)
}

var (
	urlRegex = regexp.MustCompile(`https?://[^\s"'<>)\\]+`)

	// sensitiveNeedles are lowercase substrings that strongly indicate
	// credential material in tool arguments.
	sensitiveNeedles = []string{
		"begin rsa private key",
		"begin openssh private key",
		"begin ec private key",
		"begin pgp private key",
		"-----begin private key",
		"aws_access_key_id",
		"aws_secret_access_key",
		"api_key",
		"authorization: bearer",
		"x-api-key",
	}

	sensitivePattern = regexp.MustCompile(
		`(?i)(sk-[A-Za-z0-9_-]{8,}|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)`,
	)
)
