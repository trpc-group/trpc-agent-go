// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package findings

import (
	"testing"
)

func makeFinding(severity Severity, category Category, ruleID, file string, line int, confidence float64) Finding {
	return *NewFinding(severity, category, ruleID, "title", file, line, "evidence", "rec", confidence, "source")
}

func TestDeduplicate_Empty(t *testing.T) {
	result := Deduplicate(nil)
	if len(result.Findings) != 0 || len(result.Warnings) != 0 || result.Removed != 0 {
		t.Error("空输入应返回空结果")
	}
}

func TestDeduplicate_NoDuplicates(t *testing.T) {
	input := []Finding{
		makeFinding(SeverityHigh, CategorySecurity, "SEC-001", "a.go", 10, 0.9),
		makeFinding(SeverityMedium, CategoryResource, "RES-001", "b.go", 20, 0.8),
	}

	result := Deduplicate(input)
	if len(result.Findings) != 2 {
		t.Errorf("Findings = %d, 期望 2", len(result.Findings))
	}
	if result.Removed != 0 {
		t.Errorf("Removed = %d, 期望 0", result.Removed)
	}
}

func TestDeduplicate_SameFileSameLineSameCategory(t *testing.T) {
	// 同一文件、同一行、同一分类、同一规则 → 应该去重
	input := []Finding{
		makeFinding(SeverityHigh, CategorySecurity, "SEC-001", "a.go", 10, 0.8),
		makeFinding(SeverityHigh, CategorySecurity, "SEC-001", "a.go", 10, 0.95),
	}

	result := Deduplicate(input)
	if len(result.Findings) != 1 {
		t.Errorf("Findings = %d, 期望 1（去重后）", len(result.Findings))
	}
	if result.Removed != 1 {
		t.Errorf("Removed = %d, 期望 1", result.Removed)
	}
	// 应保留置信度高的
	if result.Findings[0].Confidence != 0.95 {
		t.Errorf("保留的置信度 = %.2f, 期望 0.95", result.Findings[0].Confidence)
	}
}

func TestDeduplicate_SameFileDiffLine(t *testing.T) {
	// 同一文件、不同行 → 不去重
	input := []Finding{
		makeFinding(SeverityHigh, CategorySecurity, "SEC-001", "a.go", 10, 0.9),
		makeFinding(SeverityHigh, CategorySecurity, "SEC-001", "a.go", 20, 0.9),
	}

	result := Deduplicate(input)
	if len(result.Findings) != 2 {
		t.Errorf("Findings = %d, 期望 2（不同行不去重）", len(result.Findings))
	}
}

func TestDeduplicate_SameFileSameLineDiffCategory(t *testing.T) {
	// 同一文件、同一行、不同分类 → 不去重
	input := []Finding{
		makeFinding(SeverityHigh, CategorySecurity, "SEC-001", "a.go", 10, 0.9),
		makeFinding(SeverityHigh, CategoryResource, "RES-001", "a.go", 10, 0.9),
	}

	result := Deduplicate(input)
	if len(result.Findings) != 2 {
		t.Errorf("Findings = %d, 期望 2（不同分类不去重）", len(result.Findings))
	}
}

func TestDeduplicate_LowConfidenceGoesToWarnings(t *testing.T) {
	input := []Finding{
		makeFinding(SeverityHigh, CategorySecurity, "SEC-001", "a.go", 10, 0.9),   // 高置信
		makeFinding(SeverityMedium, CategoryResource, "RES-001", "b.go", 20, 0.5), // 低置信
	}

	result := Deduplicate(input)
	if len(result.Findings) != 1 {
		t.Errorf("Findings = %d, 期望 1", len(result.Findings))
	}
	if len(result.Warnings) != 1 {
		t.Errorf("Warnings = %d, 期望 1", len(result.Warnings))
	}
}

func TestDeduplicate_SortOrder(t *testing.T) {
	input := []Finding{
		makeFinding(SeverityLow, CategoryTesting, "TST-001", "a.go", 30, 0.9),
		makeFinding(SeverityHigh, CategorySecurity, "SEC-001", "a.go", 10, 0.9),
		makeFinding(SeverityMedium, CategoryResource, "RES-001", "a.go", 20, 0.9),
	}

	result := Deduplicate(input)
	if len(result.Findings) != 3 {
		t.Fatalf("Findings = %d, 期望 3", len(result.Findings))
	}

	// 应该按严重级别降序：high → medium → low
	if result.Findings[0].Severity != SeverityHigh {
		t.Errorf("第一个 = %q, 期望 high", result.Findings[0].Severity)
	}
	if result.Findings[1].Severity != SeverityMedium {
		t.Errorf("第二个 = %q, 期望 medium", result.Findings[1].Severity)
	}
	if result.Findings[2].Severity != SeverityLow {
		t.Errorf("第三个 = %q, 期望 low", result.Findings[2].Severity)
	}
}

func TestDeduplicate_MultipleDuplicates(t *testing.T) {
	// 3 条重复的 → 只保留 1 条
	input := []Finding{
		makeFinding(SeverityHigh, CategorySecurity, "SEC-001", "a.go", 10, 0.7),
		makeFinding(SeverityHigh, CategorySecurity, "SEC-001", "a.go", 10, 0.9),
		makeFinding(SeverityHigh, CategorySecurity, "SEC-001", "a.go", 10, 0.8),
	}

	result := Deduplicate(input)
	if len(result.Findings) != 1 {
		t.Errorf("Findings = %d, 期望 1", len(result.Findings))
	}
	if result.Removed != 2 {
		t.Errorf("Removed = %d, 期望 2", result.Removed)
	}
}
