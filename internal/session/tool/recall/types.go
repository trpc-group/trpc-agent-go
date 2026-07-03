//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package recall provides agent-facing tools for on-demand
// session history search and loading.
package recall

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	// SearchToolName searches session history.
	SearchToolName = "session_search"
	// LoadToolName loads a small history window around one anchor result.
	LoadToolName = "session_load"

	// ScopeCurrentHidden searches summarized-away history in the current
	// session.
	ScopeCurrentHidden = "current_hidden"
	// ScopeCurrentSession searches the current session regardless of summary
	// cutoff. Use this when current-session details may have been compacted
	// out of the projected request.
	ScopeCurrentSession = "current_session"
	// ScopeOtherSessions searches other sessions owned by the same user.
	ScopeOtherSessions = "other_sessions"
	// ScopeAllSessions searches both current hidden history and other
	// sessions.
	ScopeAllSessions = "all_sessions"

	defaultSearchTopK    = 5
	maxSearchTopK        = 10
	defaultWindowBefore  = 1
	defaultWindowAfter   = 1
	defaultContentLimit  = 16 * 1024
	maxContentLimit      = 64 * 1024
	maxWindowSpan        = 4
	maxSessionScanEvents = 10000
	searchExpandedHits   = 2
	searchSnippetBefore  = 2
	searchSnippetAfter   = 2
)

var (
	errInvocationContextRequired = errors.New("no invocation context found")
	errSessionRequired           = errors.New("invocation exists but no session available")
	errSearchUnavailable         = errors.New("session search is not available for this session service")
	errWindowUnavailable         = errors.New("session window loading is not available for this session service")
)

const (
	summaryLastIncludedTsKey      = session.SummaryLastIncludedTimestampStateKey
	summaryLastIncludedEventIDKey = session.SummaryLastIncludedEventIDStateKey
)

// SearchSessionRequest is the input for session_search.
type SearchSessionRequest struct {
	Query      string             `json:"query" jsonschema:"description=Search query for prior conversation details. Prefer short keyword-style queries."`
	Scope      string             `json:"scope,omitempty" jsonschema:"description=Search scope: current_hidden, current_session, other_sessions, or all_sessions."`
	TopK       int                `json:"top_k,omitempty" jsonschema:"description=Maximum number of results to return. Defaults to 5 and is capped."`
	MinScore   float64            `json:"min_score,omitempty" jsonschema:"description=Optional minimum relevance score threshold between 0 and 1."`
	SearchMode session.SearchMode `json:"search_mode,omitempty" jsonschema:"description=Retrieval mode: dense or hybrid. Defaults to hybrid."`
}

// SearchSessionHit is one session_search result.
type SearchSessionHit struct {
	Scope     string                 `json:"scope"`
	SessionID string                 `json:"session_id"`
	EventID   string                 `json:"event_id"`
	Created   time.Time              `json:"created"`
	Role      model.Role             `json:"role,omitempty"`
	Score     float64                `json:"score"`
	Snippet   string                 `json:"snippet"`
	Context   []LoadedSessionMessage `json:"context,omitempty"`

	ToolCallID       string `json:"tool_call_id,omitempty"`
	ToolName         string `json:"tool_name,omitempty"`
	ContentBytes     int    `json:"content_bytes,omitempty"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
}

// SearchSessionResponse is the response from session_search.
type SearchSessionResponse struct {
	Query   string             `json:"query"`
	Scope   string             `json:"scope"`
	Results []SearchSessionHit `json:"results"`
	Count   int                `json:"count"`
}

// LoadSessionRequest is the input for session_load.
type LoadSessionRequest struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"description=Target session ID. Defaults to the current session when omitted or set to current."`
	EventID   string `json:"event_id,omitempty" jsonschema:"description=Anchor event ID to load around. Prefer this when available."`
	// ToolCallID is a current-session fallback when event_id is unavailable.
	ToolCallID string `json:"tool_call_id,omitempty" jsonschema:"description=Optional fallback tool call ID when event_id is unavailable."`
	Before     int    `json:"before,omitempty" jsonschema:"description=How many messages before the anchor to include. Defaults to 1."`
	After      int    `json:"after,omitempty" jsonschema:"description=How many messages after the anchor to include. Defaults to 1."`
	// ContentOffset and ContentLimit select a byte window for large tool
	// results. UTF-8 boundaries are preserved in returned content.
	ContentOffset int `json:"content_offset,omitempty" jsonschema:"description=Byte offset for loading a large tool result in slices."`
	ContentLimit  int `json:"content_limit,omitempty" jsonschema:"description=Maximum bytes to return for the target tool result. Defaults to a conservative limit."`
}

