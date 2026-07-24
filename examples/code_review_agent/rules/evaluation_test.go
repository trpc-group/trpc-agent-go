//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rules

import (
	"fmt"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diffparser"
)

func TestRuleEvaluationCorpus(t *testing.T) {
	positiveTemplates := []struct {
		rule    string
		snippet string
	}{
		{"SEC001", `var password{{n}} = "long-secret-value-{{n}}"`},
		{"SEC002", `query{{n}} := fmt.Sprintf("SELECT * FROM users WHERE id = %d", id)`},
		{"GOR001", `go func() { process{{n}}() }()`},
		{"GOR002", `ticks{{n}} := time.Tick(time.Second)`},
		{"RES001", `file{{n}}, err := os.Open("input.txt")`},
		{"ERR001", `value{{n}}, _ := strconv.Atoi(input)`},
		{"DB001", `tx{{n}}, err := db.BeginTx(ctx, nil)`},
		{"PANIC001", `panic("invalid state {{n}}")`},
	}
	benignTemplates := []string{
		`password{{n}} := os.Getenv("PASSWORD")`,
		`query{{n}} := "SELECT * FROM users WHERE id = ?"`,
		`go func() { select { case <-ctx.Done(): return } }()`,
		`ticker{{n}} := time.NewTicker(time.Second); defer ticker{{n}}.Stop()`,
		`file{{n}}, err := os.Open("input.txt"); if err == nil { defer file{{n}}.Close() }`,
		`value{{n}}, err := strconv.Atoi(input); if err != nil { return err }`,
		`tx{{n}}, err := db.BeginTx(ctx, nil); defer tx{{n}}.Rollback(); return tx{{n}}.Commit()`,
		`return fmt.Errorf("invalid state {{n}}")`,
	}

	totalPositive, detectedPositive := 0, 0
	for _, template := range positiveTemplates {
		for variant := 1; variant <= 5; variant++ {
			totalPositive++
			snippet := strings.ReplaceAll(template.snippet, "{{n}}", fmt.Sprint(variant))
			result := scanEvaluationSnippet(t, snippet)
			if containsHighConfidenceRule(result, template.rule) {
				detectedPositive++
			}
		}
	}

	totalNegative, falsePositive := 0, 0
	for _, template := range benignTemplates {
		for variant := 1; variant <= 5; variant++ {
			totalNegative++
			snippet := strings.ReplaceAll(template, "{{n}}", fmt.Sprint(variant))
			result := scanEvaluationSnippet(t, snippet)
			if hasHighRiskFinding(result) {
				falsePositive++
			}
		}
	}

	recall := float64(detectedPositive) / float64(totalPositive)
	falsePositiveRate := float64(falsePositive) / float64(totalNegative)
	t.Logf("rule evaluation: recall=%.3f (%d/%d), false_positive_rate=%.3f (%d/%d)",
		recall, detectedPositive, totalPositive, falsePositiveRate, falsePositive, totalNegative)
	if recall < 0.80 {
		t.Fatalf("high-risk recall %.3f, want >= 0.80", recall)
	}
	if falsePositiveRate > 0.15 {
		t.Fatalf("high-risk false-positive rate %.3f, want <= 0.15", falsePositiveRate)
	}
}

func scanEvaluationSnippet(t *testing.T, snippet string) Result {
	t.Helper()
	snippet = strings.ReplaceAll(snippet, "; ", "\n+")
	diff := fmt.Sprintf("diff --git a/eval.go b/eval.go\n--- a/eval.go\n+++ b/eval.go\n@@ -1 +1,4 @@\n package eval\n+func evaluate(ctx context.Context) error {\n+%s\n+return nil\n+}\n", snippet)
	files, err := diffparser.ParseUnifiedDiff([]byte(diff))
	if err != nil {
		t.Fatal(err)
	}
	return Scan(files)
}

func containsHighConfidenceRule(result Result, ruleID string) bool {
	for _, finding := range result.Findings {
		if finding.RuleID == ruleID {
			return true
		}
	}
	return false
}

func hasHighRiskFinding(result Result) bool {
	for _, finding := range result.Findings {
		if finding.RuleID != "TEST001" {
			return true
		}
	}
	return false
}
