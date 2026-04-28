//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package todo provides a session-scoped todo-list tool that lets an LLM
// agent plan, track and report multi-step work inside a single conversation.
//
// The tool stores the current checklist as ephemeral session state and, on
// every call, returns a short "continue using the list" nudge so that the
// model is reminded to keep the plan up to date. Key design choices:
//
//   - Storage goes through session.State (the temp: prefix), so every
//     write lives with the session and disappears when the session ends.
//   - Sub-agent isolation is achieved via Invocation.Branch (each branch
//     keeps its own checklist, so parent and child agents do not step on
//     each other).
//   - Behaviour hooks (NudgeHook) let callers attach policies such as
//     verification reminders, loop detection or token budget warnings
//     without modifying the tool itself.
//
// Typical use:
//
//	todoTool := todo.New()
//	agent := llmagent.New("assistant",
//	    llmagent.WithTools([]tool.Tool{todoTool}),
//	)
//
// Public surface:
//
//   - todo.New / todo.Option — construct and configure the tool.
//   - todo.Tool — CallableTool implementation (plug into llmagent).
//   - todo.Item / todo.Status / todo.Output — structured types that
//     show up in tool-call results; frontends (e.g. AG-UI) consume
//     these directly and never need to parse raw session state.
//   - todo.GetTodos / todo.GetTodosWithPrefix — read the current list
//     from a session (server-side integrations, REST endpoints, etc).
//
// The package deliberately does NOT ship a text/ASCII pretty-printer.
// Presentation is the caller's concern: a CLI prints plain text, a web
// UI paints rich components, an AG-UI frontend renders its own cards.
// See examples/todo for a minimal terminal-style formatter.
//
// The session key layout is also NOT part of the public API: any code
// that needs to read the list should go through GetTodos.
package todo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Default configuration constants.
const (
	// DefaultToolName is the default registered name of the todo tool.
	// It is intentionally snake_case to satisfy strict LLM tool-naming
	// rules enforced by some providers (e.g. Kimi / DeepSeek require
	// ^[a-zA-Z0-9_-]+$).
	DefaultToolName = "todo_write"

	// DefaultStateKeyPrefix is the session.State key prefix used to persist
	// the current list. The final key is "temp:todos[:<branch>]" so that:
	//   - the temp: prefix marks it as session-scoped ephemeral state;
	//   - child agents isolated by Branch get their own list automatically.
	DefaultStateKeyPrefix = "temp:todos"

	// DefaultNudgeMessage is the fixed reminder returned after every update.
	// Keeping this message in the tool response (rather than only in the
	// system prompt) materially improves the model's follow-through on
	// long multi-step tasks.
	DefaultNudgeMessage = "Todos have been modified successfully. " +
		"Ensure that you continue to use the todo list to track your progress. " +
		"Please proceed with the current tasks if applicable."
)

// Status is the life-cycle state of a single todo item.
type Status string

// Valid todo statuses.
const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in_progress"
	StatusCompleted  Status = "completed"
)

// IsValid reports whether the status is one of the three accepted values.
func (s Status) IsValid() bool {
	switch s {
	case StatusPending, StatusInProgress, StatusCompleted:
		return true
	default:
		return false
	}
}

// Item is one entry in the checklist.
//
// Content and ActiveForm are both required:
//   - Content is the imperative form, used by the model for planning
//     ("Run tests", "Fix auth bug").
//   - ActiveForm is the present-continuous form, intended for UI spinners
//     or status lines ("Running tests", "Fixing auth bug").
type Item struct {
	Content    string `json:"content"    description:"Imperative description of the task, e.g. 'Run tests'"`
	ActiveForm string `json:"activeForm" description:"Present-continuous form shown while the task is running, e.g. 'Running tests'"`
	Status     Status `json:"status"     jsonschema:"enum=pending,enum=in_progress,enum=completed" description:"One of: pending | in_progress | completed"`
}

