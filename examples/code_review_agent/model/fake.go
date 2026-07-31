//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package model 提供 fake model 实现，用于测试
package model

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
)

// FakeModel 是一个模拟的 LLM，用于测试
type FakeModel struct {
	name string
}

// NewFakeModel 创建 fake model
func NewFakeModel(name string) *FakeModel {
	return &FakeModel{name: name}
}

// GenerateResponse 根据输入生成模拟响应
func (m *FakeModel) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	findings := m.analyzeDiff(prompt)
	summary := fmt.Sprintf("Found %d issues", len(findings))
	if len(findings) == 0 {
		summary = "No issues found in the code changes."
	}
	result := map[string]any{
		"findings": findings,
		"summary":  summary,
	}

	jsonData, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}

	return string(jsonData), nil
}

// analyzeDiff 分析 diff 内容，返回模拟的 findings
func (m *FakeModel) analyzeDiff(prompt string) []store.Finding {
	findings := make([]store.Finding, 0)
	currentFile := "unknown.go"
	currentLine := 1
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "File: ") {
			currentFile = strings.TrimSpace(strings.TrimPrefix(line, "File: "))
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "new=") {
				if n, err := strconv.Atoi(strings.TrimPrefix(field, "new=")); err == nil && n > 0 {
					currentLine = n
				}
			}
		}
	}

	// 检测 SQL 注入
	if strings.Contains(prompt, "fmt.Sprintf") && strings.Contains(prompt, "SELECT") {
		findings = append(findings, store.Finding{
			Severity:       "critical",
			Category:       "security",
			File:           currentFile,
			Line:           currentLine,
			Title:          "SQL Injection Risk (AI)",
			Description:    "AI detected: SQL query constructed using string formatting",
			Evidence:       "fmt.Sprintf(\"SELECT * FROM users WHERE name = '%s'\", username)",
			Recommendation: "Use parameterized queries",
			Confidence:     0.95,
			Source:         "ai",
			RuleID:         "AI_SEC001",
		})
	}

	// 检测资源泄漏
	if strings.Contains(prompt, "os.Open") && !strings.Contains(prompt, "defer") {
		findings = append(findings, store.Finding{
			Severity:       "high",
			Category:       "resource",
			File:           "file.go",
			Line:           15,
			Title:          "Resource Leak (AI)",
			Description:    "AI detected: File opened but not closed",
			Evidence:       "os.Open(...) without defer .Close()",
			Recommendation: "Add defer f.Close()",
			Confidence:     0.90,
			Source:         "ai",
			RuleID:         "AI_RES001",
		})
	}

	// 检测 goroutine 泄漏
	if strings.Contains(prompt, "go func") && strings.Contains(prompt, "for {") {
		findings = append(findings, store.Finding{
			Severity:       "high",
			Category:       "goroutine",
			File:           "worker.go",
			Line:           currentLine,
			Title:          "Goroutine Leak (AI)",
			Description:    "AI detected: Goroutine with infinite loop",
			Evidence:       "go func() { for { ... } }",
			Recommendation: "Add context or channel exit mechanism",
			Confidence:     0.85,
			Source:         "ai",
			RuleID:         "AI_GR001",
		})
	}

	// 检测敏感信息
	if strings.Contains(prompt, "api_key") || strings.Contains(prompt, "password") {
		findings = append(findings, store.Finding{
			Severity:       "critical",
			Category:       "security",
			File:           "config.go",
			Line:           5,
			Title:          "Sensitive Information (AI)",
			Description:    "AI detected: Hardcoded sensitive information",
			Evidence:       "<redacted>",
			Recommendation: "Use environment variables",
			Confidence:     0.95,
			Source:         "ai",
			RuleID:         "AI_SEC002",
		})
	}

	return findings
}
