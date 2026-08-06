// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package safety 提供敏感信息脱敏能力。
//
// 脱敏逻辑统一在此包中，供 sandbox、rules、report 等模块复用。
package safety

import (
	"regexp"
	"strings"
)

// ========== 脱敏正则模式 ==========

var (
	// AWS Access Key
	awsKeyPattern = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	// GitHub Token
	ghTokenPattern = regexp.MustCompile(`gh[pous]_[a-zA-Z0-9]{36,}`)
	// Stripe Key
	stripeKeyPattern = regexp.MustCompile(`(?:sk|pk)_(?:live|test)_[0-9a-zA-Z]{20,}`)
	// Slack Token
	slackTokenPattern = regexp.MustCompile(`xox[bpors]-[0-9a-zA-Z\-]{10,}`)
	// JWT Token
	jwtPattern = regexp.MustCompile(`eyJ[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}\.[a-zA-Z0-9_-]{10,}`)
	// Private Key
	privateKeyPattern = regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`)
	// Database connection string
	dbConnPattern = regexp.MustCompile(`(?i)(?:mysql|postgres(?:ql)?|mongodb(?:\+srv)?|redis)://[^\s]+:[^\s]+@`)
	// URL with credentials
	urlCredPattern = regexp.MustCompile(`https?://[^/\s]+:[^/\s]+@`)
	// Long hex string (64+ chars, likely a hash/token)
	longHexPattern = regexp.MustCompile(`[0-9a-f]{64,}`)
	// Key=value patterns for sensitive fields
	sensitiveKVPattern = regexp.MustCompile(`(?i)(password|passwd|pwd|secret|token|api_?key|private_?key|credential|dsn)\s*[:=]\s*\S+`)
)

// MaskSensitiveInfo 对文本中的敏感信息进行脱敏。
//
// 覆盖的敏感信息类型：
//   - AWS Access Key（AKIA...）
//   - GitHub Token（ghp_..., gho_...）
//   - Stripe Key（sk_live_..., pk_test_...）
//   - Slack Token（xox...）
//   - JWT Token（eyJ...）
//   - 私钥头（-----BEGIN PRIVATE KEY-----）
//   - 数据库连接串（含密码）
//   - URL 嵌入凭据（http://user:pass@host）
//   - key=value 形式的敏感字段
//   - 长 hex 字符串（64+ 位）
func MaskSensitiveInfo(text string) string {
	// 按行处理，每行独立脱敏
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = maskLine(line)
	}
	return strings.Join(lines, "\n")
}

// maskLine 对单行文本进行脱敏。
func maskLine(line string) string {
	// 1. AWS Key
	line = awsKeyPattern.ReplaceAllString(line, "AKIA***REDACTED***")
	// 2. GitHub Token
	line = ghTokenPattern.ReplaceAllString(line, "***REDACTED***")
	// 3. Stripe Key（保留前缀）
	line = stripeKeyPattern.ReplaceAllStringFunc(line, func(s string) string {
		idx := strings.Index(s[3:], "_")
		if idx >= 0 {
			return s[:3+idx+1] + "***REDACTED***"
		}
		return "***REDACTED***"
	})
	// 4. Slack Token
	line = slackTokenPattern.ReplaceAllString(line, "***REDACTED***")
	// 5. JWT Token
	line = jwtPattern.ReplaceAllString(line, "eyJ***REDACTED***")
	// 6. Private Key
	line = privateKeyPattern.ReplaceAllString(line, "***PRIVATE_KEY_REMOVED***")
	// 7. Database connection string（保留协议，替换密码）
	line = dbConnPattern.ReplaceAllStringFunc(line, func(s string) string {
		atIdx := strings.LastIndex(s, "@")
		colonIdx := strings.LastIndex(s[:atIdx], ":")
		if colonIdx > 0 {
			return s[:colonIdx+1] + "***REDACTED***" + s[atIdx:]
		}
		return s
	})
	// 8. URL with credentials
	line = urlCredPattern.ReplaceAllStringFunc(line, func(s string) string {
		atIdx := strings.LastIndex(s, "@")
		colonIdx := strings.LastIndex(s[:atIdx], ":")
		if colonIdx > 0 {
			return s[:colonIdx+1] + "***REDACTED***@" + s[atIdx+1:]
		}
		return s
	})
	// 9. Long hex string
	line = longHexPattern.ReplaceAllStringFunc(line, func(s string) string {
		if len(s) > 16 {
			return s[:8] + "***REDACTED***"
		}
		return s
	})
	// 10. Sensitive key=value patterns
	line = sensitiveKVPattern.ReplaceAllStringFunc(line, func(s string) string {
		// 保留 key 部分，替换 value
		for _, sep := range []string{"=", ": ", ":\t"} {
			if idx := strings.Index(s, sep); idx > 0 {
				return s[:idx+len(sep)] + "***REDACTED***"
			}
		}
		return s
	})

	return line
}

// MaskFindingsEvidence 对 findings 中的证据进行脱敏。
func MaskFindingsEvidence(evidence string) string {
	return MaskSensitiveInfo(evidence)
}
