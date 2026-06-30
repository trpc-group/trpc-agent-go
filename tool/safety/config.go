//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// ============================================================
// YAML 策略配置文件
// ============================================================

// PolicyFile is loaded from tool_safety_policy.yaml.
type PolicyFile struct {
	// 拒绝的命令列表（如 curl, wget, rm -rf）
	DeniedCommands []string `yaml:"denied_commands" json:"denied_commands"`
	// 拒绝访问的路径（如 ~/.ssh, /etc/shadow）
	DeniedPaths []string `yaml:"denied_paths" json:"denied_paths"`
	// 白名单域名（仅允许这些域名的网络访问）
	AllowedDomains []string `yaml:"allowed_domains" json:"allowed_domains,omitempty"`
	// 拒绝的域名
	DeniedDomains []string `yaml:"denied_domains" json:"denied_domains,omitempty"`
	// 最大命令执行超时（秒）
	MaxTimeoutSeconds int `yaml:"max_timeout_seconds" json:"max_timeout_seconds"`
	// 最大输出大小（字节）
	MaxOutputBytes int `yaml:"max_output_bytes" json:"max_output_bytes"`

	// 额外环境变量白名单
	AllowedEnvVars []string `yaml:"allowed_env_vars" json:"allowed_env_vars,omitempty"`
}

// DefaultPolicy returns a sensible default policy.
func DefaultPolicy() PolicyFile {
	return PolicyFile{
		DeniedCommands: []string{
			"curl", "wget", "nc", "ssh", "telnet",
			"rm -rf", "mkfs", "fdisk",
			"eval", "exec", "source", "sudo",
		},
		DeniedPaths: []string{
			"/etc/shadow", "/etc/passwd",
			"~/.ssh", "~/.aws",
			".env",
		},
		MaxTimeoutSeconds: 300,
		MaxOutputBytes:    10 * 1024 * 1024, // 10MB
	}
}

// LoadPolicyFile reads and parses a YAML policy file.
// It starts from DefaultPolicy and overlays file values so an
// incomplete YAML does not silently disable security checks.
func LoadPolicyFile(path string) (*PolicyFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy file %s: %w", path, err)
	}
	p := DefaultPolicy() // Start from defaults, not zero.
	if err := yaml.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse policy file %s: %w", path, err)
	}
	if p.MaxTimeoutSeconds == 0 {
		p.MaxTimeoutSeconds = 300
	}
	if p.MaxOutputBytes == 0 {
		p.MaxOutputBytes = 10 * 1024 * 1024
	}
	return &p, nil
}

// ============================================================
// 扫描报告（结构化 JSON 输出）
// ============================================================

// ScanReport is the complete structured output for one scan.
type ScanReport struct {
	// ToolName is the name of the tool that produced this report.
	ToolName string `json:"tool_name"`
	// Command is the original command string.
	Command string `json:"command"`
	// Backend is the executor backend type.
	Backend string `json:"backend"`
	// Timestamp is the RFC3339 UTC time of the scan.
	Timestamp string `json:"timestamp"`

	// Decision is the final safety decision.
	Decision Decision `json:"decision"`
	// RiskLevel is the assigned risk severity.
	RiskLevel RiskLevel `json:"risk_level"`
	// Blocked indicates whether execution was intercepted.
	Blocked bool `json:"blocked"`

	// RuleID is the identifier of the rule that fired.
	RuleID string `json:"rule_id"`
	// Evidence is the matched keyword/pattern.
	Evidence string `json:"evidence"`
	// Reason is a human-readable explanation.
	Reason string `json:"reason"`
	// Recommendation is a suggested follow-up action.
	Recommendation string `json:"recommendation"`

	// DurationUs is the scan duration in microseconds.
	DurationUs int64 `json:"duration_us"`
}