// writeInput is the LLM-facing input for a single tool call.
// The entire previous list is replaced by Todos on every call; callers do
// not send diffs.
type writeInput struct {
	Todos []Item `json:"todos" description:"The complete, updated todo list. Replaces the previous list entirely."`
}

// Output is the structured result of a tool call. It serves two
// audiences simultaneously:
//
//   - The LLM sees it (serialized as JSON) as the tool response, where
//     Message acts as a nudge to keep it on plan.
//   - External consumers (server-side code, AG-UI frontends, logs)
//     consume Todos and OldTodos directly from the tool-result event
//     without having to fetch the session store or parse StateDelta.
//
// The list itself is small and echoing it costs few tokens; in exchange
// every event consumer gets the current state inline and can render
// a diff against OldTodos when desired.
type Output struct {
	// Message is the guidance text the model sees in the tool response.
	Message string `json:"message"`
	// Todos is the checklist after this write. When the tool clears the
	// list (see WithClearOnAllDone) this is empty.
	Todos []Item `json:"todos"`
	// OldTodos is the checklist as it was before this write. It is
	// omitted on the very first call of a session and whenever no prior
	// state exists.
	OldTodos []Item `json:"oldTodos,omitempty"`
}

// Tool is a CallableTool implementation of the todo-list writer.
type Tool struct {
	opts options
}

// Ensure interface satisfaction at compile time.
var _ tool.CallableTool = (*Tool)(nil)

// New constructs a Tool with the provided options. All options are
// independent; any that is not supplied falls back to a sensible default.
func New(opts ...Option) *Tool {
	o := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	return &Tool{opts: o}
}

// Declaration implements tool.Tool.
func (t *Tool) Declaration() *tool.Declaration {
	// Build an explicit schema so that the item sub-schema, enum and
	// descriptions are stable regardless of reflection quirks.
	itemSchema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"content": {
				Type:        "string",
				Description: "Imperative description of the task, e.g. 'Run tests'.",
			},
			"activeForm": {
				Type:        "string",
				Description: "Present-continuous form shown while the task is running, e.g. 'Running tests'.",
			},
			"status": {
				Type:        "string",
				Description: "Task status. One of: pending | in_progress | completed.",
				Enum: []any{
					string(StatusPending),
					string(StatusInProgress),
					string(StatusCompleted),
				},
			},
		},
		Required: []string{"content", "activeForm", "status"},
	}

	input := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"todos": {
				Type:        "array",
				Description: "The complete, updated todo list. Replaces the previous list entirely.",
				Items:       itemSchema,
			},
		},
		Required: []string{"todos"},
	}

	output := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"message": {
				Type:        "string",
				Description: "Guidance for the next step.",
			},
			"todos": {
				Type:        "array",
				Description: "The checklist after this write.",
				Items:       itemSchema,
			},
			"oldTodos": {
				Type:        "array",
				Description: "The checklist before this write (omitted on first write).",
				Items:       itemSchema,
			},
		},
	}

	return &tool.Declaration{
		Name:         t.opts.toolName,
		Description:  t.opts.description,
		InputSchema:  input,
		OutputSchema: output,
	}
}

