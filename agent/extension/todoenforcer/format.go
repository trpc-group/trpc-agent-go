//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package todoenforcer

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/tool/todo"
)

// NudgeContext captures everything a NudgeFormatter needs to
// render the reminder message. Implementations must be
// deterministic and side-effect free: the formatter can be
// invoked twice (once for logging, once for the actual injection)
// and must not mutate the input slices.
type NudgeContext struct {
	// AgentName is the name of the agent whose final response was
	// blocked.
	AgentName string

	// Pending lists the items still in StatusPending. The slice is
	// owned by the enforcer and must not be modified.
	Pending []todo.Item

	// InProgress lists the items still in StatusInProgress.
	InProgress []todo.Item

	// AttemptNumber is 1 on the first nudge, 2 on the second, …,
	// up to MaxRetries. Surfaced so the formatter can convey
	// "attempt N of M" to the model.
	AttemptNumber int

	// MaxRetries echoes the configured budget for the formatter
	// to embed in its message.
	MaxRetries int

	// TodoToolName is the registered name of the todo-write tool.
	// The default formatter quotes this so the model sees the
	// exact name even when the underlying todo.Tool was supplied
	// via WithTodoTool(todo.New(todo.WithToolName(...))).
	TodoToolName string

	// DeclareBlockerToolName is the registered name of
	// todo_declare_blocker. The default formatter quotes this so
	// the model sees the exact name even when it is overridden
	// via WithDeclareBlockerToolName.
	DeclareBlockerToolName string
}

// NudgeFormatter renders the reminder body. The role is fixed by
// the enforcer (RoleUser) — only the text is formatter-controlled.
//
// Returning the empty string opts into a "silent block": the
// pending flag is still consumed but no message is appended,
// useful for tests or operators who prefer enforcement to be
// invisible to the model.
type NudgeFormatter func(ctx NudgeContext) string

// DefaultNudgeFormatter is the formatter used when
// WithNudgeFormatter is not specified. The wording is intentional:
//
//   - explicit about the contract violation (the model "marked
//     its response as final but items remain") — politely-worded
//     reminders are easy for capable models to ignore;
//   - structured: in-progress before pending, so the model
//     can pick up where it left off without re-planning;
//   - prescriptive about the two valid next actions, with the
//     exact tool names a model can invoke. The escape hatch is
//     framed as a *declaration of an external blocker*, NOT as
//     "give up if it's hard" — capable models honour this
//     framing reliably and only invoke todo_declare_blocker on
//     genuine missing-precondition cases.
//
// Plain ASCII to avoid tokenizer edge cases on smaller open-source
// models; no embedded JSON because experiments showed some models
// start replying with JSON when the prior message looks
// structured, which interfered with downstream prompts.
func DefaultNudgeFormatter(ctx NudgeContext) string {
	var b strings.Builder
	fmt.Fprintf(&b,
		"[todo enforcement] You marked your response as final, but the todo list "+
			"still has open items (attempt %d of %d).\n\n",
		ctx.AttemptNumber, ctx.MaxRetries,
	)
	if len(ctx.InProgress) > 0 {
		b.WriteString("Currently in progress:\n")
		for _, it := range ctx.InProgress {
			fmt.Fprintf(&b, "  - %s (%s)\n", it.ActiveForm, it.Content)
		}
	}
	if len(ctx.Pending) > 0 {
		b.WriteString("Still pending:\n")
		for _, it := range ctx.Pending {
			fmt.Fprintf(&b, "  - %s\n", it.Content)
		}
	}
	declareBlockerName := ctx.DeclareBlockerToolName
	if declareBlockerName == "" {
		declareBlockerName = DefaultDeclareBlockerToolName
	}
	todoToolName := ctx.TodoToolName
	if todoToolName == "" {
		todoToolName = todo.DefaultToolName
	}
	fmt.Fprintf(&b,
		"\nYou must either:\n"+
			"  1) continue executing — pick the next item, do the work, then call "+
			"%s to update the list, or\n"+
			"  2) call %s ONLY if an objective external blocker prevents further "+
			"progress (missing user permission or credentials, an ambiguous "+
			"requirement that needs the user to clarify, infrastructure not yet "+
			"provisioned, a sensitive decision that must be made by the user). "+
			"In that case, after the call you may produce a final message "+
			"explaining what input is missing.\n"+
			"Do NOT use option 2 to give up on hard but tractable work, and do "+
			"NOT produce a final answer while items remain open.",
		todoToolName, declareBlockerName,
	)
	return b.String()
}

// splitByStatus partitions items into in-progress and pending
// buckets, dropping completed entries. Unknown non-completed
// statuses are treated as pending so a corrupted or externally
// written state entry remains visible/actionable instead of
// causing an invisible enforcement loop. Stable order is preserved
// within each bucket — the model sees them in the order it wrote
// them, which makes the nudge feel like a continuation rather
// than a re-ordered scolding.
func splitByStatus(items []todo.Item) (inProgress, pending []todo.Item) {
	for _, it := range items {
		switch it.Status {
		case todo.StatusInProgress:
			inProgress = append(inProgress, it)
		case todo.StatusPending:
			pending = append(pending, it)
		case todo.StatusCompleted:
			// terminal: intentionally omitted
		default:
			pending = append(pending, it)
		}
	}
	return
}

// hasOpenItems is the gate AfterModel uses to decide whether to
// fire enforcement at all. Cheaper than splitByStatus when we only
// need the boolean. Unknown statuses are open for the same reason
// splitByStatus keeps them visible as pending.
func hasOpenItems(items []todo.Item) bool {
	for _, it := range items {
		if it.Status != todo.StatusCompleted {
			return true
		}
	}
	return false
}