// NewReport creates a ScanReport from a ScanResult.
func NewReport(result *ScanResult, input ScanInput, toolName string, dur time.Duration) ScanReport {
	r := ScanReport{
		ToolName:   toolName,
		Command:    input.Command,
		Backend:    input.ExecutorType,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		DurationUs: dur.Microseconds(),
	}
	if r.Backend == "" {
		r.Backend = "local"
	}

	if result == nil {
		r.Decision = DecisionAllow
		r.RiskLevel = RiskNone
		r.Blocked = false
		r.Recommendation = "命令安全，已放行"
		return r
	}

	r.Decision = result.Decision
	r.RiskLevel = result.RiskLevel
	r.RuleID = result.RuleID
	r.Evidence = result.Evidence
	r.Reason = result.Reason
	r.Blocked = result.Decision == DecisionDeny

	switch result.Decision {
	case DecisionDeny:
		r.Recommendation = "命令已拦截，请使用安全替代方案"
	case DecisionAsk:
		r.Recommendation = "需要人工审核后才能执行"
	default:
		r.Recommendation = "命令已放行"
	}
	return r
}

// ============================================================
// 审计日志（JSONL 格式）
// ============================================================

// AuditEvent is one line in the audit log.
type AuditEvent struct {
	// ToolName is the name of the tool that triggered the event.
	ToolName string `json:"tool_name"`
	// Command is the original command string.
	Command string `json:"command"`
	// Decision is the safety decision as a string.
	Decision string `json:"decision"`
	// RiskLevel is the risk level as a string.
	RiskLevel string `json:"risk_level"`
	// RuleID is the identifier of the rule that fired.
	RuleID string `json:"rule_id"`
	// Evidence is the matched keyword/pattern.
	Evidence string `json:"evidence"`
	// Backend is the executor backend type.
	Backend string `json:"backend"`
	// Blocked indicates whether execution was intercepted.
	Blocked bool `json:"blocked"`
	// Sanitized indicates whether sensitive data was redacted.
	Sanitized bool `json:"sanitized"`
	// DurationUs is the scan duration in microseconds.
	DurationUs int64 `json:"duration_us"`
	// Timestamp is the RFC3339 UTC time of the scan.
	Timestamp string `json:"timestamp"`
}

// NewAuditEvent creates an AuditEvent from a ScanReport.
func NewAuditEvent(r ScanReport) AuditEvent {
	return AuditEvent{
		ToolName:   r.ToolName,
		Command:    r.Command,
		Decision:   string(r.Decision),
		RiskLevel:  string(r.RiskLevel),
		RuleID:     r.RuleID,
		Evidence:   r.Evidence,
		Backend:    r.Backend,
		Blocked:    r.Blocked,
		Sanitized:  false, // 默认不脱敏
		DurationUs: r.DurationUs,
		Timestamp:  r.Timestamp,
	}
}

// ============================================================
// OpenTelemetry Span 属性预留
// ============================================================
// 以下常量定义了 OTel span 的 attribute key。
// 当项目启用 OpenTelemetry tracing 时，调用方可以将这些
// 键值对设置到当前 span 上，用于安全决策的可观测性。

const (
	// SpanAttrDecision is the safety decision for this tool call.
	// Values: "allow", "deny", "ask"
	SpanAttrDecision = "tool.safety.decision"

	// SpanAttrRiskLevel is the risk level assigned to this tool call.
	// Values: "none", "low", "medium", "high", "critical"
	SpanAttrRiskLevel = "tool.safety.risk_level"

	// SpanAttrRuleID is the ID of the rule that triggered.
	// e.g. "danger_cmd_001", "network_002"
	SpanAttrRuleID = "tool.safety.rule_id"

	// SpanAttrBackend is the executor backend type.
	// Values: "local", "container", "e2b"
	SpanAttrBackend = "tool.safety.backend"

	// SpanAttrBlocked indicates whether execution was intercepted.
	SpanAttrBlocked = "tool.safety.blocked"
)

// SetSpanAttributes returns the key-value pairs suitable for
// setting on an OTel span.
func SetSpanAttributes(r ScanReport) map[string]string {
	return map[string]string{
		SpanAttrDecision:  string(r.Decision),
		SpanAttrRiskLevel: string(r.RiskLevel),
		SpanAttrRuleID:    r.RuleID,
		SpanAttrBackend:   r.Backend,
		SpanAttrBlocked:   fmt.Sprintf("%t", r.Blocked),
	}
}
