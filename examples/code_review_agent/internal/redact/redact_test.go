//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redact

import (
	"strings"
	"testing"
)

func TestText(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int    // minimum count (>=)
		contains  string // redacted marker that MUST appear
		absent    string // original secret that MUST NOT appear
	}{
		{"api_key", `API_KEY = "sk-abc123def456ghi789jkl012mno345pqr678"`, 1, "[REDACTED:api_key]", "sk-abc123def456ghi789"},
		{"bearer", `Authorization: Bearer abcdefghijklmnopqrstuvwxyz1234567890`, 1, "[REDACTED:bearer]", "abcdefghijklmnopqrstuvwxyz1234567890"},
		{"password", `password = "supersecret123"`, 1, "[REDACTED:password]", "supersecret123"},
		{"private_key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----", 1, "[REDACTED:private_key]", "MIIEowIBAAKCAQEA"},
		{"credit_card", "4111 1111 1111 1111", 1, "[REDACTED:credit_card]", "4111 1111 1111 1111"},
		{"conn_str", `postgres://user:pass@host:5432/db`, 1, "[REDACTED:connection_string]", "user:pass@host"},
		{"jwt", "eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0NTY3ODkw.SIG_signature_here_1234567890", 1, "[REDACTED:jwt]", "eyJhbGciOiJIUzI1"},
		{"aws", "AKIAIOSFODNN7EXAMPLE", 1, "[REDACTED:aws_access_key]", "AKIAIOSFODNN7EXAMPLE"},
		{"slack", "xoxb-1234567890-abcdefghij", 1, "[REDACTED:slack_token]", "xoxb-1234567890"},
		{"secret", `SECRET=verylongsecretvalue123`, 1, "[REDACTED:secret]", "verylongsecretvalue123"},
		{"github_pat", "ghp_" + strings.Repeat("a", 36), 1, "[REDACTED:github_pat]", "ghp_" + strings.Repeat("a", 36)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, n := Text(tt.input)
			if n < tt.wantCount {
				t.Errorf("count = %d, want >= %d", n, tt.wantCount)
			}
			if !strings.Contains(out, tt.contains) {
				t.Errorf("missing marker %q in %q", tt.contains, out)
			}
			if tt.absent != "" && strings.Contains(out, tt.absent) {
				t.Errorf("secret %q still present in %q", tt.absent, out)
			}
		})
	}
}

func TestText_Mixed(t *testing.T) {
	input := `API_KEY = "sk-abc123def456ghi789" and password = "supersecret123"`
	out, n := Text(input)
	if n < 2 {
		t.Errorf("count = %d, want >= 2", n)
	}
	if strings.Contains(out, "sk-abc123") {
		t.Error("api key not redacted")
	}
	if strings.Contains(out, "supersecret123") {
		t.Error("password not redacted")
	}
}

func TestText_Clean(t *testing.T) {
	out, n := Text("hello world")
	if n != 0 {
		t.Errorf("count = %d, want 0", n)
	}
	if out != "hello world" {
		t.Errorf("got %q, want %q", out, "hello world")
	}
}

func TestText_DetectionRate(t *testing.T) {
	secrets := []string{
		`API_KEY="sk-abcdefghijklmnopqrstuvwxyz0123456789"`,
		`apikey: "sk-abcdefghijklmnopqrstuvwxyz0123456789"`,
		`API_KEY = "sk-abcdefghijklmnopqrstuvwxyz0123456789"`,
		`password = "mysecret1234"`,
		`passwd: "anothersecret"`,
		`pwd="secretpass1234"`,
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
		"4111 1111 1111 1111",
		"5500 0000 0000 0004",
		`postgres://user:pass@host:5432/db`,
		`mysql://root:secret@localhost:3306/test`,
		"eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0NTY3ODkw.SIG_signature_here_1234567890",
		"AKIAIOSFODNN7EXAMPLE",
		"AKIA1234567890ABCDEF",
		"xoxb-1234567890-abcdefghij",
		"xoxp-0987654321-zyxwvuts",
		`SECRET=verylongsecretvalue123`,
		`token: "tok_abcdefghijklmnopqrstuvwxyz1234567890"`,
		"ghp_" + strings.Repeat("a", 36),
		`auth = "authvalue12345678"`,
	}
	redacted := 0
	for _, s := range secrets {
		out, _ := Text(s)
		if strings.Contains(out, "[REDACTED") {
			redacted++
		}
	}
	rate := float64(redacted) / float64(len(secrets))
	if rate < 0.95 {
		t.Errorf("detection rate = %.2f, want >= 0.95 (%d/%d)", rate, redacted, len(secrets))
	}
}
