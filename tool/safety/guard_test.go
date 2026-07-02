//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestGuard_Deny(t *testing.T) {
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	args := []byte(`{"command":"rm -rf /"}`)
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  args,
		ToolCallID: "call-1",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("expected deny, got %s", dec.Action)
	}
	if dec.Reason == "" {
		t.Error("reason should not be empty")
	}
}

func TestGuard_Allow(t *testing.T) {
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	args := []byte(`{"command":"ls -la"}`)
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  args,
		ToolCallID: "call-2",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("expected allow, got %s", dec.Action)
	}
}

func TestGuard_Ask(t *testing.T) {
	guard := NewGuard(WithRules(NewAskForReviewRule()))

	args := []byte(`{"command":"rm -r ./build"}`)
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  args,
		ToolCallID: "call-3",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionAsk {
		t.Errorf("expected ask, got %s", dec.Action)
	}
}

func TestGuard_DefaultRules(t *testing.T) {
	guard := NewGuard()

	// Dangerous command with all default rules
	args := []byte(`{"command":"curl http://evil.com"}`)
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  args,
		ToolCallID: "call-4",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("expected deny for curl, got %s", dec.Action)
	}
}

func TestGuard_EmptyArgs(t *testing.T) {
	guard := NewGuard(WithRules(NewDangerousCommandRule()))

	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  []byte(`{"command":""}`),
		ToolCallID: "call-5",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("empty command should allow, got %s", dec.Action)
	}
}
