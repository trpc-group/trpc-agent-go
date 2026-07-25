//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderReportsUsesCountLabelsWithoutPluralAgreement(t *testing.T) {
	report := ReviewReport{
		TaskID: "review-1", Status: "completed", Conclusion: "needs_human_review",
		Mode: "dry-run", Runtime: "fake", Skill: "code-review",
		Input: InputSummary{ChangedFiles: []string{"main.go"}},
		Findings: []Finding{{
			Severity: severityHigh, Title: "finding",
		}},
		Warnings: []Finding{{
			Severity: severityLow, Title: "warning",
		}},
		Metrics: Metrics{
			Severity: map[string]int{
				severityCritical: 0,
				severityHigh:     1,
				severityMedium:   0,
				severityLow:      1,
			},
			Errors: map[string]int{},
		},
	}

	_, markdown, err := RenderReports(report)
	require.NoError(t, err)
	require.Contains(
		t, string(markdown),
		"Findings: 1 high-confidence; warnings: 1; changed files: 1.",
	)
}
