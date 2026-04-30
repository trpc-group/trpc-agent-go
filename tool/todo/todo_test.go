//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package todo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// newTestCtx builds a context wired with a fresh Invocation + Session for
// the given branch. Returning both lets tests inspect persisted state.
func newTestCtx(branch string) (context.Context, *session.Session) {
	sess := session.NewSession("app", "user", "sid")
	inv := &agent.Invocation{
		AgentName: "tester",
		Branch:    branch,
		Session:   sess,
	}
	ctx := agent.NewInvocationContext(context.Background(), inv)
	return ctx, sess
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestDeclaration_DefaultsAndOverrides(t *testing.T) {
	tl := New()
	d := tl.Declaration()
	if d.Name != DefaultToolName {
		t.Fatalf("default name: got %q want %q", d.Name, DefaultToolName)
	}
	if d.InputSchema == nil || d.InputSchema.Properties["todos"] == nil {
		t.Fatalf("missing todos input schema: %#v", d.InputSchema)
	}
	items := d.InputSchema.Properties["todos"]
	if items.Type != "array" || items.Items == nil {
		t.Fatalf("todos must be array with items, got %#v", items)
	}
	status := items.Items.Properties["status"]
	if status == nil || len(status.Enum) != 3 {
		t.Fatalf("status enum should have 3 values, got %#v", status)
	}

	custom := New(
		WithToolName("plan_write"),
		WithDescription("alt desc"),
	)
	cd := custom.Declaration()
	if cd.Name != "plan_write" || cd.Description != "alt desc" {
		t.Fatalf("overrides not applied: %#v", cd)
	}
}

func TestCall_WritesState_AndReturnsNudge(t *testing.T) {
	ctx, sess := newTestCtx("")
	tl := New()

	args := mustMarshal(t, writeInput{Todos: []Item{
		{Content: "Scan repo", ActiveForm: "Scanning repo", Status: StatusInProgress},
		{Content: "Write tests", ActiveForm: "Writing tests", Status: StatusPending},
	}})
	res, err := tl.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call err: %v", err)
	}
	out, ok := res.(Output)
	if !ok {
		t.Fatalf("expected Output, got %T", res)
	}
	if !strings.Contains(out.Message, "continue to use the todo list") {
		t.Fatalf("default nudge missing in %q", out.Message)
	}

	got, err := GetTodos(sess, "")
	if err != nil {
		t.Fatalf("GetTodos: %v", err)
	}
	if len(got) != 2 || got[0].Status != StatusInProgress || got[1].Content != "Write tests" {
		t.Fatalf("state did not persist correctly: %#v", got)
	}
}

func TestCall_BranchIsolation(t *testing.T) {
	sess := session.NewSession("app", "user", "sid")

	runOn := func(branch string, todos []Item) {
		inv := &agent.Invocation{Branch: branch, Session: sess}
		ctx := agent.NewInvocationContext(context.Background(), inv)
		args := mustMarshal(t, writeInput{Todos: todos})
		if _, err := New().Call(ctx, args); err != nil {
			t.Fatalf("Call(%s): %v", branch, err)
		}
	}

	runOn("", []Item{{Content: "parent task", ActiveForm: "parenting", Status: StatusPending}})
	runOn("sub", []Item{
		{Content: "child task", ActiveForm: "childing", Status: StatusInProgress},
	})

	parent, _ := GetTodos(sess, "")
	child, _ := GetTodos(sess, "sub")
	if len(parent) != 1 || parent[0].Content != "parent task" {
		t.Fatalf("parent list wrong: %#v", parent)
	}
	if len(child) != 1 || child[0].Content != "child task" {
		t.Fatalf("child list wrong: %#v", child)
	}
}

