//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package history

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	// SearchToolName is the name of the tool exposed to the model.
	SearchToolName = "search_history"
)

// searchArgs is the JSON argument payload for search_history.
type searchArgs struct {
	// Query is a keyword query. Empty means no keyword filtering.
	Query string `json:"query,omitempty"`
	// Roles filters by message roles (e.g., user, assistant, tool).
	Roles []string `json:"roles,omitempty"`
	// SinceMs filters events with timestamp >= sinceMs.
	SinceMs *int64 `json:"sinceMs,omitempty"`
	// UntilMs filters events with timestamp <= untilMs.
	UntilMs *int64 `json:"untilMs,omitempty"`
	// Cursor is the pagination cursor returned by a previous call.
	Cursor string `json:"cursor,omitempty"`
	// Limit is the requested max number of results. The server will clamp it.
	Limit int `json:"limit,omitempty"`
	// MaxChars is the requested max characters per snippet. The server will clamp it.
	MaxChars int `json:"maxChars,omitempty"`
}

// searchItem is a single search hit with a bounded snippet.
type searchItem struct {
	// EventID is the stable identifier of the matched event.
	EventID string `json:"eventId"`
	// TimestampMs is the unix-ms timestamp of the event.
	TimestampMs int64 `json:"timestampMs"`
	// Role is the message role (user, assistant, tool, system).
	Role string `json:"role"`
	// Snippet is the truncated text snippet.
	Snippet string `json:"snippet"`
	// Truncated indicates whether Snippet was truncated.
	Truncated bool `json:"truncated"`
	// TotalChars is the original text length before truncation.
	TotalChars int `json:"totalChars"`
}

// searchResult is the structured output of search_history.
type searchResult struct {
	// Success indicates whether the call succeeded.
	Success bool `json:"success"`
	// Message carries an error message when Success is false.
	Message string `json:"message,omitempty"`
	// Items are the matched results.
	Items []searchItem `json:"items,omitempty"`
	// NextCursor is the cursor for the next page.
	NextCursor string `json:"nextCursor,omitempty"`
	// BudgetRemaining is the remaining invocation-scoped budget snapshot.
	BudgetRemaining *budget `json:"budgetRemaining,omitempty"`
}

// searchTool implements the search_history tool.
type searchTool struct{}

func newSearchTool() *searchTool { return &searchTool{} }

// Declaration returns the tool declaration.
func (t *searchTool) Declaration() *tool.Declaration {
	schema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"query": {
				Type:        "string",
				Description: "Keyword query. Empty means no keyword filtering",
			},
			"roles": {
				Type:        "array",
				Description: "Filter by message roles (e.g., user, assistant, tool)",
				Items:       &tool.Schema{Type: "string"},
			},
			"sinceMs":  {Type: "number", Description: "Only include events after this unix-ms timestamp"},
			"untilMs":  {Type: "number", Description: "Only include events before this unix-ms timestamp"},
			"cursor":   {Type: "string", Description: "Pagination cursor from previous call"},
			"limit":    {Type: "number", Description: "Max number of results. Server may clamp to a small value"},
			"maxChars": {Type: "number", Description: "Max characters per snippet. Server may clamp"},
		},
	}
	return &tool.Declaration{
		Name:        SearchToolName,
		Description: "Search current session history and return small snippets with stable event IDs",
		InputSchema: schema,
	}
}

