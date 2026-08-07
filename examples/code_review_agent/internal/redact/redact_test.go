//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redact

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStringEmpty(t *testing.T) {
	assert.Equal(t, "", String(""))
}

func TestStringNoSecret(t *testing.T) {
	s := "hello world"
	assert.Equal(t, s, String(s))
}

func TestStringOpenAIKey(t *testing.T) {
	s := "sk-1234567890abcdef1234567890abcdef"
	result := String(s)
	assert.Contains(t, result, "[REDACTED:OPENAI_KEY]")
	assert.NotContains(t, result, "sk-12345")
}

func TestStringGitHubToken(t *testing.T) {
	s := "ghp_1234567890abcdef1234567890"
	result := String(s)
	assert.Contains(t, result, "[REDACTED:GITHUB_TOKEN]")
}

func TestStringJWT(t *testing.T) {
	s := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	result := String(s)
	assert.Contains(t, result, "[REDACTED:JWT]")
}

func TestStringPassword(t *testing.T) {
	s := `password = "mysecret123"`
	result := String(s)
	assert.Contains(t, result, "[REDACTED:PASSWORD]")
	assert.NotContains(t, result, "mysecret123")
}

func TestStringPasswordColon(t *testing.T) {
	s := `password: "super_secret"`
	result := String(s)
	assert.Contains(t, result, "[REDACTED:PASSWORD]")
}

func TestStringAWSAccessKey(t *testing.T) {
	s := "AKIAIOSFODNN7EXAMPLE"
	result := String(s)
	assert.Contains(t, result, "[REDACTED:AWS_ACCESS_KEY]")
}

func TestStringConnString(t *testing.T) {
	s := "mysql://admin:root_password@localhost/db"
	result := String(s)
	assert.Contains(t, result, "[REDACTED:CREDENTIALS]")
}

func TestContainsSecret(t *testing.T) {
	assert.True(t, ContainsSecret("sk-1234567890abcdef1234567890abcdef"))
	assert.True(t, ContainsSecret("password: secret"))
	assert.False(t, ContainsSecret("hello world"))
	assert.False(t, ContainsSecret(""))
}

func TestStringIdempotent(t *testing.T) {
	secrets := []string{
		"sk-1234567890abcdef1234567890abcdef",
		"ghp_1234567890abcdef1234567890",
		"AKIAIOSFODNN7EXAMPLE",
		`password = "mysecret123"`,
	}

	for _, secret := range secrets {
		once := String(secret)
		twice := String(once)
		assert.Equal(t, once, twice, "double redaction should be idempotent for: %s", secret)
	}
}

func TestContainsSecretEqualsStringInequality(t *testing.T) {
	s := "sk-1234567890abcdef1234567890abcdef"
	assert.True(t, ContainsSecret(s))
	assert.NotEqual(t, s, String(s))
}
