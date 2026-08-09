//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// These tests guard issue 07: the hostexec_risk rule must enforce the
// structured JSON "background" boolean carried by the hostexec and
// workspaceexec argument shapes, not a textual search for "&" / "nohup"
// markers in the command string.  A request that sets "background": true
// with a command that happens to contain no background marker must still
// be denied when allow_background is false — otherwise the guard can be
// bypassed by "background": true plus a clean command string.
//
// The seam under test is the public PermissionPolicy.CheckToolPermission
// method, driven with the same fakeBackendTool used by the other
// permission tests to declare a backend without spinning up a real exec
// tool.
//
// Chosen semantics:
//
//   - The structured "background" boolean is authoritative.  When it is
//     true and the backend's allow_background is false, the scan is denied
//     regardless of whether the command text contains a background marker.
//   - The textual background markers ("&", "nohup", "disown", "setsid")
//     remain a fallback signal for the direct ScanCommand path where no
//     structured argument is available.  A command that textually requests
//     background execution is denied even when the structured flag is
//     absent or false.

// newBackgroundPolicy builds a policy with allow_background disabled (and
// require_human_review disabled so the only risk under test is the
// background risk) for the given backend.
func newBackgroundPolicy(t *testing.T, backend Backend) *Policy {
	t.Helper()
	p := DefaultPolicy()
	p.DefaultVerdict = VerdictAllow
	switch backend {
	case BackendHostExec:
		p.BackendPolicies.HostExec.AllowBackground = false
		p.BackendPolicies.HostExec.RequireHumanReview = false
	case BackendWorkspaceExec:
		p.BackendPolicies.WorkspaceExec.AllowBackground = false
		p.BackendPolicies.WorkspaceExec.RequireHumanReview = false
	default:
		t.Fatalf("unsupported backend for background policy: %q", backend)
	}
	return p
}

// checkBackgroundTool drives CheckToolPermission for a fake tool that
// declares backend with the given JSON arguments, returning the decision.
func checkBackgroundTool(t *testing.T, backend Backend, args string) tool.PermissionDecision {
	t.Helper()
	pp := NewPermissionPolicyFromScanner(mustScanner(t, newBackgroundPolicy(t, backend)), nil)
	req := &tool.PermissionRequest{
		Tool:      &fakeBackendTool{declared: backend, name: "exec_command"},
		ToolName:  "exec_command",
		Arguments: []byte(args),
	}
	dec, err := pp.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	return dec
}

// mustScanner constructs a Scanner from p, failing the test on error.
func mustScanner(t *testing.T, p *Policy) *Scanner {
	t.Helper()
	s, err := NewScanner(p)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	return s
}

// TestHostExecRisk_BackgroundTrue_NoTextualMarker_Denied is the core
// regression for issue 07.  A request carrying "background": true whose
// command text contains no "&" / "nohup" marker must be denied when
// allow_background is false.  Before the fix, hostexec_risk searched only
// the command text, so this request slipped past allow_background: false.
func TestHostExecRisk_BackgroundTrue_NoTextualMarker_Denied(t *testing.T) {
	// The command is a completely benign shell command with no background
	// marker.  Only the structured "background": true flag marks it as a
	// background request, which is exactly the bypass this issue closes.
	dec := checkBackgroundTool(t, BackendHostExec, `{"command":"echo hello","background":true}`)
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("hostexec background:true (clean command): got action %q want %q",
			dec.Action, tool.PermissionActionDeny)
	}
}

// TestHostExecRisk_BackgroundTrue_WorkspaceExec_Denied proves the same
// bypass fix applies to the workspaceexec backend, which also exposes
// background as a structured boolean and is subject to the same
// hostexec_risk rule.
func TestHostExecRisk_BackgroundTrue_WorkspaceExec_Denied(t *testing.T) {
	dec := checkBackgroundTool(t, BackendWorkspaceExec, `{"command":"echo hello","background":true}`)
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("workspaceexec background:true (clean command): got action %q want %q",
			dec.Action, tool.PermissionActionDeny)
	}
}

// TestHostExecRisk_BackgroundFalse_CommandWithAmpersand_Denied documents
// the chosen semantics for the inverse case: a request with background:false
// (or absent) whose command text contains a background operator "&" is still
// denied.  The structured flag does not excuse an explicit textual background
// operator; shellsafe also rejects "&" as a structural parse error.  This is
// strictly more conservative than the bypass being fixed.
func TestHostExecRisk_BackgroundFalse_CommandWithAmpersand_Denied(t *testing.T) {
	dec := checkBackgroundTool(t, BackendHostExec, `{"command":"echo hello &","background":false}`)
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("hostexec background:false + '&' marker: got action %q want %q",
			dec.Action, tool.PermissionActionDeny)
	}
}

// TestHostExecRisk_BackgroundAbsent_CommandWithNohup_Denied confirms the
// textual fallback still works when the structured flag is absent: a command
// using "nohup" is denied even though no "background" argument was supplied.
func TestHostExecRisk_BackgroundAbsent_CommandWithNohup_Denied(t *testing.T) {
	dec := checkBackgroundTool(t, BackendHostExec, `{"command":"nohup echo hello"}`)
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("hostexec absent background + 'nohup' marker: got action %q want %q",
			dec.Action, tool.PermissionActionDeny)
	}
}

// TestHostExecRisk_BackgroundTrue_AllowBackgroundAllows proves that when
// allow_background is true, a background:true request is not denied by the
// hostexec_risk background check.  The command text is benign and no other
// rule fires, so the result is allow.
func TestHostExecRisk_BackgroundTrue_AllowBackgroundAllows(t *testing.T) {
	p := DefaultPolicy()
	p.DefaultVerdict = VerdictAllow
	p.BackendPolicies.HostExec.AllowBackground = true
	p.BackendPolicies.HostExec.RequireHumanReview = false
	pp := NewPermissionPolicyFromScanner(mustScanner(t, p), nil)

	req := &tool.PermissionRequest{
		Tool:      &fakeBackendTool{declared: BackendHostExec, name: "exec_command"},
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"echo hello","background":true}`),
	}
	dec, err := pp.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("hostexec background:true with allow_background=true: got action %q want %q",
			dec.Action, tool.PermissionActionAllow)
	}
}

// TestHostExecRisk_BackgroundTrue_DeniedUnderReviewRequiredPolicy is the
// regression for a review finding on issue 07: hostExecRiskRule checked
// RequireHumanReview before the structured background flag, so under the
// default policy (HostExec.RequireHumanReview=true) a background:true
// request with a clean command text was only asked for review, never
// denied.  allow_background: false was therefore unenforceable out of the
// box.  The background deny must take precedence over the softer ask.
func TestHostExecRisk_BackgroundTrue_DeniedUnderReviewRequiredPolicy(t *testing.T) {
	// The default policy keeps HostExec.RequireHumanReview=true and
	// AllowBackground=false, which is the configuration in which the
	// ordering bug surfaced.
	pp := NewPermissionPolicyFromScanner(mustScanner(t, DefaultPolicy()), nil)

	req := &tool.PermissionRequest{
		Tool:      &fakeBackendTool{declared: BackendHostExec, name: "exec_command"},
		ToolName:  "exec_command",
		Arguments: []byte(`{"command":"echo hello","background":true}`),
	}
	dec, err := pp.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("hostexec background:true under default policy: got action %q want %q",
			dec.Action, tool.PermissionActionDeny)
	}
}
