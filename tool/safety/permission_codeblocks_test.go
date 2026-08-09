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
	"encoding/json"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// These tests are the regression suite for the code_blocks argument shape.
//
// The model-visible execute_code tool declares its input schema as
//
//	{ "code_blocks": [ {"language": "...", "code": "..."}, ... ] } }
//
// (see tool/codeexec/codeexec.go Declaration).  Before the fix,
// extractScanRequest looked for a non-existent top-level "code" field, so
// every real execute_code call failed extraction and CheckToolPermission
// returned Ask regardless of the block content — the source was never
// inspected by the scanner.  These tests drive the public CheckToolPermission
// seam with the real code_blocks shape and assert that the verdict reflects
// the block content rather than an automatic ask for a missing argument.
//
// The seam is the public PermissionPolicy.CheckToolPermission method; the
// fakeBackendTool (declared in backend_provider_test.go) advertises
// BackendCodeExec so inferBackend routes to the codeexec rule set without
// relying on tool-name matching.

func codeBlocksArgs(t *testing.T, blocks ...map[string]string) []byte {
	t.Helper()
	items := make([]map[string]any, len(blocks))
	for i, b := range blocks {
		items[i] = map[string]any{"language": b["language"], "code": b["code"]}
	}
	args, err := json.Marshal(map[string]any{"code_blocks": items})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return args
}

func newCodeExecPolicy(t *testing.T) *PermissionPolicy {
	t.Helper()
	// DefaultVerdict=allow so a safe block (no rule fires) yields Allow,
	// letting the test distinguish "scanned and safe" from "never scanned".
	policy := DefaultPolicy()
	policy.DefaultVerdict = VerdictAllow
	scanner, err := NewScanner(policy)
	if err != nil {
		t.Fatalf("NewScanner: %v", err)
	}
	return NewPermissionPolicyFromScanner(scanner, nil)
}

func checkCodeBlocks(t *testing.T, pp *PermissionPolicy, args []byte) tool.PermissionDecision {
	t.Helper()
	req := &tool.PermissionRequest{
		Tool:      &fakeBackendTool{declared: BackendCodeExec, name: "execute_code"},
		ToolName:  "execute_code",
		Arguments: args,
	}
	dec, err := pp.CheckToolPermission(context.Background(), req)
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	return dec
}

// TestCheckToolPermission_CodeBlocks_DangerousCodeIsDenied proves that an
// execute_code call carrying code_blocks reaches the scanner and is denied
// when a block contains dangerous code.  Before the fix this call was
// misrouted to Ask for a missing top-level "code" argument, so the dangerous
// code below would have been sent for human review instead of blocked.
func TestCheckToolPermission_CodeBlocks_DangerousCodeIsDenied(t *testing.T) {
	pp := newCodeExecPolicy(t)
	args := codeBlocksArgs(t, map[string]string{
		"language": "python",
		"code":     "import os; os.system('rm -rf /')",
	})

	dec := checkCodeBlocks(t, pp, args)

	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("dangerous code_blocks: got action %q want %q "+
			"(block source must be scanned and blocked, not auto-asked)",
			dec.Action, tool.PermissionActionDeny)
	}
}

// TestCheckToolPermission_CodeBlocks_SafeCodeIsAllowed proves that a
// code_blocks call with safe content is not misrouted to Ask for a missing
// top-level "code" argument.  With DefaultVerdict=allow and no rule firing,
// the verdict must be Allow.
func TestCheckToolPermission_CodeBlocks_SafeCodeIsAllowed(t *testing.T) {
	pp := newCodeExecPolicy(t)
	args := codeBlocksArgs(t, map[string]string{
		"language": "python",
		"code":     "print('hello')",
	})

	dec := checkCodeBlocks(t, pp, args)

	if dec.Action != tool.PermissionActionAllow {
		t.Errorf("safe code_blocks: got action %q want %q "+
			"(must not auto-ask for a missing top-level code field)",
			dec.Action, tool.PermissionActionAllow)
	}
}

// TestCheckToolPermission_CodeBlocks_EachBlockIsScanned proves that when
// code_blocks carries multiple blocks, every block's source reaches the
// scanner: a dangerous second block is denied even when the first block is
// safe.  This guards the contract that each block is scanned, not just the
// first.
func TestCheckToolPermission_CodeBlocks_EachBlockIsScanned(t *testing.T) {
	pp := newCodeExecPolicy(t)
	args := codeBlocksArgs(t,
		map[string]string{"language": "python", "code": "print('safe')"},
		map[string]string{"language": "python", "code": "import os; os.system('rm -rf /')"},
	)

	dec := checkCodeBlocks(t, pp, args)

	if dec.Action != tool.PermissionActionDeny {
		t.Errorf("second dangerous block must be scanned and denied: "+
			"got action %q want %q", dec.Action, tool.PermissionActionDeny)
	}
}

// TestCheckToolPermission_CodeBlocks_MissingArgumentAsks proves that an
// execute_code call with no code_blocks at all still fails safe: the policy
// cannot scan absent source, so it asks for human review rather than
// allowing or denying blindly.
func TestCheckToolPermission_CodeBlocks_MissingArgumentAsks(t *testing.T) {
	pp := newCodeExecPolicy(t)
	args, err := json.Marshal(map[string]any{"execution_id": "abc"})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	dec := checkCodeBlocks(t, pp, args)

	if dec.Action != tool.PermissionActionAsk {
		t.Errorf("missing code_blocks: got action %q want %q "+
			"(must fail safe to ask when there is no source to scan)",
			dec.Action, tool.PermissionActionAsk)
	}
}