// Call implements tool.CallableTool.
//
// Behaviour:
//  1. Parse and validate the new list.
//  2. Resolve the storage key for the current branch.
//  3. If every item is completed, clear the list (prevents completed
//     items from piling up in context across turns).
//  4. Persist the new list on the Session's in-memory State.
//  5. Run any configured NudgeHook to append extra guidance strings.
//  6. Return {message: "...default nudge... + hook output"}.
//
// An error is returned only for malformed input; operational failures
// (missing invocation / session) degrade gracefully into a tool-side
// message so that a misconfigured agent still gets feedback from the
// model instead of a hard stop.
func (t *Tool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	in, err := decodeWriteInput(jsonArgs)
	if err != nil {
		return nil, fmt.Errorf("todo_write: %w", err)
	}

	// Resolve scope. Without an invocation we still accept the call so
	// that tests and single-shot usage work, but we report it clearly.
	inv, _ := agent.InvocationFromContext(ctx)
	branch := ""
	if inv != nil {
		branch = inv.Branch
	}
	key := stateKey(t.opts.stateKeyPrefix, branch)

	// Compute next list. All-done => clear to avoid unbounded growth.
	// Normalise the cleared list to an empty (non-nil) slice so that
	// the marshalled Output.Todos and the persisted state both emit
	// `[]` rather than `null`, matching the declared output schema.
	newTodos := in.Todos
	if t.opts.clearOnAllDone && allCompleted(newTodos) {
		newTodos = []Item{}
	}

	// Read old list (best-effort) and persist new list.
	var oldTodos []Item
	if inv != nil && inv.Session != nil {
		oldTodos, _ = readTodos(inv.Session, key)
		encoded, err := json.Marshal(newTodos)
		if err != nil {
			return nil, fmt.Errorf("todo_write: encode state: %w", err)
		}
		inv.Session.SetState(key, encoded)
	}

	// Snapshot the returned lists up front: the persisted state was
	// marshalled above, so even if a misbehaving hook mutates the
	// slices it receives, our Output and the canonical store stay in
	// sync. The contract on NudgeHook (see options.go) already says
	// hooks must be read-only; these clones are belt-and-braces so a
	// buggy hook cannot silently corrupt the tool's response.
	outputTodos := cloneItems(newTodos)
	outputOldTodos := cloneItems(oldTodos)

	// Compose message: default nudge + hooks.
	msg := t.opts.defaultNudge
	for _, hook := range t.opts.nudgeHooks {
		if hook == nil {
			continue
		}
		if extra := hook(ctx, cloneItems(oldTodos), cloneItems(in.Todos)); extra != "" {
			msg += "\n\n" + extra
		}
	}

	return Output{
		Message:  msg,
		Todos:    outputTodos,
		OldTodos: outputOldTodos,
	}, nil
}

// StateDeltaForInvocation publishes the new checklist as a session
// state delta so the session service persists it beyond the current
// invocation. The in-run SetState above keeps reads fast within a
// single Run; this method keeps the canonical store in sync so the
// list survives across turns (and shows up when external code calls
// session.Service.GetSession).
//
// It is discovered by the function-call response processor via duck
// typing and is safe to call with zero or nil arguments.
func (t *Tool) StateDeltaForInvocation(
	inv *agent.Invocation,
	_ string,
	args []byte,
	_ []byte,
) map[string][]byte {
	// The function-call response processor only reaches here after
	// Call() has already returned successfully, so args is guaranteed
	// to decode and validate. We re-check defensively and silently
	// drop the delta on mismatch: if this path ever fires it indicates
	// a framework-level bug, and corrupting the canonical session
	// store is strictly worse than losing one turn of persistence.
	in, err := decodeWriteInput(args)
	if err != nil {
		return nil
	}
	branch := ""
	if inv != nil {
		branch = inv.Branch
	}
	key := stateKey(t.opts.stateKeyPrefix, branch)

	// Mirror the clear-on-all-done normalisation performed by Call():
	// an empty (non-nil) slice so that the persisted state serialises
	// to []  - keeping the in-run SetState and the canonical store
	// byte-identical regardless of backend (inmemory, Redis, ...).
	newTodos := in.Todos
	if t.opts.clearOnAllDone && allCompleted(newTodos) {
		newTodos = []Item{}
	}
	encoded, err := json.Marshal(newTodos)
	if err != nil {
		return nil
	}
	return map[string][]byte{key: encoded}
}

