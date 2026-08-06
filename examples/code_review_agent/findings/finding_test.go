// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package findings

import (
	"testing"
)

func TestNewFinding(t *testing.T) {
	f := NewFinding(
		SeverityHigh,
		CategorySecurity,
		"SEC-001",
		"Hardcoded API key",
		"config.go",
		10,
		`APIKey = "sk-abc123"`,
		"Use environment variable",
		0.95,
		"rule:hardcoded_secret",
	)

	if f.Severity != SeverityHigh {
		t.Errorf("Severity = %q, 期望 %q", f.Severity, SeverityHigh)
	}
	if f.Category != CategorySecurity {
		t.Errorf("Category = %q, 期望 %q", f.Category, CategorySecurity)
	}
	if f.RuleID != "SEC-001" {
		t.Errorf("RuleID = %q, 期望 %q", f.RuleID, "SEC-001")
	}
	if f.File != "config.go" {
		t.Errorf("File = %q, 期望 %q", f.File, "config.go")
	}
	if f.Line != 10 {
		t.Errorf("Line = %d, 期望 %d", f.Line, 10)
	}
	if f.Timestamp == "" {
		t.Error("Timestamp 不应为空")
	}
	if f.dedupKey == "" {
		t.Error("dedupKey 不应为空")
	}
}

func TestDedupKey(t *testing.T) {
	f := NewFinding(
		SeverityHigh, CategorySecurity, "SEC-001", "title",
		"config.go", 10, "evidence", "rec", 0.9, "source",
	)

	key := f.DedupKey()
	expected := "config.go:10:security:SEC-001"
	if key != expected {
		t.Errorf("DedupKey() = %q, 期望 %q", key, expected)
	}
}

func TestIsHighConfidence(t *testing.T) {
	tests := []struct {
		confidence float64
		want       bool
	}{
		{1.0, true},
		{0.95, true},
		{0.7, true},
		{0.69, false},
		{0.5, false},
		{0.0, false},
	}

	for _, tt := range tests {
		f := &Finding{Confidence: tt.confidence}
		got := f.IsHighConfidence()
		if got != tt.want {
			t.Errorf("IsHighConfidence(%.2f) = %v, 期望 %v", tt.confidence, got, tt.want)
		}
	}
}

func TestSeverityOrder(t *testing.T) {
	tests := []struct {
		severity Severity
		want     int
	}{
		{SeverityHigh, 4},
		{SeverityMedium, 3},
		{SeverityLow, 2},
		{SeverityInfo, 1},
		{"unknown", 0},
	}

	for _, tt := range tests {
		f := &Finding{Severity: tt.severity}
		got := f.SeverityOrder()
		if got != tt.want {
			t.Errorf("SeverityOrder(%q) = %d, 期望 %d", tt.severity, got, tt.want)
		}
	}
}

func TestFindingString(t *testing.T) {
	f := &Finding{
		Severity: SeverityHigh,
		Category: CategorySecurity,
		File:     "config.go",
		Line:     10,
		Title:    "Hardcoded API key",
	}

	s := f.String()
	expected := "[high][security] config.go:10 - Hardcoded API key"
	if s != expected {
		t.Errorf("String() = %q, 期望 %q", s, expected)
	}
}
