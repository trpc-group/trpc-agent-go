//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights
// reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fakellm

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

// TestGenerateContentDeterministic verifies that two calls with the
// same input produce identical content. The fake model must be a pure
// function of its input — no randomness, no timestamps in the body.
func TestGenerateContentDeterministic(t *testing.T) {
	m := New()
	req := model.NewRequest([]model.Message{model.NewUserMessage("diff --git a/c.go b/c.go\n+++ b/c.go\n+password = \"abc\"\n")})

	r1, err := m.GenerateContent(context.Background(), req)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	r2, err := m.GenerateContent(context.Background(), req)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	c1 := drainContent(t, r1)
	c2 := drainContent(t, r2)
	if c1 != c2 {
		t.Fatalf("non-deterministic output:\n  first:  %s\n  second: %s", c1, c2)
	}
}

// TestScanCredential verifies the LLM-001 hardcoded-credential
// heuristic fires on common credential identifiers and is suppressed
// when the value is empty.
func TestScanCredential(t *testing.T) {
	cases := []struct {
		name string
		diff string
		want bool
	}{
		{"password assignment", "+++ b/c.go\n+password = \"abc\"\n", true},
		{"api_key assignment", "+++ b/c.go\n+api_key = \"sk-123\"\n", true},
		{"token assignment", "+++ b/c.go\n+token: \"xyz\"\n", true},
		{"empty password", "+++ b/c.go\n+password = \"\"\n", false},
		{"unrelated line", "+++ b/c.go\n+fmt.Println(\"hi\")\n", false},
		{"substring not match", "+++ b/c.go\n+passwdcount = 5\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := scan(tc.diff)
			if hasRuleID(findings, "LLM-001") != tc.want {
				t.Fatalf("LLM-001 fire=%v, want %v; findings: %+v", !tc.want, tc.want, findings)
			}
		})
	}
}

// TestScanTODO verifies the LLM-002 TODO/FIXME/XXX comment heuristic.
func TestScanTODO(t *testing.T) {
	cases := []struct {
		name string
		diff string
		want bool
	}{
		{"TODO comment", "+++ b/c.go\n+// TODO: fix this\n", true},
		{"FIXME comment", "+++ b/c.go\n+/* FIXME: broken */\n", true},
		{"XXX comment", "+++ b/c.go\n+// XXX: hack\n", true},
		{"lowercase todo", "+++ b/c.go\n+// todo later\n", true},
		{"no comment", "+++ b/c.go\n+x := 1\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings := scan(tc.diff)
			if hasRuleID(findings, "LLM-002") != tc.want {
				t.Fatalf("LLM-002 fire=%v, want %v; findings: %+v", !tc.want, tc.want, findings)
			}
		})
	}
}

// TestScanDebugPrint verifies the LLM-003 debug-print heuristic fires
// in production files but is suppressed in _test.go files.
func TestScanDebugPrint(t *testing.T) {
	prod := "+++ b/svc.go\n+fmt.Println(\"debug\")\n"
	findings := scan(prod)
	if !hasRuleID(findings, "LLM-003") {
		t.Fatalf("expected LLM-003 in production file, got: %+v", findings)
	}

	test := "+++ b/svc_test.go\n+fmt.Println(\"debug\")\n"
	testFindings := scan(test)
	if hasRuleID(testFindings, "LLM-003") {
		t.Fatalf("LLM-003 should not fire in _test.go, got: %+v", testFindings)
	}
}

// TestParseFindingsRoundTrip verifies that findings produced by scan
// can be marshalled and re-parsed without loss.
func TestParseFindingsRoundTrip(t *testing.T) {
	diff := "+++ b/c.go\n+password = \"abc\"\n+// TODO: fix\n+fmt.Println(\"x\")\n"
	original := scan(diff)
	if len(original) == 0 {
		t.Fatalf("expected findings, got none")
	}
	encoded := encodeFindings(original)
	parsed := ParseFindings(encoded)
	if len(parsed) != len(original) {
		t.Fatalf("round-trip length mismatch: %d vs %d", len(parsed), len(original))
	}
	for i := range original {
		if parsed[i] != original[i] {
			t.Fatalf("finding %d mismatch:\n  orig: %+v\n  parsed: %+v", i, original[i], parsed[i])
		}
	}
}

// TestParseFindingsEmpty verifies the empty-input contract: empty
// string and "[]" both yield nil.
func TestParseFindingsEmpty(t *testing.T) {
	if out := ParseFindings(""); out != nil {
		t.Fatalf("empty string should yield nil, got %+v", out)
	}
	if out := ParseFindings("[]"); out != nil {
		t.Fatalf("'[]' should yield nil, got %+v", out)
	}
}

// TestInfo verifies the model metadata.
func TestInfo(t *testing.T) {
	info := New().Info()
	if info.Name != ModelName {
		t.Fatalf("Info().Name = %q, want %q", info.Name, ModelName)
	}
	if info.ContextWindow <= 0 {
		t.Fatalf("ContextWindow should be positive, got %d", info.ContextWindow)
	}
}

// drainContent reads all responses from the channel and returns the
// concatenated Content of choice 0. The fake model sends a single
// Done=true response, so this is usually one read.
func drainContent(t *testing.T, ch <-chan *model.Response) string {
	t.Helper()
	var b strings.Builder
	for r := range ch {
		if r.Error != nil {
			t.Fatalf("response error: %s", r.Error.Message)
		}
		if len(r.Choices) == 0 {
			continue
		}
		b.WriteString(r.Choices[0].Message.Content)
	}
	return b.String()
}

// hasRuleID reports whether any finding has the given rule ID.
func hasRuleID(findings []Finding, id string) bool {
	for _, f := range findings {
		if f.RuleID == id {
			return true
		}
	}
	return false
}
