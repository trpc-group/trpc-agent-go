//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func req(name, args string) *tool.PermissionRequest {
	return &tool.PermissionRequest{ToolName: name, Arguments: []byte(args)}
}

func TestCheckToolPermissionDeny(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil))
	d, err := p.CheckToolPermission(context.Background(), req("exec_command", `{"command":"rm -rf /"}`))
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if d.Action != tool.PermissionActionDeny {
		t.Errorf("action=%s want deny", d.Action)
	}
	if !strings.Contains(d.Reason, "cmd.dangerous_delete") {
		t.Errorf("reason should name the rule, got %q", d.Reason)
	}
}

func TestCheckToolPermissionAsk(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil))
	d, _ := p.CheckToolPermission(context.Background(), req("workspace_exec", `{"command":"pip install requests"}`))
	if d.Action != tool.PermissionActionAsk {
		t.Errorf("action=%s want ask", d.Action)
	}
}

func TestCheckToolPermissionAllow(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil))
	d, _ := p.CheckToolPermission(context.Background(), req("workspace_exec", `{"command":"go test ./..."}`))
	if d.Action != tool.PermissionActionAllow {
		t.Errorf("action=%s want allow", d.Action)
	}
}

func TestCheckToolPermissionNonExecAllowed(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil))
	// A tool we do not recognise as an exec surface is always allowed.
	d, _ := p.CheckToolPermission(context.Background(), req("read_file", `{"path":"/etc/shadow"}`))
	if d.Action != tool.PermissionActionAllow {
		t.Errorf("non-exec tool should be allowed, got %s", d.Action)
	}
}

func TestCheckToolPermissionCodeExec(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil), WithToolBackend("execute_code", BackendCodeExec))
	args := `{"code_blocks":[{"language":"python","code":"import os\nos.system('rm -rf /')"}]}`
	d, _ := p.CheckToolPermission(context.Background(), req("execute_code", args))
	if d.Action == tool.PermissionActionAllow {
		t.Errorf("python os.system should not be allowed")
	}
}

func TestCheckToolPermissionWorkdirTimeout(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil))
	// timeout_sec beyond the default max (120) must ask.
	d, _ := p.CheckToolPermission(context.Background(), req("exec_command", `{"command":"go test ./...","timeout_sec":600}`))
	if d.Action != tool.PermissionActionAsk {
		t.Errorf("oversized timeout should ask, got %s", d.Action)
	}
}

func TestPermissionAuditIntegration(t *testing.T) {
	var buf bytes.Buffer
	aw := NewAuditWriter(&buf, WithoutTimestamp())
	p := NewPermissionPolicy(NewScanner(nil), WithAuditWriter(aw), WithTelemetry(false))
	if _, err := p.CheckToolPermission(context.Background(), req("exec_command", `{"command":"rm -rf /"}`)); err != nil {
		t.Fatalf("check: %v", err)
	}
	if !strings.Contains(buf.String(), `"decision":"deny"`) {
		t.Errorf("expected an audit line for the denied call, got %q", buf.String())
	}
}

func TestScanRequestSkipsUnknownTool(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil))
	if _, scanned := p.ScanRequest(context.Background(), req("browser", `{}`)); scanned {
		t.Errorf("unknown tool should not be scanned")
	}
}
