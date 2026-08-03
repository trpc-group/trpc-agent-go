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
