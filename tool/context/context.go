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
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// BudgetReport is an optional host-supplied token accounting snapshot.
// Hosts that assemble the real model request (messages plus tool schemas)
// can report that size so check_budget matches the send guard.
type BudgetReport struct {
	EstimatedTokens     int
	UpperBoundTokens    int
	MaxInputTokens      int
	OutputReserveTokens int
	HeadroomTokens      int
	ContextWindowTokens int
	// CapKnown is true when MaxInputTokens is a real serving or window cap.
	// When false, used_pct is omitted so callers do not treat a fallback
	// window as a percentage denominator.
	CapKnown bool
}

// BudgetReporter supplies the latest assembled-request budget for a session.
// ReportBudget returns false when no snapshot exists yet.
type BudgetReporter interface {
	ReportBudget(ctx context.Context) (BudgetReport, bool)
}

// Option configures Pensieve context tools.
type Option func(*toolsConfig)

type toolsConfig struct {
	reporter BudgetReporter
}

// WithBudgetReporter installs a host reporter used by check_budget and the
// compact note receipt. Without a reporter, those tools return event counts
// only.
func WithBudgetReporter(reporter BudgetReporter) Option {
	return func(cfg *toolsConfig) {
		cfg.reporter = reporter
	}
}

func applyOptions(opts ...Option) toolsConfig {
	var cfg toolsConfig
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&cfg)
	}
	return cfg
}

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

// pensieveHygieneToolNames are meta tools whose prior rounds should leave the
// visible transcript once delete_context succeeds. Keeping their catalogs and
// budget snapshots would re-consume the context they just freed.
var pensieveHygieneToolNames = []string{
	"list_context",
	"check_budget",
	"delete_context",
}

// NewDeleteContextTool creates a tool that allows the LLM to prune specific
// events from its visible context. Events are soft-masked (hidden from view
// but preserved for audit). This is the Pensieve paradigm's "deleteContext".
//
// After masking the requested event_ids, prior list_context, check_budget, and
// completed delete_context tool rounds are also masked so the prune
// meta-transcript does not linger in later LLM turns. The current
// delete_context result is not in the session yet, so its short receipt still
// returns to the model for this turn.
func NewDeleteContextTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, input DeleteContextInput) (DeleteContextOutput, error) {
			inv, ok := agent.InvocationFromContext(ctx)
			if !ok || inv == nil || inv.Session == nil {
				return DeleteContextOutput{
					Message: "no session available",
				}, nil
			}

			key := session.Key{
				AppName:   inv.Session.AppName,
				UserID:    inv.Session.UserID,
				SessionID: inv.Session.ID,
			}
			ids := mergeEventIDs(
				input.EventIDs,
				session.EventIDsForNamedToolResults(inv.Session.Events, pensieveHygieneToolNames),
			)
			masked, err := inv.Session.MaskAndPersistEvents(
				ctx,
				inv.SessionService,
				key,
				ids...,
			)
			if err != nil {
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
				"Use list_context first to obtain event_ids, then call this after extracting "+
				"key information into notes to reduce context pressure. "+
				"Also removes prior list_context, check_budget, and delete_context rounds "+
				"from visible history so the prune cycle itself does not refill the window.",
		),
	)
}