func TestCall_ClearOnAllDone(t *testing.T) {
	ctx, sess := newTestCtx("")
	tl := New()

	args := mustMarshal(t, writeInput{Todos: []Item{
		{Content: "a", ActiveForm: "Aing", Status: StatusCompleted},
		{Content: "b", ActiveForm: "Bing", Status: StatusCompleted},
	}})
	raw, err := tl.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	got, _ := GetTodos(sess, "")
	if len(got) != 0 {
		t.Fatalf("expected empty list after all-done clear, got %#v", got)
	}

	// Regression guard: the cleared list must marshal to "todos": [],
	// not "todos": null. Output.Todos has no omitempty and the
	// declared output schema says the field is an array, so emitting
	// null here would break schema-aware LLMs and AG-UI-style
	// frontends that call .length / .map directly on the field.
	out, ok := raw.(Output)
	if !ok {
		t.Fatalf("Call returned unexpected type %T", raw)
	}
	if out.Todos == nil {
		t.Fatalf("Output.Todos must be non-nil after clear, got nil")
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal Output: %v", err)
	}
	if !strings.Contains(string(encoded), `"todos":[]`) {
		t.Fatalf("expected marshalled output to contain \"todos\":[], got %s", encoded)
	}
	if strings.Contains(string(encoded), `"todos":null`) {
		t.Fatalf("output must not emit \"todos\":null, got %s", encoded)
	}

	// With clear disabled, items should be retained.
	ctx2, sess2 := newTestCtx("")
	tl2 := New(WithClearOnAllDone(false))
	if _, err := tl2.Call(ctx2, args); err != nil {
		t.Fatalf("Call: %v", err)
	}
	got2, _ := GetTodos(sess2, "")
	if len(got2) != 2 {
		t.Fatalf("expected retained list, got %#v", got2)
	}
}

// TestStateDeltaForInvocation_ClearOnAllDone guards against the
// canonical session store receiving "null" bytes when clear-on-all-done
// fires. Call() persists `[]` via SetState, and StateDeltaForInvocation
// must publish a matching delta so every backend (inmemory, Redis, ...)
// converges on the same on-disk shape.
func TestStateDeltaForInvocation_ClearOnAllDone(t *testing.T) {
	tl := New()
	inv := &agent.Invocation{AgentName: "tester", Session: session.NewSession("app", "user", "sid")}

	args, err := json.Marshal(writeInput{Todos: []Item{
		{Content: "a", ActiveForm: "Aing", Status: StatusCompleted},
		{Content: "b", ActiveForm: "Bing", Status: StatusCompleted},
	}})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	delta := tl.StateDeltaForInvocation(inv, "", args, nil)
	if delta == nil {
		t.Fatalf("expected non-nil state delta")
	}
	encoded, ok := delta[stateKey(DefaultStateKeyPrefix, "")]
	if !ok {
		t.Fatalf("state delta missing default key, got %v", delta)
	}
	if string(encoded) != `[]` {
		t.Fatalf("expected cleared state to encode as `[]`, got %q", encoded)
	}
}

func TestCall_NudgeHook(t *testing.T) {
	ctx, _ := newTestCtx("")
	called := false
	hook := func(_ context.Context, old, newT []Item) string {
		called = true
		if len(old) != 0 {
			t.Fatalf("first call old should be empty, got %d", len(old))
		}
		if len(newT) != 1 {
			t.Fatalf("newT should have 1 item, got %d", len(newT))
		}
		return "EXTRA_HINT"
	}
	tl := New(WithNudgeHook(hook))
	args := mustMarshal(t, writeInput{Todos: []Item{
		{Content: "x", ActiveForm: "Xing", Status: StatusPending},
	}})
	res, err := tl.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !called {
		t.Fatalf("hook not called")
	}
	if !strings.Contains(res.(Output).Message, "EXTRA_HINT") {
		t.Fatalf("hook output not appended: %q", res.(Output).Message)
	}
}

func TestCall_HookReceivesOldList(t *testing.T) {
	ctx, _ := newTestCtx("")
	var gotOld []Item
	hook := func(_ context.Context, old, _ []Item) string {
		gotOld = append(gotOld, old...)
		return ""
	}
	tl := New(WithNudgeHook(hook))

	// First write establishes state.
	_, err := tl.Call(ctx, mustMarshal(t, writeInput{Todos: []Item{
		{Content: "first", ActiveForm: "firsting", Status: StatusPending},
	}}))
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(gotOld) != 0 {
		t.Fatalf("first call old should be empty, got %#v", gotOld)
	}

	// Second write: hook should see the previous list.
	_, err = tl.Call(ctx, mustMarshal(t, writeInput{Todos: []Item{
		{Content: "first", ActiveForm: "firsting", Status: StatusInProgress},
	}}))
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(gotOld) != 1 || gotOld[0].Content != "first" {
		t.Fatalf("hook did not receive old list: %#v", gotOld)
	}
}

