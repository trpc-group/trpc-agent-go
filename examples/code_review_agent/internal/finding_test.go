//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDedupFindings_SameKeyKeepsHighestConfidence(t *testing.T) {
	findings := []Finding{
		{File: "a.go", Line: 10, Category: "security", Confidence: 0.7, Severity: SeverityHigh},
		{File: "a.go", Line: 10, Category: "security", Confidence: 0.9, Severity: SeverityCritical},
		{File: "a.go", Line: 10, Category: "security", Confidence: 0.5, Severity: SeverityMedium},
	}
	deduped := DedupFindings(findings)
	require.Len(t, deduped, 1)
	require.Equal(t, 0.9, deduped[0].Confidence)
	require.Equal(t, SeverityCritical, deduped[0].Severity)
}

func TestDedupFindings_DifferentKeys(t *testing.T) {
	findings := []Finding{
		{File: "a.go", Line: 10, Category: "security", Confidence: 0.9},
		{File: "a.go", Line: 20, Category: "security", Confidence: 0.8},
		{File: "a.go", Line: 10, Category: "resource_leak", Confidence: 0.7},
	}
	deduped := DedupFindings(findings)
	require.Len(t, deduped, 3)
}

func TestDedupFindings_Empty(t *testing.T) {
	require.Nil(t, DedupFindings(nil))
}

func TestSplitFindings(t *testing.T) {
	findings := []Finding{
		{File: "a.go", Line: 10, Category: "security", Confidence: 0.9, Severity: SeverityCritical},
		{File: "a.go", Line: 20, Category: "test", Confidence: 0.3, Severity: SeverityLow},
	}
	confirmed, warnings := SplitFindings(findings)
	require.Len(t, confirmed, 1)
	require.Len(t, warnings, 1)
	require.Equal(t, 0.9, confirmed[0].Confidence)
	require.Equal(t, 0.3, warnings[0].Confidence)
}

func TestRedactSensitiveInfo_APIKey(t *testing.T) {
	input := `const apiKey = "sk-1234567890abcdef1234567890"`
	redacted := RedactSensitiveInfo(input)
	require.NotContains(t, redacted, "sk-1234567890abcdef1234567890")
	require.Contains(t, redacted, redactedValue)
}

func TestRedactSensitiveInfo_Token(t *testing.T) {
	input := `token = "tok_1234567890abcdef1234567890abcd"`
	redacted := RedactSensitiveInfo(input)
	require.NotContains(t, redacted, "tok_1234567890abcdef1234567890abcd")
}

func TestRedactSensitiveInfo_Password(t *testing.T) {
	input := `password = "MySecretPassword123"`
	redacted := RedactSensitiveInfo(input)
	require.NotContains(t, redacted, "MySecretPassword123")
}

func TestRedactSensitiveInfo_Bearer(t *testing.T) {
	input := `Authorization: Bearer abcdef1234567890abcdef1234567890abcd`
	redacted := RedactSensitiveInfo(input)
	require.NotContains(t, redacted, "abcdef1234567890abcdef1234567890abcd")
}

func TestRedactSensitiveInfo_PrivateKey(t *testing.T) {
	input := `-----BEGIN RSA PRIVATE KEY-----`
	redacted := RedactSensitiveInfo(input)
	require.Contains(t, redacted, redactedValue)
}

func TestRedactSensitiveInfo_ConnectionString(t *testing.T) {
	input := `postgres://admin:supersecret@localhost:5432/mydb`
	redacted := RedactSensitiveInfo(input)
	require.NotContains(t, redacted, "supersecret")
}

func TestRedactSensitiveInfo_NoSecret(t *testing.T) {
	input := `fmt.Println("hello world")`
	redacted := RedactSensitiveInfo(input)
	require.Equal(t, input, redacted)
}

func TestContainsSensitiveInfo(t *testing.T) {
	require.True(t, ContainsSensitiveInfo(`api_key = "sk-1234567890abcdef1234567890"`))
	require.True(t, ContainsSensitiveInfo(`password = "secret123"`))
	require.False(t, ContainsSensitiveInfo(`fmt.Println("hello")`))
}

func TestCountBySeverity(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityCritical},
		{Severity: SeverityCritical},
		{Severity: SeverityHigh},
		{Severity: SeverityMedium},
	}
	counts := CountBySeverity(findings)
	require.Equal(t, 2, counts[SeverityCritical])
	require.Equal(t, 1, counts[SeverityHigh])
	require.Equal(t, 1, counts[SeverityMedium])
	require.Equal(t, 0, counts[SeverityLow])
}
