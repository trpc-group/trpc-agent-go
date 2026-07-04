//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"regexp"
	"strings"
)

// 敏感信息脱敏正则模式。
var sensitivePatterns = []struct {
	Pattern     *regexp.Regexp
	Replacement string
	Description string
}{
	{regexp.MustCompile(`(api[Kk]ey|API_KEY)\s*[:=]\s*["'][^"']{8,}["']`),
		`${1}=***`, "API Key"},
	{regexp.MustCompile(`(secret|SECRET)\s*[:=]\s*["'][^"']{6,}["']`),
		`${1}=***`, "Secret"},
	{regexp.MustCompile(`(password|PASSWORD)\s*[:=]\s*["'][^"']+["']`),
		`${1}=***`, "Password"},
	{regexp.MustCompile(`(token|TOKEN)\s*[:=]\s*["'][^"']{8,}["']`),
		`${1}=***`, "Token"},
	{regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`),
		`sk-***`, "OpenAI API Key"},
	{regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		`AKIA***`, "AWS Access Key"},
	{regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
		`github_pat_***`, "GitHub PAT"},
	{regexp.MustCompile(`-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----[\s\S]*?-----END\s+(RSA\s+)?PRIVATE\s+KEY-----`),
		`-----BEGIN PRIVATE KEY-----***-----END PRIVATE KEY-----`, "Private Key"},
	{regexp.MustCompile(`\b\d{4}[-\s]?\d{4}[-\s]?\d{4}[-\s]?\d{4}\b`),
		`****-****-****-****`, "Credit Card"},
	{regexp.MustCompile(`"//\d+\.\d+\.\d+\.\d+:\d+"`),
		`"//***.***.***.***:**"`, "IP Address"},
}

// MaskSensitive 对文本中的敏感信息进行脱敏处理。
// 返回脱敏后的文本和被脱敏的数量。
func MaskSensitive(text string) (string, int) {
	if text == "" {
		return text, 0
	}

	count := 0
	result := text
	for _, sp := range sensitivePatterns {
		matches := sp.Pattern.FindAllString(result, -1)
		if len(matches) > 0 {
			count += len(matches)
			result = sp.Pattern.ReplaceAllString(result, sp.Replacement)
		}
	}

	return result, count
}

// MaskSensitiveInFindings 对 findings 中的敏感信息进行脱敏。
// 包括 evidence 和 recommendation 字段。
func MaskSensitiveInFindings(findings []Finding) ([]Finding, int) {
	totalCount := 0
	for i := range findings {
		masked, c1 := MaskSensitive(findings[i].Evidence)
		findings[i].Evidence = masked
		masked, c2 := MaskSensitive(findings[i].Recommendation)
		findings[i].Recommendation = masked
		totalCount += c1 + c2
	}
	return findings, totalCount
}

// ContainsSensitive 检查文本是否包含敏感信息。
func ContainsSensitive(text string) bool {
	if text == "" {
		return false
	}
	for _, sp := range sensitivePatterns {
		if sp.Pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// DetectSensitiveInfo 在 diff 文件中检测敏感信息泄漏。
// 返回新创建的 findings。
func DetectSensitiveInfo(df DiffFile) []Finding {
	var findings []Finding

	for _, hunk := range df.Hunks {
		for _, line := range hunk.Lines {
			// 检查新增行（+）和上下文行（ ）是否存在敏感信息
			if line.Type == LineDelete {
				continue
			}
			content := strings.TrimSpace(line.Content)
			if content == "" {
				continue
			}
			for _, sp := range sensitivePatterns {
				if sp.Pattern.MatchString(content) {
					f := NewFinding(
						SeverityCritical,
						CategorySensitive,
						df.NewPath,
						line.NewNo,
						"敏感信息泄漏: "+sp.Description,
						content,
						"移除或脱敏处理此信息，使用环境变量或密钥管理服务替代。",
						"rule",
						"sens_detect_001",
					)
					findings = append(findings, f)
				}
			}
		}
	}

	return findings
}
