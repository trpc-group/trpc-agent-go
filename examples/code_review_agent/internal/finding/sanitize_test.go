//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finding

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizer_OpenAIKey(t *testing.T) {
	s := NewSanitizer()
	input := `apiKey := "sk-abc123def456ghi789jkl012mnop345"`
	result := s.Sanitize(input)
	assert.NotContains(t, result, "sk-abc123")
	assert.Contains(t, result, "***REDACTED***")
}

func TestSanitizer_GitHubToken(t *testing.T) {
	s := NewSanitizer()
	input := `token := "ghp_abc123def456ghi789jkl012mnop345qrs678tuv"`
	result := s.Sanitize(input)
	assert.NotContains(t, result, "ghp_abc123")
	assert.Contains(t, result, "***REDACTED***")
}

func TestSanitizer_PrivateKey(t *testing.T) {
	s := NewSanitizer()
	input := `-----BEGIN RSA PRIVATE KEY-----
MIIEpAIBAAKCAQEA...
-----END RSA PRIVATE KEY-----`
	result := s.Sanitize(input)
	assert.Contains(t, result, "***REDACTED***")
	assert.NotContains(t, result, "BEGIN RSA PRIVATE KEY")
}

func TestSanitizer_CleanText(t *testing.T) {
	s := NewSanitizer()
	input := `func hello() { fmt.Println("hello") }`
	result := s.Sanitize(input)
	assert.Equal(t, input, result)
}

func TestSanitizer_EmptyString(t *testing.T) {
	s := NewSanitizer()
	assert.Equal(t, "", s.Sanitize(""))
}

func TestSanitizer_Finding(t *testing.T) {
	s := NewSanitizer()
	f := Finding{
		Evidence: `key := "sk-abc123def456ghi789jkl012mnop345"`,
		File:     "config.go",
		Line:     5,
	}
	result := s.SanitizeFinding(f)
	assert.True(t, result.Sanitized)
	assert.NotContains(t, result.Evidence, "sk-abc123")
	assert.Contains(t, result.Evidence, "***REDACTED***")
	// Original should be unchanged.
	assert.Contains(t, f.Evidence, "sk-abc123")
}

func TestSanitizer_FindingClean(t *testing.T) {
	s := NewSanitizer()
	f := Finding{Evidence: `x := 1`, File: "main.go"}
	result := s.SanitizeFinding(f)
	assert.False(t, result.Sanitized)
	assert.Equal(t, "x := 1", result.Evidence)
}

func TestSanitizer_HasSensitiveContent(t *testing.T) {
	s := NewSanitizer()
	assert.True(t, s.HasSensitiveContent("sk-abc123def456ghi789jkl012mnop345"))
	assert.False(t, s.HasSensitiveContent("hello world"))
	assert.True(t, s.HasSensitiveContent("apiKey = \"secret-value-12345\""))
}

func TestSanitizer_AddPattern(t *testing.T) {
	s := NewSanitizer()
	// Built-in patterns
	assert.True(t, s.HasSensitiveContent("sk-test12345678901234567890"))

	// Add custom pattern
	s.AddPattern(regexp.MustCompile(`MY_CUSTOM_SECRET`))
	assert.True(t, s.HasSensitiveContent("MY_CUSTOM_SECRET"))
}

func TestDefaultSanitizer(t *testing.T) {
	assert.Contains(t, Sanitize("sk-abc123def456ghi789jkl012mnop345"), "***REDACTED***")
	assert.True(t, HasSensitiveContent("ghp_test123456789012345678901234567890123456"))
	assert.False(t, HasSensitiveContent("clean data"))
}

func TestSanitizer_AWSKey(t *testing.T) {
	s := NewSanitizer()
	input := `awsKey := "AKIAIOSFODNN7EXAMPLE"`
	result := s.Sanitize(input)
	assert.NotContains(t, result, "AKIAIOSFOD")
	assert.Contains(t, result, "***REDACTED***")
}