func TestCall_ValidatesInput(t *testing.T) {
	ctx, _ := newTestCtx("")
	tl := New()

	cases := []struct {
		name string
		in   writeInput
	}{
		{"empty content", writeInput{Todos: []Item{{Content: "", ActiveForm: "X", Status: StatusPending}}}},
		{"empty activeForm", writeInput{Todos: []Item{{Content: "A", ActiveForm: "", Status: StatusPending}}}},
		{"bad status", writeInput{Todos: []Item{{Content: "A", ActiveForm: "X", Status: Status("other")}}}},
		{"multiple in_progress", writeInput{Todos: []Item{
			{Content: "A", ActiveForm: "Doing A", Status: StatusInProgress},
			{Content: "B", ActiveForm: "Doing B", Status: StatusInProgress},
		}}},
		{"duplicate content", writeInput{Todos: []Item{
			{Content: "A", ActiveForm: "Doing A", Status: StatusInProgress},
			{Content: "A", ActiveForm: "Doing A again", Status: StatusPending},
		}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tl.Call(ctx, mustMarshal(t, tc.in))
			if err == nil {
				t.Fatalf("expected validation error")
			}
		})
	}
}

func TestCall_MalformedJSON(t *testing.T) {
	ctx, _ := newTestCtx("")
	tl := New()
	if _, err := tl.Call(ctx, []byte("{not json")); err == nil {
		t.Fatalf("expected json error")
	}
}

func TestCall_NoInvocationContext(t *testing.T) {
	// Without an invocation the tool must still answer (with the nudge)
	// but cannot persist state. Useful for smoke tests and SDK demos.
	tl := New()
	args := mustMarshal(t, writeInput{Todos: []Item{
		{Content: "x", ActiveForm: "X", Status: StatusPending},
	}})
	res, err := tl.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call without invocation: %v", err)
	}
	if !strings.Contains(res.(Output).Message, "continue to use the todo list") {
		t.Fatalf("expected nudge message, got %q", res.(Output).Message)
	}
}

func TestStateKey(t *testing.T) {
	cases := []struct {
		prefix, branch, want string
	}{
		{"", "", DefaultStateKeyPrefix},
		{"", "sub", DefaultStateKeyPrefix + ":sub"},
		{"custom", "", "custom"},
		{"custom", "b1", "custom:b1"},
	}
	for _, tc := range cases {
		if got := stateKey(tc.prefix, tc.branch); got != tc.want {
			t.Errorf("stateKey(%q,%q) = %q, want %q", tc.prefix, tc.branch, got, tc.want)
		}
	}
}

func TestGetTodos_NilAndEmpty(t *testing.T) {
	if items, err := GetTodos(nil, ""); err != nil || items != nil {
		t.Fatalf("nil session should return (nil,nil), got (%v,%v)", items, err)
	}
	sess := session.NewSession("a", "u", "s")
	items, err := GetTodos(sess, "")
	if err != nil || items != nil {
		t.Fatalf("empty session should return (nil,nil), got (%v,%v)", items, err)
	}
}

func TestGetTodos_CorruptState(t *testing.T) {
	sess := session.NewSession("a", "u", "s")
	sess.SetState(stateKey(DefaultStateKeyPrefix, ""), []byte("not-json"))
	if _, err := GetTodos(sess, ""); err == nil {
		t.Fatalf("expected decode error on corrupt state")
	}
}

func TestStatus_IsValid(t *testing.T) {
	for _, s := range []Status{StatusPending, StatusInProgress, StatusCompleted} {
		if !s.IsValid() {
			t.Errorf("%q should be valid", s)
		}
	}
	if Status("done").IsValid() {
		t.Errorf("'done' should be invalid")
	}
}

