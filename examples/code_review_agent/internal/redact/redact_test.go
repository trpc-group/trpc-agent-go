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
