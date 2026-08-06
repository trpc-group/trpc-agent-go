//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redact

import (
	"strings"
	"testing"
)

func TestStringSecretCorpus(t *testing.T) {
	secrets := []string{
		"sk-abcdefghijklmnopqrstuvwxyz123456",
		"ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		"AIzaSyABCDEFGHIJKLMNOPQRSTUVWXYZ123456",
		"AKIAABCDEFGHIJKLMNOP",
		"Bearer abcdefghijklmnopqrstuvwxyz.123",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature123",
		"postgres://admin:secret-password@db.example/app",
		"https://admin:secret-password@example.com/path",
		`password = "correct-horse-battery-staple"`,
		"token=cli-secret-value",
		"-----BEGIN PRIVATE KEY-----\nabcdef123456\n-----END PRIVATE KEY-----",
	}
	for _, secret := range secrets {
		redacted := String("prefix " + secret + " suffix")
		if strings.Contains(redacted, secret) || !strings.Contains(redacted, "[REDACTED:") || ContainsSecret(redacted) {
			t.Fatalf("secret not redacted: %q => %q", secret, redacted)
		}
		if redacted != String("prefix "+secret+" suffix") {
			t.Fatal("redaction is not stable")
		}
		if second := String(redacted); second != redacted {
			t.Fatalf("redaction is not idempotent: %q => %q", redacted, second)
		}
	}
}

func TestStringBenignTextUnchanged(t *testing.T) {
	value := "token count and password policy contain no credential"
	if got := String(value); got != value {
		t.Fatalf("String() = %q", got)
	}
}

func TestStringPreservesMarkdownEscapedRedactionTag(t *testing.T) {
	value := `\[REDACTED:named_secret:01234567\]`
	if got := String(value); got != value || ContainsSecret(value) {
		t.Fatalf("String() = %q", got)
	}
	for _, malformed := range []string{`\[REDACTED:named_secret:01234567]`, `[REDACTED:named_secret:01234567\]`} {
		if got := String(malformed); got == malformed {
			t.Fatalf("malformed tag remained stable: %q", got)
		}
	}
	adjacent := "[REDACTED:named_secret:11111111][REDACTED:named_secret:22222222]"
	if got := String(adjacent); got != adjacent || ContainsSecret(adjacent) {
		t.Fatalf("adjacent tags changed: %q", got)
	}
}
