//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
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