// mergeEventIDs concatenates id lists while dropping empties and duplicates,
// preserving first-seen order.
func mergeEventIDs(parts ...[]string) []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, part := range parts {
		for _, id := range part {
			if id == "" {
				continue
			}
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// --- list_context tool ---

// ListContextOrderBytesDesc sorts visible events largest-first by content
// bytes. It is the default when order is omitted.
const ListContextOrderBytesDesc = "bytes_desc"

// ListContextOrderSession keeps the session's visible-event order.
const ListContextOrderSession = "session"

// ListContextInput is the input for the list_context tool.
type ListContextInput struct {
	// Limit caps how many events are returned. Zero returns every visible
	// event. Positive values keep the first Limit entries after ordering.
	Limit int `json:"limit,omitempty" jsonschema:"description=Maximum events to return after ordering. 0 returns all visible events."`
	// Order is bytes_desc (default) or session.
	Order string `json:"order,omitempty" jsonschema:"description=Event ordering: bytes_desc (default, largest first) or session (visible order)."`
}

// ContextEventEntry summarises one LLM-visible session event.
type ContextEventEntry struct {
	ID      string `json:"id"`
	Author  string `json:"author,omitempty"`
	Kind    string `json:"kind"`
	Preview string `json:"preview,omitempty"`
	// Bytes is the UTF-8 byte length of the content used to build Preview.
	Bytes int `json:"bytes"`
}

// ListContextOutput is the output for the list_context tool.
type ListContextOutput struct {
	Events []ContextEventEntry `json:"events"`
	Count  int                 `json:"count"`
	// TotalCount is the number of visible events before applying Limit.
	TotalCount int `json:"total_count"`
	// OmittedCount is TotalCount minus Count when Limit truncates the list.
	OmittedCount int `json:"omitted_count"`
	// Order is the ordering that was applied.
	Order string `json:"order,omitempty"`
}

const listContextPreviewMaxRunes = 120

// NewListContextTool creates a tool that lists visible session events with
// stable IDs so the model can pass them to delete_context.
func NewListContextTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, input ListContextInput) (ListContextOutput, error) {
			sess := sessionFromContext(ctx)
			if sess == nil {
				return ListContextOutput{Events: []ContextEventEntry{}}, nil
			}

			order := normalizeListContextOrder(input.Order)
			visible := sess.GetVisibleEvents()
			entries := make([]ContextEventEntry, 0, len(visible))
			for _, evt := range visible {
				content := contextEventContent(evt)
				entries = append(entries, ContextEventEntry{
					ID:      evt.ID,
					Author:  evt.Author,
					Kind:    contextEventKind(evt),
					Preview: truncatePreview(content, listContextPreviewMaxRunes),
					Bytes:   len(content),
				})
			}
			if order == ListContextOrderBytesDesc {
				sort.SliceStable(entries, func(i, j int) bool {
					return entries[i].Bytes > entries[j].Bytes
				})
			}

			total := len(entries)
			omitted := 0
			if input.Limit > 0 && len(entries) > input.Limit {
				omitted = len(entries) - input.Limit
				entries = entries[:input.Limit]
			}

			return ListContextOutput{
				Events:       entries,
				Count:        len(entries),
				TotalCount:   total,
				OmittedCount: omitted,
				Order:        order,
			}, nil
		},
		function.WithName("list_context"),
		function.WithDescription(
			"List visible session events with stable event IDs, authors, kinds, "+
				"byte sizes, and short previews. Default order is largest-first "+
				"(bytes_desc); pass order=session for visible order. Optional limit "+
				"bounds the returned slice and reports total_count plus omitted_count. "+
				"Call this before delete_context so you can pass real event_ids.",
		),
	)
}

func normalizeListContextOrder(order string) string {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "", ListContextOrderBytesDesc:
		return ListContextOrderBytesDesc
	case ListContextOrderSession:
		return ListContextOrderSession
	default:
		return ListContextOrderBytesDesc
	}
}

func contextEventKind(evt event.Event) string {
	if evt.Response == nil {
		return "other"
	}
	if evt.IsToolCallResponse() {
		return "tool_call"
	}
	if evt.IsToolResultResponse() {
		return "tool_result"
	}
	if evt.IsUserMessage() {
		return "user"
	}
	if len(evt.Response.Choices) > 0 {
		role := evt.Response.Choices[0].Message.Role
		if role != "" {
			return string(role)
		}
	}
	return "assistant"
}

// contextEventContent returns the flat text used for preview and byte sizing.
func contextEventContent(evt event.Event) string {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return ""
	}
	choice := evt.Response.Choices[0]
	content := strings.TrimSpace(choice.Message.Content)
	if content == "" {
		content = strings.TrimSpace(choice.Delta.Content)
	}
	if content == "" && len(choice.Message.ToolCalls) > 0 {
		content = choice.Message.ToolCalls[0].Function.Name
	}
	if content == "" && len(choice.Delta.ToolCalls) > 0 {
		content = choice.Delta.ToolCalls[0].Function.Name
	}
	if content == "" && choice.Message.ToolID != "" {
		content = choice.Message.ToolID
	}
	if content == "" && choice.Delta.ToolID != "" {
		content = choice.Delta.ToolID
	}
	if content == "" {
		return ""
	}
	return strings.Join(strings.Fields(content), " ")
}

