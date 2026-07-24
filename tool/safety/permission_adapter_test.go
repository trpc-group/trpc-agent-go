//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestPermissionAdapterRealJSONRequests(t *testing.T) {
	adapter := NewPermissionAdapter(nil, nil)
	cases := []struct {
		name   string
		tool   tool.Tool
		args   string
		action tool.PermissionAction
	}{
		{"deny", testExecutionTool{kind: tool.ExecutionToolKindHostShell}, `{"command":"rm -rf generated"}`, tool.PermissionActionDeny},
		{"ask", testExecutionTool{kind: tool.ExecutionToolKindHostShell}, `{"command":"echo ok","background":true}`, tool.PermissionActionAsk},
		{"allow", testExecutionTool{kind: tool.ExecutionToolKindWorkspaceShell}, `{"command":"echo ok","env":{"SAFE":"1"}}`, tool.PermissionActionAllow},
		{"code deny", testExecutionTool{kind: tool.ExecutionToolKindCode}, `{"code_blocks":[{"language":"bash","code":"rm -rf generated"}]}`, tool.PermissionActionDeny},
		{"unrelated tool", testOrdinaryTool{}, `{not json`, tool.PermissionActionAllow},
		{"parse failure", testExecutionTool{kind: tool.ExecutionToolKindHostShell}, `{not json`, tool.PermissionActionDeny},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := adapter.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				Tool: tc.tool, ToolName: tc.tool.Declaration().Name, Arguments: []byte(tc.args),
			})
			if err != nil {
				t.Fatalf("CheckToolPermission() error = %v", err)
			}
			if decision.Action != tc.action {
				t.Fatalf("action = %q, want %q (%s)", decision.Action, tc.action, decision.Reason)
			}
		})
	}
}

func TestPermissionAdapterWrapperLimitsEnvironmentAndResult(t *testing.T) {
	inner := &recordingExecutionTool{kind: tool.ExecutionToolKindHostShell}
	adapter := NewPermissionAdapter(&Policy{MaxTimeoutMS: 1000, MaxOutputBytes: 4, EnvWhitelist: []string{"SAFE"}}, nil)
	wrapped := adapter.Wrap(inner)
	if _, ok := wrapped.(*executionToolAdapter); !ok {
		t.Fatalf("Wrap() did not wrap execution tool: %T", wrapped)
	}
	result, err := wrapped.Call(context.Background(), []byte(`{"command":"echo ok","env":{"SAFE":"1","SECRET":"leak"},"timeout_sec":60}`))
	if err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	var got map[string]any
	encoded, _ := json.Marshal(result)
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got["output"] != "abcd" || got["output_truncated"] != true {
		t.Fatalf("result = %#v, want redacted truncated output", got)
	}
	var passed map[string]json.RawMessage
	if err := json.Unmarshal(inner.args, &passed); err != nil {
		t.Fatal(err)
	}
	var env map[string]string
	if err := json.Unmarshal(passed["env"], &env); err != nil {
		t.Fatal(err)
	}
	if len(env) != 1 || env["SAFE"] != "1" {
		t.Fatalf("env = %#v, want SAFE only", env)
	}
	var timeoutMS int
	if err := json.Unmarshal(passed["timeout_ms"], &timeoutMS); err != nil || timeoutMS != 1000 {
		t.Fatalf("timeout_ms = %d, err = %v", timeoutMS, err)
	}
}

func TestPermissionAdapterAuditorFailOpen(t *testing.T) {
	// An allow decision must stand even when the auditor fails.
	adapter := NewPermissionAdapter(nil, nil,
		WithAuditor(errorAuditor{err: ErrAuditorClosed}, AuditFailOpen),
	)
	decision, err := adapter.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		Tool:      testExecutionTool{kind: tool.ExecutionToolKindWorkspaceShell},
		ToolName:  "test_exec",
		Arguments: []byte(`{"command":"echo ok"}`),
	})
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if decision.Action != tool.PermissionActionAllow {
		t.Fatalf("action = %q, want allow (fail-open must not block)", decision.Action)
	}
}

func TestPermissionAdapterAuditorFailClosed(t *testing.T) {
	// An allow decision must be overridden to deny when the auditor fails
	// under fail-closed.
	adapter := NewPermissionAdapter(nil, nil,
		WithAuditor(errorAuditor{err: ErrAuditorClosed}, AuditFailClosed),
	)
	decision, err := adapter.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		Tool:      testExecutionTool{kind: tool.ExecutionToolKindWorkspaceShell},
		ToolName:  "test_exec",
		Arguments: []byte(`{"command":"echo ok"}`),
	})
	if err != nil {
		t.Fatalf("CheckToolPermission: %v", err)
	}
	if decision.Action != tool.PermissionActionDeny {
		t.Fatalf("action = %q, want deny (fail-closed must block)", decision.Action)
	}
	if !strings.Contains(decision.Reason, "audit write failed") {
		t.Fatalf("reason = %q, want audit write failure message", decision.Reason)
	}
}

