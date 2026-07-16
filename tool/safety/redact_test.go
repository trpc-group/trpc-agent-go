//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRedactor_APIKey verifies that API keys are redacted.
func TestRedactor_APIKey(t *testing.T) {
	r := NewRedactor()

	// The API key redaction pattern matches "api_key=<20+ word chars>".
	input := `export API_KEY=abcdefghij0123456789xy`
	result := r.RedactString(input)

	assert.Contains(t, result, "[REDACTED]")
	assert.NotContains(t, result, "abcdefghij0123456789xy")
}

// TestRedactor_AWSKey verifies that AWS access keys are redacted.
func TestRedactor_AWSKey(t *testing.T) {
	r := NewRedactor()

	input := `AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE`
	result := r.RedactString(input)

	assert.Contains(t, result, "[REDACTED]")
	assert.NotContains(t, result, "AKIAIOSFODNN7EXAMPLE")
}

// TestRedactor_PrivateKey verifies that private keys are redacted.
func TestRedactor_PrivateKey(t *testing.T) {
	r := NewRedactor()

	input := `-----BEGIN RSA PRIVATE KEY-----MIIEpAIBAAKCAQEA...`
	result := r.RedactString(input)

	assert.Contains(t, result, "[REDACTED]")
	assert.NotContains(t, result, "BEGIN RSA PRIVATE KEY")
}

// TestRedactor_BearerToken verifies that bearer tokens are redacted.
func TestRedactor_BearerToken(t *testing.T) {
	r := NewRedactor()

	input := `Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.abc123`
	result := r.RedactString(input)

	assert.Contains(t, result, "[REDACTED]")
}

// TestRedactor_PasswordInURL verifies that passwords in URLs are redacted.
func TestRedactor_PasswordInURL(t *testing.T) {
	r := NewRedactor()

	input := `curl http://user:secret123@api.example.com/data`
	result := r.RedactString(input)

	assert.Contains(t, result, "[REDACTED]")
	assert.NotContains(t, result, "secret123")
}

// TestRedactor_SafeString verifies that safe strings are not redacted.
func TestRedactor_SafeString(t *testing.T) {
	r := NewRedactor()

	input := `go test ./...`
	result := r.RedactString(input)

	assert.Equal(t, input, result, "safe strings should not be redacted")
}

// TestRedactor_RedactFindings verifies that findings are redacted.
func TestRedactor_RedactFindings(t *testing.T) {
	r := NewRedactor()

	findings := []Finding{
		{
			RuleID:   "R-SECRET-001",
			RuleName: "Secret Leakage",
			Evidence: "password=supersecretvalue123 detected",
		},
	}

	redacted := r.RedactFindings(findings)

	assert.Contains(t, redacted[0].Evidence, "[REDACTED]")
	assert.NotContains(t, redacted[0].Evidence, "supersecretvalue123")
	// Original should not be modified.
	assert.Contains(t, findings[0].Evidence, "supersecretvalue123")
}

// TestRedactor_RedactReport verifies that reports are redacted.
func TestRedactor_RedactReport(t *testing.T) {
	r := NewRedactor()

	report := &Report{
		Command: "curl -H 'Authorization: Bearer mytoken123' http://example.com",
		Findings: []Finding{
			{
				RuleID:   "R-SECRET-001",
				Evidence: "Bearer mytoken123 detected",
			},
		},
	}

	r.RedactReport(report)

	assert.Contains(t, report.Command, "[REDACTED]")
	assert.Contains(t, report.Findings[0].Evidence, "[REDACTED]")
}

// TestRedactor_RedactReport_Nil verifies that nil report is handled safely.
func TestRedactor_RedactReport_Nil(t *testing.T) {
	r := NewRedactor()
	r.RedactReport(nil) // should not panic
}

// TestRedactor_RedactAuditEvent verifies that audit events can be redacted.
func TestRedactor_RedactAuditEvent(t *testing.T) {
	r := NewRedactor()
	event := AuditEvent{
		ToolName:  "workspace_exec",
		Decision:  DecisionDeny,
		RiskLevel: RiskLevelHigh,
	}
	r.RedactAuditEvent(&event) // currently a no-op, should not panic
}

// TestRedactor_GitHubPAT verifies that GitHub PATs are redacted.
func TestRedactor_GitHubPAT(t *testing.T) {
	r := NewRedactor()

	// ghp_ followed by 36 alphanumeric characters.
	input := `token=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij`
	result := r.RedactString(input)

	assert.Contains(t, result, "[REDACTED]")
}

// TestRedactor_SlackToken verifies that Slack tokens are redacted.
func TestRedactor_SlackToken(t *testing.T) {
	r := NewRedactor()

	input := `token=xoxb-1234567890-abcdefg`
	result := r.RedactString(input)

	assert.Contains(t, result, "[REDACTED]")
}

// TestRedactor_PasswordAssignment verifies that password assignments are redacted.
func TestRedactor_PasswordAssignment(t *testing.T) {
	r := NewRedactor()

	input := `password=mysecretpassword123`
	result := r.RedactString(input)

	assert.Contains(t, result, "[REDACTED]")
	assert.NotContains(t, result, "mysecretpassword123")
}

// TestRedactor_AWSSecretAccessKey verifies that AWS secret access keys are redacted.
func TestRedactor_AWSSecretAccessKey(t *testing.T) {
	r := NewRedactor()

	input := `AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY`
	result := r.RedactString(input)

	assert.Contains(t, result, "[REDACTED]")
}

// TestRedactor_MultipleSecrets verifies that multiple secrets in one string are all redacted.
func TestRedactor_MultipleSecrets(t *testing.T) {
	r := NewRedactor()

	input := `AKIAIOSFODNN7EXAMPLE and password=supersecret123`
	result := r.RedactString(input)

	assert.True(t, strings.Count(result, "[REDACTED]") >= 2, "both secrets should be redacted")
}

// TestRedactor_EmptyString verifies that empty strings are handled.
func TestRedactor_EmptyString(t *testing.T) {
	r := NewRedactor()

	result := r.RedactString("")
	assert.Equal(t, "", result)
}
