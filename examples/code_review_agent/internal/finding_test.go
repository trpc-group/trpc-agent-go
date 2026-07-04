package internal

import (
	"testing"
)

func TestNewFinding_DedupKey(t *testing.T) {
	f1 := NewFinding(SeverityHigh, CategorySecurity, "main.go", 10,
		"硬编码密钥", "apiKey = \"sk-abc\"", "使用环境变量", "rule", "sec_001")
	f2 := NewFinding(SeverityHigh, CategorySecurity, "main.go", 10,
		"硬编码密钥", "apiKey = \"sk-abc\"", "使用环境变量", "rule", "sec_001")

	if f1.DedupKey != f2.DedupKey {
		t.Error("相同参数应产生相同 dedup key")
	}

	// 不同 category 应产生不同 dedup key
	f3 := NewFinding(SeverityHigh, CategoryConcurrency, "main.go", 10,
		"硬编码密钥", "apiKey = \"sk-abc\"", "使用环境变量", "rule", "sec_001")
	if f1.DedupKey == f3.DedupKey {
		t.Error("不同 category 应产生不同 dedup key")
	}
}

func TestDeduplicateFindings_ExactDuplicates(t *testing.T) {
	f1 := NewFinding(SeverityMedium, CategorySecurity, "main.go", 10,
		"问题", "证据", "建议", "rule", "rule_001")
	f2 := NewFinding(SeverityLow, CategorySecurity, "main.go", 10,
		"问题", "证据", "建议", "rule", "rule_001")
	f3 := NewFinding(SeverityHigh, CategorySecurity, "main.go", 10,
		"问题", "证据", "建议", "rule", "rule_001")

	result := DeduplicateFindings([]Finding{f1, f2, f3})

	// 应保留 severity 最高的（f3 = high）
	nonDup := CountNonDuplicate(result)
	if nonDup != 1 {
		t.Errorf("期望 1 个非重复 finding, 实际 %d", nonDup)
	}
	for _, f := range result {
		if !f.IsDuplicate && f.Severity != SeverityHigh {
			t.Errorf("应保留 High severity, 实际 %s", f.Severity)
		}
	}
}

func TestDeduplicateFindings_DifferentFiles(t *testing.T) {
	f1 := NewFinding(SeverityHigh, CategorySecurity, "file_a.go", 10,
		"安全问题", "证据", "建议", "rule", "rule_001")
	f2 := NewFinding(SeverityHigh, CategorySecurity, "file_b.go", 10,
		"安全问题", "证据", "建议", "rule", "rule_001")

	result := DeduplicateFindings([]Finding{f1, f2})

	// 不同文件都保留
	nonDup := CountNonDuplicate(result)
	if nonDup != 2 {
		t.Errorf("期望 2 个非重复 finding, 实际 %d", nonDup)
	}
}

func TestDeduplicateFindings_EmptyInput(t *testing.T) {
	result := DeduplicateFindings(nil)
	if len(result) != 0 {
		t.Errorf("nil 输入期望空列表, 实际 %d", len(result))
	}

	result = DeduplicateFindings([]Finding{})
	if len(result) != 0 {
		t.Errorf("空输入期望空列表, 实际 %d", len(result))
	}
}

func TestDeduplicateFindings_SingleElement(t *testing.T) {
	f := NewFinding(SeverityHigh, CategorySecurity, "main.go", 10,
		"问题", "证据", "建议", "rule", "rule_001")
	result := DeduplicateFindings([]Finding{f})
	if len(result) != 1 {
		t.Errorf("期望 1 个 finding, 实际 %d", len(result))
	}
	if result[0].IsDuplicate {
		t.Error("单个元素不应被标记为重复")
	}
}

func TestComputeSummary(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityCritical, IsDuplicate: false},
		{Severity: SeverityCritical, IsDuplicate: false},
		{Severity: SeverityHigh, IsDuplicate: false},
		{Severity: SeverityMedium, IsDuplicate: false},
		{Severity: SeverityLow, IsDuplicate: true},
		{Severity: SeverityWarning, IsDuplicate: false},
	}

	s := ComputeSummary(findings)
	if s.Total != 6 {
		t.Errorf("Total = %d, 期望 6", s.Total)
	}
	if s.Critical != 2 {
		t.Errorf("Critical = %d, 期望 2", s.Critical)
	}
	if s.High != 1 {
		t.Errorf("High = %d, 期望 1", s.High)
	}
	if s.Duplicates != 1 {
		t.Errorf("Duplicates = %d, 期望 1", s.Duplicates)
	}
}

func TestSortFindings(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityLow, File: "a.go", Line: 1},
		{Severity: SeverityCritical, File: "a.go", Line: 5},
		{Severity: SeverityHigh, File: "b.go", Line: 1},
		{Severity: SeverityHigh, File: "a.go", Line: 10},
	}

	SortFindings(findings)

	// 期望顺序: critical, high (a.go:10), high (b.go:1), low
	if findings[0].Severity != SeverityCritical {
		t.Errorf("第1个应为 critical, 实际 %s", findings[0].Severity)
	}
	if findings[1].Severity != SeverityHigh || findings[1].File != "a.go" {
		t.Errorf("第2个应为 high a.go, 实际 %s %s", findings[1].Severity, findings[1].File)
	}
	if findings[3].Severity != SeverityLow {
		t.Errorf("第4个应为 low, 实际 %s", findings[3].Severity)
	}
}

func TestSeverityOrder(t *testing.T) {
	tests := []struct {
		s1, s2 Severity
		gt     bool // s1 > s2
	}{
		{SeverityCritical, SeverityHigh, true},
		{SeverityHigh, SeverityMedium, true},
		{SeverityMedium, SeverityLow, true},
		{SeverityLow, SeverityWarning, true},
		{SeverityLow, SeverityLow, false},
		{SeverityWarning, SeverityCritical, false},
	}

	for _, tt := range tests {
		result := severityOrder(tt.s1) > severityOrder(tt.s2)
		if result != tt.gt {
			t.Errorf("severityOrder(%s) > severityOrder(%s) = %v, 期望 %v",
				tt.s1, tt.s2, result, tt.gt)
		}
	}
}