func TestPermissionAdapterAuditorReceivesReport(t *testing.T) {
	rec := &recordingAuditor{}
	adapter := NewPermissionAdapter(nil, nil, WithAuditor(rec, AuditFailOpen))
	_, _ = adapter.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		Tool:      testExecutionTool{kind: tool.ExecutionToolKindHostShell},
		ToolName:  "test_exec",
		Arguments: []byte(`{"command":"rm -rf generated"}`),
	})
	if len(rec.reports) != 1 {
		t.Fatalf("auditor received %d reports, want 1", len(rec.reports))
	}
	r := rec.reports[0]
	if r.ToolName != "test_exec" {
		t.Fatalf("tool_name = %q", r.ToolName)
	}
	if r.Decision != DecisionDeny {
		t.Fatalf("decision = %q, want deny", r.Decision)
	}
	if !r.Intercepted {
		t.Fatal("intercepted should be true for denied call")
	}
	if r.DurationMS < 0 {
		t.Fatalf("duration_ms = %d, should be >= 0", r.DurationMS)
	}
}

func TestPermissionAdapterJSONLAuditorEndToEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	auditor, err := NewJSONLAuditor(path)
	if err != nil {
		t.Fatalf("NewJSONLAuditor: %v", err)
	}
	defer auditor.Close()

	adapter := NewPermissionAdapter(nil, nil, WithAuditor(auditor, AuditFailOpen))
	_, _ = adapter.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		Tool:      testExecutionTool{kind: tool.ExecutionToolKindHostShell},
		ToolName:  "test_exec",
		Arguments: []byte(`{"command":"rm -rf generated"}`),
	})
	// Also test an allowed call.
	_, _ = adapter.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		Tool:      testExecutionTool{kind: tool.ExecutionToolKindWorkspaceShell},
		ToolName:  "test_exec",
		Arguments: []byte(`{"command":"echo ok"}`),
	})

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer file.Close()

	sc := bufio.NewScanner(file)
	count := 0
	for sc.Scan() {
		var event AuditEvent
		if err := json.Unmarshal(sc.Bytes(), &event); err != nil {
			t.Fatalf("line %d: invalid JSON: %v", count, err)
		}
		if event.ToolName != "test_exec" {
			t.Fatalf("line %d: tool_name = %q", count, event.ToolName)
		}
		if event.DurationMS < 0 {
			t.Fatalf("line %d: duration_ms = %d", count, event.DurationMS)
		}
		switch count {
		case 0:
			if event.Decision != DecisionDeny {
				t.Fatalf("line 0: decision = %q, want deny", event.Decision)
			}
			if !event.Intercepted {
				t.Fatal("line 0: intercepted should be true")
			}
		case 1:
			if event.Decision != DecisionAllow {
				t.Fatalf("line 1: decision = %q, want allow", event.Decision)
			}
			if event.Intercepted {
				t.Fatal("line 1: intercepted should be false for allowed call")
			}
		}
		count++
	}
	if count != 2 {
		t.Fatalf("event count = %d, want 2", count)
	}
}

type testExecutionTool struct{ kind tool.ExecutionToolKind }

func (t testExecutionTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "test_exec"}
}
func (t testExecutionTool) ExecutionToolKind() tool.ExecutionToolKind { return t.kind }

type testOrdinaryTool struct{}

func (testOrdinaryTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: "ordinary"} }

type recordingExecutionTool struct {
	kind tool.ExecutionToolKind
	args []byte
}

func (t *recordingExecutionTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "record_exec"}
}
func (t *recordingExecutionTool) ExecutionToolKind() tool.ExecutionToolKind { return t.kind }
func (t *recordingExecutionTool) Call(_ context.Context, args []byte) (any, error) {
	t.args = append([]byte(nil), args...)
	return map[string]any{"output": "abcdef"}, nil
}

type errorAuditor struct{ err error }

func (a errorAuditor) Write(Report) error { return a.err }

type recordingAuditor struct {
	reports []Report
}

func (a *recordingAuditor) Write(r Report) error {
	a.reports = append(a.reports, r)
	return nil
}
