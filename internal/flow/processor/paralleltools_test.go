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
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func processParallelTools(req *model.Request) {
	annotateParallelTools(req, tool.ConcurrencyConfig{})
}

func annotateParallelTools(req *model.Request, concurrency tool.ConcurrencyConfig) {
	NewToolBatchingNotice(concurrency).Annotate(
		context.Background(),
		&agent.Invocation{},
		req,
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
			"read":     safeStubTool{name: "read"},
			"grep":     safeStubTool{name: "grep"},
			"transfer": unsafeStubTool{name: "transfer"},
			"await":    unsafeStubTool{name: "await"},
			"write":    metadataStubTool{name: "write", safe: false},
		},
	}
	processParallelTools(req)

	got := systemContent(req)
	if !strings.HasPrefix(got, "base\n\n") {
		t.Fatalf("the existing system message must be preserved, got %q", got)
	}
	for _, want := range []string{"`await`", "`transfer`"} {
		if !strings.Contains(got, want) {
			t.Errorf("note must name %s, got %q", want, got)
		}
	}
	for _, unwanted := range []string{"`read`", "`grep`"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("note must not name the admissible tool %s, got %q", unwanted, got)
		}
	}
	// Metadata is descriptive and never objects, so a tool that merely publishes
	// ConcurrencySafe: false is not an exception the model needs to hear about —
	// the scheduler will batch it. Naming it would restrict the model for nothing.
	if strings.Contains(got, "`write`") {
		t.Errorf("note must not name a tool that only publishes metadata, got %q", got)
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

// The notice must describe the concurrency this run actually grants.
//
// Run-scoped tool concurrency limits can withhold parallelism the scheduler would
// otherwise allow, so an unconditional promise that a turn's calls run
// concurrently is not generally true.
func TestParallelToolsReflectsConcurrencyLimits(t *testing.T) {
	newRequest := func() *model.Request {
		return &model.Request{
			Messages: []model.Message{model.NewSystemMessage("base")},
			Tools: map[string]tool.Tool{
				"read":  safeStubTool{name: "read"},
				"agent": unsafeStubTool{name: "agent"},
			},
		}
	}

	t.Run("no limits promise concurrency outright", func(t *testing.T) {
		req := newRequest()
		annotateParallelTools(req, tool.ConcurrencyConfig{})
		got := systemContent(req)
		if !strings.Contains(got, noticeRunsConcurrently) {
			t.Errorf("an unlimited run should state calls run concurrently, got %q", got)
		}
		if strings.Contains(got, noticeMayRunLimited) {
			t.Errorf("an unlimited run must not mention limits, got %q", got)
		}
	})

	t.Run("an overall limit softens the claim", func(t *testing.T) {
		req := newRequest()
		annotateParallelTools(req, tool.ConcurrencyConfig{MaxConcurrency: 2})
		got := systemContent(req)
		if !strings.Contains(got, noticeMayRunLimited) {
			t.Errorf("a limited run must not promise concurrency outright, got %q", got)
		}
		if !strings.Contains(got, "`agent`") {
			t.Errorf("the exclusive tool must still be named, got %q", got)
		}
	})

	t.Run("a group limit softens the claim", func(t *testing.T) {
		req := newRequest()
		annotateParallelTools(req, tool.ConcurrencyConfig{
			Groups: []tool.ConcurrencyGroup{{ToolNames: []string{"read"}, Limit: 1}},
		})
		if got := systemContent(req); !strings.Contains(got, noticeMayRunLimited) {
			t.Errorf("a grouped run must not promise concurrency outright, got %q", got)
		}
	})

	// With an overall limit of one, nothing in the run ever runs beside anything
	// else. There is no parallelism for an exclusive tool to cost, so describing
	// the exception would only advertise a capability every tool is denied.
	t.Run("an overall limit of one says nothing", func(t *testing.T) {
		req := newRequest()
		annotateParallelTools(req, tool.ConcurrencyConfig{MaxConcurrency: 1})
		if got := systemContent(req); got != "base" {
			t.Errorf("expected an untouched prompt, got %q", got)
		}
	})

	// A group whose limit is zero is ignored by the limiter, so it must not change
	// the wording either.
	t.Run("an inert group is ignored", func(t *testing.T) {
		req := newRequest()
		annotateParallelTools(req, tool.ConcurrencyConfig{
			Groups: []tool.ConcurrencyGroup{{ToolNames: []string{"read"}}, {Limit: 3}},
		})
		if got := systemContent(req); !strings.Contains(got, noticeRunsConcurrently) {
			t.Errorf("an inert group must leave the wording unchanged, got %q", got)
		}
	})
}

// A declaration overlay hides the wrapped tool's optional interfaces, so the
// notice has to resolve the objection through tool.IsConcurrencySafe rather than
// type-asserting the wrapper it is handed.
func TestParallelToolsSeesThroughDeclarationOverlays(t *testing.T) {
	patched := itool.ApplyDeclarations(
		[]tool.Tool{unsafeStubTool{name: "agent"}},
		[]tool.Declaration{{Name: "agent", Description: "patched by a host surface"}},
	)[0]
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("base")},
		Tools: map[string]tool.Tool{
			"read":  safeStubTool{name: "read"},
			"agent": patched,
		},
	}
	processParallelTools(req)

	if got := systemContent(req); !strings.Contains(got, "`agent`") {
		t.Errorf("a patched declaration must not hide the tool from the notice, got %q", got)
	}
}
