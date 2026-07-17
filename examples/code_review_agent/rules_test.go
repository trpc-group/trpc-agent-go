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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeduplicateAndPartitionFindings(t *testing.T) {
	in := []Finding{
		{File: "a.go", Line: 1, Category: "security", RuleID: "r", Confidence: 0.9, Severity: severityHigh},
		{File: "a.go", Line: 1, Category: "security", RuleID: "r", Confidence: 0.9, Severity: severityHigh},
		{File: "a.go", Line: 2, Category: "testing", RuleID: "t", Confidence: 0.6, Severity: severityLow},
		{File: "a.go", Line: 3, Category: "style", RuleID: "s", Confidence: 0.3, Severity: severityLow},
	}
	findings, warnings, human := PartitionFindings(DeduplicateFindings(in))
	require.Len(t, findings, 1)
	require.Len(t, human, 1)
	require.Len(t, warnings, 1)
}

func TestRedactSecrets(t *testing.T) {
	out := RedactSecrets(`apiKey = "AKID1234567890SECRET" token: abcdefghijklmno password=supersecretpassword123`)
	require.NotContains(t, out, "AKID1234567890SECRET")
	require.NotContains(t, out, "abcdefghijklmno")
	require.NotContains(t, out, "supersecretpassword123")
	require.Contains(t, out, "[REDACTED]")
}

func TestAnalyzeDiffFindsSecurity(t *testing.T) {
	raw, err := loadFixture("testdata/fixtures", "security")
	require.NoError(t, err)
	sum, err := ParseUnifiedDiff(raw)
	require.NoError(t, err)
	findings, _, _ := AnalyzeDiff(sum)
	require.NotEmpty(t, findings)
	require.True(t, strings.Contains(findings[0].RuleID, "security"))
	require.NotContains(t, findings[0].Evidence, "AKID1234567890SECRET")
}

func TestAnalyzeDiffRuleVariants(t *testing.T) {
	raw, err := loadFixture("testdata/fixtures", "goroutine_context")
	require.NoError(t, err)
	sum, err := ParseUnifiedDiff(raw)
	require.NoError(t, err)
	findings, _, _ := AnalyzeDiff(sum)
	require.NotEmpty(t, findings)
	require.Equal(t, "go.concurrency.context", findings[0].RuleID)

	raw, err = loadFixture("testdata/fixtures", "resource_close")
	require.NoError(t, err)
	sum, err = ParseUnifiedDiff(raw)
	require.NoError(t, err)
	findings, _, _ = AnalyzeDiff(sum)
	require.NotEmpty(t, findings)
	require.Equal(t, "go.resource.close", findings[0].RuleID)
}