func TestAllCompleted(t *testing.T) {
	if allCompleted(nil) {
		t.Errorf("empty should not be all-completed")
	}
	if allCompleted([]Item{{Status: StatusCompleted}, {Status: StatusPending}}) {
		t.Errorf("mixed should not be all-completed")
	}
	if !allCompleted([]Item{{Status: StatusCompleted}, {Status: StatusCompleted}}) {
		t.Errorf("all done should return true")
	}
}

// TestCall_RejectsNullAndMissingTodos pins down the strict-input
// contract: a missing `todos` field or `{"todos": null}` payload is
// refused at the edge instead of being silently treated as "clear all".
// The destructive shape (wipe the entire checklist) must be reachable
// only via an explicit empty array, see
// TestCall_AcceptsExplicitEmptyArrayClear.
//
// Beyond the error path we also verify that the rejection is total:
// no in-run state was written, no canonical state delta is published,
// and any pre-existing list in the session is left intact.
func TestCall_RejectsNullAndMissingTodos(t *testing.T) {
	inputs := map[string][]byte{
		"null_todos":    []byte(`{"todos": null}`),
		"missing_field": []byte(`{}`),
	}
	for name, args := range inputs {
		t.Run(name, func(t *testing.T) {
			ctx, sess := newTestCtx("")
			tl := New()

			// Seed a real plan so we can prove the rejection does
			// not collateral-damage prior state.
			seedArgs := mustMarshal(t, writeInput{Todos: []Item{
				{Content: "Keep me", ActiveForm: "Keeping me", Status: StatusInProgress},
			}})
			if _, err := tl.Call(ctx, seedArgs); err != nil {
				t.Fatalf("seed Call: %v", err)
			}
			key := stateKey(DefaultStateKeyPrefix, "")
			before, ok := sess.GetState(key)
			if !ok {
				t.Fatalf("seed state missing at %q", key)
			}

			res, err := tl.Call(ctx, args)
			if err == nil {
				t.Fatalf("Call(%s) must reject, got Output=%#v", name, res)
			}
			if !strings.Contains(err.Error(), "todos") {
				t.Fatalf("error must mention todos field, got %v", err)
			}

			after, ok := sess.GetState(key)
			if !ok {
				t.Fatalf("session state vanished after rejection")
			}
			if string(after) != string(before) {
				t.Fatalf("session state mutated by a rejected call:\n  before=%s\n  after =%s",
					before, after)
			}

			// The canonical state delta must also be empty: a
			// rejected call must not publish anything.
			inv, _ := agent.InvocationFromContext(ctx)
			if delta := tl.StateDeltaForInvocation(inv, "", args, nil); delta != nil {
				t.Fatalf("StateDeltaForInvocation must drop rejected input, got %v", delta)
			}
		})
	}
}

// TestCall_AcceptsExplicitEmptyArrayClear documents the only
// supported clear gesture: an explicit empty array. Compared with
// TestCall_RejectsNullAndMissingTodos this is the symmetric "yes"
// case — the destructive operation is reachable, but only when the
// caller spells it out.
func TestCall_AcceptsExplicitEmptyArrayClear(t *testing.T) {
	ctx, sess := newTestCtx("")
	tl := New()

	// Seed a real plan first.
	seedArgs := mustMarshal(t, writeInput{Todos: []Item{
		{Content: "First", ActiveForm: "Doing first", Status: StatusInProgress},
	}})
	if _, err := tl.Call(ctx, seedArgs); err != nil {
		t.Fatalf("seed Call: %v", err)
	}

	res, err := tl.Call(ctx, []byte(`{"todos": []}`))
	if err != nil {
		t.Fatalf("Call with explicit []: %v", err)
	}
	out, ok := res.(Output)
	if !ok {
		t.Fatalf("unexpected result type: %T", res)
	}
	if out.Todos == nil {
		t.Fatalf("Output.Todos must be non-nil empty, got nil")
	}
	if len(out.Todos) != 0 {
		t.Fatalf("Output.Todos must be empty, got %#v", out.Todos)
	}
	if len(out.OldTodos) != 1 || out.OldTodos[0].Content != "First" {
		t.Fatalf("Output.OldTodos should reflect prior state, got %#v", out.OldTodos)
	}

	blob, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal Output: %v", err)
	}
	if !strings.Contains(string(blob), `"todos":[]`) {
		t.Fatalf("Output must serialise todos as [], got %s", blob)
	}
	if strings.Contains(string(blob), `"todos":null`) {
		t.Fatalf("Output must not serialise todos as null: %s", blob)
	}

	key := stateKey(DefaultStateKeyPrefix, "")
	raw, ok := sess.GetState(key)
	if !ok {
		t.Fatalf("expected persisted state at %q", key)
	}
	if string(raw) != "[]" {
		t.Fatalf("persisted state = %q, want %q", raw, "[]")
	}

	inv, _ := agent.InvocationFromContext(ctx)
	delta := tl.StateDeltaForInvocation(inv, "", []byte(`{"todos": []}`), nil)
	deltaRaw, ok := delta[key]
	if !ok {
		t.Fatalf("StateDeltaForInvocation missing key %q", key)
	}
	if string(deltaRaw) != "[]" {
		t.Fatalf("state delta = %q, want %q", deltaRaw, "[]")
	}
}

