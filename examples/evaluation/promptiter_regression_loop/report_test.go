//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAtomicWriteReplacesCompleteFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	require.NoError(t, atomicWrite(path, []byte("first"), 0o600))
	require.NoError(t, atomicWrite(path, []byte("second"), 0o600))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "second", string(data))
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".optimization-report-*"))
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestRenderMarkdownIncludesDecisionEvidence(t *testing.T) {
	report := &optimizationReport{
		Mode: "fake", Seed: 7,
		Model:      modelAudit{Provider: "deterministic", Name: "fake"},
		Gate:       GateResult{Accepted: false, Checks: []GateCheck{{Name: "minimum_score_gain", Passed: false}}},
		Comparison: Comparison{PassK: 3},
		AttributionSummary: attributionAudit{
			TrainBaseline: map[FailureCategory]int{FailureCategoryPrompt: 2},
		},
	}
	markdown := renderMarkdown(report)
	assert.Contains(t, markdown, "REJECTED")
	assert.Contains(t, markdown, "minimum_score_gain")
	assert.Contains(t, markdown, "`prompt`: 2")
}

func TestRenderMarkdownEscapesUntrustedContent(t *testing.T) {
	report := &optimizationReport{
		Mode: "fake`mode", Seed: 7,
		Model: modelAudit{Provider: "provider", Name: "model`name"},
		Gate:  GateResult{Checks: []GateCheck{{Name: "check|name\nspoof", Operator: ">="}}},
		Comparison: Comparison{PassK: 5, Deltas: []CaseDelta{{
			ID: "case|name\n## injected",
		}}},
		SelectedPrompt: "prompt\n```text\n## injected\n```",
	}

	markdown := renderMarkdown(report)
	assert.Contains(t, markdown, `case\|name<br>## injected`)
	assert.Contains(t, markdown, `check\|name<br>spoof`)
	assert.Contains(t, markdown, "5 repeated runs")
	assert.NotContains(t, markdown, "three repeated runs")
	assert.Contains(t, markdown, "````text\nprompt\n```text\n## injected\n```\n````")
}
