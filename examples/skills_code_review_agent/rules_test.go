//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRulesDetectGoRisks(t *testing.T) {
	const patch = `diff --git a/service.go b/service.go
--- a/service.go
+++ b/service.go
@@ -1,3 +1,12 @@
 package service
+import "database/sql"
+const apiKey = "sk-live-1234567890abcdef"
+func run(db *sql.DB) error {
+	go func() { runForever() }()
+	rows, err := db.Query("SELECT * FROM users")
+	if err != nil { return nil }
+	tx, err := db.Begin()
+	if err != nil { return err }
+	return tx.Commit()
+}
`
	parsed, err := ParseUnifiedDiff([]byte(patch))
	require.NoError(t, err)
	findings, warnings := AnalyzeDiff(parsed)
	requireFindingRule(t, findings, "SEC001")
	requireFindingRule(t, findings, "CON001")
	requireFindingRule(t, findings, "DB001")
	requireFindingRule(t, findings, "DB002")
	requireFindingRule(t, findings, "ERR001")
	requireFindingRule(t, warnings, "TST001")
	for _, finding := range append(findings, warnings...) {
		require.NotContains(t, finding.Evidence, "sk-live-")
	}
}

func TestDeduplicateFindings(t *testing.T) {
	input := []Finding{
		{
			Severity: severityMedium, Category: "resource_lifecycle",
			File: "a.go", Line: 7, Confidence: 0.80, RuleID: "RES001",
		},
		{
			Severity: severityHigh, Category: "resource_lifecycle",
			File: "a.go", Line: 7, Confidence: 0.95, RuleID: "RES002",
		},
		{
			Severity: severityHigh, Category: "error_handling",
			File: "a.go", Line: 7, Confidence: 0.90, RuleID: "ERR001",
		},
	}
	got := DeduplicateFindings(input)
	require.Len(t, got, 2)
	requireFindingRule(t, got, "RES002")
	requireFindingRule(t, got, "ERR001")
}

func requireFindingRule(t *testing.T, findings []Finding, ruleID string) {
	t.Helper()
	for _, finding := range findings {
		if finding.RuleID == ruleID {
			return
		}
	}
	t.Fatalf("rule %s not found in %#v", ruleID, findings)
}
