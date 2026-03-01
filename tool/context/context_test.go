//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package context

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ctxWithSession creates a context containing an Invocation with a real Session.
func ctxWithSession(sess *session.Session) context.Context {
	inv := agent.NewInvocation()
	inv.Session = sess
	return agent.NewInvocationContext(context.Background(), inv)
}

func newTestEvent(id string) event.Event {
	return event.Event{
		Response: &model.Response{},
		ID:       id,
		Author:   "test",
	}
}

// --- delete_context tests ---

func TestDeleteContextTool(t *testing.T) {
	t.Run("masks specified events", func(t *testing.T) {
		sess := session.NewSession("app", "user", "s1")
		sess.Events = []event.Event{
			newTestEvent("e1"),
			newTestEvent("e2"),
			newTestEvent("e3"),
		}
		ctx := ctxWithSession(sess)

		tool := NewDeleteContextTool()
		result, err := tool.Call(ctx, []byte(`{"event_ids":["e1","e3"]}`))
		if err != nil {
			t.Fatal(err)
		}

		out := result.(DeleteContextOutput)
		if out.Masked != 2 {
			t.Fatalf("expected 2 masked, got %d", out.Masked)
		}

		visible := sess.GetVisibleEvents()
		if len(visible) != 1 || visible[0].ID != "e2" {
			t.Fatal("expected only e2 visible")
		}
	})

	t.Run("no session returns graceful message", func(t *testing.T) {
		ctx := context.Background()
		tool := NewDeleteContextTool()
		result, err := tool.Call(ctx, []byte(`{"event_ids":["e1"]}`))
		if err != nil {
			t.Fatal(err)
		}
		out := result.(DeleteContextOutput)
		if out.Masked != 0 {
			t.Fatal("expected 0 masked without session")
		}
	})
}

// --- check_budget tests ---

