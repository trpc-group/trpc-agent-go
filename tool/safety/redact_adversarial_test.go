//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactorPrefixedSecretKeysAndCredentialURLs(t *testing.T) {
	input := map[string]any{
		"OPENAI_API_KEY":     "openai-secret-value",
		"DB_PASSWORD":        "database-secret-value",
		"service-auth-token": "service-secret-value",
		"nested": map[string]string{
			"MY_CLIENT_SECRET": "client-secret-value",
		},
		"safe": "ordinary",
	}
	clean, count := NewRedactor().RedactValue(input)
	encoded, err := json.Marshal(clean)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	for _, secret := range []string{
		"openai-secret-value", "database-secret-value",
		"service-secret-value", "client-secret-value",
	} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("structured redaction leaked %q", secret)
		}
	}
	if count < 4 {
		t.Fatalf("redaction count = %d, want at least 4", count)
	}

	text := "OPENAI_API_KEY=openai-secret DB_PASSWORD:db-secret " +
		"--service-auth-token flag-secret " +
		"postgres://agent:uri-secret@db.internal/app"
	redacted, count := NewRedactor().RedactString(text)
	for _, secret := range []string{"openai-secret", "db-secret", "flag-secret", "uri-secret"} {
		if strings.Contains(redacted, secret) {
			t.Errorf("text redaction leaked %q: %s", secret, redacted)
		}
	}
	if count < 4 {
		t.Fatalf("text redaction count = %d, want at least 4: %s", count, redacted)
	}
}

func TestRedactorDetectionCorpusExceedsNinetyFivePercent(t *testing.T) {
	cases := []struct {
		secret string
		text   string
	}{
		{"alpha-openai", "OPENAI_API_KEY=alpha-openai"},
		{"bravo-db", "DB_PASSWORD=bravo-db"},
		{"charlie-token", "service_access_token: charlie-token"},
		{"delta-secret", `--client-secret "delta-secret"`},
		{"echo-pass", `{"user_password":"echo-pass"}`},
		{"foxtrot-bearer-value", "Authorization: Bearer foxtrot-bearer-value"},
		{"ghp_1234567890abcdefghijklmnop", "token ghp_1234567890abcdefghijklmnop"},
		{"sk-proj-1234567890abcdefghijklmnop", "key sk-proj-1234567890abcdefghijklmnop"},
		{"npm_1234567890abcdefghijklmnop", "npm npm_1234567890abcdefghijklmnop"},
		{"uri-password", "https://agent:uri-password@example.com/path"},
		{"refresh-value", "my_refresh_token=refresh-value"},
		{"basic-secret-value", "Authorization: Basic basic-secret-value"},
	}
	detected := 0
	for _, test := range cases {
		redacted, count := NewRedactor().RedactString(test.text)
		if count > 0 && !strings.Contains(redacted, test.secret) {
			detected++
		}
	}
	rate := float64(detected) * 100 / float64(len(cases))
	if rate < 95 {
		t.Fatalf("secret detection rate = %.2f%% (%d/%d), want >= 95%%",
			rate, detected, len(cases))
	}
}

func TestRedactorDoesNotMatchKeyLikeOrdinaryWords(t *testing.T) {
	input := "monkey=banana passwordless=true api_keyboard=qwerty"
	redacted, count := NewRedactor().RedactString(input)
	if count != 0 || redacted != input {
		t.Fatalf("ordinary text was redacted: count=%d text=%q", count, redacted)
	}
}

func TestRedactorCoversShellQuotesMultiwordAndNestedKeys(t *testing.T) {
	redactor := NewRedactor()
	for _, test := range []struct {
		name   string
		secret string
		input  string
	}{
		{name: "ANSI-C assignment", secret: "supersecret", input: "TOKEN=$'supersecret'"},
		{name: "ANSI-C flag", secret: "flagsecret", input: "run --token $'flagsecret'"},
		{name: "multiword password", secret: "correct horse battery staple", input: "password: correct horse battery staple"},
		{name: "curl short auth", secret: "agent:curl-secret", input: "curl -u agent:curl-secret https://go.dev"},
		{name: "sshpass short flag", secret: "ssh-secret", input: "sshpass -p ssh-secret ssh host"},
	} {
		t.Run(test.name, func(t *testing.T) {
			clean, count := redactor.RedactString(test.input)
			if count == 0 || strings.Contains(clean, test.secret) {
				t.Fatalf("RedactString() = %q, count=%d", clean, count)
			}
			again, secondCount := redactor.RedactString(clean)
			if again != clean || secondCount != 0 {
				t.Fatalf("second redaction = %q, count=%d; want unchanged", again, secondCount)
			}
		})
	}

	input := map[string]any{
		"db.password":    "database phrase",
		"private_key":    "private material",
		"tls/passphrase": "key phrase",
		"dbPassword":     "camel database secret",
		"serviceToken":   "camel token secret",
		"tlsPassphrase":  "camel passphrase secret",
	}
	clean, count := redactor.RedactValue(input)
	encoded, err := json.Marshal(clean)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if count != len(input) {
		t.Fatalf("redaction count = %d, want %d", count, len(input))
	}
	for _, secret := range []string{
		"database phrase", "private material", "key phrase",
		"camel database secret", "camel token secret", "camel passphrase secret",
	} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("structured redaction leaked %q: %s", secret, encoded)
		}
	}
}

func TestRedactorReplacesNonJSONValues(t *testing.T) {
	for _, value := range []any{func() {}, make(chan int), complex(1, 2)} {
		clean, count := NewRedactor().RedactValue(value)
		if clean != RedactedValue || count == 0 {
			t.Fatalf("RedactValue(%T) = %#v, %d", value, clean, count)
		}
	}
	for _, value := range []any{true, int64(7), uint(8), 1.5} {
		clean, count := NewRedactor().RedactValue(value)
		if clean != value || count != 0 {
			t.Fatalf("RedactValue(%T) = %#v, %d", value, clean, count)
		}
	}
}
