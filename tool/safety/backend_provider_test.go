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
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// fakeBackendTool is a minimal tool whose only purpose is to declare a
// safety backend via BackendProvider.  It deliberately advertises a
// ToolName that the legacy name heuristic would map to a *different*
// backend, so a test can prove that inferBackend honors the declared
// backend and never consults the name.
type fakeBackendTool struct {
	declared Backend
	name     string
}

func (f *fakeBackendTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: f.name}
}

// SafetyBackend implements BackendProvider.
func (f *fakeBackendTool) SafetyBackend() Backend { return f.declared }

// TestInferBackend_HonorsDeclaredBackend_IgnoresName proves that when
// the tool implements BackendProvider, inferBackend returns the
// declared backend and does not fall through to the name-based
// heuristic.  This is the contract that lets the three exec tools
// (hostexec, workspaceexec, codeexec) declare their backends without
// depending on name substring matching, and is what makes the
// heuristic unreachable for them.
//
// The fake tool's ToolName "execute_code" would, under the legacy
// heuristic, route to BackendCodeExec (because it contains "execute").
// The tool instead declares BackendHostExec; if inferBackend honors
// the interface, the result must be BackendHostExec.  If the name
// branch were reachable here, the test would fail.
func TestInferBackend_HonorsDeclaredBackend_IgnoresName(t *testing.T) {
	// "execute_code" → heuristic maps to CodeExec; declared HostExec.
	req := &tool.PermissionRequest{
		Tool:     &fakeBackendTool{declared: BackendHostExec, name: "execute_code"},
		ToolName: "execute_code",
	}
	if got := inferBackend(req); got != BackendHostExec {
		t.Errorf("inferBackend: got %q want %q (declared backend must win over name)",
			got, BackendHostExec)
	}
}

// TestInferBackend_FallbackUsedOnlyWhenBackendNotDeclared confirms the
// name-based heuristic remains available as a last resort for tools
// that do not implement BackendProvider.  This preserves backward
// compatibility for ad-hoc tools while keeping the heuristic out of
// the path for tools that declare their backend.
func TestInferBackend_FallbackUsedOnlyWhenBackendNotDeclared(t *testing.T) {
	// A plain tool with no SafetyBackend method: the heuristic applies.
	plain := &fakeNoBackendTool{name: "host_runner"}
	req := &tool.PermissionRequest{
		Tool:     plain,
		ToolName: "host_runner",
	}
	// "host_runner" contains "host" → BackendHostExec via heuristic.
	if got := inferBackend(req); got != BackendHostExec {
		t.Errorf("inferBackend fallback: got %q want %q", got, BackendHostExec)
	}
}

// TestInferBackend_FallbackNonExecNameReturnsNone proves that a tool name
// carrying no execution signal (no "host", "code", or "execute" substring)
// resolves to BackendNone, marking it as a non-execution tool.  This is
// the distinction that lets CheckToolPermission short-circuit file, search,
// and MCP tools instead of intercepting them for a missing "command".
func TestInferBackend_FallbackNonExecNameReturnsNone(t *testing.T) {
	for _, name := range []string{"file_read", "search", "mcp_weather", "list_dir"} {
		req := &tool.PermissionRequest{
			Tool:     &fakeNoBackendTool{name: name},
			ToolName: name,
		}
		if got := inferBackend(req); got != BackendNone {
			t.Errorf("inferBackend(%q): got %q want %q (non-exec name must resolve to none)",
				name, got, BackendNone)
		}
	}
}

// fakeNoBackendTool implements tool.Tool but NOT BackendProvider, so
// the name heuristic is the only signal available.
type fakeNoBackendTool struct{ name string }

func (f *fakeNoBackendTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: f.name}
}