// Call executes the tool with JSON arguments.
func (t *searchTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	args, err := parseSearchArgs(jsonArgs)
	if err != nil {
		return searchResult{Success: false, Message: err.Error()}, nil
	}

	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return searchResult{Success: false, Message: "no session history available"}, nil
	}

	budget := getOrInitBudget(inv)
	if budget.SearchCallsRemaining <= 0 {
		return searchResult{Success: false, Message: "history search budget exceeded", BudgetRemaining: budget}, nil
	}

	limit := clamp(args.Limit, 1, 10)
	maxChars := clamp(args.MaxChars, 50, 500)
	offset := parseSearchCursor(args.Cursor)
	roles := normalizeRoles(args.Roles)

	events := snapshotEvents(inv)
	items, spentChars := filterSearchItems(events, roles, args.Query, args.SinceMs, args.UntilMs, offset, limit, maxChars)

	if err := spendChars(budget, spentChars); err != nil {
		return searchResult{Success: false, Message: "history budget exceeded", BudgetRemaining: budget}, nil
	}
	budget.SearchCallsRemaining--

	return searchResult{
		Success:         true,
		Items:           items,
		NextCursor:      buildNextCursor(offset, len(items), len(events)),
		BudgetRemaining: budget,
	}, nil
}

type toolEventView struct {
	ID          string
	TimestampMs int64
	Role        string
	Text        string
}

func parseSearchArgs(jsonArgs []byte) (searchArgs, error) {
	var args searchArgs
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return searchArgs{}, fmt.Errorf("invalid args: %v", err)
	}
	return args, nil
}

func normalizeRoles(rs []string) map[string]struct{} {
	roles := make(map[string]struct{}, len(rs))
	for _, r := range rs {
		r = strings.TrimSpace(r)
		if r != "" {
			roles[r] = struct{}{}
		}
	}
	return roles
}

// parseSearchCursor parses a cursor string into an integer offset.
func parseSearchCursor(cursor string) int {
	if cursor == "" {
		return 0
	}
	if b, err := base64.StdEncoding.DecodeString(cursor); err == nil {
		if n, err2 := strconv.Atoi(string(b)); err2 == nil {
			return maxInt(0, n)
		}
	}
	if n, err := strconv.Atoi(cursor); err == nil {
		return maxInt(0, n)
	}
	return 0
}

func snapshotEvents(inv *agent.Invocation) []toolEventView {
	sess := inv.Session
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	events := make([]toolEventView, 0, len(sess.Events))
	for i := range sess.Events {
		e := sess.Events[i]
		ev := toolEventView{ID: e.ID, TimestampMs: toUnixMs(e.Timestamp)}
		ev.Role, ev.Text = eventMessageText(e)
		events = append(events, ev)
	}
	return events
}

func filterSearchItems(
	events []toolEventView,
	roles map[string]struct{},
	query string,
	sinceMs *int64,
	untilMs *int64,
	offset int,
	limit int,
	maxChars int,
) ([]searchItem, int) {
	q := strings.TrimSpace(query)
	qLower := strings.ToLower(q)

	since := int64(0)
	until := int64(0)
	if sinceMs != nil {
		since = *sinceMs
	}
	if untilMs != nil {
		until = *untilMs
	}

	items := make([]searchItem, 0, limit)
	spentChars := 0
	for i := offset; i < len(events); i++ {
		ev := events[i]
		if ev.Text == "" {
			continue
		}
		if since > 0 && ev.TimestampMs > 0 && ev.TimestampMs < since {
			continue
		}
		if until > 0 && ev.TimestampMs > 0 && ev.TimestampMs > until {
			continue
		}
		if len(roles) > 0 {
			if _, ok := roles[ev.Role]; !ok {
				continue
			}
		}
		if qLower != "" && !strings.Contains(strings.ToLower(ev.Text), qLower) {
			continue
		}

		snippet, truncated := truncate(ev.Text, maxChars)
		spentChars += len(snippet)
		items = append(items, searchItem{
			EventID:     ev.ID,
			TimestampMs: ev.TimestampMs,
			Role:        ev.Role,
			Snippet:     snippet,
			Truncated:   truncated,
			TotalChars:  len(ev.Text),
		})
		if len(items) >= limit {
			break
		}
	}
	return items, spentChars
}

func buildNextCursor(offset, returned, total int) string {
	nextOffset := offset + returned
	if returned == 0 || nextOffset >= total {
		return ""
	}
	b := []byte(fmt.Sprintf("%d", nextOffset))
	return base64.StdEncoding.EncodeToString(b)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