func TestCheckBudgetTool(t *testing.T) {
	t.Run("reports counts correctly", func(t *testing.T) {
		sess := session.NewSession("app", "user", "s2")
		sess.Events = []event.Event{
			newTestEvent("e1"),
			newTestEvent("e2"),
			newTestEvent("e3"),
		}
		sess.MaskEvents("e2")
		ctx := ctxWithSession(sess)

		tool := NewCheckBudgetTool()
		result, err := tool.Call(ctx, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}

		out := result.(CheckBudgetOutput)
		if out.TotalEvents != 3 {
			t.Fatalf("expected 3 total, got %d", out.TotalEvents)
		}
		if out.VisibleEvents != 2 {
			t.Fatalf("expected 2 visible, got %d", out.VisibleEvents)
		}
		if out.MaskedEvents != 1 {
			t.Fatalf("expected 1 masked, got %d", out.MaskedEvents)
		}
	})

	t.Run("no session returns zeros", func(t *testing.T) {
		tool := NewCheckBudgetTool()
		result, err := tool.Call(context.Background(), []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		out := result.(CheckBudgetOutput)
		if out.TotalEvents != 0 || out.VisibleEvents != 0 || out.MaskedEvents != 0 {
			t.Fatal("expected all zeros without session")
		}
	})

	t.Run("visible_events stays non-negative after truncation", func(t *testing.T) {
		sess := session.NewSession("app", "user", "s2-trunc")
		sess.Events = []event.Event{
			newTestEvent("e1"),
			newTestEvent("e2"),
			newTestEvent("e3"),
		}
		// Mask all three events.
		sess.MaskEvents("e1", "e2", "e3")

		// Truncate the Events slice (e.g. external event pruning).
		sess.Events = []event.Event{newTestEvent("e3")}

		ctx := ctxWithSession(sess)
		tool := NewCheckBudgetTool()
		result, err := tool.Call(ctx, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}

		out := result.(CheckBudgetOutput)
		if out.TotalEvents != 1 {
			t.Fatalf("expected 1 total, got %d", out.TotalEvents)
		}
		if out.VisibleEvents != 0 {
			t.Fatalf("expected 0 visible, got %d", out.VisibleEvents)
		}
		if out.MaskedEvents != 1 {
			t.Fatalf("expected 1 masked, got %d", out.MaskedEvents)
		}
	})
}

// --- note tests ---

func TestNoteTool(t *testing.T) {
	t.Run("stores note in session state", func(t *testing.T) {
		sess := session.NewSession("app", "user", "s3")
		ctx := ctxWithSession(sess)

		tool := NewNoteTool()
		_, err := tool.Call(ctx, []byte(`{"key":"findings","content":"LLMs are cool"}`))
		if err != nil {
			t.Fatal(err)
		}

		val, ok := sess.GetState("note:findings")
		if !ok {
			t.Fatal("expected note:findings in state")
		}
		if string(val) != "LLMs are cool" {
			t.Fatalf("unexpected value: %s", val)
		}
	})

	t.Run("overwrites existing note", func(t *testing.T) {
		sess := session.NewSession("app", "user", "s4")
		ctx := ctxWithSession(sess)

		tool := NewNoteTool()
		_, _ = tool.Call(ctx, []byte(`{"key":"plan","content":"v1"}`))
		_, _ = tool.Call(ctx, []byte(`{"key":"plan","content":"v2"}`))

		val, _ := sess.GetState("note:plan")
		if string(val) != "v2" {
			t.Fatalf("expected v2, got %s", val)
		}
	})
}

// --- read_notes tests ---

func TestReadNotesTool(t *testing.T) {
	t.Run("reads all notes", func(t *testing.T) {
		sess := session.NewSession("app", "user", "s5")
		sess.SetState("note:a", []byte("alpha"))
		sess.SetState("note:b", []byte("beta"))
		sess.SetState("other_key", []byte("ignore me"))
		ctx := ctxWithSession(sess)

		tool := NewReadNotesTool()
		result, err := tool.Call(ctx, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}

		out := result.(ReadNotesOutput)
		if out.Count != 2 {
			t.Fatalf("expected 2 notes, got %d", out.Count)
		}
		if out.Notes["a"] != "alpha" || out.Notes["b"] != "beta" {
			t.Fatalf("unexpected notes: %v", out.Notes)
		}
	})

	t.Run("empty when no notes", func(t *testing.T) {
		sess := session.NewSession("app", "user", "s6")
		ctx := ctxWithSession(sess)

		tool := NewReadNotesTool()
		result, err := tool.Call(ctx, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}

		out := result.(ReadNotesOutput)
		if out.Count != 0 {
			t.Fatalf("expected 0, got %d", out.Count)
		}
	})
}

// --- Tools() convenience ---

func TestToolsReturnsAllFour(t *testing.T) {
	tools := Tools()
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Declaration().Name] = true
	}

	for _, expected := range []string{"delete_context", "check_budget", "note", "read_notes"} {
		if !names[expected] {
			t.Fatalf("missing tool: %s", expected)
		}
	}
}

// --- noteTool StateDelta tests ---

func TestNoteTool_StateDelta(t *testing.T) {
	nt := NewNoteTool().(*noteTool)

	tests := []struct {
		name      string
		args      string
		wantKey   string
		wantValue string
		wantNil   bool
	}{
		{
			name:      "returns correct delta",
			args:      `{"key":"findings","content":"LLMs are cool"}`,
			wantKey:   "note:findings",
			wantValue: "LLMs are cool",
		},
		{
			name:    "empty key returns nil",
			args:    `{"key":"","content":"whatever"}`,
			wantNil: true,
		},
		{
			name:    "invalid JSON returns nil",
			args:    `{broken`,
			wantNil: true,
		},
		{
			name:      "empty content stores empty string",
			args:      `{"key":"empty","content":""}`,
			wantKey:   "note:empty",
			wantValue: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delta := nt.StateDelta([]byte(tt.args), nil)
			if tt.wantNil {
				if delta != nil {
					t.Fatalf("expected nil delta, got %v", delta)
				}
				return
			}
			if delta == nil {
				t.Fatal("expected non-nil delta")
			}
			val, ok := delta[tt.wantKey]
			if !ok {
				t.Fatalf("expected key %q in delta", tt.wantKey)
			}
			if string(val) != tt.wantValue {
				t.Fatalf("expected value %q, got %q", tt.wantValue, string(val))
			}
		})
	}
}
