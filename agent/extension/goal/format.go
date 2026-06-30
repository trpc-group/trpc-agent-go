//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package goal

import (
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const defaultGuidanceTemplate = `## Goal extension

You have access to session goal tools. A goal is a durable objective for this conversation, not a todo list and not a generic memory entry.

Goal tools require serial semantics. In one model response, call at most one goal tool. Do not call %s and %s in the same response; create the goal first, then continue in a later model turn before marking it complete or blocked.

Use %s only when the user explicitly asks you to keep working toward a multi-step objective across model-loop boundaries, or when their request clearly requires a persistent session objective. Do not create goals for ordinary one-turn questions.

Use %s when you need to inspect the current session goal.

Use %s to mark the active goal complete only after the objective has actually been achieved. Mark it blocked only when the same blocking condition has repeated across goal attempts and you cannot make meaningful progress without user input or an external-state change. Do not mark a goal blocked merely because the work is hard, slow, uncertain, incomplete, or would benefit from clarification.

While a goal is active, a final answer is not enough. Either continue working, or call %s with complete or blocked.`

func renderGuidance(getToolName, createToolName, updateToolName string) string {
	if getToolName == "" {
		getToolName = DefaultGetGoalToolName
	}
	if createToolName == "" {
		createToolName = DefaultCreateGoalToolName
	}
	if updateToolName == "" {
		updateToolName = DefaultUpdateGoalToolName
	}
	return fmt.Sprintf(
		defaultGuidanceTemplate,
		createToolName,
		updateToolName,
		createToolName,
		getToolName,
		updateToolName,
		updateToolName,
	)
}

// NudgeContext captures everything a NudgeFormatter needs to render the
// continuation reminder.
type NudgeContext struct {
	// AgentName is the name of the agent whose final response was blocked.
	AgentName string
	// Goal is the currently active session goal.
	Goal *Goal
	// AttemptNumber is 1 on the first nudge, 2 on the second, ...
	AttemptNumber int
	// MaxRetries is the configured enforcement budget.
	MaxRetries int
	// UpdateGoalToolName is the registered update tool name.
	UpdateGoalToolName string
}

// NudgeFormatter renders the user-role continuation reminder.
type NudgeFormatter func(ctx NudgeContext) string

// DefaultNudgeFormatter is the default continuation reminder.
func DefaultNudgeFormatter(ctx NudgeContext) string {
	var objective string
	if ctx.Goal != nil {
		objective = strings.TrimSpace(ctx.Goal.Objective)
	}
	if objective == "" {
		objective = "(unknown objective)"
	}
	updateTool := ctx.UpdateGoalToolName
	if updateTool == "" {
		updateTool = DefaultUpdateGoalToolName
	}

	return fmt.Sprintf(
		"[goal enforcement] You marked your response as final, but the session goal is still active (attempt %d of %d).\n\n"+
			"Active goal:\n%s\n\n"+
			"You must either continue working toward the goal, or call %s with status complete or blocked. "+
			"Use blocked only when the same blocking condition has repeated across goal attempts and you cannot make meaningful progress without user input or an external-state change. "+
			"Do not produce a final answer while the goal remains active.",
		ctx.AttemptNumber,
		ctx.MaxRetries,
		objective,
		updateTool,
	)
}

func insertGuidance(req *model.Request, guidance string) {
	if req == nil {
		return
	}
	guidance = strings.TrimSpace(guidance)
	if guidance == "" {
		return
	}
	msg := model.NewSystemMessage(guidance)
	idx := 0
	for idx < len(req.Messages) && req.Messages[idx].Role == model.RoleSystem {
		idx++
	}
	req.Messages = append(req.Messages, model.Message{})
	copy(req.Messages[idx+1:], req.Messages[idx:])
	req.Messages[idx] = msg
}
