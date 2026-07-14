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
	"strings"
	"testing"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type fakeExecResult struct {
	Status string `json:"status"`
	Output string `json:"output"`
}

func TestOutputLimitCallback(t *testing.T) {
	p := DefaultPolicy()
	p.Limits.MaxOutputBytes = 16
	pol := NewPermissionPolicy(NewScanner(p))
	cb := pol.OutputLimitCallback()
	ctx := context.Background()

	// Exec tool with oversized output -> truncated + marked.
	big := strings.Repeat("A", 1000)
	res, err := cb(ctx, &tool.AfterToolArgs{
		ToolName: "workspace_exec",
		Result:   fakeExecResult{Status: "exited", Output: big},
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
	if !strings.HasPrefix(out, strings.Repeat("A", 16)) || !strings.Contains(out, "truncated") {
		t.Errorf("unexpected truncated output: %q", out)
	}
	if m["status"] != "exited" {
		t.Errorf("other fields must be preserved, got status=%v", m["status"])
	}

	// Output within the cap -> untouched (nil result).
	small, _ := cb(ctx, &tool.AfterToolArgs{ToolName: "workspace_exec", Result: fakeExecResult{Output: "short"}})
	if small != nil {
		t.Errorf("output within cap should not be modified")
	}

	// Non-exec tool -> untouched even if large.
	other, _ := cb(ctx, &tool.AfterToolArgs{ToolName: "read_file", Result: fakeExecResult{Output: big}})
	if other != nil {
		t.Errorf("non-exec tool output must not be limited")
	}

	// Prefixed exec tool name is still bounded.
	pfx, _ := cb(ctx, &tool.AfterToolArgs{ToolName: "hostexec_exec_command", Result: fakeExecResult{Output: big}})
	if pfx == nil || pfx.CustomResult == nil {
		t.Errorf("prefixed exec tool output should be bounded")
	}
}

func TestOutputLimitDisabledWhenZero(t *testing.T) {
	p := DefaultPolicy()
	p.Limits.MaxOutputBytes = 0 // disabled
	cb := NewPermissionPolicy(NewScanner(p)).OutputLimitCallback()
	r, _ := cb(context.Background(), &tool.AfterToolArgs{
		ToolName: "workspace_exec",
		Result:   fakeExecResult{Output: strings.Repeat("A", 1000)},
	})
	if r != nil {
		t.Errorf("zero MaxOutputBytes should disable the cap")
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