// decodeWriteInput parses the raw tool arguments into a writeInput,
// rejecting structurally legal but semantically destructive shapes
// before any persistence happens.
//
// Specifically: a missing `todos` field and `{"todos": null}` both
// unmarshal to a nil slice and would otherwise round-trip through
// validateTodos as "empty list = clear all". We refuse them at the
// edge so that an upstream provider, retry middleware, or hand-rolled
// caller that accidentally drops the field cannot wipe a session's
// plan in one shot. The legitimate clear gesture is an explicit empty
// array (`{"todos": []}`); making the destructive path require an
// explicit token preserves the symmetry the JSON schema declares
// (`required: ["todos"]`, type: array) and matches how the same shape
// is enforced for any structured tool input.
//
// The helper is shared between Call() and StateDeltaForInvocation()
// so the two persistence layers cannot drift on whether to accept a
// given payload.
func decodeWriteInput(jsonArgs []byte) (writeInput, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(jsonArgs, &raw); err != nil {
		return writeInput{}, fmt.Errorf("invalid arguments: %w", err)
	}
	todosRaw, ok := raw["todos"]
	if !ok {
		return writeInput{}, fmt.Errorf(
			"todos field is required and must be an array " +
				"(use [] to clear the checklist)",
		)
	}
	if bytes.Equal(bytes.TrimSpace(todosRaw), []byte("null")) {
		return writeInput{}, fmt.Errorf(
			"todos must be an array, got null " +
				"(use [] to clear the checklist)",
		)
	}
	var todos []Item
	if err := json.Unmarshal(todosRaw, &todos); err != nil {
		return writeInput{}, fmt.Errorf("todos must be an array: %w", err)
	}
	if todos == nil {
		// json.Unmarshal of `[]` produces a non-nil empty slice;
		// any other shape that survives the checks above and still
		// yields nil is treated as the same kind of structural mishap
		// the explicit checks above are there to catch.
		todos = []Item{}
	}
	if err := validateTodos(todos); err != nil {
		return writeInput{}, err
	}
	return writeInput{Todos: todos}, nil
}

// validateTodos checks the list against the tool's structural contract.
//
// The rules enforced here are the ones a healthy agent cannot violate
// by accident; stylistic guidance ("prefer exactly one in_progress
// while actively working", item ordering, etc.) stays in the prompt.
//
// Enforced:
//
//  1. Per-item: content and activeForm are non-empty, status is one of
//     pending | in_progress | completed.
//  2. At most one item is in_progress. Two or more in_progress items
//     almost always mean the model has lost track of what it is doing;
//     zero in_progress is legal (pure planning, batch-flip interlude,
//     all-completed terminal state).
//  3. Content is unique across the list. Duplicate content is a
//     strong signal of double-tracking / copy-paste accidents.
func validateTodos(todos []Item) error {
	var inProgress int
	seen := make(map[string]int, len(todos))
	for i, it := range todos {
		if it.Content == "" {
			return fmt.Errorf("todos[%d].content must not be empty", i)
		}
		if it.ActiveForm == "" {
			return fmt.Errorf("todos[%d].activeForm must not be empty", i)
		}
		if !it.Status.IsValid() {
			return fmt.Errorf(
				"todos[%d].status %q is invalid (want pending|in_progress|completed)",
				i, it.Status,
			)
		}
		if it.Status == StatusInProgress {
			inProgress++
			if inProgress > 1 {
				return fmt.Errorf(
					"at most one item may be in_progress, got multiple (first at todos[%d])",
					i,
				)
			}
		}
		if prev, ok := seen[it.Content]; ok {
			return fmt.Errorf(
				"todos[%d].content %q duplicates todos[%d].content",
				i, it.Content, prev,
			)
		}
		seen[it.Content] = i
	}
	return nil
}

// allCompleted returns true if every item is in completed state.
// An empty slice is not treated as all-completed (nothing to clear).
func allCompleted(todos []Item) bool {
	if len(todos) == 0 {
		return false
	}
	for _, it := range todos {
		if it.Status != StatusCompleted {
			return false
		}
	}
	return true
}

// cloneItems returns a fresh []Item with the same contents as items.
// A nil input maps to a nil output so that optional-empty semantics
// ("first call, no OldTodos") continue to marshal as an omitted
// field rather than `[]`.
//
// Item has only scalar fields, so a shallow copy is sufficient to
// isolate callers from each other.
func cloneItems(items []Item) []Item {
	if items == nil {
		return nil
	}
	out := make([]Item, len(items))
	copy(out, items)
	return out
}
