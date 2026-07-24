//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWriteReportJSON verifies writing a report as JSON.
func TestWriteReportJSON(t *testing.T) {
	report := Report{
		Version:     "1.0.0",
		Decision:    DecisionDeny,
		RiskLevel:   RiskLevelCritical,
		ToolName:    "workspace_exec",
		Command:     "rm -rf /",
		Backend:     "workspaceexec",
		Intercepted: true,
		Findings: []Finding{
			{
				RuleID:         "R-DEL-001",
				RuleName:       "Dangerous Command",
				RiskLevel:      RiskLevelCritical,
				Decision:       DecisionDeny,
				Evidence:       "rm -rf",
				Recommendation: "Remove or restrict the destructive command.",
			},
		},
	}

	var buf bytes.Buffer
	err := WriteReportJSON(&buf, report)
	require.NoError(t, err)

	// Verify it's valid JSON.
	var parsed Report
	require.NoError(t, json.Unmarshal(buf.Bytes(), &parsed))

	assert.Equal(t, report.Version, parsed.Version)
	assert.Equal(t, report.Decision, parsed.Decision)
	assert.Equal(t, report.RiskLevel, parsed.RiskLevel)
	assert.Equal(t, report.ToolName, parsed.ToolName)
	assert.Equal(t, report.Command, parsed.Command)
	assert.Equal(t, report.Intercepted, parsed.Intercepted)
	assert.Len(t, parsed.Findings, 1)
	assert.Equal(t, "R-DEL-001", parsed.Findings[0].RuleID)
}

// TestWriteReportFile verifies writing a report to a file with atomic write.
func TestWriteReportFile(t *testing.T) {
	report := Report{
		Version:     "1.0.0",
		Decision:    DecisionDeny,
		RiskLevel:   RiskLevelCritical,
		ToolName:    "workspace_exec",
		Command:     "rm -rf /",
		Backend:     "workspaceexec",
		Intercepted: true,
		Findings: []Finding{
			{
				RuleID:         "R-DEL-001",
				RuleName:       "Dangerous Command",
				RiskLevel:      RiskLevelCritical,
				Decision:       DecisionDeny,
				Evidence:       "rm -rf",
				Recommendation: "Remove or restrict the destructive command.",
			},
		},
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")

	err := WriteReportFile(path, report)
	require.NoError(t, err)

	// Verify file exists.
	_, err = os.Stat(path)
	require.NoError(t, err)

	// Verify file content.
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var parsed Report
	require.NoError(t, json.Unmarshal(data, &parsed))

	assert.Equal(t, report.Version, parsed.Version)
	assert.Equal(t, report.Decision, parsed.Decision)
	assert.Equal(t, report.RiskLevel, parsed.RiskLevel)
	assert.Equal(t, report.ToolName, parsed.ToolName)
	assert.Equal(t, report.Intercepted, parsed.Intercepted)
}

// TestNewReport verifies that NewReport creates a report from a ScanResult.
func TestNewReport(t *testing.T) {
	result := ScanResult{
		Decision:    DecisionDeny,
		RiskLevel:   RiskLevelCritical,
		ToolName:    "workspace_exec",
		Command:     "rm -rf /",
		Backend:     "workspaceexec",
		Intercepted: true,
		Findings: []Finding{
			{
				RuleID:    "R-DEL-001",
				RuleName:  "Dangerous Command",
				RiskLevel: RiskLevelCritical,
				Decision:  DecisionDeny,
			},
		},
	}

	report := NewReport(result)

	assert.Equal(t, "1.0.0", report.Version)
	assert.NotNil(t, report.GeneratedAt)
	assert.Equal(t, result.Decision, report.Decision)
	assert.Equal(t, result.RiskLevel, report.RiskLevel)
	assert.Equal(t, result.ToolName, report.ToolName)
	assert.Equal(t, result.Command, report.Command)
	assert.Equal(t, result.Backend, report.Backend)
	assert.Equal(t, result.Intercepted, report.Intercepted)
	assert.Equal(t, result.Findings, report.Findings)
}

// TestNewReport_EmptyFindings verifies NewReport with no findings.
func TestNewReport_EmptyFindings(t *testing.T) {
	result := ScanResult{
		Decision:  DecisionAllow,
		RiskLevel: RiskLevelInfo,
		ToolName:  "workspace_exec",
		Command:   "go test ./...",
		Backend:   "workspaceexec",
	}

	report := NewReport(result)

	assert.Equal(t, DecisionAllow, report.Decision)
	assert.Equal(t, RiskLevelInfo, report.RiskLevel)
	assert.Empty(t, report.Findings)
	assert.False(t, report.Intercepted)
}