// LoadedSessionMessage is one historical message returned by session_load.
type LoadedSessionMessage struct {
	EventID string     `json:"event_id"`
	Role    model.Role `json:"role"`
	Created time.Time  `json:"created"`
	Content string     `json:"content"`

	ToolCallID       string `json:"tool_call_id,omitempty"`
	ToolName         string `json:"tool_name,omitempty"`
	ContentOffset    int    `json:"content_offset,omitempty"`
	ContentBytes     int    `json:"content_bytes,omitempty"`
	ReturnedBytes    int    `json:"returned_bytes,omitempty"`
	ContentTruncated bool   `json:"content_truncated,omitempty"`
}

// LoadSessionResponse is the response from session_load.
type LoadSessionResponse struct {
	SessionID string                 `json:"session_id"`
	EventID   string                 `json:"event_id"`
	Before    int                    `json:"before"`
	After     int                    `json:"after"`
	Note      string                 `json:"note"`
	Messages  []LoadedSessionMessage `json:"messages"`
	Count     int                    `json:"count"`
}

// SupportsSearch reports whether session_search can be offered for this invocation.
func SupportsSearch(inv *agent.Invocation) bool {
	if inv == nil || inv.Session == nil || inv.SessionService == nil {
		return false
	}
	_, ok := inv.SessionService.(session.SearchableService)
	return ok
}

// SupportsLoad reports whether session_load can be offered for this invocation.
func SupportsLoad(inv *agent.Invocation) bool {
	if inv == nil || inv.Session == nil || inv.SessionService == nil {
		return false
	}
	_, ok := inv.SessionService.(session.WindowService)
	return ok
}

// SupportsOnDemandSession reports whether any on-demand session tool is available.
func SupportsOnDemandSession(inv *agent.Invocation) bool {
	return SupportsSearch(inv) || SupportsLoad(inv)
}

func invocationFromContext(
	ctx context.Context,
) (*agent.Invocation, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return nil, errInvocationContextRequired
	}
	if inv.Session == nil {
		return nil, errSessionRequired
	}
	return inv, nil
}

func searchableServiceFromContext(
	ctx context.Context,
) (session.SearchableService, *agent.Invocation, error) {
	inv, err := invocationFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	searchable, ok := inv.SessionService.(session.SearchableService)
	if !ok {
		return nil, inv, errSearchUnavailable
	}
	return searchable, inv, nil
}

func windowServiceFromContext(
	ctx context.Context,
) (session.WindowService, *agent.Invocation, error) {
	inv, err := invocationFromContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	windowSvc, ok := inv.SessionService.(session.WindowService)
	if !ok {
		return nil, inv, errWindowUnavailable
	}
	return windowSvc, inv, nil
}

func optionalWindowServiceFromInvocation(
	inv *agent.Invocation,
) session.WindowService {
	if inv == nil || inv.SessionService == nil {
		return nil
	}
	windowSvc, ok := inv.SessionService.(session.WindowService)
	if !ok {
		return nil
	}
	return windowSvc
}

func currentUserKey(
	inv *agent.Invocation,
) (session.UserKey, error) {
	if inv == nil || inv.Session == nil {
		return session.UserKey{}, errSessionRequired
	}
	userKey := session.UserKey{
		AppName: inv.Session.AppName,
		UserID:  inv.Session.UserID,
	}
	if err := userKey.CheckUserKey(); err != nil {
		return session.UserKey{}, err
	}
	return userKey, nil
}

