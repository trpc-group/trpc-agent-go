//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package review

import "testing"

func TestRedactorRemovesSecretsBeforeSinks(t *testing.T) {
	canaries := []string{
		"fixture-secret-value-aws-key",
		"fixture-secret-value-github-token",
		"password = \"super-secret-value\"",
		"https://user:secret@example.com/repo.git",
		"-----BEGIN PRIVATE KEY-----\nabc123\n-----END PRIVATE KEY-----",
	}
	r := NewRedactor()
	for _, canary := range canaries {
		got := r.Redact("prefix " + canary + " suffix")
		if ContainsAny(got, []string{canary, "super-secret-value", "user:secret", "abc123"}) {
			t.Fatalf("redacted output leaked canary %q: %q", canary, got)
		}
	}
}

func TestRedactorSafeNegatives(t *testing.T) {
	r := NewRedactor()
	safe := []string{
		`password = "${PASSWORD}"`,
		`token: "${GITHUB_TOKEN}"`,
	}
	for _, s := range safe {
		if got := r.Redact(s); got != s {
			t.Fatalf("safe text redacted: got %q want %q", got, s)
		}
	}
}

func TestRedactorDoesNotTrustSafeReferenceSubstringsInsideSecrets(t *testing.T) {
	r := NewRedactor()
	cases := []string{
		`password = "real-GETENV-secret"`,
		`password = "real-${DB_PASSWORD}-secret"`,
		`password = "real-PLACEHOLDER-secret"`,
	}
	for _, s := range cases {
		got := r.Redact(s)
		if ContainsAny(got, []string{"real-GETENV-secret", "real-${DB_PASSWORD}-secret", "real-PLACEHOLDER-secret"}) {
			t.Fatalf("redacted output leaked unsafe reference substring secret %q: %q", s, got)
		}
	}
}