// TestCall_HookMutationsDoNotCorruptSnapshot verifies that a
// misbehaving NudgeHook cannot affect the returned Output or the
// persisted state. Hooks declare themselves read-only (see
// options.go), but the framework defends against contract violations
// via shallow clones so a buggy hook only hurts itself.
func TestCall_HookMutationsDoNotCorruptSnapshot(t *testing.T) {
	ctx, sess := newTestCtx("")

	// Seed an initial list so OldTodos is non-empty on the next call.
	seedTool := New()
	_, err := seedTool.Call(ctx, mustMarshal(t, writeInput{Todos: []Item{
		{Content: "Alpha", ActiveForm: "Doing Alpha", Status: StatusPending},
	}}))
	if err != nil {
		t.Fatalf("seed Call: %v", err)
	}

	tl := New(WithNudgeHook(func(_ context.Context, oldTodos, submitted []Item) string {
		// Malicious hook: clobber both slices in place.
		for i := range oldTodos {
			oldTodos[i].Content = "MUTATED_OLD"
		}
		for i := range submitted {
			submitted[i].Content = "MUTATED_NEW"
		}
		return "ack"
	}))

	args := mustMarshal(t, writeInput{Todos: []Item{
		{Content: "Beta", ActiveForm: "Doing Beta", Status: StatusInProgress},
	}})
	res, err := tl.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	out := res.(Output)

	if len(out.Todos) != 1 || out.Todos[0].Content != "Beta" {
		t.Fatalf("Output.Todos corrupted by hook: %#v", out.Todos)
	}
	if len(out.OldTodos) != 1 || out.OldTodos[0].Content != "Alpha" {
		t.Fatalf("Output.OldTodos corrupted by hook: %#v", out.OldTodos)
	}

	// Persisted state must also still reflect the submitted "Beta",
	// since the clone happens before any hook runs.
	key := stateKey(DefaultStateKeyPrefix, "")
	raw, _ := sess.GetState(key)
	var persisted []Item
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("decode persisted state: %v", err)
	}
	if len(persisted) != 1 || persisted[0].Content != "Beta" {
		t.Fatalf("persisted state corrupted by hook: %#v", persisted)
	}
}

func TestWithNudgeMessage_Empty(t *testing.T) {
	ctx, _ := newTestCtx("")
	// Disable default nudge; hook provides the only text.
	tl := New(
		WithNudgeMessage(""),
		WithNudgeHook(func(_ context.Context, _, _ []Item) string { return "ONLY_HOOK" }),
	)
	args := mustMarshal(t, writeInput{Todos: []Item{
		{Content: "x", ActiveForm: "X", Status: StatusPending},
	}})
	res, err := tl.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	msg := res.(Output).Message
	if strings.Contains(msg, "continue to use the todo list") {
		t.Fatalf("default nudge should be disabled, got %q", msg)
	}
	if !strings.Contains(msg, "ONLY_HOOK") {
		t.Fatalf("hook output missing: %q", msg)
	}
}
