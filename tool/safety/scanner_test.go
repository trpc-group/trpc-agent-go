//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "testing"

func TestScanner_FirstDenyWins(t *testing.T) {
	// "shutdown -h now" hits DangerousCommandRule (danger_cmd_001)
	// which is registered first. The result should come from that rule.
	scanner := NewScanner(
		NewDangerousCommandRule(),
		NewNetworkAccessRule(),
	)
	res := scanner.Scan(ScanInput{Command: "shutdown -h now"})
	if res.Decision != DecisionDeny {
		t.Errorf("expected deny, got %s", res.Decision)
	}
	if res.RuleID != "danger_cmd_001" {
		t.Errorf("first rule should win: expected danger_cmd_001, got %s", res.RuleID)
	}
	if res.Evidence != "shutdown" {
		t.Errorf("evidence mismatch: got %s", res.Evidence)
	}
}

func TestScanner_SecondDenyAlsoWins(t *testing.T) {
	// "curl" only hits NetworkAccessRule (2nd), still must deny.
	scanner := NewScanner(
		NewDangerousCommandRule(),
		NewNetworkAccessRule(),
	)
	res := scanner.Scan(ScanInput{Command: "curl http://evil.com"})
	if res.Decision != DecisionDeny {
		t.Errorf("expected deny from second rule, got %s", res.Decision)
	}
	if res.RuleID != "network_002" {
		t.Errorf("expected network_002, got %s", res.RuleID)
	}
}

func TestScanner_EmptyCommand(t *testing.T) {
	scanner := NewScanner(NewDangerousCommandRule())
	res := scanner.Scan(ScanInput{Command: ""})
	if res.Decision != DecisionAllow {
		t.Errorf("empty command should allow, got %s", res.Decision)
	}
}

func TestScanner_AllRulesPass(t *testing.T) {
	scanner := NewScanner(
		NewDangerousCommandRule(),
		NewNetworkAccessRule(),
	)
	res := scanner.Scan(ScanInput{Command: "echo hello"})
	if res.Decision != DecisionAllow {
		t.Errorf("safe command should allow, got %s", res.Decision)
	}
}

func TestScanner_NewScanner(t *testing.T) {
	scanner := NewScanner()
	if scanner == nil {
		t.Fatal("NewScanner() returned nil")
	}
	res := scanner.Scan(ScanInput{Command: "rm -rf /"})
	if res.Decision != DecisionAllow {
		t.Errorf("empty scanner should allow, got %s", res.Decision)
	}
}

func TestScanner_RuleReturnsNil(t *testing.T) {
	scanner := NewScanner(
		NewDangerousCommandRule(),
		NewNetworkAccessRule(),
	)
	res := scanner.Scan(ScanInput{Command: "go test ./..."})
	if res.Decision != DecisionAllow {
		t.Errorf("expected allow, got %s", res.Decision)
	}
	if res.RiskLevel != RiskNone {
		t.Errorf("expected none risk, got %s", res.RiskLevel)
	}
}

func TestScanner_CodeBlocksOnly(t *testing.T) {
	// CodeBlocks without Command should also be scanned.
	scanner := NewScanner(NewNetworkAccessRule())
	res := scanner.Scan(ScanInput{
		CodeBlocks: []CodeBlock{
			{Language: "python", Code: "import requests; requests.get('http://evil.com')"},
		},
	})
	if res.Decision != DecisionDeny {
		t.Errorf("expected deny for CodeBlocks with network access, got %s", res.Decision)
	}
}

func TestScanner_AskResultIsTracked(t *testing.T) {
	// AskForReviewRule returns DecisionAsk. The scanner should return it
	// instead of falling through to allow.
	scanner := NewScanner(NewAskForReviewRule())
	res := scanner.Scan(ScanInput{Command: "rm -r ./build"})
	if res.Decision != DecisionAsk {
		t.Errorf("expected ask, got %s", res.Decision)
	}
}

func TestScanner_NilResultGoesToAllow(t *testing.T) {
	scanner := NewScanner(NewDangerousCommandRule())
	res := scanner.Scan(ScanInput{Command: "echo safe"})
	if res.Decision != DecisionAllow {
		t.Errorf("expected allow, got %s", res.Decision)
	}
	if res.Reason != "no safety rules triggered" {
		t.Errorf("expected default reason, got %s", res.Reason)
	}
}

func TestSeverity(t *testing.T) {
	tests := []struct {
		level RiskLevel
		want  int
	}{
		{RiskNone, 0},
		{RiskLow, 1},
		{RiskMedium, 2},
		{RiskHigh, 3},
		{RiskCritical, 4},
		{RiskLevel("unknown"), 0},
	}
	for _, tt := range tests {
		if got := severity(tt.level); got != tt.want {
			t.Errorf("severity(%s) = %d, want %d", tt.level, got, tt.want)
		}
	}
}

func TestContainsSubstring(t *testing.T) {
	// match
	if !containsSubstring("hello world", []string{"hello"}) {
		t.Error("should match 'hello'")
	}
	// case-insensitive
	if !containsSubstring("HELLO", []string{"hello"}) {
		t.Error("should match case-insensitive")
	}
	// no match
	if containsSubstring("foo bar", []string{"baz"}) {
		t.Error("should not match 'baz'")
	}
	// empty patterns
	if containsSubstring("foo", nil) {
		t.Error("should not match empty patterns")
	}
}
