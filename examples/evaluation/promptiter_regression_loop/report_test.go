//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"errors"
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

func TestWriteReportsRollsBackJSONWhenMarkdownPublicationFails(t *testing.T) {
	tests := []struct {
		name         string
		previousJSON []byte
		previousMD   []byte
	}{
		{
			name:         "restore previous report pair",
			previousJSON: []byte("old JSON report\n"),
			previousMD:   []byte("old Markdown report\n"),
		},
		{name: "restore missing report pair"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outputDir := t.TempDir()
			jsonPath := filepath.Join(outputDir, "optimization_report.json")
			markdownPath := filepath.Join(outputDir, "optimization_report.md")
			if test.previousJSON != nil {
				require.NoError(t, os.WriteFile(jsonPath, test.previousJSON, 0o600))
				require.NoError(t, os.WriteFile(markdownPath, test.previousMD, 0o600))
			}
			rename := func(oldPath, newPath string) error {
				if newPath == markdownPath {
					return errors.New("injected Markdown publication failure")
				}
				return os.Rename(oldPath, newPath)
			}

			err := writeReportsWithRename(
				outputDir,
				&optimizationReport{Mode: "new report"},
				rename,
			)

			assert.ErrorContains(t, err, "injected Markdown publication failure")
			if test.previousJSON == nil {
				assert.NoFileExists(t, jsonPath)
				assert.NoFileExists(t, markdownPath)
			} else {
				jsonData, readErr := os.ReadFile(jsonPath)
				require.NoError(t, readErr)
				markdownData, readErr := os.ReadFile(markdownPath)
				require.NoError(t, readErr)
				assert.Equal(t, test.previousJSON, jsonData)
				assert.Equal(t, test.previousMD, markdownData)
			}
			matches, globErr := filepath.Glob(
				filepath.Join(outputDir, ".optimization-report-*"),
			)
			require.NoError(t, globErr)
			assert.Empty(t, matches)
		})
	}
}

func TestRenderMarkdownIncludesDecisionEvidence(t *testing.T) {
	report := &optimizationReport{
		Mode: "fake", Seed: 7,
		EvaluationModel: modelAudit{Provider: "deterministic", Name: "fake"},
		OptimizerModel:  modelAudit{Provider: "deterministic", Name: "fake-optimizer"},
		Gate:            GateResult{Accepted: false, Checks: []GateCheck{{Name: "minimum_score_gain", Passed: false}}},
		Comparison:      Comparison{PassK: 3},
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
		EvaluationModel: modelAudit{Provider: "provider", Name: "model`name"},
		OptimizerModel:  modelAudit{Provider: "provider", Name: "optimizer`name"},
		Gate:            GateResult{Checks: []GateCheck{{Name: "check|name\nspoof", Operator: ">="}}},
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
