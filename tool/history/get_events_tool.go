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
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	GetEventsToolName = "get_history_events"
)

type GetEventsArgs struct {
	EventIDs  []string `json:"eventIds"`
	MaxChars  int      `json:"maxChars,omitempty"`
}

type HistoryEvent struct {
	EventID     string `json:"eventId"`
	TimestampMs int64  `json:"timestampMs"`
	Role        string `json:"role"`
	Content     string `json:"content"`
	Truncated   bool   `json:"truncated"`
	TotalChars  int    `json:"totalChars"`
}

type GetEventsResult struct {
	Success         bool          `json:"success"`
	Message         string        `json:"message,omitempty"`
	Items           []HistoryEvent `json:"items,omitempty"`
	BudgetRemaining *Budget       `json:"budgetRemaining,omitempty"`
}

type GetEventsTool struct{}

func NewGetEventsTool() *GetEventsTool { return &GetEventsTool{} }

func (t *GetEventsTool) Declaration() *tool.Declaration {
	schema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"eventIds": {
				Type:        "array",
				Description: "Event IDs to retrieve. Server may clamp the count",
				Items:       &tool.Schema{Type: "string"},
			},
			"maxChars": {
				Type:        "number",
				Description: "Max characters per event content. Server may clamp",
			},
		},
		Required: []string{"eventIds"},
	}
	return &tool.Declaration{
		Name:        GetEventsToolName,
		Description: "Get bounded event content from current session by event ID",
		InputSchema: schema,
	}
}

func (t *GetEventsTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var args GetEventsArgs
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return GetEventsResult{Success: false, Message: fmt.Sprintf("invalid args: %v", err)}, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return GetEventsResult{Success: false, Message: "no session history available"}, nil
	}
	budget := getOrInitBudget(inv)
	if budget.GetCallsRemaining <= 0 {
		return GetEventsResult{Success: false, Message: "history get budget exceeded", BudgetRemaining: budget}, nil
	}

	maxChars := clamp(args.MaxChars, 200, 3000)

	// De-dup and clamp ids.
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(args.EventIDs))
	for _, id := range args.EventIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		if len(ids) >= 3 {
			break
		}
	}
	if len(ids) == 0 {
		return GetEventsResult{Success: false, Message: "eventIds is empty", BudgetRemaining: budget}, nil
	}

	sess := inv.Session
	// Build a map for quick lookup.
	sess.EventMu.RLock()
	byID := make(map[string]int, len(sess.Events))
	for i := range sess.Events {
		byID[sess.Events[i].ID] = i
	}

	items := make([]HistoryEvent, 0, len(ids))
	spent := 0
	for _, id := range ids {
		idx, ok := byID[id]
		if !ok {
			continue
		}
		ev := sess.Events[idx]
		role, txt := eventMessageText(ev)
		if txt == "" {
			continue
		}
		content, truncated := truncate(txt, maxChars)
		spent += len(content)
		items = append(items, HistoryEvent{
			EventID:     ev.ID,
			TimestampMs: toUnixMs(ev.Timestamp),
			Role:        role,
			Content:     content,
			Truncated:   truncated,
			TotalChars:  len(txt),
		})
	}
	sess.EventMu.RUnlock()

	if err := spendChars(budget, spent); err != nil {
		return GetEventsResult{Success: false, Message: "history budget exceeded", BudgetRemaining: budget}, nil
	}
	budget.GetCallsRemaining--

	return GetEventsResult{Success: true, Items: items, BudgetRemaining: budget}, nil
}
