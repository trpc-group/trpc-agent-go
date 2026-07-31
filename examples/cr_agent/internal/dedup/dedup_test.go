//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dedup

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/cr_agent/internal/types"
)

func TestDedupRemovesDuplicates(t *testing.T) {
	findings := []types.Finding{
		{RuleID: "SEC-001", File: "a.go", Line: 10, Category: types.CategorySecurity, Confidence: 0.9},
		{RuleID: "SEC-001", File: "a.go", Line: 10, Category: types.CategorySecurity, Confidence: 0.8},
		{RuleID: "SEC-002", File: "a.go", Line: 10, Category: types.CategorySecurity, Confidence: 0.8},
	}
	deduped, warnings := Apply(findings, 0.0)
	if len(deduped) != 2 {
		t.Errorf("expected 2 deduped findings, got %d", len(deduped))
	}
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d", len(warnings))
	}
}

func TestDedupDemotesLowConfidence(t *testing.T) {
	findings := []types.Finding{
		{RuleID: "SEC-001", File: "a.go", Line: 10, Category: types.CategorySecurity, Confidence: 0.3, Title: "test"},
	}
	deduped, warnings := Apply(findings, 0.5)
	if len(deduped) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(deduped))
	}
	if !deduped[0].NeedsHumanReview {
		t.Error("expected NeedsHumanReview=true for low confidence finding")
	}
	if len(warnings) != 1 {
		t.Errorf("expected 1 warning, got %d", len(warnings))
	}
}

func TestDedupKeepsHighConfidence(t *testing.T) {
	findings := []types.Finding{
		{RuleID: "SEC-001", File: "a.go", Line: 10, Category: types.CategorySecurity, Confidence: 0.9},
	}
	deduped, warnings := Apply(findings, 0.5)
	if len(deduped) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(deduped))
	}
	if deduped[0].NeedsHumanReview {
		t.Error("expected NeedsHumanReview=false for high confidence finding")
	}
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d", len(warnings))
	}
}

func TestDedupEmptyInput(t *testing.T) {
	deduped, warnings := Apply(nil, 0.5)
	if len(deduped) != 0 {
		t.Errorf("expected 0 findings, got %d", len(deduped))
	}
	if len(warnings) != 0 {
		t.Errorf("expected 0 warnings, got %d", len(warnings))
	}
}

func TestDedupDifferentLinesNotDeduped(t *testing.T) {
	findings := []types.Finding{
		{RuleID: "SEC-001", File: "a.go", Line: 10, Category: types.CategorySecurity, Confidence: 0.9},
		{RuleID: "SEC-001", File: "a.go", Line: 20, Category: types.CategorySecurity, Confidence: 0.9},
	}
	deduped, _ := Apply(findings, 0.0)
	if len(deduped) != 2 {
		t.Errorf("expected 2 findings (different lines), got %d", len(deduped))
	}
}
