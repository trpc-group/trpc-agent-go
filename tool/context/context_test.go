//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
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

	t.Run("ignores non-existent event IDs", func(t *testing.T) {
		sess := session.NewSession("app", "user", "s1b")
		sess.Events = []event.Event{
			newTestEvent("e1"),
		}
		ctx := ctxWithSession(sess)

		tool := NewDeleteContextTool()
		result, err := tool.Call(ctx, []byte(`{"event_ids":["e1","ghost"]}`))
		if err != nil {
			t.Fatal(err)
		}

		out := result.(DeleteContextOutput)
		if out.Masked != 1 {
			t.Fatalf("expected 1 masked, got %d", out.Masked)
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
		if out.Message != "no session available" {
			t.Fatalf("unexpected message: %s", out.Message)
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
		sess.MaskEvents([]string{"e2"})
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

	t.Run("after mask and unmask", func(t *testing.T) {
		sess := session.NewSession("app", "user", "s2b")
		sess.Events = []event.Event{
			newTestEvent("e1"),
			newTestEvent("e2"),
			newTestEvent("e3"),
			newTestEvent("e4"),
		}
		sess.MaskEvents([]string{"e1", "e2", "e3"})
		sess.UnmaskEvents([]string{"e1"})
		ctx := ctxWithSession(sess)

		tool := NewCheckBudgetTool()
		result, err := tool.Call(ctx, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}

		out := result.(CheckBudgetOutput)
		if out.TotalEvents != 4 {
			t.Fatalf("expected 4 total, got %d", out.TotalEvents)
		}
		if out.MaskedEvents != 2 {
			t.Fatalf("expected 2 masked, got %d", out.MaskedEvents)
		}
		if out.VisibleEvents != 2 {
			t.Fatalf("expected 2 visible, got %d", out.VisibleEvents)
		}
	})
}

// --- Tools() convenience ---

func TestToolsReturnsBoth(t *testing.T) {
	tools := Tools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}

	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Declaration().Name] = true
	}

	for _, expected := range []string{"delete_context", "check_budget"} {
		if !names[expected] {
			t.Fatalf("missing tool: %s", expected)
		}
	}
}
