//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package scripts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiffSummarySummarizesUnifiedDiff(t *testing.T) {
	diff := readFixture(t, "security_secret.diff")
	summary, err := DiffSummary(diff)
	if err != nil {
		t.Fatalf("DiffSummary() error = %v", err)
	}
	if summary.ChangedFileCount != 1 {
		t.Fatalf("ChangedFileCount = %d, want 1", summary.ChangedFileCount)
	}
	if summary.AddedLineCount == 0 {
		t.Fatal("AddedLineCount = 0, want added lines")
	}
	if len(summary.Files) != 1 || summary.Files[0] != "pkg/config.go" {
		t.Fatalf("Files = %v, want [pkg/config.go]", summary.Files)
	}
}

func TestGoChecksReturnsFindings(t *testing.T) {
	diff := readFixture(t, "security_secret.diff")
	findings, err := GoChecks(diff)
	if err != nil {
		t.Fatalf("GoChecks() error = %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("GoChecks() returned no findings")
	}
	var sawSecret bool
	for _, finding := range findings {
		if finding.RuleID == "security.secret_leak" {
			sawSecret = true
		}
	}
	if !sawSecret {
		t.Fatalf("GoChecks() findings = %#v, want security.secret_leak", findings)
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "..", "testdata", "fixtures", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(raw)
}