func currentSessionKey(
	inv *agent.Invocation,
	sessionID string,
) (session.Key, error) {
	if inv == nil || inv.Session == nil {
		return session.Key{}, errSessionRequired
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || isCurrentSessionAlias(sessionID) {
		sessionID = inv.Session.ID
	}
	key := session.Key{
		AppName:   inv.Session.AppName,
		UserID:    inv.Session.UserID,
		SessionID: sessionID,
	}
	if err := key.CheckSessionKey(); err != nil {
		return session.Key{}, err
	}
	return key, nil
}

func isCurrentSessionAlias(sessionID string) bool {
	switch strings.ToLower(strings.TrimSpace(sessionID)) {
	case "current",
		"current_session",
		"current-session",
		"active",
		"active_session",
		"active-session",
		"self":
		return true
	default:
		return false
	}
}

func normalizeScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case ScopeCurrentSession:
		return ScopeCurrentSession
	case ScopeOtherSessions:
		return ScopeOtherSessions
	case ScopeAllSessions:
		return ScopeAllSessions
	case ScopeCurrentHidden:
		fallthrough
	default:
		return ScopeCurrentHidden
	}
}

func normalizeSearchMode(mode session.SearchMode) session.SearchMode {
	switch mode {
	case session.SearchModeDense:
		return session.SearchModeDense
	case session.SearchModeHybrid:
		fallthrough
	default:
		return session.SearchModeHybrid
	}
}

func normalizeTopK(topK int) int {
	if topK <= 0 {
		return defaultSearchTopK
	}
	if topK > maxSearchTopK {
		return maxSearchTopK
	}
	return topK
}

func normalizeWindowSize(
	before, after int,
) (int, int) {
	if before < 0 {
		before = 0
	}
	if after < 0 {
		after = 0
	}
	if before == 0 && after == 0 {
		before = defaultWindowBefore
		after = defaultWindowAfter
	}
	for before+after > maxWindowSpan {
		if after >= before && after > 0 {
			after--
			continue
		}
		if before > 0 {
			before--
			continue
		}
		break
	}
	return before, after
}

func normalizeContentWindow(
	offset, limit int,
) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = defaultContentLimit
	}
	if limit > maxContentLimit {
		limit = maxContentLimit
	}
	return offset, limit
}

func currentSummaryCutoff(
	inv *agent.Invocation,
) time.Time {
	boundary := currentSummaryBoundary(inv)
	if boundary != nil {
		return boundary.CutoffTime()
	}
	return time.Time{}
}

func currentSummaryBoundary(
	inv *agent.Invocation,
) *session.SummaryBoundary {
	if inv == nil || inv.Session == nil {
		return nil
	}

	filterKey := strings.TrimSpace(inv.GetEventFilterKey())
	if boundary, ok := summaryBoundaryForFilter(inv.Session, filterKey); ok {
		return boundary
	}
	boundary := summaryBoundaryFromState(inv.Session)
	if boundary == nil {
		return nil
	}
	return boundary
}

func summaryBoundaryFromState(sess *session.Session) *session.SummaryBoundary {
	cutoff := summaryCutoffFromState(sess)
	if cutoff.IsZero() {
		return nil
	}
	return session.NewSummaryBoundaryWithEventID(
		"",
		cutoff,
		summaryLastIncludedEventIDFromState(sess),
	)
}

