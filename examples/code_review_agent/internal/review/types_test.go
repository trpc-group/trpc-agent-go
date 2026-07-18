//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import (
	"strings"
	"testing"
)

func TestFindingKeyDedupesSameFileLineRule(t *testing.T) {
	f1 := Finding{File: "main.go", Line: 12, Category: "resource", RuleID: "close-file"}
	f2 := Finding{File: "main.go", Line: 12, Category: "resource", RuleID: "close-file"}

	if f1.DedupeKey() != f2.DedupeKey() {
		t.Fatalf("expected identical dedupe keys, got %q and %q", f1.DedupeKey(), f2.DedupeKey())
	}
}

func TestRedactSecretsMasksCommonTokenShapes(t *testing.T) {
	input := strings.Join([]string{
		`apiKey=sk-1234567890abcdef`,
		`llmkey="llm-live-1234567890abcdef"`,
		`openaiKey="sk-proj-1234567890abcdef"`,
		`Authorization: Bearer abc.def.ghi`,
		`passwd="super-secret-passphrase"`,
		`client_secret="github_pat_1234567890abcdef1234567890abcdef"`,
		`password=plain-password`,
		`githubToken="ghp_1234567890abcdef1234567890abcdef1234"`,
		`token=eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature`,
		`private_key="-----BEGIN PRIVATE KEY-----MIIEvQIBADANBgkqhkiG9w0BAQEFAASC-----END PRIVATE KEY-----"`,
		`dsn="postgres://reviewer:db-password-123@db.example.com/app?sslmode=require"`,
	}, " ")

	got := RedactSecrets(input)
	for _, raw := range []string{
		"sk-1234567890abcdef",
		"llm-live-1234567890abcdef",
		"sk-proj-1234567890abcdef",
		"abc.def.ghi",
		"super-secret-passphrase",
		"github_pat_1234567890abcdef1234567890abcdef",
		"plain-password",
		"ghp_1234567890abcdef1234567890abcdef1234",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjMifQ.signature",
		"-----BEGIN PRIVATE KEY-----",
		"db-password-123",
	} {
		if strings.Contains(got, raw) {
			t.Fatalf("redacted output still contains %q: %s", raw, got)
		}
	}
	for _, wantContext := range []string{"apiKey=", "llmkey=", "openaiKey=", "Authorization:", "passwd=", "client_secret=", "password=", "githubToken=", "token=", "private_key=", "postgres://reviewer:"} {
		if !strings.Contains(got, wantContext) {
			t.Fatalf("redaction should preserve context %q, got %s", wantContext, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("expected redaction marker, got %s", got)
	}
}

func TestRedactSecretsMasksMultilinePEMBlocksBeforeGenericKeyPatterns(t *testing.T) {
	input := strings.Join([]string{
		"provider request failed:",
		"private_key=-----BEGIN PRIVATE KEY-----",
		"MIIEvQIBADANBgkqhkiG9w0BAQEFAASC",
		"ZXhhbXBsZS1tdWx0aWxpbmUtc2VjcmV0",
		"-----END PRIVATE KEY-----",
		"retry=false",
	}, "\n")

	got := RedactSecrets(input)

	for _, raw := range []string{
		"-----BEGIN PRIVATE KEY-----",
		"MIIEvQIBADANBgkqhkiG9w0BAQEFAASC",
		"ZXhhbXBsZS1tdWx0aWxpbmUtc2VjcmV0",
		"-----END PRIVATE KEY-----",
	} {
		if strings.Contains(got, raw) {
			t.Fatalf("redacted output still contains %q: %s", raw, got)
		}
	}
	if !strings.Contains(got, "private_key=[REDACTED_PRIVATE_KEY]") {
		t.Fatalf("expected multiline PEM block to be fully redacted, got %s", got)
	}
	if !strings.Contains(got, "retry=false") {
		t.Fatalf("expected surrounding context to remain, got %s", got)
	}
}