func contextEventPreview(evt event.Event) string {
	return truncatePreview(contextEventContent(evt), listContextPreviewMaxRunes)
}

func truncatePreview(content string, maxRunes int) string {
	if content == "" {
		return ""
	}
	runes := []rune(content)
	if len(runes) <= maxRunes {
		return content
	}
	return string(runes[:maxRunes]) + "…"
}

// --- check_budget tool ---

// CheckBudgetInput is the input for the check_budget tool (empty — no args needed).
type CheckBudgetInput struct{}

// CheckBudgetOutput is the output for the check_budget tool.
// Token fields are present only when a host BudgetReporter returns a snapshot.
type CheckBudgetOutput struct {
	TotalEvents   int `json:"total_events"`
	VisibleEvents int `json:"visible_events"`
	MaskedEvents  int `json:"masked_events"`

	EstimatedTokens     *int     `json:"estimated_tokens,omitempty"`
	UpperBoundTokens    *int     `json:"upper_bound_tokens,omitempty"`
	MaxInputTokens      *int     `json:"max_input_tokens,omitempty"`
	OutputReserveTokens *int     `json:"output_reserve_tokens,omitempty"`
	HeadroomTokens      *int     `json:"headroom_tokens,omitempty"`
	ContextWindowTokens *int     `json:"context_window_tokens,omitempty"`
	UsedPct             *float64 `json:"used_pct,omitempty"`
}

// budgetFromSession returns the event counts used by check_budget and note.
// Keeping one calculation prevents their budget snapshots from drifting.
func budgetFromSession(sess *session.Session) CheckBudgetOutput {
	if sess == nil {
		return CheckBudgetOutput{}
	}
	return CheckBudgetOutput{
		TotalEvents:   sess.GetEventCount(),
		VisibleEvents: len(sess.GetVisibleEvents()),
		MaskedEvents:  sess.MaskedEventCount(),
	}
}

func enrichBudgetWithReporter(
	ctx context.Context,
	out CheckBudgetOutput,
	reporter BudgetReporter,
) CheckBudgetOutput {
	if reporter == nil {
		return out
	}
	report, ok := reporter.ReportBudget(ctx)
	if !ok {
		return out
	}
	estimated := report.EstimatedTokens
	upper := report.UpperBoundTokens
	maxInput := report.MaxInputTokens
	outputReserve := report.OutputReserveTokens
	headroom := report.HeadroomTokens
	window := report.ContextWindowTokens
	out.EstimatedTokens = &estimated
	out.UpperBoundTokens = &upper
	out.MaxInputTokens = &maxInput
	out.OutputReserveTokens = &outputReserve
	out.HeadroomTokens = &headroom
	out.ContextWindowTokens = &window
	if report.CapKnown && maxInput > 0 {
		pct := float64(upper) / float64(maxInput)
		out.UsedPct = &pct
	}
	return out
}

