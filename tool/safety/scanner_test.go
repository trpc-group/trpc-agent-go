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
