// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package scoring

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

func TestCalculate_NoFindings(t *testing.T) {
	result := Calculate(nil, nil)

	if result.Score != 0 {
		t.Errorf("Score = %.2f, 期望 0", result.Score)
	}
	if result.Grade != "A" {
		t.Errorf("Grade = %q, 期望 A", result.Grade)
	}
	if result.FindingCount != 0 {
		t.Errorf("FindingCount = %d, 期望 0", result.FindingCount)
	}
}

func TestCalculate_HighSecurityRisk(t *testing.T) {
	findingsList := []findings.Finding{
		*findings.NewFinding(findings.SeverityHigh, findings.CategorySecurity, "SEC-001", "t", "a.go", 1, "e", "r", 0.9, "s"),
		*findings.NewFinding(findings.SeverityHigh, findings.CategorySecurity, "SEC-002", "t", "b.go", 2, "e", "r", 0.9, "s"),
	}

	result := Calculate(findingsList, nil)

	// 安全维度应有高分（2 个 high → 100 分）
	secDim := result.Breakdown["安全问题"]
	if secDim.Score < 80 {
		t.Errorf("安全维度得分 = %.2f, 期望 >= 80", secDim.Score)
	}
	if secDim.Count != 2 {
		t.Errorf("安全维度 count = %d, 期望 2", secDim.Count)
	}

	// 总分 = 安全维度 100 × 权重 0.30 = 30
	if result.Score < 25 || result.Score > 35 {
		t.Errorf("总分 = %.2f, 期望 25-35", result.Score)
	}

	// 等级应为 B（20-40 范围）
	if result.Grade != "B" {
		t.Errorf("Grade = %q, 期望 B", result.Grade)
	}
}

func TestCalculate_AllDimensionsHigh(t *testing.T) {
	// 所有维度都有 high severity findings → 应该接近 F
	findingsList := []findings.Finding{
		*findings.NewFinding(findings.SeverityHigh, findings.CategorySecurity, "SEC-001", "t", "a.go", 1, "e", "r", 0.9, "s"),
		*findings.NewFinding(findings.SeverityHigh, findings.CategorySensitiveLeak, "SEC-002", "t", "b.go", 2, "e", "r", 0.9, "s"),
		*findings.NewFinding(findings.SeverityHigh, findings.CategoryResource, "RES-001", "t", "c.go", 3, "e", "r", 0.9, "s"),
		*findings.NewFinding(findings.SeverityHigh, findings.CategoryErrorHandling, "ERR-001", "t", "d.go", 4, "e", "r", 0.9, "s"),
		*findings.NewFinding(findings.SeverityHigh, findings.CategoryTesting, "TST-001", "t", "e.go", 5, "e", "r", 0.9, "s"),
		*findings.NewFinding(findings.SeverityHigh, findings.CategoryConcurrency, "CON-001", "t", "f.go", 6, "e", "r", 0.9, "s"),
	}

	result := Calculate(findingsList, nil)

	// 所有维度都有 high → 总分应接近 100
	if result.Score < 80 {
		t.Errorf("总分 = %.2f, 期望 >= 80", result.Score)
	}
	if result.Grade != "F" {
		t.Errorf("Grade = %q, 期望 F", result.Grade)
	}
}

func TestCalculate_MixedFindings(t *testing.T) {
	findingsList := []findings.Finding{
		*findings.NewFinding(findings.SeverityHigh, findings.CategorySecurity, "SEC-001", "t", "a.go", 1, "e", "r", 0.9, "s"),
		*findings.NewFinding(findings.SeverityMedium, findings.CategoryResource, "RES-001", "t", "b.go", 2, "e", "r", 0.8, "s"),
		*findings.NewFinding(findings.SeverityLow, findings.CategoryTesting, "TST-001", "t", "c.go", 3, "e", "r", 0.7, "s"),
	}

	result := Calculate(findingsList, nil)

	// 各维度应有数据
	secDim := result.Breakdown["安全问题"]
	resDim := result.Breakdown["资源泄漏"]
	tstDim := result.Breakdown["测试覆盖"]

	if secDim.Count != 1 {
		t.Errorf("安全维度 count = %d, 期望 1", secDim.Count)
	}
	if resDim.Count != 1 {
		t.Errorf("资源维度 count = %d, 期望 1", resDim.Count)
	}
	if tstDim.Count != 1 {
		t.Errorf("测试维度 count = %d, 期望 1", tstDim.Count)
	}

	// 总分应在合理范围
	if result.Score < 10 || result.Score > 80 {
		t.Errorf("总分 = %.2f, 期望 10-80", result.Score)
	}
}

func TestCalculate_WithWarnings(t *testing.T) {
	findingsList := []findings.Finding{
		*findings.NewFinding(findings.SeverityHigh, findings.CategorySecurity, "SEC-001", "t", "a.go", 1, "e", "r", 0.9, "s"),
	}
	warnings := []findings.Finding{
		*findings.NewFinding(findings.SeverityLow, findings.CategorySecurity, "SEC-002", "t", "b.go", 2, "e", "r", 0.5, "s"),
	}

	result := Calculate(findingsList, warnings)

	if result.WARNING == "" {
		t.Error("有 warnings 时应有警告信息")
	}
}

func TestCalculate_InvalidCategory(t *testing.T) {
	// 不在维度定义中的分类不应影响评分
	findingsList := []findings.Finding{
		*findings.NewFinding(findings.SeverityHigh, "unknown_category", "X-001", "t", "a.go", 1, "e", "r", 0.9, "s"),
	}

	result := Calculate(findingsList, nil)

	// 总分应为 0（因为 unknown_category 不在评分维度中）
	if result.Score != 0 {
		t.Errorf("未知分类不应计入得分，Score = %.2f", result.Score)
	}
}

func TestCalculateGrade(t *testing.T) {
	tests := []struct {
		score float64
		grade string
	}{
		{90, "F"},
		{70, "D"},
		{50, "C"},
		{30, "B"},
		{10, "A"},
		{0, "A"},
	}

	for _, tt := range tests {
		got := calculateGrade(tt.score)
		if got != tt.grade {
			t.Errorf("calculateGrade(%.0f) = %q, 期望 %q", tt.score, got, tt.grade)
		}
	}
}

func TestToReport(t *testing.T) {
	findingsList := []findings.Finding{
		*findings.NewFinding(findings.SeverityHigh, findings.CategorySecurity, "SEC-001", "t", "a.go", 1, "e", "r", 0.9, "s"),
	}

	result := Calculate(findingsList, nil)
	report := result.ToReport()

	if report == "" {
		t.Error("报告不应为空")
	}
	if !containsStr(report, "风险评分") {
		t.Error("报告应包含 '风险评分'")
	}
	if !containsStr(report, "安全问题") {
		t.Error("报告应包含 '安全问题'")
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
