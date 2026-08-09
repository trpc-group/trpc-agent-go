//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// These tests guard the contract that a secret detected by the
// sensitive_leak rule is not echoed into the model-visible permission
// result.  Before the fix, sensitiveLeakRule.Check copied up to 40 bytes
// of the matched secret into the risk evidence, and buildRecommendation /
// toPermissionDecision then surfaced that evidence in the
// PermissionDecision.Reason.  A real-looking API key in a scanned command
// therefore leaked back into the conversation and downstream result
// logging.
//
// The seam is the public PermissionPolicy.CheckToolPermission method.
// fakeBackendTool (declared in backend_provider_test.go) advertises
// BackendWorkspaceExec so the command is routed through the scanner's rule
// set.  We assert both sides of the contract: the deny verdict is still
// produced (redaction must not weaken detection), and the literal key
// string is absent from the returned reason (redaction must hide the
// secret).
//
// The key below is a deliberately fake value used only to prove the
// redaction contract; it matches the default `sk-[a-zA-Z0-9]{32,}`
// sensitive pattern.
const redactionTestAPIKey = "sk-ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmn"

// redactionTestKeyPrefix is a leading slice of the fake key that falls well
// inside the pre-fix evidence truncation window (40 bytes), so it would be
// echoed verbatim by the old `truncate(match, 40)` evidence.  We assert its
// absence rather than the whole key's, because the old bug truncated long
// matches — a whole-key assertion would pass even with the bug present.
const redactionTestKeyPrefix = "sk-ABCDEFGHIJKLMNOPQ"

func newWorkspaceExecPolicy(t *testing.T) *PermissionPolicy {
	t.Helper()
	policy := DefaultPolicy()
	scanner, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	return NewPermissionPolicyFromScanner(scanner, nil)
}

func checkCommand(t *testing.T, pp *PermissionPolicy, command string) tool.PermissionDecision {
	t.Helper()
	req := &tool.PermissionRequest{
		Tool:      &fakeBackendTool{declared: BackendWorkspaceExec, name: "workspace_exec"},
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"` + command + `"}`),
	}
	dec, err := pp.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	return dec
}

// TestCheckToolPermission_SecretDenied_ReasonDoesNotEchoKey proves that
// when the sensitive_leak rule denies a command containing a real-looking
// API key, the literal key string does not appear anywhere in the returned
// PermissionDecision reason.  Before the fix the key's bytes were copied
// into the risk evidence and surfaced via buildRecommendation into the
// denied reason.
func TestCheckToolPermission_SecretDenied_ReasonDoesNotEchoKey(t *testing.T) {
	pp := newWorkspaceExecPolicy(t)
	command := "echo " + redactionTestAPIKey

	dec := checkCommand(t, pp, command)

	if strings.Contains(dec.Reason, redactionTestKeyPrefix) {
		t.Errorf("permission reason must not echo the detected secret:\n"+
			"reason contains the literal key prefix %q\nreason: %s",
			redactionTestKeyPrefix, dec.Reason)
	}
}

// TestCheckToolPermission_SecretDenied_VerdictStillDeny proves that
// redacting the evidence does not weaken detection: a command embedding a
// real-looking API key is still denied.  This guards the "redaction must
// not suppress the risk" half of the contract.
func TestCheckToolPermission_SecretDenied_VerdictStillDeny(t *testing.T) {
	pp := newWorkspaceExecPolicy(t)
	command := "echo " + redactionTestAPIKey

	dec := checkCommand(t, pp, command)

	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("command with embedded secret: got action %q want %q "+
			"(redaction must not weaken detection)",
			dec.Action, tool.PermissionActionDeny)
	}
}
