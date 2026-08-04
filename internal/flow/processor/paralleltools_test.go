//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func processParallelTools(req *model.Request) {
	NewParallelToolsRequestProcessor().ProcessRequest(
		context.Background(),
		&agent.Invocation{},
		req,
		nil,
	)
}

func systemContent(req *model.Request) string {
	idx := findSystemMessageIndex(req.Messages)
	if idx < 0 {
		return ""
	}
	return req.Messages[idx].Content
}

func TestParallelToolsNamesExclusiveTools(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("base")},
		Tools: map[string]tool.Tool{
			"read":  safeStubTool{name: "read"},
			"grep":  safeStubTool{name: "grep"},
			"agent": unsafeStubTool{name: "agent"},
			"write": metadataStubTool{name: "write", safe: false},
		},
	}
	processParallelTools(req)

	got := systemContent(req)
	if !strings.HasPrefix(got, "base\n\n") {
		t.Fatalf("the existing system message must be preserved, got %q", got)
	}
	for _, want := range []string{"`agent`", "`write`"} {
		if !strings.Contains(got, want) {
			t.Errorf("note must name %s, got %q", want, got)
		}
	}
	for _, unwanted := range []string{"`read`", "`grep`"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("note must not name the safe tool %s, got %q", unwanted, got)
		}
	}
}

// The note joins the run's cached prompt prefix, so it must be byte-identical
// across requests. Map iteration order is not, which is why the names are
// sorted.
func TestParallelToolsNoteIsStable(t *testing.T) {
	tools := map[string]tool.Tool{
		"zeta":  unsafeStubTool{name: "zeta"},
		"alpha": unsafeStubTool{name: "alpha"},
		"mid":   unsafeStubTool{name: "mid"},
		"safe":  safeStubTool{name: "safe"},
	}
	var first string
	for i := 0; i < 20; i++ {
		req := &model.Request{
			Messages: []model.Message{model.NewSystemMessage("base")},
			Tools:    tools,
		}
		processParallelTools(req)
		got := systemContent(req)
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("note is not stable across requests:\n%q\n%q", first, got)
		}
	}
	if !strings.Contains(first, "`alpha`, `mid`, `zeta`") {
		t.Errorf("names must be sorted, got %q", first)
	}
}

func TestParallelToolsStaysSilent(t *testing.T) {
	tests := []struct {
		name  string
		tools map[string]tool.Tool
	}{
		{
			// The common case: nobody publishes metadata, so the prompt is untouched.
			name: "every tool is safe",
			tools: map[string]tool.Tool{
				"read": safeStubTool{name: "read"},
				"grep": safeStubTool{name: "grep"},
			},
		},
		{
			// One tool has nothing to be batched with.
			name:  "single tool",
			tools: map[string]tool.Tool{"agent": unsafeStubTool{name: "agent"}},
		},
		{
			name:  "no tools",
			tools: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &model.Request{
				Messages: []model.Message{model.NewSystemMessage("base")},
				Tools:    tt.tools,
			}
			processParallelTools(req)
			if got := systemContent(req); got != "base" {
				t.Fatalf("expected an untouched prompt, got %q", got)
			}
		})
	}
}

func TestParallelToolsCreatesSystemMessage(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
		Tools: map[string]tool.Tool{
			"read":  safeStubTool{name: "read"},
			"agent": unsafeStubTool{name: "agent"},
		},
	}
	processParallelTools(req)

	if len(req.Messages) != 2 {
		t.Fatalf("expected a system message to be added, got %d messages", len(req.Messages))
	}
	if req.Messages[0].Role != model.RoleSystem {
		t.Errorf("the system message must come first, got role %q", req.Messages[0].Role)
	}
	if req.Messages[1].Content != "hello" {
		t.Errorf("the user message must be preserved, got %q", req.Messages[1].Content)
	}
}

func TestParallelToolsNilRequest(t *testing.T) {
	processParallelTools(nil)
}
