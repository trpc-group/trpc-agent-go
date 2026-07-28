//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TestRecordingToolPolicy_NilInnerDenies ensures missing policies fail closed.
func TestRecordingToolPolicy_NilInnerDenies(t *testing.T) {
	r := safety.NewRecordingToolPolicy(nil)
	dec, err := r.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"skills/code-review/scripts/run_checks.sh"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Fatalf("action=%s want deny", dec.Action)
	}
	got := r.Decisions()
	if len(got) != 1 || got[0].Action != safety.ActionDeny {
		t.Fatalf("audit=%+v", got)
	}
}

// TestRecordingToolPolicy_RecordsInnerErrors keeps failed checks auditable.
func TestRecordingToolPolicy_RecordsInnerErrors(t *testing.T) {
	inner := tool.PermissionPolicyFunc(func(
		context.Context, *tool.PermissionRequest,
	) (tool.PermissionDecision, error) {
		return tool.DenyPermission("boom"), errors.New("policy backend unavailable")
	})
	r := safety.NewRecordingToolPolicy(inner)
	_, err := r.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"bash -lc id"}`),
	})
	if err == nil {
		t.Fatal("expected error")
	}
	got := r.Decisions()
	if len(got) != 1 {
		t.Fatalf("decisions=%d", len(got))
	}
	if got[0].Action != safety.ActionDeny {
		t.Fatalf("action=%s", got[0].Action)
	}
	if !strings.Contains(got[0].Command, "bash") {
		t.Fatalf("command=%q", got[0].Command)
	}
	if !strings.Contains(got[0].Reason, "unavailable") {
		t.Fatalf("reason=%q", got[0].Reason)
	}
}
