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
	// GetEventsToolName is the name of the tool exposed to the model.
	GetEventsToolName = "get_history_events"
)

// getEventsArgs is the JSON argument payload for get_history_events.
type getEventsArgs struct {
	// EventIDs are the event IDs to retrieve.
	EventIDs []string `json:"eventIds"`
	// MaxChars is the requested max characters per event content. The server will clamp it.
	MaxChars int `json:"maxChars,omitempty"`
}

// historyEvent is a bounded view of a single session event.
type historyEvent struct {
	// EventID is the stable identifier of the event.
	EventID string `json:"eventId"`
	// TimestampMs is the unix-ms timestamp of the event.
	TimestampMs int64 `json:"timestampMs"`
	// Role is the message role (user, assistant, tool, system).
	Role string `json:"role"`
	// Content is the bounded event content.
	Content string `json:"content"`
	// Truncated indicates whether Content was truncated.
	Truncated bool `json:"truncated"`
	// TotalChars is the original text length before truncation.
	TotalChars int `json:"totalChars"`
}

// getEventsResult is the structured output of get_history_events.
type getEventsResult struct {
	// Success indicates whether the call succeeded.
	Success bool `json:"success"`
	// Message carries an error message when Success is false.
	Message string `json:"message,omitempty"`
	// Items are the retrieved events.
	Items []historyEvent `json:"items,omitempty"`
	// BudgetRemaining is the remaining invocation-scoped budget snapshot.
	BudgetRemaining *budget `json:"budgetRemaining,omitempty"`
}

// getEventsTool implements the get_history_events tool.
type getEventsTool struct{}

func newGetEventsTool() *getEventsTool { return &getEventsTool{} }

// Declaration returns the tool declaration.
func (t *getEventsTool) Declaration() *tool.Declaration {
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

// Call executes the tool with JSON arguments.
func (t *getEventsTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	var args getEventsArgs
	if err := json.Unmarshal(jsonArgs, &args); err != nil {
		return getEventsResult{Success: false, Message: fmt.Sprintf("invalid args: %v", err)}, nil
	}
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return getEventsResult{Success: false, Message: "no session history available"}, nil
	}
	budget := getOrInitBudget(inv)
	if budget.GetCallsRemaining <= 0 {
		return getEventsResult{Success: false, Message: "history get budget exceeded", BudgetRemaining: budget}, nil
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
		return getEventsResult{Success: false, Message: "eventIds is empty", BudgetRemaining: budget}, nil
	}

	sess := inv.Session
	// Build a map for quick lookup.
	sess.EventMu.RLock()
	byID := make(map[string]int, len(sess.Events))
	for i := range sess.Events {
		byID[sess.Events[i].ID] = i
	}

	items := make([]historyEvent, 0, len(ids))
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
		items = append(items, historyEvent{
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
		return getEventsResult{Success: false, Message: "history budget exceeded", BudgetRemaining: budget}, nil
	}
	budget.GetCallsRemaining--

	return getEventsResult{Success: true, Items: items, BudgetRemaining: budget}, nil
}
