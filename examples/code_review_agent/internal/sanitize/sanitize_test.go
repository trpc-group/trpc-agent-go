//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sanitize

import (
	"testing"
)

func TestRedact_APIKeyInAssignment(t *testing.T) {
	r := NewRedactor(nil, "***REDACTED***")

	input := `const APIKey = "sk-proj-abc123def456ghi789jkl012mno345pqr"`
	output := r.Redact(input)

	if output == input {
		t.Error("API key was not redacted")
	}
	if output == `const APIKey = "***REDACTED***"` {
		t.Log("API key correctly redacted")
	}
}

func TestRedact_PasswordInAssignment(t *testing.T) {
	r := NewRedactor(nil, "***REDACTED***")

	input := `Password = "my-secret-password-12345"`
	output := r.Redact(input)

	if output == input {
		t.Error("password was not redacted")
	}
}

func TestRedact_BearerToken(t *testing.T) {
	r := NewRedactor(nil, "***REDACTED***")

	input := `Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9`
	output := r.Redact(input)

	if output == input {
		t.Error("Bearer token was not redacted")
	}
}

func TestRedact_PrivateKey(t *testing.T) {
	r := NewRedactor(nil, "***REDACTED***")

	input := `const privKey = "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA..."`
	output := r.Redact(input)

	if output == input {
		t.Error("private key was not redacted")
	}
}

func TestRedact_CleanTextUnchanged(t *testing.T) {
	r := NewRedactor(nil, "***REDACTED***")

	inputs := []string{
		"func main() { fmt.Println(\"hello\") }",
		"var config = loadConfig()",
		"// TODO: implement rate limiting",
		"SELECT * FROM users WHERE id = ?",
	}

	for _, input := range inputs {
		output := r.Redact(input)
		if output != input {
			t.Errorf("clean text was modified: %q -> %q", input, output)
		}
	}
}

func TestRedact_ConfidenceCheck(t *testing.T) {
	// Verify detection rate: at least 3 out of 4 patterns detected
	r := NewRedactor(nil, "***REDACTED***")

	secrets := []struct {
		input string
		desc  string
	}{
		{`api_key = "abcdefgh12345678"`, "api_key"},
		{`API_SECRET = "my-super-secret-key-12345"`, "api_secret"},
		{`token: "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"`, "token"},
		{`password = "p@ssw0rd!"`, "password"},
	}

	detected := 0
	for _, s := range secrets {
		if r.Redact(s.input) != s.input {
			detected++
		} else {
			t.Logf("NOT DETECTED: %s (%s)", s.desc, s.input)
		}
	}

	rate := float64(detected) / float64(len(secrets))
	t.Logf("Detection rate: %d/%d = %.0f%%", detected, len(secrets), rate*100)

	if rate < 0.75 {
		t.Errorf("detection rate %.0f%% below 75%% threshold", rate*100)
	}
}

func TestContainsSensitive(t *testing.T) {
	r := NewRedactor(nil, "***REDACTED***")

	if !r.ContainsSensitive(`api_key = "secret123456789"`) {
		t.Error("should detect sensitive data")
	}
	if r.ContainsSensitive("clean code with no secrets") {
		t.Error("should not flag clean text")
	}
}

func TestDiffsPresent(t *testing.T) {
	before := `token = "abc123def456ghi789"`
	after := NewRedactor(nil, "***REDACTED***").Redact(before)

	if !DiffsPresent(before, after) {
		t.Error("DiffsPresent should return true")
	}
	if DiffsPresent("clean", "clean") {
		t.Error("DiffsPresent should return false for unchanged text")
	}
}
