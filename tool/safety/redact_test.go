//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "testing"

func TestRedactor_RedactLiteral(t *testing.T) {
	r := NewRedactor()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "api_key unquoted",
			in:   "curl -H api_key=abcdef1234567890 https://api.example.com",
			want: "curl -H api_key=abcd***REDACTED***7890 https://api.example.com",
		},
		{
			name: "password with quoted value",
			in:   `db_password="Sup3rS3cretPassw0rd"`,
			want: `db_password="Sup3***REDACTED***w0rd"`,
		},
		{
			name: "no match",
			in:   "echo hello world",
			want: "echo hello world",
		},
		{
			name: "empty",
			in:   "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Redact(tt.in)
			if got != tt.want {
				t.Errorf("Redact(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRedactor_RedactAWSKey(t *testing.T) {
	r := NewRedactor()
	in := "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE"
	if !contains(r.Redact(in), "***REDACTED***") {
		t.Errorf("expected AWS key to be redacted, got %q", r.Redact(in))
	}
}

func TestRedactor_RedactGitHubPAT(t *testing.T) {
	r := NewRedactor()
	// 36 chars after the prefix.
	in := "Authorization: ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	if !contains(r.Redact(in), "***REDACTED***") {
		t.Errorf("expected GitHub PAT to be redacted, got %q", r.Redact(in))
	}
}

func TestRedactor_RedactBearerToken(t *testing.T) {
	r := NewRedactor()
	in := "Authorization: Bearer abcdefghijklmnop1234567890"
	if !contains(r.Redact(in), "***REDACTED***") {
		t.Errorf("expected bearer token to be redacted, got %q", r.Redact(in))
	}
}

func TestRedactor_RedactReport(t *testing.T) {
	r := NewRedactor()
	report := ScanReport{
		Command:  "curl -H api_key=abcdef1234567890 https://api.example.com",
		Evidence: "api_key=abcdef1234567890",
		Reason:   "network access: api_key=abcdef1234567890",
	}
	out := r.RedactReport(report)
	if out.Command == report.Command {
		t.Error("expected command to be redacted")
	}
	if out.Evidence == report.Evidence {
		t.Error("expected evidence to be redacted")
	}
	if out.Reason == report.Reason {
		t.Error("expected reason to be redacted")
	}
}

func TestRedactor_RedactAuditEvent(t *testing.T) {
	r := NewRedactor()
	event := AuditEvent{
		Command: "password=hunter2plaintext",
	}
	out := r.RedactAuditEvent(event)
	if !out.Sanitized {
		t.Error("expected Sanitized to be true")
	}
	if out.Command == event.Command {
		t.Error("expected command to be redacted")
	}
}

// TestRedactor_RedactCommand verifies the convenience wrapper RedactCommand.
func TestRedactor_RedactCommand(t *testing.T) {
	r := NewRedactor()
	got := r.RedactCommand("api_key=supersecret12345")
	if got == "api_key=supersecret12345" {
		t.Error("expected RedactCommand to redact the key, got unchanged")
	}
	if !contains(got, "***REDACTED***") {
		t.Errorf("expected redaction marker in output, got %q", got)
	}
}

// TestRedactor_RedactEmptyCommand covers empty input for RedactCommand.
func TestRedactor_RedactCommand_Empty(t *testing.T) {
	r := NewRedactor()
	if got := r.RedactCommand(""); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// TestRedactor_ShortSecretFullyRedacted verifies that secrets shorter
// than minShortSecretLen (12) are fully replaced without leaking
// boundary characters.
func TestRedactor_ShortSecretFullyRedacted(t *testing.T) {
	r := NewRedactor()
	// 8-char secret — should be fully redacted, no partial reveal.
	out := r.Redact("api_key=abcdefgh")
	want := "api_key=***REDACTED***"
	if out != want {
		t.Errorf("short secret should be fully redacted: got %q, want %q", out, want)
	}
}

// contains is a small helper to avoid pulling in strings for tests.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
