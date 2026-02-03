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
	SearchToolName = "search_history"
)

type SearchArgs struct {
	Query    string   `json:"query,omitempty"`
	Roles    []string `json:"roles,omitempty"`
	SinceMs  *int64   `json:"sinceMs,omitempty"`
	UntilMs  *int64   `json:"untilMs,omitempty"`
	Cursor   string   `json:"cursor,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	MaxChars int      `json:"maxChars,omitempty"`
}

type SearchItem struct {
	EventID     string `json:"eventId"`
	TimestampMs int64  `json:"timestampMs"`
	Role        string `json:"role"`
	Snippet     string `json:"snippet"`
	Truncated   bool   `json:"truncated"`
	TotalChars  int    `json:"totalChars"`
}

type SearchResult struct {
	Success          bool        `json:"success"`
	Message          string      `json:"message,omitempty"`
	Items            []SearchItem `json:"items,omitempty"`
	NextCursor       string      `json:"nextCursor,omitempty"`
	BudgetRemaining  *Budget     `json:"budgetRemaining,omitempty"`
}

type SearchTool struct{}

func NewSearchTool() *SearchTool { return &SearchTool{} }

func (t *SearchTool) Declaration() *tool.Declaration {
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
				Items: &tool.Schema{Type: "string"},
			},
			"sinceMs": {Type: "number", Description: "Only include events after this unix-ms timestamp"},
			"untilMs": {Type: "number", Description: "Only include events before this unix-ms timestamp"},
			"cursor":  {Type: "string", Description: "Pagination cursor from previous call"},
			"limit":   {Type: "number", Description: "Max number of results. Server may clamp to a small value"},
			"maxChars": {Type: "number", Description: "Max characters per snippet. Server may clamp"},
		},
	}
	return &tool.Declaration{
		Name:        SearchToolName,
		Description: "Search current session history and return small snippets with stable event IDs",
		InputSchema: schema,
	}
}

func (t *SearchTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var args SearchArgs
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return SearchResult{Success: false, Message: fmt.Sprintf("invalid args: %v", err)}, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return SearchResult{Success: false, Message: "no session history available"}, nil
	}
	budget := getOrInitBudget(inv)
	if budget.SearchCallsRemaining <= 0 {
		return SearchResult{Success: false, Message: "history search budget exceeded", BudgetRemaining: budget}, nil
	}

	limit := clamp(args.Limit, 1, 10)
	maxChars := clamp(args.MaxChars, 50, 500)

	roles := make(map[string]struct{}, len(args.Roles))
	for _, r := range args.Roles {
		r = strings.TrimSpace(r)
		if r != "" {
			roles[r] = struct{}{}
		}
	}

	// Cursor: base64 encoded integer offset.
	offset := 0
	if args.Cursor != "" {
		if b, err := base64.StdEncoding.DecodeString(args.Cursor); err == nil {
			if n, err2 := strconv.Atoi(string(b)); err2 == nil {
				offset = n
			}
		}
		// Fallback: try plain int.
		if offset == 0 {
			if n, err := strconv.Atoi(args.Cursor); err == nil {
				offset = n
			}
		}
		if offset < 0 {
			offset = 0
		}
	}

	sess := inv.Session
	sess.EventMu.RLock()
	events := make([]toolEventView, 0, len(sess.Events))
	for i := range sess.Events {
		e := sess.Events[i]
		ev := toolEventView{ID: e.ID, TimestampMs: toUnixMs(e.Timestamp)}
		ev.Role, ev.Text = eventMessageText(e)
		events = append(events, ev)
	}
	sess.EventMu.RUnlock()

	query := strings.TrimSpace(args.Query)
	since := int64(0)
	until := int64(0)
	if args.SinceMs != nil {
		since = *args.SinceMs
	}
	if args.UntilMs != nil {
		until = *args.UntilMs
	}

	// Filter in chronological order.
	items := make([]SearchItem, 0, limit)
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
		if query != "" && !strings.Contains(strings.ToLower(ev.Text), strings.ToLower(query)) {
			continue
		}

		snippet, truncated := truncate(ev.Text, maxChars)
		spentChars += len(snippet)
		items = append(items, SearchItem{
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

	if err := spendChars(budget, spentChars); err != nil {
		return SearchResult{Success: false, Message: "history budget exceeded", BudgetRemaining: budget}, nil
	}
	budget.SearchCallsRemaining--

	next := ""
	if offset+len(items) < len(events) {
		// Use plain int encoded in base64 to avoid tool hallucination issues.
		b := []byte(fmt.Sprintf("%d", offset+len(items)))
		next = base64.StdEncoding.EncodeToString(b)
	}

	return SearchResult{
		Success:         true,
		Items:           items,
		NextCursor:      next,
		BudgetRemaining: budget,
	}, nil
}

type toolEventView struct {
	ID          string
	TimestampMs int64
	Role        string
	Text        string
}
