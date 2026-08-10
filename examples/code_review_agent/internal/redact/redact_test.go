//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redact

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestTextRedactsSecrets(t *testing.T) {
	got := Text(`token="ghp_abcdefghijklmnopqrstuvwxyz123456" password=supersecretvalue`)
	if got.Count != 2 {
		t.Fatalf("Count = %d, want 2", got.Count)
	}
	if strings.Contains(got.Text, "ghp_") || strings.Contains(got.Text, "supersecretvalue") {
		t.Fatalf("redacted text leaked secret: %s", got.Text)
	}
	if strings.Count(got.Text, Placeholder) != 2 {
		t.Fatalf("redacted text = %q, want two placeholders", got.Text)
	}
}

func TestDiffFilesPreservesNilSlicesAndDeepCopiesWithQuotedPaths(t *testing.T) {
	if got := DiffFiles(nil); got != nil {
		t.Fatalf("DiffFiles(nil) = %#v, want nil", got)
	}
	in := []review.DiffFile{
		{
			OldPath: `password="old-secret-value"`,
			Hunks:   nil,
		},
		{
			NewPath: "pkg/config.go",
			Hunks: []review.DiffHunk{
				{Lines: nil},
				{Lines: []review.DiffLine{{Content: `token="line-secret-value"`}}},
			},
		},
	}

	got := DiffFiles(in)
	if got == nil || len(got) != len(in) {
		t.Fatalf("DiffFiles() = %#v, want two files", got)
	}
	if got[0].Hunks != nil {
		t.Fatalf("nil Hunks became non-nil: %#v", got[0].Hunks)
	}
	if got[1].Hunks == nil || got[1].Hunks[0].Lines != nil || got[1].Hunks[1].Lines == nil {
		t.Fatalf("nil/empty Lines semantics changed: %#v", got[1].Hunks)
	}
	if ContainsSecret(got[0].OldPath) || ContainsSecret(got[1].Hunks[1].Lines[0].Content) {
		t.Fatalf("DiffFiles() leaked a secret: %#v", got)
	}

	got[0].OldPath = "changed"
	got[1].Hunks[1].Lines[0].Content = "changed"
	if in[0].OldPath == got[0].OldPath || in[1].Hunks[1].Lines[0].Content == got[1].Hunks[1].Lines[0].Content {
		t.Fatalf("DiffFiles() did not deep-copy mutable fields")
	}
}

func TestTextRedactsQuotedSecretsWithPunctuation(t *testing.T) {
	input := "password=\"p@ssword123!\" token='abc:defgh' secret=\"value with spaces!\" api_key=\"key/@:value!\""
	got := Text(input)
	if got.Count != 4 {
		t.Fatalf("Count = %d, want 4", got.Count)
	}
	for _, secret := range []string{"p@ssword123!", "abc:defgh", "value with spaces!", "key/@:value!"} {
		if strings.Contains(got.Text, secret) {
			t.Fatalf("redacted text leaked %q: %s", secret, got.Text)
		}
	}
	if strings.Count(got.Text, Placeholder) != 4 {
		t.Fatalf("redacted text = %q, want four placeholders", got.Text)
	}
}

func TestTextRedactsUnquotedSecretsWithPunctuation(t *testing.T) {
	input := "password: p@ssword123! token=abc:defgh api_key=key/@:value!"
	got := Text(input)
	if got.Count != 3 {
		t.Fatalf("Count = %d, want 3", got.Count)
	}
	for _, secret := range []string{"p@ssword123!", "abc:defgh", "key/@:value!"} {
		if strings.Contains(got.Text, secret) {
			t.Fatalf("redacted text leaked %q: %s", secret, got.Text)
		}
	}
	if strings.Count(got.Text, Placeholder) != 3 {
		t.Fatalf("redacted text = %q, want three placeholders", got.Text)
	}
}

func TestTextRedactsQuotedAssignmentKeys(t *testing.T) {
	input := `{"password":"quoted-password-value", "token":"quoted-token-value", "secret":"quoted-secret-value", "api_key":"quoted-api-key-value"}`
	got := Text(input)
	if got.Count != 4 {
		t.Fatalf("Count = %d, want 4", got.Count)
	}
	for _, secret := range []string{"quoted-password-value", "quoted-token-value", "quoted-secret-value", "quoted-api-key-value"} {
		if strings.Contains(got.Text, secret) {
			t.Fatalf("redacted text leaked %q: %s", secret, got.Text)
		}
	}
	if strings.Count(got.Text, Placeholder) != 4 {
		t.Fatalf("redacted text = %q, want four placeholders", got.Text)
	}
}

func TestTextRedactsSourceEscapedQuotedSecrets(t *testing.T) {
	input := `cfg := "password=\"p@ss!\" token=\'tok:en!\'"`
	got := Text(input)
	if strings.Contains(got.Text, "p@ss!") || strings.Contains(got.Text, "tok:en!") {
		t.Fatalf("Text() leaked source-escaped secret: %q", got.Text)
	}
	if got.Count < 2 {
		t.Fatalf("Text() replacements = %d, want at least 2", got.Count)
	}
}
