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

// SensitivePattern 描述一类敏感信息的脱敏与检出规则。
// Re 是脱敏用正则（宽松超集，必须覆盖所有检出命中）；
// DetectRe 是检出用正则（更严格、防误报，nil 时退化为 Re）。
type SensitivePattern struct {
	Description       string
	Re                *regexp.Regexp
	DetectRe          *regexp.Regexp
	Replacement       string
	DetectsCredential bool // 是否参与 sec_hardcoded_key_001 硬编码凭据检出
}

// sensitivePatterns 是敏感信息模式的单一注册表：检出（scanner.go 的
// sec_hardcoded_key_001 与 DetectSensitiveInfo）与脱敏（MaskSensitive）
// 都从这里派生，从根上避免"检出命中但脱敏不命中"的漂移（验收标准 5）。
var sensitivePatterns = []SensitivePattern{
	{
		// 通用凭据 key=value：大小写不敏感，前缀 [a-z0-9_.-]*? 覆盖
		// apiToken / AppSecret / dbPassword / ACCESS_TOKEN / API_KEY 等写法。
		// DetectRe 的值长度门槛更高（>=8 位）以抑制弱信号误报。
		Description: "API Key / 令牌 / 密码等凭据",
		Re: regexp.MustCompile(
			`(?i)([a-z0-9_.-]*?(?:api[_-]?key|access[_-]?token|token|secret|password|passwd|pwd)\s*[:=]\s*)["'][^"']{4,}["']`),
		DetectRe: regexp.MustCompile(
			`(?i)(?:api[_-]?key|access[_-]?token|token|secret|password|passwd|pwd)\s*[:=]\s*["'][^"']{8,}["']`),
		Replacement:       `${1}***`,
		DetectsCredential: true,
	},
	{Description: "OpenAI API Key", Re: regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`), Replacement: `sk-***`, DetectsCredential: true},
	{Description: "Anthropic API Key", Re: regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{20,}`), Replacement: `sk-ant-***`, DetectsCredential: true},
	{Description: "AWS Access Key", Re: regexp.MustCompile(`AKIA[0-9A-Z]{16}`), Replacement: `AKIA***`, DetectsCredential: true},
	{Description: "GitHub fine-grained PAT", Re: regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`), Replacement: `github_pat_***`, DetectsCredential: true},
	{Description: "GitHub classic PAT", Re: regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`), Replacement: `ghp_***`, DetectsCredential: true},
	{Description: "GitLab PAT", Re: regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`), Replacement: `glpat-***`, DetectsCredential: true},
	{Description: "Slack token", Re: regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`), Replacement: `xox***-***`, DetectsCredential: true},
	{
		// 私钥由 sens_private_key_001 专项检出，不参与 sec_hardcoded_key_001。
		Description: "私钥",
		Re: regexp.MustCompile(
			`-----BEGIN\s+(RSA\s+|EC\s+|OPENSSH\s+)?PRIVATE\s+KEY-----[\s\S]*?-----END\s+(RSA\s+|EC\s+|OPENSSH\s+)?PRIVATE\s+KEY-----`),
		Replacement: `-----BEGIN PRIVATE KEY-----***-----END PRIVATE KEY-----`,
	},
	{
		// 信用卡号由 sens_credit_card_001 专项检出，不参与 sec_hardcoded_key_001。
		Description: "信用卡号",
		Re:          regexp.MustCompile(`\b\d{4}[-\s]?\d{4}[-\s]?\d{4}[-\s]?\d{4}\b`),
		Replacement: `****-****-****-****`,
	},
	{Description: "IP 地址", Re: regexp.MustCompile(`"//\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}:\d+"`), Replacement: `"//***.***.***.***:**"`},
}

// credentialPatterns 返回参与 sec_hardcoded_key_001 硬编码凭据检出的正则。
// 由注册表派生，保证检出侧与脱敏侧一致。
func credentialPatterns() []string {
	var out []string
	for _, sp := range sensitivePatterns {
		if !sp.DetectsCredential {
			continue
		}
		if sp.DetectRe != nil {
			out = append(out, sp.DetectRe.String())
		} else {
			out = append(out, sp.Re.String())
		}
	}
	return out
}

// detectRe 返回检出用正则（DetectRe，缺失时退化为 Re）。
func (sp SensitivePattern) detectRe() *regexp.Regexp {
	if sp.DetectRe != nil {
		return sp.DetectRe
	}
	return sp.Re
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
		re := sp.Re
		matches := re.FindAllString(result, -1)
		if len(matches) > 0 {
			count += len(matches)
			result = re.ReplaceAllString(result, sp.Replacement)
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
		if sp.Re.MatchString(text) {
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
			// 只检查新增行（+）：删除行和上下文行不属于本次变更，
			// 避免 PR 修改无关代码时误报历史遗留的敏感信息。
			if line.Type != LineAdd {
				continue
			}
			content := strings.TrimSpace(line.Content)
			if content == "" {
				continue
			}
			for _, sp := range sensitivePatterns {
				if sp.detectRe().MatchString(content) {
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
