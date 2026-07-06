//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package diffparse

import (
	"os"
	"testing"
)

func TestParseUnifiedDiff(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/fixtures/security_secret.diff")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	files, err := Parse(string(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("len(files) = %d, want 1", len(files))
	}
	file := files[0]
	if file.NewPath != "pkg/config.go" {
		t.Fatalf("NewPath = %q, want pkg/config.go", file.NewPath)
	}
	if !file.IsNew {
		t.Fatalf("IsNew = false, want true")
	}
	if file.PackageDir != "pkg" {
		t.Fatalf("PackageDir = %q, want pkg", file.PackageDir)
	}
	if got := file.Hunks[0].Lines[3].NewLine; got != 4 {
		t.Fatalf("secret line NewLine = %d, want 4", got)
	}
	if got := file.Hunks[0].Lines[3].Content; got != "\treturn \"api_key=sk-live1234567890abcdef\"" {
		t.Fatalf("secret line Content = %q", got)
	}
}
