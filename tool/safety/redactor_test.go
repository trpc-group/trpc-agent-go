//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactTextSensitiveForms(t *testing.T) {
	tests := []string{
		"api_key=top-secret-value",
		"Authorization: Bearer abc.def.ghi",
		"AKIAIOSFODNN7EXAMPLE",
		"ghp_123456789012345678901234567890",
		"https://example.com/path?token=top-secret-value",
		"C:\\Users\\alice\\repo",
		"/home/alice/repo",
		"-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----",
	}
	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			got, changed := redactText(input)
			require.True(t, changed)
			require.Contains(t, got, redactedValue)
			require.NotEqual(t, input, got)
		})
	}
}

func TestRedactReportRecursesIntoFindings(t *testing.T) {
	secret := "api_key=top-secret-value"
	report := redactReport(Report{
		Evidence: secret,
		Findings: []Finding{{Evidence: secret}},
	})
	encoded := report.Evidence + report.Findings[0].Evidence
	require.True(t, report.Redacted)
	require.False(t, strings.Contains(encoded, "top-secret-value"))
}

func TestRedactTextLeavesSafeValueUnchanged(t *testing.T) {
	got, changed := redactText("go test ./...")
	require.False(t, changed)
	require.Equal(t, "go test ./...", got)
}
