//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package context provides tools for LLM self-context management.
//
// These tools implement the Pensieve paradigm (arXiv:2602.12108), enabling
// language models to actively manage their own context window. Instead of
// relying on external truncation, the model can:
//   - Prune processed context via delete_context
//   - Check remaining budget via check_budget
//   - Maintain persistent notes via note / read_notes
package context

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// --- delete_context tool ---

// DeleteContextInput is the input for the delete_context tool.
type DeleteContextInput struct {
	// EventIDs is the list of event IDs to mask (hide) from visible context.
	EventIDs []string `json:"event_ids" jsonschema:"description=IDs of events to remove from visible context,required"`
}

// DeleteContextOutput is the output for the delete_context tool.
type DeleteContextOutput struct {
	Masked  int    `json:"masked"`
	Message string `json:"message"`
}

// sessionFromContext retrieves the session from the invocation context.
// Returns nil if not available.
func sessionFromContext(ctx context.Context) *session.Session {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil
	}
	return inv.Session
}

// NewDeleteContextTool creates a tool that allows the LLM to prune specific
// events from its visible context. Events are soft-masked (hidden from view
// but preserved for audit). This is the Pensieve paradigm's "deleteContext".
func NewDeleteContextTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, input DeleteContextInput) (DeleteContextOutput, error) {
			sess := sessionFromContext(ctx)
			if sess == nil {
				return DeleteContextOutput{
					Message: "no session available",
				}, nil
			}

			masked := sess.MaskEvents(input.EventIDs...)
			return DeleteContextOutput{
				Masked:  masked,
				Message: fmt.Sprintf("masked %d events from context", masked),
			}, nil
		},
		function.WithName("delete_context"),
		function.WithDescription(
			"Remove specific events from your visible context to free up space. "+
				"Events are soft-hidden (preserved for audit) but no longer sent to the LLM. "+
				"Use this after extracting key information into notes to reduce context pressure. "+
				"Pass the event IDs you want to hide.",
		),
	)
}

// --- check_budget tool ---

// CheckBudgetInput is the input for the check_budget tool (empty — no args needed).
type CheckBudgetInput struct{}

// CheckBudgetOutput is the output for the check_budget tool.
type CheckBudgetOutput struct {
	TotalEvents   int `json:"total_events"`
	VisibleEvents int `json:"visible_events"`
	MaskedEvents  int `json:"masked_events"`
}

// NewCheckBudgetTool creates a tool that reports the current context budget.
// The LLM can query this to decide when to prune context.
func NewCheckBudgetTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, _ CheckBudgetInput) (CheckBudgetOutput, error) {
			sess := sessionFromContext(ctx)
			if sess == nil {
				return CheckBudgetOutput{}, nil
			}

			total := sess.GetEventCount()
			visible := len(sess.GetVisibleEvents())

			return CheckBudgetOutput{
				TotalEvents:   total,
				VisibleEvents: visible,
				MaskedEvents:  total - visible,
			}, nil
		},
		function.WithName("check_budget"),
		function.WithDescription(
			"Check how much context budget remains. Returns the total number of events, "+
				"visible events (sent to LLM), and masked events (hidden). "+
				"Use this proactively to decide when to prune context via delete_context.",
		),
	)
}

// --- note / read_notes tools ---

const noteKeyPrefix = "note:"

// NoteInput is the input for the note tool.
type NoteInput struct {
	Key     string `json:"key" jsonschema:"description=Short key name for the note (e.g. 'findings' or 'plan'),required"`
	Content string `json:"content" jsonschema:"description=The content to store. Overwrites any existing note with this key.,required"`
}

// NoteOutput is the output for the note tool.
type NoteOutput struct {
	Message string `json:"message"`
}

// NewNoteTool creates a tool that writes a persistent note to session state.
// Notes survive context pruning (delete_context) — they are stored in session
// state, not in the event stream. Use this to distill key information before
// pruning the raw context that contained it.
func NewNoteTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, input NoteInput) (NoteOutput, error) {
			sess := sessionFromContext(ctx)
			if sess == nil {
				return NoteOutput{Message: "no session available"}, nil
			}

			sess.SetState(noteKeyPrefix+input.Key, []byte(input.Content))
			return NoteOutput{
				Message: fmt.Sprintf("note '%s' saved (%d bytes)", input.Key, len(input.Content)),
			}, nil
		},
		function.WithName("note"),
		function.WithDescription(
			"Save a persistent note that survives context pruning. "+
				"Use this to distill key findings, plans, or intermediate results "+
				"before removing raw context via delete_context. "+
				"Notes are stored by key and can be overwritten.",
		),
	)
}

// ReadNotesInput is the input for the read_notes tool.
type ReadNotesInput struct{}

// ReadNotesOutput is the output for the read_notes tool.
type ReadNotesOutput struct {
	Notes map[string]string `json:"notes"`
	Count int               `json:"count"`
}

// NewReadNotesTool creates a tool that lists all persistent notes.
// The LLM uses this to recall distilled information after pruning context.
func NewReadNotesTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, _ ReadNotesInput) (ReadNotesOutput, error) {
			sess := sessionFromContext(ctx)
			if sess == nil {
				return ReadNotesOutput{Notes: map[string]string{}}, nil
			}

			snapshot := sess.SnapshotState()
			notes := make(map[string]string)
			for k, v := range snapshot {
				if strings.HasPrefix(k, noteKeyPrefix) {
					notes[strings.TrimPrefix(k, noteKeyPrefix)] = string(v)
				}
			}

			return ReadNotesOutput{
				Notes: notes,
				Count: len(notes),
			}, nil
		},
		function.WithName("read_notes"),
		function.WithDescription(
			"Read all persistent notes previously saved via the note tool. "+
				"Returns a map of key→content. Use this to recall distilled "+
				"information after pruning raw context.",
		),
	)
}

// Tools returns all context management tools as a convenience.
func Tools() []tool.Tool {
	return []tool.Tool{
		NewDeleteContextTool(),
		NewCheckBudgetTool(),
		NewNoteTool(),
		NewReadNotesTool(),
	}
}
