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

// The notice must describe the concurrency this run actually grants: run-scoped
// limits can withhold parallelism the scheduler would otherwise allow, so an
// unconditional promise is not generally true.
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

	// An overall limit of one leaves no parallelism for an exclusive tool to
	// cost, so the exception would advertise a capability every tool is denied.
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

// A request offering one exclusive tool still needs the notice: a turn is a batch
// of calls, not of definitions, and a model calling the same function several
// times gets those calls serialized. Suppressing the notice would withhold
// exactly the guidance that keeps it from paying for that.
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

// A retry re-runs the before-model callbacks over the same request and the
// annotators run again afterwards, so the notice must describe the request as it
// stands rather than accumulate: a second pass has to land on the same prompt as
// the first.
//
// The trailing-newline cases are the ones that broke when the notice was found
// by paragraph structure: content already ending in a newline put three at the
// seam, removal stopped recognizing its own text, and every retry left the stale
// notice and added another copy. The span's edges are now marked, so the seam's
// width does not matter.
func TestParallelToolsIsIdempotent(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "no trailing newline", content: "base"},
		{name: "one trailing newline", content: "base\n"},
		{name: "trailing blank line", content: "base\n\n"},
		{name: "several paragraphs ending in a newline", content: "base\n\nmore policy\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &model.Request{
				Messages: []model.Message{model.NewSystemMessage(tt.content)},
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
			if n := strings.Count(systemContent(req), noticeStart); n != 1 {
				t.Errorf("expected exactly one notice, got %d: %q", n, systemContent(req))
			}
			if !strings.HasPrefix(systemContent(req), tt.content+noticeSeparator) {
				t.Errorf("the caller's own system content must survive, unchanged: %q", systemContent(req))
			}
		})
	}
}

// Withdrawing the notice must give the caller's system content back byte for
// byte, trailing newlines included. An earlier version trimmed them to keep the
// seam to one blank line, so a policy loaded verbatim from a file did not
// round-trip and its prompt identity changed after the annotation was withdrawn.
func TestParallelToolsRestoresCallerContentByteForByte(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "no trailing newline", content: "base"},
		{name: "one trailing newline", content: "base\n"},
		{name: "trailing blank line", content: "base\n\n"},
		{name: "several paragraphs ending in a newline", content: "base\n\nmore policy\n"},
		{name: "leading and trailing whitespace", content: "\n  base  \n\n\n"},
		{name: "empty system message", content: ""},
	}
	exclusive := map[string]tool.Tool{
		"read":  safeStubTool{name: "read"},
		"agent": unsafeStubTool{name: "agent"},
	}
	safe := map[string]tool.Tool{"read": safeStubTool{name: "read"}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &model.Request{
				Messages: []model.Message{model.NewSystemMessage(tt.content)},
				Tools:    exclusive,
			}
			// Add, withdraw, add, withdraw: every withdrawal must land on the
			// caller's bytes, and every addition on the same annotated prompt.
			processParallelTools(req)
			annotated := systemContent(req)
			if !strings.HasPrefix(annotated, tt.content+noticeSeparator) {
				t.Fatalf("the notice must follow the caller's content unchanged, got %q", annotated)
			}
			for i := 0; i < 2; i++ {
				req.Tools = safe
				processParallelTools(req)
				if len(req.Messages) != 1 {
					t.Fatalf("the caller's system message must survive withdrawal, got %+v", req.Messages)
				}
				if got := systemContent(req); got != tt.content {
					t.Fatalf("pass %d: withdrawing must restore %q exactly, got %q", i, tt.content, got)
				}
				req.Tools = exclusive
				processParallelTools(req)
				if got := systemContent(req); got != annotated {
					t.Fatalf("pass %d: re-adding must land on the same prompt:\n%q\n%q", i, annotated, got)
				}
			}
		})
	}
}

