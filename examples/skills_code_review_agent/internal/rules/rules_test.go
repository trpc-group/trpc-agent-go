//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package rules_test

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/parser"
	"trpc.group/trpc-go/trpc-agent-go/examples/skills_code_review_agent/internal/rules"
)

func findingsFor(t *testing.T, diff string) []rules.Finding {
	t.Helper()
	files, err := parser.Parse(strings.NewReader(diff))
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	return rules.Run(files)
}

func hasRule(findings []rules.Finding, ruleID string) bool {
	for _, f := range findings {
		if f.RuleID == ruleID {
			return true
		}
	}
	return false
}

// A bare channel send must not suppress the goroutine-leak rule (GL-001).
func TestGoroutineLeakBareSendNotSuppressed(t *testing.T) {
	diff := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,5 @@\n" +
		"+func leak() {\n" +
		"+\tgo func() {\n" +
		"+\t\tch <- 1\n" +
		"+\t}()\n" +
		"+}\n"
	if !hasRule(findingsFor(t, diff), "GL-001") {
		t.Error("bare channel send should no longer suppress GL-001")
	}
}

// An unrelated defer must not suppress the resource-leak rule (RL-001), but a
// deferred Close still should.
func TestResourceLeakDeferSpecificity(t *testing.T) {
	unrelated := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,3 @@\n" +
		"+\tf, _ := os.Open(\"x\")\n" +
		"+\tdefer mu.Unlock()\n" +
		"+\t_ = f\n"
	if !hasRule(findingsFor(t, unrelated), "RL-001") {
		t.Error("unrelated defer should not suppress RL-001")
	}

	closed := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,3 @@\n" +
		"+\tf, _ := os.Open(\"x\")\n" +
		"+\tdefer f.Close()\n" +
		"+\t_ = f\n"
	if hasRule(findingsFor(t, closed), "RL-001") {
		t.Error("deferred Close should still suppress RL-001")
	}
}

// The ctx parameter usually lives on an unchanged signature line, so CC-001 must
// consider context lines, not only added ones.
func TestContextMisuseFromContextLine(t *testing.T) {
	diff := "--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,4 @@\n" +
		" func handler(ctx context.Context) {\n" +
		"+\tc := context.Background()\n" +
		"+\t_ = c\n" +
		" }\n"
	if !hasRule(findingsFor(t, diff), "CC-001") {
		t.Error("CC-001 should fire when the ctx signature is an unchanged context line")
	}
}
