//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redaction

import (
	"strings"
	"testing"
)

// TestRedactText verifies known secret shapes are masked.
func TestRedactText(t *testing.T) {
	in := `api_key = "sk-abcdefghijklmnopqrstuvwxyz123456" password=super-secret`
	out := RedactText(in)
	if strings.Contains(out, "abcdefghijklmnopqrstuvwxyz") || strings.Contains(out, "super-secret") {
		t.Fatalf("secret leaked: %s", out)
	}
	if !strings.Contains(out, "[REDACTED_SECRET]") {
		t.Fatalf("missing placeholder: %s", out)
	}
}

func TestRedactTextRecallAndNegativeSamples(t *testing.T) {
	positives := []string{
		`api_key = "sk-abcdefghijklmnopqrstuvwxyz123456"`,
		`password=super-secret`,
		`const githubToken = "ghp_abcdefghijklmnopqrstuvwxyz123456"`,
		`glpat-abcdefghijklmnopqrstuvwxyz1234`,
		// Build provider-shaped values at runtime so repository push
		// protection does not mistake test data for a live credential.
		strings.Join([]string{"xox", "b-1234567890-", "abcdefghijklmnop"}, ""),
		`AKIAIOSFODNN7EXAMPLE`,
		`AIzaSyD-abcdefghijklmnopqrstuvwxyz123456`,
		`sk_live_abcdefghijklmnopqrstuv`,
		`rk_live_abcdefghijklmnopqrstuv`,
		`Authorization: Bearer abcdefghijklmnopqrstuvwxyz`,
		`Authorization: Basic dXNlcjpwYXNzd29yZA==`,
		`eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.c2lnbmF0dXJlMTIzNDU2`,
		`postgres://admin:secret-password@db.example/app`,
		`redis://default:hunter2password@cache:6379/0`,
		"-----BEGIN PRIVATE KEY-----\nabcdef0123456789\n-----END PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----\nabcdef0123456789\n-----END RSA PRIVATE KEY-----",
		`tencentcloud_secretkey = "abcdefghijklmnopqrstuvwxyz123456"`,
		`client_secret: abcdefghijklmnop`,
		`auth_token := "abcdefghijklmnop"`,
		`credential = "abcdefghijklmnop"`,
	}
	redacted := 0
	for _, sample := range positives {
		out := RedactText(sample)
		if out != sample && strings.Contains(out, "REDACTED") {
			redacted++
		}
	}
	recall := float64(redacted) / float64(len(positives))
	if recall < 0.95 {
		t.Fatalf("redaction recall %.2f (%d/%d), want >= 0.95", recall, redacted, len(positives))
	}

	negatives := []string{
		`password := os.Getenv("PASSWORD")`,
		`token, ok := os.LookupEnv("TOKEN")`,
		`const tokenCount = 12`,
		`https://example.com/public/path`,
		`sha256: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`,
		`password=short`,
		`Bearer token`,
		`[REDACTED_SECRET]`,
		`func validatePassword(password string) error`,
		`the password policy requires twelve characters`,
	}
	for _, sample := range negatives {
		if got := RedactText(sample); got != sample {
			t.Fatalf("benign sample changed:\n got %q\nwant %q", got, sample)
		}
	}
}