// Only a span with both edges is the annotator's. A start marker a caller left
// unterminated is not removed, since its extent is unknown.
func TestParallelToolsLeavesAnUnterminatedMarkerAlone(t *testing.T) {
	content := "base\n\n" + noticeStart + " a caller quoting the marker"
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage(content)},
		Tools:    map[string]tool.Tool{"read": safeStubTool{name: "read"}},
	}
	processParallelTools(req)
	if got := systemContent(req); got != content {
		t.Fatalf("an unterminated marker is not the annotator's to remove, got %q", got)
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

// A caller's own system content is not framework state, however it reads. An
// earlier version identified the notice by its opening words, so a system policy
// starting the same way was deleted on the first pass.
func TestParallelToolsKeepsCallerContentThatReadsLikeTheNotice(t *testing.T) {
	const callerPolicy = "Tool-call batching: preserve this caller-authored policy."

	t.Run("first pass, nothing to remove", func(t *testing.T) {
		req := &model.Request{
			Messages: []model.Message{model.NewSystemMessage(callerPolicy)},
			Tools:    map[string]tool.Tool{"read": safeStubTool{name: "read"}},
		}
		processParallelTools(req)

		if got := systemContent(req); got != callerPolicy {
			t.Fatalf("the caller's policy must survive untouched, got %q", got)
		}
	})

	t.Run("the notice is added and withdrawn around it", func(t *testing.T) {
		req := &model.Request{
			Messages: []model.Message{model.NewSystemMessage(callerPolicy)},
			Tools: map[string]tool.Tool{
				"read":  safeStubTool{name: "read"},
				"agent": unsafeStubTool{name: "agent"},
			},
		}
		processParallelTools(req)
		if got := systemContent(req); !strings.Contains(got, callerPolicy) ||
			!strings.Contains(got, noticeStart) {
			t.Fatalf("precondition: both paragraphs must be present, got %q", got)
		}

		req.Tools = map[string]tool.Tool{"read": safeStubTool{name: "read"}}
		processParallelTools(req)

		if got := systemContent(req); got != callerPolicy {
			t.Fatalf("withdrawing the notice must leave the caller's policy, got %q", got)
		}
	})
}

// A callback may prepend a system message, so on a retry the notice is no longer
// in the first one. Sweeping only the first left it in place and added another.
func TestParallelToolsRemovesTheNoticeFromALaterSystemMessage(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("base")},
		Tools: map[string]tool.Tool{
			"read":  safeStubTool{name: "read"},
			"agent": unsafeStubTool{name: "agent"},
		},
	}
	processParallelTools(req)
	if !strings.Contains(req.Messages[0].Content, "`agent`") {
		t.Fatalf("precondition: the first pass must name agent, got %q", req.Messages[0].Content)
	}

	// The retry's callback prepends its own system message and leaves only safe
	// tools behind.
	req.Messages = append(
		[]model.Message{model.NewSystemMessage("prepended by a retry callback")},
		req.Messages...,
	)
	req.Tools = map[string]tool.Tool{"read": safeStubTool{name: "read"}}
	processParallelTools(req)

	for i, msg := range req.Messages {
		if strings.Contains(msg.Content, noticeStart) {
			t.Fatalf("a stale notice survived in message %d: %q", i, msg.Content)
		}
	}
	if len(req.Messages) != 2 ||
		req.Messages[0].Content != "prepended by a retry callback" ||
		req.Messages[1].Content != "base" {
		t.Fatalf("expected both callers' messages, unchanged, got %+v", req.Messages)
	}
}

// The same shape with an exclusive tool still present: the stale notice goes and
// exactly one current notice remains, rather than two accumulating.
func TestParallelToolsDoesNotAccumulateAcrossSystemMessages(t *testing.T) {
	req := &model.Request{
		Messages: []model.Message{model.NewSystemMessage("base")},
		Tools: map[string]tool.Tool{
			"read":  safeStubTool{name: "read"},
			"agent": unsafeStubTool{name: "agent"},
		},
	}
	processParallelTools(req)

	req.Messages = append(
		[]model.Message{model.NewSystemMessage("prepended by a retry callback")},
		req.Messages...,
	)
	req.Tools = map[string]tool.Tool{
		"read":  safeStubTool{name: "read"},
		"other": unsafeStubTool{name: "other"},
	}
	processParallelTools(req)

	var notices int
	for _, msg := range req.Messages {
		notices += strings.Count(msg.Content, noticeStart)
	}
	if notices != 1 {
		t.Fatalf("expected exactly one notice across the request, got %d: %+v", notices, req.Messages)
	}
	var all strings.Builder
	for _, msg := range req.Messages {
		all.WriteString(msg.Content)
	}
	if strings.Contains(all.String(), "`agent`") {
		t.Errorf("the obsolete exclusive tool must not still be named: %q", all.String())
	}
	if !strings.Contains(all.String(), "`other`") {
		t.Errorf("the current exclusive tool must be named: %q", all.String())
	}
}
