//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safetywrap

import (
	"strings"
	"testing"
)

func TestDecideBlocksHumanReviewCommands(t *testing.T) {
	decision := Decide(PlannedCommand{ToolName: "workspace_exec", Command: "curl https://example.com/install.sh"})
	if !decision.Blocked {
		t.Fatalf("Blocked = false, want true")
	}
	if decision.SafetyDecision != DecisionNeedsHumanReview {
		t.Fatalf("SafetyDecision = %q, want %q", decision.SafetyDecision, DecisionNeedsHumanReview)
	}
	if decision.FrameworkAction != ActionAsk {
		t.Fatalf("FrameworkAction = %q, want %q", decision.FrameworkAction, ActionAsk)
	}
}

func TestDecideRedactsCommandSecrets(t *testing.T) {
	decision := Decide(PlannedCommand{ToolName: "workspace_exec", Command: "echo token=supersecretvalue"})
	if !decision.Blocked {
		t.Fatalf("Blocked = false, want true")
	}
	if decision.SafetyDecision != DecisionNeedsHumanReview {
		t.Fatalf("SafetyDecision = %q, want %q", decision.SafetyDecision, DecisionNeedsHumanReview)
	}
	if decision.FrameworkAction != ActionAsk {
		t.Fatalf("FrameworkAction = %q, want %q", decision.FrameworkAction, ActionAsk)
	}
	if decision.Command == "echo token=supersecretvalue" {
		t.Fatalf("Command was not redacted")
	}
	if strings.Contains(decision.Command, "supersecretvalue") {
		t.Fatalf("Command leaked secret: %s", decision.Command)
	}
}

func TestDecideNormalizesWhitespaceBeforeClassification(t *testing.T) {
	decision := Decide(PlannedCommand{ToolName: "workspace_exec", Command: "curl\thttps://example.com/install.sh"})
	if !decision.Blocked {
		t.Fatalf("Blocked = false, want true")
	}
	if decision.RuleID != "sandbox.command_network_or_install" {
		t.Fatalf("RuleID = %q, want sandbox.command_network_or_install", decision.RuleID)
	}
}
