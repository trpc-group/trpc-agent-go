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
	"sort"
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
			inv, ok := agent.InvocationFromContext(ctx)
			if !ok || inv == nil || inv.Session == nil {
				return DeleteContextOutput{
					Message: "no session available",
				}, nil
			}

			masked := inv.Session.MaskEvents(input.EventIDs...)
			key := session.Key{
				AppName:   inv.Session.AppName,
				UserID:    inv.Session.UserID,
				SessionID: inv.Session.ID,
			}
			if err := inv.Session.PersistMaskedEvents(ctx, inv.SessionService, key); err != nil {
				return DeleteContextOutput{}, fmt.Errorf("persist masked events: %w", err)
			}

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
			masked := sess.MaskedEventCount()
			visible := len(sess.GetVisibleEvents())

			return CheckBudgetOutput{
				TotalEvents:   total,
				VisibleEvents: visible,
				MaskedEvents:  masked,
			}, nil
		},
		function.WithName("check_budget"),
		function.WithDescription(
			"Check how much context budget remains. Returns total, visible, and "+
				"masked event counts (visible uses len(GetVisibleEvents())). "+
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
			inv, ok := agent.InvocationFromContext(ctx)
			if !ok || inv == nil || inv.Session == nil {
				return NoteOutput{Message: "no session available"}, nil
			}

			keyStr := noteKeyPrefix + input.Key
			byteContent := []byte(input.Content)

			if inv.SessionService != nil {
				key := session.Key{
					AppName:   inv.Session.AppName,
					UserID:    inv.Session.UserID,
					SessionID: inv.Session.ID,
				}
				err := inv.SessionService.UpdateSessionState(ctx, key, session.StateMap{
					keyStr: byteContent,
				})
				if err != nil {
					return NoteOutput{}, fmt.Errorf("persist note: %w", err)
				}
			}

			inv.Session.SetState(keyStr, byteContent)

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

			// Sort keys for deterministic output.
			keys := make([]string, 0, len(notes))
			for k := range notes {
				keys = append(keys, k)
			}
			sort.Strings(keys)

			ordered := make(map[string]string, len(keys))
			for _, k := range keys {
				ordered[k] = notes[k]
			}

			return ReadNotesOutput{
				Notes: ordered,
				Count: len(ordered),
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

// --- notes_index tool ---

// NotesIndexInput is the input for the notes_index tool (empty — no args).
type NotesIndexInput struct{}

// NoteIndexEntry summarises a single persistent note without sending its
// full body back to the model. It carries everything the LLM needs to
// decide whether to fetch the body via read_notes.
type NoteIndexEntry struct {
	// Key is the note key (without the internal note: prefix).
	Key string `json:"key"`
	// Bytes is the raw byte length of the stored content, useful when the
	// model is reasoning about its remaining context budget.
	Bytes int `json:"bytes"`
	// Preview is the first PreviewMaxChars characters of the note content
	// with a trailing ellipsis when truncated. It exists so the LLM can
	// disambiguate similarly-named notes without paying for the whole body.
	Preview string `json:"preview,omitempty"`
}

// NotesIndexOutput is the output for the notes_index tool.
type NotesIndexOutput struct {
	// Notes lists every persistent note in deterministic key order.
	Notes []NoteIndexEntry `json:"notes"`
	// Count is len(Notes), surfaced for cheap "do I have any notes?" checks.
	Count int `json:"count"`
	// TotalBytes is the sum of all note byte lengths. Hosts can use this
	// alongside their context budget to decide when to prune.
	TotalBytes int `json:"total_bytes"`
}

// notesIndexPreviewMaxChars caps how much of each note body the index
// returns. Long enough to disambiguate notes by content, short enough that
// indexing 100 notes stays well under 8 KB of total payload.
const notesIndexPreviewMaxChars = 80

// notesIndexPreview returns the first notesIndexPreviewMaxChars characters
// of content, collapsing runs of whitespace into a single space and
// appending an ellipsis when the original was longer. Empty input returns
// the empty string so callers don't have to special-case it.
func notesIndexPreview(content string) string {
	if content == "" {
		return ""
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	flat := strings.Join(strings.Fields(trimmed), " ")
	runes := []rune(flat)
	if len(runes) <= notesIndexPreviewMaxChars {
		return flat
	}
	return string(runes[:notesIndexPreviewMaxChars]) + "…"
}

// NewNotesIndexTool creates a tool that returns a lightweight index of all
// persistent notes — keys, byte sizes, and short previews — without
// dumping every note body into the prompt.
//
// This pairs with note / read_notes to support a "browse → fetch" pattern
// for context-pressed agents: the LLM scans the index, picks the note(s)
// it actually needs, and only then calls read_notes (or, in a future
// iteration, a keyed fetch). It's the on-demand alternative to read_notes
// returning the entire map every time.
func NewNotesIndexTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, _ NotesIndexInput) (NotesIndexOutput, error) {
			sess := sessionFromContext(ctx)
			if sess == nil {
				return NotesIndexOutput{Notes: []NoteIndexEntry{}}, nil
			}

			snapshot := sess.SnapshotState()
			// Collect note keys first so the index is emitted in a
			// deterministic order regardless of map iteration order.
			keys := make([]string, 0, len(snapshot))
			for k := range snapshot {
				if strings.HasPrefix(k, noteKeyPrefix) {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)

			entries := make([]NoteIndexEntry, 0, len(keys))
			total := 0
			for _, k := range keys {
				body := snapshot[k]
				entries = append(entries, NoteIndexEntry{
					Key:     strings.TrimPrefix(k, noteKeyPrefix),
					Bytes:   len(body),
					Preview: notesIndexPreview(string(body)),
				})
				total += len(body)
			}

			return NotesIndexOutput{
				Notes:      entries,
				Count:      len(entries),
				TotalBytes: total,
			}, nil
		},
		function.WithName("notes_index"),
		function.WithDescription(
			"List the keys, byte sizes, and short previews of every persistent "+
				"note saved via the note tool, without returning their full "+
				"content. Use this to discover what notes exist before deciding "+
				"whether to fetch any of them via read_notes — much cheaper than "+
				"read_notes when the agent only needs the index, not the bodies.",
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
		NewNotesIndexTool(),
	}
}
