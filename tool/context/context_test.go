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
	"strings"
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

// --- notes_index tests ---

func TestNotesIndexTool(t *testing.T) {
	t.Run("returns keys, sizes, and previews in deterministic order", func(t *testing.T) {
		sess := session.NewSession("app", "user", "ni1")
		sess.SetState("note:plan", []byte("step 1 do thing\nstep 2 do other thing"))
		sess.SetState("note:findings", []byte("the answer is 42"))
		// Non-note state must be ignored so the index stays focused on notes.
		sess.SetState("non_note_key", []byte("ignore me"))
		ctx := ctxWithSession(sess)

		tool := NewNotesIndexTool()
		result, err := tool.Call(ctx, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		out := result.(NotesIndexOutput)

		if out.Count != 2 {
			t.Fatalf("expected 2 entries, got %d", out.Count)
		}
		// Keys are sorted alphabetically so consumers can rely on order.
		if out.Notes[0].Key != "findings" || out.Notes[1].Key != "plan" {
			t.Fatalf("unexpected key order: %v", out.Notes)
		}
		if out.Notes[0].Bytes != len("the answer is 42") {
			t.Fatalf("unexpected bytes for findings: %d", out.Notes[0].Bytes)
		}
		if out.Notes[1].Preview == "" {
			t.Fatal("expected non-empty preview for plan")
		}
		// Whitespace runs should collapse so previews stay readable.
		if got := out.Notes[1].Preview; got != "step 1 do thing step 2 do other thing" {
			t.Fatalf("preview not normalised: %q", got)
		}
		expectedTotal := len("step 1 do thing\nstep 2 do other thing") + len("the answer is 42")
		if out.TotalBytes != expectedTotal {
			t.Fatalf("unexpected total bytes: %d (want %d)", out.TotalBytes, expectedTotal)
		}
	})

	t.Run("truncates long previews with ellipsis", func(t *testing.T) {
		// 200 'a's far exceeds notesIndexPreviewMaxChars (80).
		long := make([]byte, 200)
		for i := range long {
			long[i] = 'a'
		}
		sess := session.NewSession("app", "user", "ni2")
		sess.SetState("note:big", long)
		ctx := ctxWithSession(sess)

		out := mustCallNotesIndex(t, ctx)
		if out.Count != 1 {
			t.Fatalf("expected 1 entry, got %d", out.Count)
		}
		entry := out.Notes[0]
		if entry.Bytes != 200 {
			t.Fatalf("expected bytes=200, got %d", entry.Bytes)
		}
		// 80 chars of payload + the ellipsis rune.
		if entry.Preview != strings.Repeat("a", notesIndexPreviewMaxChars)+"…" {
			t.Fatalf("preview not truncated as expected: %q", entry.Preview)
		}
	})

	t.Run("empty when no notes are stored", func(t *testing.T) {
		sess := session.NewSession("app", "user", "ni3")
		ctx := ctxWithSession(sess)

		out := mustCallNotesIndex(t, ctx)
		if out.Count != 0 || out.TotalBytes != 0 || len(out.Notes) != 0 {
			t.Fatalf("expected empty index, got %+v", out)
		}
	})

	t.Run("no session returns graceful empty result", func(t *testing.T) {
		out := mustCallNotesIndex(t, context.Background())
		if out.Count != 0 || len(out.Notes) != 0 {
			t.Fatalf("expected empty index without session, got %+v", out)
		}
	})
}

// mustCallNotesIndex invokes notes_index with empty args and fails the test
// on any error. Keeps the individual notes_index sub-tests focused on the
// behaviour they're asserting rather than on plumbing.
func mustCallNotesIndex(t *testing.T, ctx context.Context) NotesIndexOutput {
	t.Helper()
	tool := NewNotesIndexTool()
	result, err := tool.Call(ctx, []byte(`{}`))
	if err != nil {
		t.Fatalf("notes_index call failed: %v", err)
	}
	return result.(NotesIndexOutput)
}

// --- Tools() convenience ---

func TestToolsReturnsAllContextTools(t *testing.T) {
	tools := Tools()
	expected := []string{"delete_context", "check_budget", "note", "read_notes", "notes_index"}
	if len(tools) != len(expected) {
		t.Fatalf("expected %d tools, got %d", len(expected), len(tools))
	}

	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Declaration().Name] = true
	}

	for _, name := range expected {
		if !names[name] {
			t.Fatalf("missing tool: %s", name)
		}
	}
}
