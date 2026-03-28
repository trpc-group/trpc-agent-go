//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package context provides Pensieve-style context management tools that allow
// LLMs to prune their own visible context. These tools implement the self-pruning
// paradigm described in https://arxiv.org/abs/2602.12108.
package context

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	DeleteContextTool = "context_delete_context"
	CheckBudgetTool   = "context_check_budget"
)

const noteKeyPrefix = "note:"

// DeleteContextInput is the input for the delete_context tool.
type DeleteContextInput struct {
	EventIDs []string `json:"event_ids" jsonschema:"description=IDs of events to remove from visible context,required"`
}

// DeleteContextOutput is the output for the delete_context tool.
type DeleteContextOutput struct {
	Masked  int    `json:"masked"`
	Message string `json:"message"`
}

// CheckBudgetInput is the input for the check_budget tool (empty — no args needed).
type CheckBudgetInput struct{}

// CheckBudgetOutput is the output for the check_budget tool.
type CheckBudgetOutput struct {
	TotalEvents   int `json:"total_events"`
	VisibleEvents int `json:"visible_events"`
	MaskedEvents  int `json:"masked_events"`
}

// sessionFromContext retrieves the session from the invocation context.
// Returns nil if not available.
func sessionFromContext(ctx context.Context) *session.Session {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		fmt.Printf("[DEBUG sessionFromContext] inv_found=%v inv_nil=%v\n", ok, inv == nil)
		return nil
	}
	fmt.Printf("[DEBUG sessionFromContext] session_nil=%v session_id=%s\n", inv.Session == nil, func() string {
		if inv.Session != nil {
			return inv.Session.ID
		}
		return "<nil>"
	}())
	return inv.Session
}

// NewDeleteContextTool creates a tool that allows the LLM to prune specific
// events from its visible context.
func NewDeleteContextTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, input DeleteContextInput) (DeleteContextOutput, error) {
			sess := sessionFromContext(ctx)
			if sess == nil {
				return DeleteContextOutput{
					Message: "no session available",
				}, nil
			}

			masked := sess.MaskEvents(input.EventIDs)
			return DeleteContextOutput{
				Masked:  masked,
				Message: fmt.Sprintf("masked %d events from context", masked),
			}, nil
		},
		function.WithName(DeleteContextTool),
		function.WithDescription(
			"Remove specific events from your visible context to free up space. "+
				"Events are soft-hidden (preserved for audit) but no longer sent to the LLM. "+
				"Use this after extracting key information into notes to reduce context pressure. "+
				"Pass the event IDs you want to hide.",
		),
	)
}

// NewCheckBudgetTool creates a tool that reports the current context budget.
func NewCheckBudgetTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, _ CheckBudgetInput) (CheckBudgetOutput, error) {
			sess := sessionFromContext(ctx)
			if sess == nil {
				return CheckBudgetOutput{}, nil
			}

			total := sess.GetEventCount()
			masked := sess.MaskedEventCount()

			return CheckBudgetOutput{
				TotalEvents:   total,
				VisibleEvents: total - masked,
				MaskedEvents:  masked,
			}, nil
		},
		function.WithName(CheckBudgetTool),
		function.WithDescription(
			"Check how much context budget remains. Returns the total number of events, "+
				"visible events (sent to LLM), and masked events (hidden). "+
				"Use this proactively to decide when to prune context via delete_context.",
		),
	)
}

// --- note / read_notes tools ---

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
			inv, ok := agent.InvocationFromContext(ctx)
			if !ok || inv == nil || inv.Session == nil {
				return NoteOutput{Message: "no session available"}, nil
			}

			stateKey := noteKeyPrefix + input.Key
			stateValue := []byte(input.Content)

			// Write to in-memory session state.
			inv.Session.SetState(stateKey, stateValue)

			// Persist to DB-backed session service if available.
			if inv.SessionService != nil {
				sessKey := session.Key{
					AppName:   inv.Session.AppName,
					UserID:    inv.Session.UserID,
					SessionID: inv.Session.ID,
				}
				_ = inv.SessionService.UpdateSessionState(ctx, sessKey, session.StateMap{
					stateKey: stateValue,
				})
			}

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
		function.WithSkipSummarization(true),
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

			keys := make([]string, 0, len(notes))
			for k := range notes {
				keys = append(keys, k)
			}
			sort.Strings(keys)

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
		function.WithSkipSummarization(true),
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

