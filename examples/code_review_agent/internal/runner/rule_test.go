//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
)

type testRule struct {
	RuleBase
	checkFn func(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error)
}

func (r *testRule) Check(ctx context.Context, file finding.ChangedFileInfo, content string) ([]finding.Finding, error) {
	if r.checkFn != nil {
		return r.checkFn(ctx, file, content)
	}
	return nil, nil
}

func TestRuleRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRuleRegistry()
	r := &testRule{RuleBase: RuleBase{IDValue: "TEST_RULE", CategoryValue: finding.CategoryBestPractice, DefaultSev: finding.SeverityLow}}
	err := reg.Register(r)
	require.NoError(t, err)

	got, err := reg.Get("TEST_RULE")
	require.NoError(t, err)
	assert.Equal(t, "TEST_RULE", got.ID())
	assert.Equal(t, finding.CategoryBestPractice, got.Category())
	assert.Equal(t, finding.SeverityLow, got.DefaultSeverity())
}

func TestRuleRegistry_Duplicate(t *testing.T) {
	reg := NewRuleRegistry()
	r := &testRule{RuleBase: RuleBase{IDValue: "DUP"}}
	require.NoError(t, reg.Register(r))
	err := reg.Register(r)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestRuleRegistry_GetNotFound(t *testing.T) {
	reg := NewRuleRegistry()
	_, err := reg.Get("NONEXISTENT")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRuleRegistry_EnabledRules(t *testing.T) {
	reg := NewRuleRegistry()
	r1 := &testRule{RuleBase: RuleBase{IDValue: "RULE1"}}
	r2 := &testRule{RuleBase: RuleBase{IDValue: "RULE2"}}
	require.NoError(t, reg.Register(r1))
	require.NoError(t, reg.Register(r2))

	// All enabled when config is nil.
	assert.Len(t, reg.EnabledRules(nil), 2)

	// Only RULE1 enabled.
	config := &CRConfig{EnabledRules: []string{"RULE1"}}
	enabled := reg.EnabledRules(config)
	require.Len(t, enabled, 1)
	assert.Equal(t, "RULE1", enabled[0].ID())

	// RULE1 disabled.
	config = &CRConfig{DisabledRules: []string{"RULE1"}}
	enabled = reg.EnabledRules(config)
	require.Len(t, enabled, 1)
	assert.Equal(t, "RULE2", enabled[0].ID())
}

func TestIsEnabled(t *testing.T) {
	assert.True(t, IsEnabled("X", nil))
	assert.True(t, IsEnabled("X", &CRConfig{}))

	// Disabled explicitly.
	assert.False(t, IsEnabled("X", &CRConfig{DisabledRules: []string{"X"}}))

	// Not in enable list.
	assert.False(t, IsEnabled("Y", &CRConfig{EnabledRules: []string{"X"}}))

	// In enable list.
	assert.True(t, IsEnabled("X", &CRConfig{EnabledRules: []string{"X", "Y"}}))
}

func TestEffectiveSeverity(t *testing.T) {
	defaultSev := finding.SeverityMedium
	assert.Equal(t, defaultSev, EffectiveSeverity("R", defaultSev, nil))
	assert.Equal(t, defaultSev, EffectiveSeverity("R", defaultSev, &CRConfig{}))

	// Override.
	config := &CRConfig{SeverityOverrides: map[string]string{"R": "high"}}
	assert.Equal(t, finding.SeverityHigh, EffectiveSeverity("R", defaultSev, config))
}

func TestNewFinding(t *testing.T) {
	base := &RuleBase{
		IDValue:       "TEST",
		CategoryValue: finding.CategorySecurity,
		DefaultSev:    finding.SeverityHigh,
	}
	f := NewFinding(base, "main.go", 10, "test title", "evidence text", "fix it", finding.ConfidenceHigh)
	assert.Equal(t, "TEST", f.RuleID)
	assert.Equal(t, finding.CategorySecurity, f.Category)
	assert.Equal(t, finding.SeverityHigh, f.Severity)
	assert.Equal(t, "main.go", f.File)
	assert.Equal(t, 10, f.Line)
	assert.Equal(t, "test title", f.Title)
	assert.Equal(t, "evidence text", f.Evidence)
	assert.Equal(t, "fix it", f.Recommendation)
	assert.Equal(t, finding.ConfidenceHigh, f.Confidence)
	assert.Equal(t, finding.SourceCustomRule, f.Source)
}

func TestAllRules(t *testing.T) {
	reg := NewRuleRegistry()
	require.NoError(t, reg.Register(&testRule{RuleBase: RuleBase{IDValue: "A"}}))
	require.NoError(t, reg.Register(&testRule{RuleBase: RuleBase{IDValue: "B"}}))
	assert.Len(t, reg.AllRules(), 2)
}

func TestRuleBase_IsEnabled(t *testing.T) {
	base := &RuleBase{IDValue: "MYRULE"}
	assert.True(t, base.IsEnabled(nil))
	assert.True(t, base.IsEnabled(&CRConfig{}))
	assert.False(t, base.IsEnabled(&CRConfig{DisabledRules: []string{"MYRULE"}}))
}

func TestNewFindingWithSeverity(t *testing.T) {
	base := &RuleBase{
		IDValue:       "TEST",
		CategoryValue: finding.CategorySecurity,
		DefaultSev:    finding.SeverityMedium,
	}
	f := NewFindingWithSeverity(finding.SeverityCritical, base, "main.go", 5, "title", "ev", "fix", finding.ConfidenceHigh)
	assert.Equal(t, finding.SeverityCritical, f.Severity)
	assert.Equal(t, "main.go", f.File)
	assert.Equal(t, 5, f.Line)
	assert.Equal(t, "TEST", f.RuleID)
}

func TestNewFindingWithSeverity_DefaultOverride(t *testing.T) {
	base := &RuleBase{
		IDValue:       "TEST",
		CategoryValue: finding.CategoryBestPractice,
		DefaultSev:    finding.SeverityLow,
	}
	// Override severity should be used, not default.
	f := NewFindingWithSeverity(finding.SeverityHigh, base, "x.go", 1, "x", "ev", "rec", finding.ConfidenceLow)
	assert.Equal(t, finding.SeverityHigh, f.Severity)
	assert.Equal(t, finding.ConfidenceLow, f.Confidence)
}
