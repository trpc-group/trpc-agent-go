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
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/noop"
)

// ctxWithSession creates a context containing an Invocation with a real Session.
func ctxWithSession(sess *session.Session) context.Context {
	inv := agent.NewInvocation()
	inv.Session = sess
	return agent.NewInvocationContext(context.Background(), inv)
}

func ctxWithSessionService(sess *session.Session, svc session.Service) context.Context {
	inv := agent.NewInvocation()
	inv.Session = sess
	inv.SessionService = svc
	return agent.NewInvocationContext(context.Background(), inv)
}

type failingNotePersistService struct {
	*noop.Service
}

func newFailingNotePersistService() session.Service {
	return &failingNotePersistService{Service: noop.NewService()}
}

func (s *failingNotePersistService) UpdateSessionState(
	context.Context,
	session.Key,
	session.StateMap,
) error {
	return errors.New("persist failed")
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

	t.Run("persists masks to session state", func(t *testing.T) {
		ctx := context.Background()
		svc := inmemory.NewSessionService()
		key := session.Key{AppName: "app", UserID: "user", SessionID: "dc-persist"}
		sess, err := svc.CreateSession(ctx, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		sess.Events = []event.Event{
			newTestEvent("e1"),
			newTestEvent("e2"),
		}
		invCtx := ctxWithSessionService(sess, svc)

		tool := NewDeleteContextTool()
		_, err = tool.Call(invCtx, []byte(`{"event_ids":["e1"]}`))
		if err != nil {
			t.Fatal(err)
		}

		raw, ok := sess.GetState(session.MaskedEventsStateKey)
		if !ok {
			t.Fatal("expected masked events persisted in session state")
		}
		reloaded := session.NewSession(key.AppName, key.UserID, key.SessionID)
		reloaded.Events = sess.Events
		reloaded.SetState(session.MaskedEventsStateKey, raw)
		visible := reloaded.GetVisibleEvents()
		if len(visible) != 1 || visible[0].ID != "e2" {
			t.Fatalf("expected hydrated mask after reload, got %v", visible)
		}
	})

	t.Run("returns error and skips state key when session service persist fails", func(t *testing.T) {
		sess := session.NewSession("app", "user", "dc-fail")
		sess.Events = []event.Event{
			newTestEvent("e1"),
			newTestEvent("e2"),
		}
		invCtx := ctxWithSessionService(sess, newFailingNotePersistService())

		tool := NewDeleteContextTool()
		_, err := tool.Call(invCtx, []byte(`{"event_ids":["e1"]}`))
		if err == nil {
			t.Fatal("expected persist error")
		}
		if _, ok := sess.GetState(session.MaskedEventsStateKey); ok {
			t.Fatal("masked events state should not be written when persist fails")
		}
		visible := sess.GetVisibleEvents()
		if len(visible) != 2 {
			t.Fatalf("expected mask rollback after persist failure, got %v", visible)
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

	t.Run("no session returns graceful message", func(t *testing.T) {
		tool := NewNoteTool()
		out, err := tool.Call(context.Background(), []byte(`{"key":"k","content":"v"}`))
		if err != nil {
			t.Fatal(err)
		}
		if out.(NoteOutput).Message != "no session available" {
			t.Fatalf("unexpected message: %v", out)
		}
	})

	t.Run("persists through session service", func(t *testing.T) {
		ctx := context.Background()
		svc := inmemory.NewSessionService()
		key := session.Key{AppName: "app", UserID: "user", SessionID: "note-persist"}
		sess, err := svc.CreateSession(ctx, key, nil)
		if err != nil {
			t.Fatal(err)
		}
		invCtx := ctxWithSessionService(sess, svc)

		tool := NewNoteTool()
		_, err = tool.Call(invCtx, []byte(`{"key":"plan","content":"saved"}`))
		if err != nil {
			t.Fatal(err)
		}

		reloaded, err := svc.GetSession(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		val, ok := reloaded.GetState("note:plan")
		if !ok || string(val) != "saved" {
			t.Fatalf("expected persisted note, got %q ok=%v", val, ok)
		}
	})

	t.Run("returns error when session service persist fails", func(t *testing.T) {
		sess := session.NewSession("app", "user", "s-note-fail")
		invCtx := ctxWithSessionService(sess, newFailingNotePersistService())

		tool := NewNoteTool()
		_, err := tool.Call(invCtx, []byte(`{"key":"k","content":"v"}`))
		if err == nil {
			t.Fatal("expected persist error")
		}
		if _, ok := sess.GetState("note:k"); ok {
			t.Fatal("note should not be stored when persist fails")
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

	t.Run("no session returns empty notes", func(t *testing.T) {
		tool := NewReadNotesTool()
		result, err := tool.Call(context.Background(), []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		out := result.(ReadNotesOutput)
		if out.Count != 0 || len(out.Notes) != 0 {
			t.Fatalf("expected empty output, got %+v", out)
		}
	})
}

// --- notes_index tests ---

func TestNotesIndexPreview(t *testing.T) {
	if got := notesIndexPreview(""); got != "" {
		t.Fatalf("expected empty for empty input, got %q", got)
	}
	if got := notesIndexPreview("   \n\t  "); got != "" {
		t.Fatalf("expected empty for whitespace-only input, got %q", got)
	}
	if got := notesIndexPreview("short"); got != "short" {
		t.Fatalf("expected unchanged short preview, got %q", got)
	}
}

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

	t.Run("truncates multi-byte runes without splitting", func(t *testing.T) {
		content := strings.Repeat("你", 100)
		sess := session.NewSession("app", "user", "ni-cjk")
		sess.SetState("note:cjk", []byte(content))
		ctx := ctxWithSession(sess)

		out := mustCallNotesIndex(t, ctx)
		if out.Count != 1 {
			t.Fatalf("expected 1 entry, got %d", out.Count)
		}
		want := strings.Repeat("你", notesIndexPreviewMaxChars) + "…"
		if out.Notes[0].Preview != want {
			t.Fatalf("preview split runes: got len=%d want len=%d", len([]rune(out.Notes[0].Preview)), len([]rune(want)))
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

// --- list_context tests ---

func TestListContextTool(t *testing.T) {
	t.Run("returns visible event IDs", func(t *testing.T) {
		sess := session.NewSession("app", "user", "lc1")
		sess.Events = []event.Event{
			newTestEvent("e1"),
			newTestEvent("e2"),
		}
		sess.MaskEvents("e1")
		ctx := ctxWithSession(sess)

		tool := NewListContextTool()
		result, err := tool.Call(ctx, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		out := result.(ListContextOutput)
		if out.Count != 1 || len(out.Events) != 1 {
			t.Fatalf("expected 1 visible event, got %+v", out)
		}
		if out.Events[0].ID != "e2" {
			t.Fatalf("expected e2, got %+v", out.Events[0])
		}
	})

	t.Run("no session returns empty list", func(t *testing.T) {
		tool := NewListContextTool()
		result, err := tool.Call(context.Background(), []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		out := result.(ListContextOutput)
		if out.Count != 0 || len(out.Events) != 0 {
			t.Fatalf("expected empty list, got %+v", out)
		}
	})

	t.Run("delta tool call and tool id populate preview", func(t *testing.T) {
		sess := session.NewSession("app", "user", "lc-delta")
		sess.Events = []event.Event{
			{
				ID:     "call-delta",
				Author: "assistant",
				Response: &model.Response{
					Choices: []model.Choice{{
						Delta: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{{
								ID: "tc-1",
								Function: model.FunctionDefinitionParam{
									Name: "lookup_weather",
								},
							}},
						},
					}},
				},
			},
			{
				ID:     "result-delta",
				Author: "tool",
				Response: &model.Response{
					Choices: []model.Choice{{
						Delta: model.Message{
							Role:   model.RoleTool,
							ToolID: "tc-1",
						},
					}},
				},
			},
		}
		ctx := ctxWithSession(sess)

		tool := NewListContextTool()
		result, err := tool.Call(ctx, []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		out := result.(ListContextOutput)
		if out.Count != 2 {
			t.Fatalf("expected 2 events, got %+v", out)
		}
		previews := map[string]string{}
		for _, e := range out.Events {
			previews[e.ID] = e.Preview
		}
		if previews["call-delta"] != "lookup_weather" {
			t.Fatalf("expected delta tool-call preview, got %q", previews["call-delta"])
		}
		if previews["result-delta"] != "tc-1" {
			t.Fatalf("expected delta tool-id preview, got %q", previews["result-delta"])
		}
	})
}

// --- Tools() convenience ---

func TestToolsReturnsAllContextTools(t *testing.T) {
	tools := Tools()
	expected := []string{
		"list_context",
		"delete_context",
		"check_budget",
		"note",
		"read_notes",
		"notes_index",
	}
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