// NewCheckBudgetTool creates a tool that reports the current context budget.
// The LLM can query this to decide when to prune context.
func NewCheckBudgetTool(opts ...Option) tool.CallableTool {
	cfg := applyOptions(opts...)
	return function.NewFunctionTool(
		func(ctx context.Context, _ CheckBudgetInput) (CheckBudgetOutput, error) {
			out := budgetFromSession(sessionFromContext(ctx))
			return enrichBudgetWithReporter(ctx, out, cfg.reporter), nil
		},
		function.WithName("check_budget"),
		function.WithDescription(
			"Check how much context budget remains. Returns total, visible, and "+
				"masked event counts (visible uses len(GetVisibleEvents())). "+
				"When the host supplies assembled-request accounting, also returns "+
				"estimated_tokens, upper_bound_tokens, max_input_tokens, and used_pct "+
				"(only when the input cap is known). Use this proactively to decide "+
				"when to prune context via delete_context.",
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

// UnmarshalJSON accepts either string or structured JSON note content. Models
// sometimes follow the semantic request to save a ledger by passing an object
// even though the schema asks for a string. Normalizing that object here keeps
// the note tool usable without requiring every host to repair its arguments.
func (n *NoteInput) UnmarshalJSON(data []byte) error {
	var input struct {
		Key     string          `json:"key"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(data, &input); err != nil {
		return fmt.Errorf("decode note input: %w", err)
	}

	n.Key = input.Key
	if len(input.Content) == 0 {
		n.Content = ""
		return nil
	}
	if input.Content[0] == '"' {
		if err := json.Unmarshal(input.Content, &n.Content); err != nil {
			return fmt.Errorf("decode note content: %w", err)
		}
		return nil
	}

	var content any
	if err := json.Unmarshal(input.Content, &content); err != nil {
		return fmt.Errorf("decode structured note content: %w", err)
	}
	normalized, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("encode structured note content: %w", err)
	}
	n.Content = string(normalized)
	return nil
}

// NoteSaveReceipt identifies the note that was persisted without repeating its
// content in the context window.
type NoteSaveReceipt struct {
	Key   string `json:"key"`
	Bytes int    `json:"bytes"`
}

// NoteOutput is the output for the note tool. Successful saves include both a
// compact receipt and the current context budget so callers do not need an
// immediate follow-up check_budget call.
type NoteOutput struct {
	Message string             `json:"message"`
	Saved   *NoteSaveReceipt   `json:"saved,omitempty"`
	Budget  *CheckBudgetOutput `json:"budget,omitempty"`
}

// NewNoteTool creates a tool that writes a persistent note to session state.
// Notes survive context pruning (delete_context) — they are stored in session
// state, not in the event stream. Use this to distill key information before
// pruning the raw context that contained it.
func NewNoteTool(opts ...Option) tool.CallableTool {
	cfg := applyOptions(opts...)
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

			budget := enrichBudgetWithReporter(
				ctx,
				budgetFromSession(inv.Session),
				cfg.reporter,
			)
			return NoteOutput{
				Message: fmt.Sprintf("note '%s' saved (%d bytes)", input.Key, len(input.Content)),
				Saved: &NoteSaveReceipt{
					Key:   input.Key,
					Bytes: len(input.Content),
				},
				Budget: &budget,
			}, nil
		},
		function.WithName("note"),
		function.WithDescription(
			"Save a persistent note that survives context pruning. "+
				"Use this to distill key findings, plans, or intermediate results "+
				"before removing raw context via delete_context. "+
				"Notes are stored by key and can be overwritten. The result confirms "+
				"what was saved and includes the current context budget, so do not call "+
				"check_budget immediately afterward.",
		),
	)
}

// ReadNotesInput is the input for the read_notes tool.
type ReadNotesInput struct {
	// Keys selects specific notes. Empty returns every note.
	Keys []string `json:"keys,omitempty" jsonschema:"description=Optional note keys to read. Empty returns all notes."`
}

// ReadNotesOutput is the output for the read_notes tool.
type ReadNotesOutput struct {
	Notes map[string]string `json:"notes"`
	Count int               `json:"count"`
	// ReloadedFromStore is true when at least one note body was copied from
	// SessionService onto the live session because the live snapshot was stale.
	ReloadedFromStore bool `json:"reloaded_from_store,omitempty"`
}

// NewReadNotesTool creates a tool that lists persistent notes.
// The LLM uses this to recall distilled information after pruning context.
func NewReadNotesTool() tool.CallableTool {
	return function.NewFunctionTool(
		func(ctx context.Context, input ReadNotesInput) (ReadNotesOutput, error) {
			inv, ok := agent.InvocationFromContext(ctx)
			if !ok || inv == nil || inv.Session == nil {
				return ReadNotesOutput{Notes: map[string]string{}}, nil
			}

			notes, reloaded := loadNotes(ctx, inv, input.Keys)
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
				Notes:             ordered,
				Count:             len(ordered),
				ReloadedFromStore: reloaded,
			}, nil
		},
		function.WithName("read_notes"),
		function.WithDescription(
			"Read persistent notes previously saved via the note tool. "+
				"Pass keys to fetch specific notes; omit keys to return all. "+
				"Returns a map of key→content. Use this to recall distilled "+
				"information after pruning raw context.",
		),
	)
}

// loadNotes returns note bodies from the live session, reloading from the
// session service when the live snapshot is missing notes the caller needs.
func loadNotes(
	ctx context.Context,
	inv *agent.Invocation,
	wantKeys []string,
) (map[string]string, bool) {
	notes := notesFromSnapshot(inv.Session.SnapshotState(), wantKeys)
	if !needsNoteReload(notes, wantKeys) {
		return notes, false
	}
	if inv.SessionService == nil {
		return notes, false
	}

	key := session.Key{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: inv.Session.ID,
	}
	loaded, err := inv.SessionService.GetSession(ctx, key)
	if err != nil || loaded == nil {
		return notes, false
	}

	reloaded := false
	for stateKey, body := range loaded.SnapshotState() {
		if !strings.HasPrefix(stateKey, noteKeyPrefix) {
			continue
		}
		name := strings.TrimPrefix(stateKey, noteKeyPrefix)
		if len(wantKeys) > 0 && !containsString(wantKeys, name) {
			continue
		}
		if _, ok := notes[name]; ok {
			continue
		}
		inv.Session.SetState(stateKey, body)
		notes[name] = string(body)
		reloaded = true
	}
	return notes, reloaded
}

func notesFromSnapshot(snapshot session.StateMap, wantKeys []string) map[string]string {
	notes := make(map[string]string)
	for k, v := range snapshot {
		if !strings.HasPrefix(k, noteKeyPrefix) {
			continue
		}
		name := strings.TrimPrefix(k, noteKeyPrefix)
		if len(wantKeys) > 0 && !containsString(wantKeys, name) {
			continue
		}
		notes[name] = string(v)
	}
	return notes
}

func needsNoteReload(notes map[string]string, wantKeys []string) bool {
	if len(wantKeys) == 0 {
		return len(notes) == 0
	}
	for _, key := range wantKeys {
		if _, ok := notes[key]; !ok {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

// --- notes_index tool ---

// NotesIndexInput is the input for the notes_index tool (empty — no args).
type NotesIndexInput struct{}

// NoteIndexEntry summarises a single persistent note without sending its
// full body back to the model. It carries everything the LLM needs to
// decide whether to fetch the body via read_notes.
type NoteIndexEntry struct {
	// Name is the note identifier (without the internal note: prefix). It is
	// named "name" rather than "key" because hosts commonly run redaction
	// layers that treat any field whose name contains "key" as a secret and
	// mask its value, which would hide the identifier the model needs to call
	// read_notes.
	Name string `json:"name"`
	// Key repeats Name so consumers written against the original field keep
	// working. Prefer Name; Key is retained only for backwards compatibility.
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
				name := strings.TrimPrefix(k, noteKeyPrefix)
				entries = append(entries, NoteIndexEntry{
					Name:    name,
					Key:     name,
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
			"List the names, byte sizes, and short previews of every persistent "+
				"note saved via the note tool, without returning their full "+
				"content. Use this to discover what notes exist before deciding "+
				"whether to fetch any of them via read_notes — much cheaper than "+
				"read_notes when the agent only needs the index, not the bodies.",
		),
	)
}

// Tools returns all context management tools as a convenience.
// Optional WithBudgetReporter attaches host token accounting to check_budget
// and the note receipt.
func Tools(opts ...Option) []tool.Tool {
	return []tool.Tool{
		NewListContextTool(),
		NewDeleteContextTool(),
		NewCheckBudgetTool(opts...),
		NewNoteTool(opts...),
		NewReadNotesTool(),
		NewNotesIndexTool(),
	}
}
