//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"strings"
	"testing"
)

// TestMaskSensitive 验证各类敏感信息的脱敏行为（验收标准 5）。
func TestMaskSensitive(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantCount int
	}{
		{"api key", `apiKey = "sk-abcdef1234567890abcdef1234"`, 1},
		{"password", `password = "hunter2secret"`, 1},
		{"openai key", "sk-abcdef1234567890abcdef1234567890", 1},
		{"aws key", "AKIAIOSFODNN7EXAMPLE", 1},
		{"github pat", "github_pat_11AZZJUYA0KF2ao68ScGLT_X8BHm0mZE27E8tkX4Sxo", 1},
		{"private key", "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFA\n-----END PRIVATE KEY-----", 1},
		{"credit card", "4242 4242 4242 4242", 1},
		{"clean text", "var greeting = \"hello world\"", 0},
		{"empty", "", 0},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			masked, count := MaskSensitive(c.in)
			if count != c.wantCount {
				t.Errorf("脱敏数量不匹配: 期望 %d, 实际 %d (输入 %q)", c.wantCount, count, c.in)
			}
			if count > 0 {
				// 脱敏后不能包含原始敏感值片段
				for _, frag := range []string{"sk-abcdef", "hunter2secret", "AKIAIOSFODNN7", "github_pat_11AZZ"} {
					if strings.Contains(masked, frag) {
						t.Errorf("脱敏后仍包含敏感片段 %q: %q", frag, masked)
					}
				}
			}
		})
	}
}

// TestDetectSensitiveInfo_AddedLinesOnly 验证敏感信息检测只扫描新增行：
// PR 修改无关代码时，历史遗留的敏感上下文行不应误报。
func TestDetectSensitiveInfo_AddedLinesOnly(t *testing.T) {
	df := DiffFile{
		OldPath: "config.go",
		NewPath: "config.go",
		Hunks: []Hunk{{
			OldStart: 1, OldCount: 3, NewStart: 1, NewCount: 4,
			Lines: []Line{
				{Type: LineContext, Content: `var apiKey = "sk-abcdef1234567890abcdef1234"`, OldNo: 1, NewNo: 1},
				{Type: LineDelete, Content: `var endpoint = "https://old.example.com"`, OldNo: 2},
				{Type: LineAdd, Content: `var endpoint = "https://new.example.com"`, NewNo: 2},
			},
		}},
	}

	findings := DetectSensitiveInfo(df)
	for _, f := range findings {
		if f.Line == 1 {
			t.Errorf("上下文行中的敏感信息不应被检出 (line=%d)", f.Line)
		}
	}
	if len(findings) != 0 {
		t.Errorf("只有删除行和上下文行含敏感信息时不应有 finding, 实际 %d", len(findings))
	}

	// 新增行含敏感信息时应检出。
	df.Hunks[0].Lines = append(df.Hunks[0].Lines, Line{
		Type: LineAdd, Content: `token = "sk-newsecret-1234567890"`, NewNo: 5,
	})
	findings = DetectSensitiveInfo(df)
	if len(findings) == 0 {
		t.Error("新增行中的敏感信息应被检出")
	}
}

// TestMaskSensitive_CoversDetection 验证"检出必被脱敏"不变量（验收标准 5）：
// sensitivePatterns 注册表中每个检出模式（detectRe）能命中的样例，
// MaskSensitive 必须同样命中并脱敏，杜绝大小写/格式漂移导致明文泄漏。
func TestMaskSensitive_CoversDetection(t *testing.T) {
	// 每个 Description 对应一条能命中 detectRe 的真实样例。
	samples := map[string]string{
		"API Key / 令牌 / 密码等凭据": `apiToken = "abcdef1234567890"`,
		"OpenAI API Key":          `sk-abcdefghijklmnopqrstuvwxyz`,
		"Anthropic API Key":       `sk-ant-api03-abcdefghijklmnopqrstuv`,
		"AWS Access Key":          `AKIAIOSFODNN7EXAMPLE`,
		"GitHub fine-grained PAT": `github_pat_11AZZJUYA0KF2ao68ScGLT_X8BHm0mZE27E8tkX4Sxo`,
		"GitHub classic PAT":      `ghp_1234567890abcdefghij`,
		"GitLab PAT":              `glpat-abcdefghijklmnopqrstuvwx`,
		"Slack token":             `xoxb-1234567890-abcdefghij-ABCDEFGHIJ`,
		"私钥":                      "-----BEGIN RSA PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFA\n-----END RSA PRIVATE KEY-----",
		"信用卡号":                   `4242 4242 4242 4242`,
	}

	covered := 0
	for _, sp := range sensitivePatterns {
		input, ok := samples[sp.Description]
		if !ok {
			continue
		}
		covered++
		t.Run(sp.Description, func(t *testing.T) {
			if !sp.detectRe().MatchString(input) {
				t.Fatalf("样例未命中检出正则: %q", input)
			}
			masked, count := MaskSensitive(input)
			if count < 1 {
				t.Fatalf("检出命中的敏感信息必须被脱敏 (count=%d)", count)
			}
			if strings.Contains(masked, input) {
				t.Errorf("脱敏后仍包含原始值: %q", masked)
			}
		})
	}
	if covered != len(samples) {
		t.Fatalf("样例与注册表未对齐: covered=%d samples=%d", covered, len(samples))
	}
}
