//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

func TestWriteReportsCreatesReadableJSONAndMarkdown(t *testing.T) {
	report := completeReport(t)
	outputDir := t.TempDir()
	require.NoError(t, WriteReports(outputDir, report))

	jsonData, err := os.ReadFile(filepath.Join(outputDir, jsonReportName))
	require.NoError(t, err)
	var decoded Report
	require.NoError(t, json.Unmarshal(jsonData, &decoded))
	assert.Equal(t, SchemaVersion, decoded.SchemaVersion)
	assert.Equal(t, report.Decision, decoded.Decision)

	markdown, err := os.ReadFile(filepath.Join(outputDir, markdownReportName))
	require.NoError(t, err)
	assert.Contains(t, string(markdown), "Selected candidate decision: ACCEPT")
	assert.Contains(t, string(markdown), "Attempt 1")
}

func TestWriteReportsOverwritesExistingReports(t *testing.T) {
	report := completeReport(t)
	outputDir := t.TempDir()
	require.NoError(t, WriteReports(outputDir, report))
	report.Run.Seed = 99
	require.NoError(t, WriteReports(outputDir, report))
	jsonData, err := os.ReadFile(filepath.Join(outputDir, jsonReportName))
	require.NoError(t, err)
	assert.Contains(t, string(jsonData), `"seed":99`)
}

func TestWriteReportsValidatesArguments(t *testing.T) {
	report := completeReport(t)
	require.ErrorContains(t, WriteReports(" ", report), "output directory is empty")
	require.ErrorContains(t, WriteReports(t.TempDir(), nil), "report is nil")
	blockingFile := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(blockingFile, []byte("blocked"), fileMode))
	require.ErrorContains(t, WriteReports(blockingFile, report), "create output directory")
}

func TestRenderMarkdownProtectsCandidatePrompt(t *testing.T) {
	report := completeReport(t)
	prompt := "first line\n```markdown\n## Injected Section\n- fake audit item\n```\nlast `value`"
	report.Rounds[0].CandidatePrompt.Text = prompt

	data, err := renderMarkdown(report)
	require.NoError(t, err)
	markdown := string(data)
	wantBlock := "- Candidate prompt:\n\n````\n" + prompt + "\n````\n"
	require.Contains(t, markdown, wantBlock)
	blockEnd := strings.Index(markdown, wantBlock) + len(wantBlock)
	usageStart := strings.Index(markdown, "## Usage")
	assert.Greater(t, usageStart, blockEnd)
}

func TestMarkdownCodeBlock(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "no backticks", value: "prompt", want: "```\nprompt\n```"},
		{name: "three backticks", value: "a```b", want: "````\na```b\n````"},
		{name: "four backticks", value: "a````b", want: "`````\na````b\n`````"},
		{name: "empty", value: "", want: "```\n\n```"},
		{name: "trailing newline", value: "prompt\n", want: "```\nprompt\n```"},
		{name: "trailing CRLF", value: "prompt\r\n", want: "```\nprompt\r\n```"},
		{name: "trailing CR", value: "prompt\r", want: "```\nprompt\r```"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, markdownCodeBlock(test.value))
		})
	}
}

func completeReport(t *testing.T) *Report {
	baseline := evaluationWithCases(caseWithMetric("a", 0.5, status.EvalStatusPassed))
	report, err := NewReport(RunMetadata{Seed: 42, Mode: "fake"}, baseline, baseline, AttributionResult{})
	require.NoError(t, err)
	require.NoError(t, AppendRound(report, completeRound(1, 0.7, true)))
	return report
}
