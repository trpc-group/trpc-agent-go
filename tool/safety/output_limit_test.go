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
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type fakeExecResult struct {
	Status   string `json:"status"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

func TestOutputLimitCallback(t *testing.T) {
	const maxBytes = 200
	p := DefaultPolicy()
	p.Limits.MaxOutputBytes = maxBytes
	pol := NewPermissionPolicy(NewScanner(p))
	cb := pol.OutputLimitCallback()
	ctx := context.Background()

	// Exec tool with oversized output -> truncated, marked, and within the cap.
	big := strings.Repeat("A", 1000)
	res, err := cb(ctx, &tool.AfterToolArgs{
		ToolName: "workspace_exec",
		Result:   fakeExecResult{Status: "exited", ExitCode: 7, Output: big},
	})
	if err != nil {
		t.Fatalf("callback error: %v", err)
	}
	if res == nil || res.CustomResult == nil {
		t.Fatalf("expected oversized output to be truncated")
	}
	m, ok := res.CustomResult.(map[string]any)
	if !ok {
		t.Fatalf("custom result is not a map: %T", res.CustomResult)
	}
	if m["output_truncated"] != true {
		t.Errorf("expected output_truncated=true, got %v", m["output_truncated"])
	}
	out := m["output"].(string)
	if int64(len(out)) > maxBytes {
		t.Errorf("truncated output is %d bytes, exceeds cap %d", len(out), maxBytes)
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("expected a truncation marker, got %q", out)
	}
	if content := strings.SplitN(out, "\n...[truncated", 2)[0]; strings.Trim(content, "A") != "" {
		t.Errorf("content before marker should be the original output, got %q", content)
	}
	if m["status"] != "exited" {
		t.Errorf("other fields must be preserved, got status=%v", m["status"])
	}
	// UseNumber keeps exit_code an exact number (not a widened float64).
	if n, ok := m["exit_code"].(json.Number); !ok || n.String() != "7" {
		t.Errorf("exit_code should round-trip exactly as json.Number 7, got %#v", m["exit_code"])
	}

	// Output within the cap -> untouched (nil result).
	if small, _ := cb(ctx, &tool.AfterToolArgs{ToolName: "workspace_exec", Result: fakeExecResult{Output: "short"}}); small != nil {
		t.Errorf("output within cap should not be modified")
	}

	// Non-exec tool -> untouched even if large.
	if other, _ := cb(ctx, &tool.AfterToolArgs{ToolName: "read_file", Result: fakeExecResult{Output: big}}); other != nil {
		t.Errorf("non-exec tool output must not be limited")
	}

	// Prefixed exec tool name is still bounded.
	if pfx, _ := cb(ctx, &tool.AfterToolArgs{ToolName: "hostexec_exec_command", Result: fakeExecResult{Output: big}}); pfx == nil || pfx.CustomResult == nil {
		t.Errorf("prefixed exec tool output should be bounded")
	}
}

func TestOutputLimitDisabledWhenNegative(t *testing.T) {
	p := DefaultPolicy()
	p.Limits.MaxOutputBytes = -1 // negative disables; zero would default to 1 MiB
	cb := NewPermissionPolicy(NewScanner(p)).OutputLimitCallback()
	r, _ := cb(context.Background(), &tool.AfterToolArgs{
		ToolName: "workspace_exec",
		Result:   fakeExecResult{Output: strings.Repeat("A", 1000)},
	})
	if r != nil {
		t.Errorf("negative MaxOutputBytes should disable the cap")
	}
}

func TestTruncateUTF8RuneBoundary(t *testing.T) {
	s := strings.Repeat("世", 10) // 3 bytes each
	got := truncateUTF8(s, 7)    // 7 bytes -> 2 full runes (6 bytes)
	if !utf8.ValidString(got) {
		t.Errorf("truncateUTF8 split a rune: %q", got)
	}
	if len([]rune(got)) != 2 {
		t.Errorf("expected 2 runes, got %d (%q)", len([]rune(got)), got)
	}
}
