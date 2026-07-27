// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package toolsafety

import (
	"context"
	"testing"
)

func TestCheckerFunc_ID(t *testing.T) {
	c := NewCheckerFunc("test_checker", nil, nil)
	if c.ID() != "test_checker" {
		t.Errorf("ID: got %q, want %q", c.ID(), "test_checker")
	}
}

func TestCheckerFunc_Check(t *testing.T) {
	expected := []RiskFinding{
		{RuleID: RuleDangerousCommand, Evidence: "test"},
	}
	c := NewCheckerFunc("test", func(ctx context.Context, req *ScanRequest) ([]RiskFinding, error) {
		return expected, nil
	}, nil)

	findings, err := c.Check(context.Background(), &ScanRequest{Command: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].RuleID != RuleDangerousCommand {
		t.Errorf("unexpected rule: %s", findings[0].RuleID)
	}
}

func TestCheckerFunc_IsEnabledDefault(t *testing.T) {
	c := NewCheckerFunc("test", nil, nil)
	if !c.IsEnabled(nil) {
		t.Error("expected enabled by default")
	}
}

func TestCheckerFunc_IsEnabledCustom(t *testing.T) {
	c := NewCheckerFunc("test", nil, func(p *SafetyPolicy) bool {
		return p != nil
	})
	if c.IsEnabled(nil) {
		t.Error("expected disabled for nil policy")
	}
	if !c.IsEnabled(&SafetyPolicy{Version: "1.0"}) {
		t.Error("expected enabled for non-nil policy")
	}
}

func TestCheckerFunc_CheckError(t *testing.T) {
	c := NewCheckerFunc("err", func(ctx context.Context, req *ScanRequest) ([]RiskFinding, error) {
		return nil, nil
	}, nil)
	findings, err := c.Check(context.Background(), &ScanRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if findings != nil {
		t.Errorf("expected nil findings, got %v", findings)
	}
}