func summaryCutoffFromState(sess *session.Session) time.Time {
	if sess == nil {
		return time.Time{}
	}
	raw, ok := sess.GetState(summaryLastIncludedTsKey)
	if !ok || len(raw) == 0 {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func summaryLastIncludedEventIDFromState(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	raw, ok := sess.GetState(summaryLastIncludedEventIDKey)
	if !ok || len(raw) == 0 {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func summaryCutoffForFilter(
	sess *session.Session,
	filterKey string,
) (time.Time, bool) {
	boundary, ok := summaryBoundaryForFilter(sess, filterKey)
	if !ok {
		return time.Time{}, false
	}
	return boundary.CutoffTime(), true
}

func summaryBoundaryForFilter(
	sess *session.Session,
	filterKey string,
) (*session.SummaryBoundary, bool) {
	if sess == nil {
		return nil, false
	}
	sess.SummariesMu.RLock()
	defer sess.SummariesMu.RUnlock()

	if len(sess.Summaries) == 0 {
		return nil, false
	}

	if sum := sess.Summaries[filterKey]; sum != nil &&
		strings.TrimSpace(sum.Summary) != "" {
		return sum.CutoffBoundary(), true
	}
	if filterKey != "" {
		if boundary, ok := session.SummaryPrefixBoundary(
			sess.Summaries,
			filterKey,
		); ok {
			return boundary, true
		}
	}
	if sum := sess.Summaries[session.SummaryFilterKeyAllContents]; sum != nil &&
		strings.TrimSpace(sum.Summary) != "" {
		return sum.CutoffBoundary(), true
	}
	return nil, false
}

func extractSessionMessageText(
	evt event.Event,
) (string, model.Role, bool) {
	if evt.Response == nil || evt.Response.IsPartial ||
		len(evt.Choices) == 0 {
		return "", "", false
	}

	msg := evt.Choices[0].Message
	if len(msg.ToolCalls) > 0 {
		return "", "", false
	}

	role := msg.Role
	if role == "" {
		role = model.RoleAssistant
	}
	if msg.ToolID != "" || role == model.RoleTool {
		role = model.RoleTool
	}
	if role != model.RoleUser && role != model.RoleAssistant && role != model.RoleTool {
		return "", "", false
	}

	text := visibleMessageText(msg)
	if text == "" {
		return "", "", false
	}
	if role == model.RoleTool {
		toolName := strings.TrimSpace(msg.ToolName)
		if toolName != "" {
			text = toolName + ": " + text
		}
	}
	return text, role, true
}

type loadContentWindow struct {
	AnchorEventID string
	ToolCallID    string
	Offset        int
	Limit         int
}

func toolResultMetadata(
	evt event.Event,
) (toolCallID, toolName string, contentBytes int, ok bool) {
	if evt.Response == nil || evt.Response.IsPartial || len(evt.Choices) == 0 {
		return "", "", 0, false
	}
	for _, choice := range evt.Choices {
		msg := choice.Message
		role := msg.Role
		if msg.ToolID != "" || role == model.RoleTool {
			text := visibleMessageText(msg)
			return strings.TrimSpace(msg.ToolID),
				strings.TrimSpace(msg.ToolName),
				len(text),
				true
		}
	}
	return "", "", 0, false
}

func toolResultEventIDByToolCallID(
	events []event.Event,
	toolCallID string,
) string {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" {
		return ""
	}
	for i := len(events) - 1; i >= 0; i-- {
		evt := events[i]
		if evt.ID == "" || evt.Response == nil || evt.Response.IsPartial {
			continue
		}
		for _, choice := range evt.Choices {
			msg := choice.Message
			if msg.ToolID == toolCallID &&
				(msg.Role == model.RoleTool || msg.ToolID != "") {
				return evt.ID
			}
		}
	}
	return ""
}

func loadedMessagesFromWindow(
	window *session.EventWindow,
	contentWindow loadContentWindow,
) []LoadedSessionMessage {
	if window == nil || len(window.Entries) == 0 {
		return nil
	}

	messages := make([]LoadedSessionMessage, 0, len(window.Entries))
	for _, entry := range window.Entries {
		msg, ok := loadedMessageFromEvent(entry.Event, entry.CreatedAt, contentWindow)
		if !ok {
			continue
		}
		messages = append(messages, msg)
	}
	if len(messages) == 0 {
		return nil
	}
	return messages
}

func loadedMessageFromEvent(
	evt event.Event,
	createdAt time.Time,
	contentWindow loadContentWindow,
) (LoadedSessionMessage, bool) {
	if evt.Response == nil || evt.Response.IsPartial || len(evt.Choices) == 0 {
		return LoadedSessionMessage{}, false
	}
	for _, choice := range evt.Choices {
		msg := choice.Message
		if skipsSelectedAnchorToolResult(evt.ID, msg, contentWindow) {
			continue
		}
		loaded, ok := loadedMessageFromModelMessage(
			evt.ID,
			createdAt,
			msg,
			contentWindow,
		)
		if ok {
			return loaded, true
		}
	}
	return LoadedSessionMessage{}, false
}

func skipsSelectedAnchorToolResult(
	eventID string,
	msg model.Message,
	contentWindow loadContentWindow,
) bool {
	if contentWindow.AnchorEventID == "" ||
		eventID != contentWindow.AnchorEventID ||
		contentWindow.ToolCallID == "" {
		return false
	}
	if msg.ToolID == "" && msg.Role != model.RoleTool {
		return false
	}
	return strings.TrimSpace(msg.ToolID) != contentWindow.ToolCallID
}

func loadedMessageFromModelMessage(
	eventID string,
	createdAt time.Time,
	msg model.Message,
	contentWindow loadContentWindow,
) (LoadedSessionMessage, bool) {
	if len(msg.ToolCalls) > 0 {
		return LoadedSessionMessage{}, false
	}

	role := msg.Role
	if role == "" {
		role = model.RoleAssistant
	}
	if msg.ToolID != "" || role == model.RoleTool {
		role = model.RoleTool
	}
	if role != model.RoleUser && role != model.RoleAssistant && role != model.RoleTool {
		return LoadedSessionMessage{}, false
	}

	text := visibleMessageText(msg)
	if text == "" {
		return LoadedSessionMessage{}, false
	}

	loaded := LoadedSessionMessage{
		EventID: eventID,
		Role:    role,
		Created: createdAt,
		Content: text,
	}
	if role != model.RoleTool {
		return loaded, true
	}

	loaded.ToolCallID = strings.TrimSpace(msg.ToolID)
	loaded.ToolName = strings.TrimSpace(msg.ToolName)
	loaded.ContentBytes = len(text)
	offset, limit, applyWindow := contentWindowForToolResult(
		eventID,
		loaded.ToolCallID,
		contentWindow,
	)
	if applyWindow {
		sliced, offset, returned, truncated := sliceContentByBytes(
			text,
			offset,
			limit,
		)
		if truncated || offset > 0 {
			loaded.Content = sliced
		} else if loaded.ToolName != "" {
			loaded.Content = loaded.ToolName + ": " + text
		} else {
			loaded.Content = sliced
		}
		loaded.ContentOffset = offset
		loaded.ReturnedBytes = returned
		loaded.ContentTruncated = truncated
		return loaded, true
	}
	if loaded.ToolName != "" {
		loaded.Content = loaded.ToolName + ": " + text
	}
	return loaded, true
}

func visibleMessageText(msg model.Message) string {
	text := strings.TrimSpace(msg.Content)
	if text != "" || len(msg.ContentParts) == 0 {
		return text
	}
	var parts []string
	for _, part := range msg.ContentParts {
		if part.Text == nil {
			continue
		}
		partText := strings.TrimSpace(*part.Text)
		if partText == "" {
			continue
		}
		parts = append(parts, partText)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func contentWindowForToolResult(
	eventID, toolCallID string,
	window loadContentWindow,
) (int, int, bool) {
	if window.AnchorEventID == "" && window.ToolCallID == "" {
		return 0, 0, false
	}
	if isAnchorToolResult(eventID, toolCallID, window) {
		return window.Offset, window.Limit, true
	}
	if window.AnchorEventID == "" {
		return 0, 0, false
	}
	return 0, window.Limit, true
}

func isAnchorToolResult(
	eventID, toolCallID string,
	window loadContentWindow,
) bool {
	if window.ToolCallID != "" && toolCallID != window.ToolCallID {
		return false
	}
	if window.AnchorEventID != "" && eventID != window.AnchorEventID {
		return false
	}
	return true
}

func sliceContentByBytes(
	content string,
	offset, limit int,
) (string, int, int, bool) {
	offset, limit = normalizeContentWindow(offset, limit)
	total := len(content)
	if offset > total {
		offset = total
	}
	start := clampUTF8Boundary(content, offset)
	end := start + limit
	if end > total {
		end = total
	}
	end = clampUTF8Boundary(content, end)
	if end == start && end < total {
		end = clampUTF8NextBoundary(content, start+limit)
	}
	if end < start {
		end = start
	}
	sliced := content[start:end]
	return sliced, start, len(sliced), start > 0 || end < total
}

func clampUTF8Boundary(s string, index int) int {
	if index <= 0 {
		return 0
	}
	if index >= len(s) {
		return len(s)
	}
	for index > 0 && !isUTF8Boundary(s, index) {
		index--
	}
	return index
}

func clampUTF8NextBoundary(s string, index int) int {
	if index <= 0 {
		return 0
	}
	if index >= len(s) {
		return len(s)
	}
	for index < len(s) && !isUTF8Boundary(s, index) {
		index++
	}
	return index
}

func isUTF8Boundary(s string, index int) bool {
	if index <= 0 || index >= len(s) {
		return true
	}
	return utf8.RuneStart(s[index])
}
