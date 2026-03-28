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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

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

			masked := sess.MaskEvents(input.EventIDs)
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

			return CheckBudgetOutput{
				TotalEvents:   total,
				VisibleEvents: total - masked,
				MaskedEvents:  masked,
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

// Tools returns all context management tools as a convenience.
func Tools() []tool.Tool {
	return []tool.Tool{
		NewDeleteContextTool(),
		NewCheckBudgetTool(),
	}
}
