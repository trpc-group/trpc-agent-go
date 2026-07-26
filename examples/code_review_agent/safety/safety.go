// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package safety 提供安全策略功能
package safety

import (
	"regexp"
	"strings"
)

// PermissionDecision 权限决策
type PermissionDecision struct {
	Decision string
	Reason   string
}

// PermissionPolicy 权限策略
type PermissionPolicy struct {
	AllowedCommands   []string
	HighRiskCommands  []string
	DangerousPatterns []*regexp.Regexp
}

// NewPermissionPolicy 创建默认权限策略
func NewPermissionPolicy() *PermissionPolicy {
	return &PermissionPolicy{
		AllowedCommands: []string{
			"go vet", "go test", "go build", "go mod",
			"gofmt", "goimports", "staticcheck", "golangci-lint",
		},
		// 高风险命令需要人工审核
		HighRiskCommands: []string{
			"go install", "curl", "wget", "docker", "git clone",
		},
		// 危险命令模式 - 直接拒绝
		DangerousPatterns: []*regexp.Regexp{
			regexp.MustCompile(`\brm\s+-rf\b`),
			regexp.MustCompile(`\bsudo\b`),
			regexp.MustCompile(`\bchmod\s+777\b`),
			regexp.MustCompile(`\bshutdown\b`),
			regexp.MustCompile(`\breboot\b`),
			// 防止通过解释器绕过
			regexp.MustCompile(`\bbash\s+-c\b`),
			regexp.MustCompile(`\bsh\s+-c\b`),
			regexp.MustCompile(`\bpython\s+-c\b`),
			regexp.MustCompile(`\bperl\s+-e\b`),
			regexp.MustCompile(`\bruby\s+-e\b`),
		},
	}
}

// Evaluate 评估命令权限
func (p *PermissionPolicy) Evaluate(command string) PermissionDecision {
	commandLower := strings.ToLower(strings.TrimSpace(command))

	// 检查危险命令
	for _, pattern := range p.DangerousPatterns {
		if pattern.MatchString(commandLower) {
			return PermissionDecision{
				Decision: "deny",
				Reason:   "dangerous command detected",
			}
		}
	}

	// 检查高风险命令
	for _, highRisk := range p.HighRiskCommands {
		if strings.HasPrefix(commandLower, strings.ToLower(highRisk)) {
			return PermissionDecision{
				Decision: "needs_human_review",
				Reason:   "high risk command",
			}
		}
	}

	// 检查白名单
	for _, allowed := range p.AllowedCommands {
		if strings.HasPrefix(commandLower, strings.ToLower(allowed)) {
			return PermissionDecision{
				Decision: "allow",
				Reason:   "command in whitelist",
			}
		}
	}

	// 默认需要审核
	return PermissionDecision{
		Decision: "needs_human_review",
		Reason:   "command not in whitelist",
	}
}

// SecretDetector 敏感信息检测器
type SecretDetector struct {
	Patterns []*regexp.Regexp
}

// NewSecretDetector 创建敏感信息检测器
func NewSecretDetector() *SecretDetector {
	return &SecretDetector{
		Patterns: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(api[_-]?key|apikey)\s*[:=]\s*['"]?([A-Za-z0-9_\-]{20,})['"]?`),
			regexp.MustCompile(`(?i)(token|access[_-]?token)\s*[:=]\s*['"]?([A-Za-z0-9_\-\.]{20,})['"]?`),
			regexp.MustCompile(`(?i)(password|passwd|pwd)\s*[:=]\s*['"]?([^\s'"]{8,})['"]?`),
			regexp.MustCompile(`(?i)(AKIA[0-9A-Z]{16})`),
			regexp.MustCompile(`(?i)-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----`),
		},
	}
}

// Detect 检测敏感信息
func (d *SecretDetector) Detect(text string) bool {
	for _, pattern := range d.Patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// RedactText 脱敏文本
func (d *SecretDetector) RedactText(text string) string {
	result := text
	for _, pattern := range d.Patterns {
		result = pattern.ReplaceAllString(result, "<redacted>")
	}
	return result
}
