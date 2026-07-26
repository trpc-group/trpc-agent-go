//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBaselineInstructionPrecedence(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "baseline-prompt.txt")
	if err := os.WriteFile(promptFile, []byte("  from file  \n"), 0o644); err != nil {
		t.Fatalf("write prompt file: %v", err)
	}
	missingFile := filepath.Join(dir, "does-not-exist.txt")

	t.Run("explicit flag wins over file", func(t *testing.T) {
		got, src, err := resolveBaselineInstruction("  from flag ", promptFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "from flag" {
			t.Errorf("instruction = %q, want %q", got, "from flag")
		}
		if src != "" {
			t.Errorf("sourceFile = %q, want empty (flag used)", src)
		}
	})

	t.Run("file used when flag empty", func(t *testing.T) {
		got, src, err := resolveBaselineInstruction("", promptFile)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "from file" {
			t.Errorf("instruction = %q, want %q", got, "from file")
		}
		if src != promptFile {
			t.Errorf("sourceFile = %q, want %q", src, promptFile)
		}
	})

	t.Run("built-in default when both flag and file empty", func(t *testing.T) {
		got, src, err := resolveBaselineInstruction("", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != defaultCandidateInstruction {
			t.Errorf("instruction = %q, want default", got)
		}
		if src != "" {
			t.Errorf("sourceFile = %q, want empty (default used)", src)
		}
	})

	t.Run("error when configured file missing", func(t *testing.T) {
		got, src, err := resolveBaselineInstruction("", missingFile)
		if err == nil {
			t.Fatalf("expected error for missing configured file, got instruction=%q src=%q", got, src)
		}
	})

	t.Run("error when configured file is blank", func(t *testing.T) {
		blankFile := filepath.Join(dir, "blank-prompt.txt")
		if err := os.WriteFile(blankFile, []byte("   \n\t\n"), 0o644); err != nil {
			t.Fatalf("write blank prompt file: %v", err)
		}
		if _, _, err := resolveBaselineInstruction("", blankFile); err == nil {
			t.Fatalf("expected error for blank configured file")
		}
	})
}
