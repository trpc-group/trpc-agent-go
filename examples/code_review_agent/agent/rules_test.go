package agent

import "testing"

func TestDeduplicateAndTriage(t *testing.T) {
	raw := []Finding{
		{Severity: SeverityMedium, Category: "security", File: "a.go", Line: 10, Confidence: 0.75, RuleID: "low"},
		{Severity: SeverityHigh, Category: "security", File: "a.go", Line: 10, Confidence: 0.80, RuleID: "high"},
		{Severity: SeverityLow, Category: "test", File: "b.go", Line: 1, Confidence: 0.50, RuleID: "human", NeedsHuman: true},
	}
	findings, warnings, human := DeduplicateAndTriage(raw)
	if len(findings) != 1 || findings[0].RuleID != "high" {
		t.Fatalf("unexpected findings: %+v", findings)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %+v", warnings)
	}
	if len(human) != 1 || human[0].RuleID != "human" {
		t.Fatalf("unexpected human review: %+v", human)
	}
}

func TestRuleEngineFindsSecretAndRedacts(t *testing.T) {
	raw := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,6 @@\n package a\n+func f() {\n+\ttoken := \"ghp_1234567890abcdefghijklmnopqrstuvwxyz\"\n+}\n"
	input, err := ParseUnifiedDiff(raw)
	if err != nil {
		t.Fatal(err)
	}
	findings := NewRuleEngine(NewRedactor()).Analyze(input)
	if len(findings) == 0 {
		t.Fatal("expected finding")
	}
	for _, f := range findings {
		if f.RuleID == "GO-SEC-001" && containsRawSecret(f.Evidence) {
			t.Fatalf("secret was not redacted: %q", f.Evidence)
		}
	}
}

func containsRawSecret(s string) bool {
	return s == "ghp_1234567890abcdefghijklmnopqrstuvwxyz" || len(s) > 0 && (s == "super-secret-password-12345")
}
