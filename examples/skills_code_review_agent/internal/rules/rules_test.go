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

// RL-001 must bind to the opened resource: another resource's Close must not
// suppress it, but a close on an unchanged context line must.
func TestResourceLeakBindsVariable(t *testing.T) {
	otherResource := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,3 @@\n" +
		"+\tf, _ := os.Open(\"x\")\n" +
		"+\tdefer g.Close()\n" +
		"+\t_ = f\n"
	if !hasRule(findingsFor(t, otherResource), "RL-001") {
		t.Error("another resource's Close should not suppress this resource's RL-001")
	}

	contextClose := "--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,3 @@\n" +
		"+\tf, _ := os.Open(\"x\")\n" +
		" \tdefer f.Close()\n" +
		" \t_ = f\n"
	if hasRule(findingsFor(t, contextClose), "RL-001") {
		t.Error("a deferred Close on an unchanged context line should suppress RL-001")
	}
}

// EH-001 must not fire when the error is checked on the assignment line itself.
func TestErrorHandlingSameLineCheck(t *testing.T) {
	diff := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,3 @@\n" +
		"+\tif err := doThing(); err != nil {\n" +
		"+\t\treturn fmt.Errorf(\"do: %w\", err)\n" +
		"+\t}\n"
	if hasRule(findingsFor(t, diff), "EH-001") {
		t.Error("same-line `if err := ...; err != nil` should not trigger EH-001")
	}
}

// MT-002 must not flag a goroutine outside a loop (no loop-variable capture),
// but must still flag one launched inside a loop.
func TestDataRaceOnlyFlagsLoopGoroutine(t *testing.T) {
	nonLoop := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,3 @@\n" +
		"+func f() {\n" +
		"+\tgo func() { doWork() }()\n" +
		"+}\n"
	if hasRule(findingsFor(t, nonLoop), "MT-002") {
		t.Error("MT-002 should not fire for a goroutine outside a loop")
	}
	inLoop := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,6 @@\n" +
		"+func f(items []int) {\n" +
		"+\tfor _, item := range items {\n" +
		"+\t\tgo func() {\n" +
		"+\t\t\thandle(item)\n" +
		"+\t\t}()\n" +
		"+\t}\n" +
		"+}\n"
	if !hasRule(findingsFor(t, inLoop), "MT-002") {
		t.Error("MT-002 should fire for a goroutine inside a loop")
	}
}

// PF-002 must not classify %v (which accepts any type) as a strconv-compatible
// conversion, but must still flag %d.
func TestFmtSprintfVerbVNotFlagged(t *testing.T) {
	verbV := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,3 @@\n" +
		"+func f(v any) string {\n" +
		"+\treturn fmt.Sprintf(\"%v\", v)\n" +
		"+}\n"
	if hasRule(findingsFor(t, verbV), "PF-002") {
		t.Error("PF-002 should not fire for the v verb")
	}
	verbD := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,3 @@\n" +
		"+func f(n int) string {\n" +
		"+\treturn fmt.Sprintf(\"%d\", n)\n" +
		"+}\n"
	if !hasRule(findingsFor(t, verbD), "PF-002") {
		t.Error("PF-002 should fire for the d verb")
	}
}

// A parameterless goroutine inside a loop that captures nothing loop-scoped is
// not a data race, so MT-002 must stay quiet.
func TestDataRaceLoopWithoutCaptureNotFlagged(t *testing.T) {
	diff := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,5 @@\n" +
		"+func f(items []int) {\n" +
		"+\tfor range items {\n" +
		"+\t\tgo func() { doWork() }()\n" +
		"+\t}\n" +
		"+}\n"
	if hasRule(findingsFor(t, diff), "MT-002") {
		t.Error("MT-002 should not fire when the goroutine captures no loop variable")
	}
}

// A checked http.Get whose body is deferred-closed is handled by RL-002; RL-001
// must not also fire (it would look for a non-existent resp.Close()).
func TestHTTPResponseNotDoubleFlagged(t *testing.T) {
	diff := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,5 @@\n" +
		"+\tresp, err := http.Get(\"http://x\")\n" +
		"+\tif err != nil {\n" +
		"+\t\treturn err\n" +
		"+\t}\n" +
		"+\tdefer resp.Body.Close()\n"
	findings := findingsFor(t, diff)
	if hasRule(findings, "RL-001") {
		t.Error("RL-001 should not fire for an http.Get with a deferred Body.Close()")
	}
	if hasRule(findings, "RL-002") {
		t.Error("RL-002 should not fire when the response body is deferred-closed")
	}
}
