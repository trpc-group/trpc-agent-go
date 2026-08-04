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
	"strings"
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

func TestGoChecksRedactsNonSecretFindingEvidence(t *testing.T) {
	diff := "diff --git a/pkg/file.go b/pkg/file.go\n--- a/pkg/file.go\n+++ b/pkg/file.go\n@@ -1,0 +1,6 @@\n+package pkg\n+import \"os\"\n+func Load() error {\n+\tf, _ := os.Open(\"password=supersecretvalue\")\n+\treturn nil\n+}\n"
	findings, err := GoChecks(diff)
	if err != nil {
		t.Fatalf("GoChecks() error = %v", err)
	}
	var sawResource bool
	for _, finding := range findings {
		if strings.Contains(finding.Evidence, "supersecretvalue") || strings.Contains(finding.Recommendation, "supersecretvalue") {
			t.Fatalf("GoChecks() leaked secret: %#v", finding)
		}
		if finding.RuleID == "resource.close_missing" {
			sawResource = true
		}
	}
	if !sawResource {
		t.Fatalf("GoChecks() findings = %#v, want resource.close_missing", findings)
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
