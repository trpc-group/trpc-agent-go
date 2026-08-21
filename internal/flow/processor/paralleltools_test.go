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
			// One admissible tool has no exception to announce.
			name:  "single safe tool",
			tools: map[string]tool.Tool{"read": safeStubTool{name: "read"}},
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

// A request offering one exclusive tool still needs the notice.
//
// A turn is a batch of calls, not of definitions: a model can call the same
// function several times in one turn, and hasConcurrentBatch will correctly
// serialize those calls. Suppressing the notice because only one definition was
// offered would withhold exactly the guidance that keeps the model from paying
// for that serialization.
func TestParallelToolsNamesASoleExclusiveTool(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("base")},
		Tools:    map[string]tool.Tool{"agent": unsafeStubTool{name: "agent"}},
	}
	processParallelTools(req)

	got := systemContent(req)
	if !strings.Contains(got, "`agent`") {
		t.Errorf("a sole exclusive tool must still be named, got %q", got)
	}
}

// The same request can reach the model more than once: a model retry re-runs the
// before-model callbacks over it and the annotators run again afterwards. The
// notice must describe the request as it stands, not accumulate.
func TestParallelToolsIsIdempotent(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("base")},
		Tools: map[string]tool.Tool{
			"read":  safeStubTool{name: "read"},
			"agent": unsafeStubTool{name: "agent"},
		},
	}
	processParallelTools(req)
	once := systemContent(req)

	processParallelTools(req)
	if got := systemContent(req); got != once {
		t.Fatalf("annotating twice must not change the prompt:\n%q\n%q", once, got)
	}
	if n := strings.Count(systemContent(req), noticePrefix); n != 1 {
		t.Errorf("expected exactly one notice, got %d", n)
	}
}

// A retry that changes the tool surface must change the notice with it.
func TestParallelToolsFollowsTheToolSurface(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("base")},
		Tools: map[string]tool.Tool{
			"read":  safeStubTool{name: "read"},
			"agent": unsafeStubTool{name: "agent"},
		},
	}
	processParallelTools(req)
	if got := systemContent(req); !strings.Contains(got, "`agent`") {
		t.Fatalf("precondition: the first pass must name agent, got %q", got)
	}

	// The final retry drops tools before asking again.
	req.Tools = map[string]tool.Tool{"transfer": unsafeStubTool{name: "transfer"}}
	processParallelTools(req)

	got := systemContent(req)
	if strings.Contains(got, "`agent`") {
		t.Errorf("the notice must not name a tool the request no longer carries, got %q", got)
	}
	if !strings.Contains(got, "`transfer`") {
		t.Errorf("the notice must name the tool the request now carries, got %q", got)
	}
	if !strings.HasPrefix(got, "base\n\n") {
		t.Errorf("the caller's own system message must survive, got %q", got)
	}
}

// A retry that drops every exclusive tool must leave no notice behind.
func TestParallelToolsWithdrawsTheNotice(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("base")},
		Tools: map[string]tool.Tool{
			"read":  safeStubTool{name: "read"},
			"agent": unsafeStubTool{name: "agent"},
		},
	}
	processParallelTools(req)
	req.Tools = map[string]tool.Tool{"read": safeStubTool{name: "read"}}
	processParallelTools(req)

	if got := systemContent(req); got != "base" {
		t.Fatalf("the prompt must return to the caller's own, got %q", got)
	}
}

// The notice creates a system message when the request has none, so withdrawing
// it must take that message away rather than leave an empty one.
func TestParallelToolsRemovesTheSystemMessageItCreated(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hello")},
		Tools:    map[string]tool.Tool{"agent": unsafeStubTool{name: "agent"}},
	}
	processParallelTools(req)
	if len(req.Messages) != 2 {
		t.Fatalf("precondition: the notice must create a system message, got %d", len(req.Messages))
	}

	req.Tools = map[string]tool.Tool{"read": safeStubTool{name: "read"}}
	processParallelTools(req)

	if len(req.Messages) != 1 || req.Messages[0].Content != "hello" {
		t.Fatalf("expected only the user message to remain, got %+v", req.Messages)
	}
}
