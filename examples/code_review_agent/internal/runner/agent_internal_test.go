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
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
)

func TestNewCRAgent(t *testing.T) {
	reg := NewRuleRegistry()
	mgr := NewSandboxManager(nil, DefaultSandboxConfig())
	store, _ := storage.NewSQLiteStore(":memory:")
	defer store.Close()

	agent := NewCRAgent(reg, mgr, store)
	assert.NotNil(t, agent)
}

func TestBuildRecommendations(t *testing.T) {
	findings := []finding.Finding{
		{Recommendation: "Use parameterized queries"},
		{Recommendation: "Add error handling"},
		{Recommendation: "Use parameterized queries"},
	}
	recs := buildRecommendations(findings)
	assert.Len(t, recs, 2)
	assert.Contains(t, recs, "Use parameterized queries")
	assert.Contains(t, recs, "Add error handling")
}

func TestBuildRecommendations_Empty(t *testing.T) {
	assert.Empty(t, buildRecommendations(nil))
	assert.Empty(t, buildRecommendations([]finding.Finding{}))
}

func TestBuildRecommendations_CapAtFive(t *testing.T) {
	var findings []finding.Finding
	for i := 0; i < 10; i++ {
		findings = append(findings, finding.Finding{Recommendation: "Fix " + string(rune('0'+i))})
	}
	recs := buildRecommendations(findings)
	assert.LessOrEqual(t, len(recs), 5)
}

func TestWithCRConfig(t *testing.T) {
	reg := NewRuleRegistry()
	store, _ := storage.NewSQLiteStore(":memory:")
	defer store.Close()

	cfg := &CRConfig{EnabledRules: []string{"RULE1"}}
	agent := NewCRAgent(reg, nil, store, WithCRConfig(cfg))
	assert.NotNil(t, agent)
	assert.Equal(t, cfg, agent.config)
}

func TestNewCRAgent_DefaultConfig(t *testing.T) {
	reg := NewRuleRegistry()
	store, _ := storage.NewSQLiteStore(":memory:")
	defer store.Close()

	agent := NewCRAgent(reg, nil, store)
	assert.NotNil(t, agent)
	assert.NotNil(t, agent.config)
	assert.NotNil(t, agent.sanitizer)
	assert.NotNil(t, agent.dedup)
}
