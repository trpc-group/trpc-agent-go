//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package goal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolKindGet = iota
	toolKindCreate
	toolKindUpdate
)

const (
	defaultGetGoalToolDescription    = "Read the current session goal, if any. Use this before deciding whether a persistent goal is active."
	defaultCreateGoalToolDescription = "Create a session goal for a user-requested multi-step objective that should remain active until it is completed or blocked. Do not call this for ordinary one-turn requests."
	defaultUpdateGoalToolDescription = "Mark the active session goal complete or blocked. Use complete only when the objective has actually been achieved. Use blocked only after the same blocking condition has repeated across goal attempts and progress cannot continue without user input or an external-state change."
)

type createGoalInput struct {
	Objective string `json:"objective"`
}

type updateGoalInput struct {
	Status GoalStatus `json:"status"`
}

type goalToolOutput struct {
	Message string `json:"message"`
	Goal    *Goal  `json:"goal,omitempty"`
}

type goalTool struct {
	kind        int
	name        string
	description string
	stateKey    string
}

var _ tool.CallableTool = (*goalTool)(nil)

func newGoalTool(kind int, name string, stateKey string) *goalTool {
	t := &goalTool{kind: kind, name: name, stateKey: stateKey}
	switch kind {
	case toolKindGet:
		if t.name == "" {
			t.name = DefaultGetGoalToolName
		}
		t.description = defaultGetGoalToolDescription
	case toolKindCreate:
		if t.name == "" {
			t.name = DefaultCreateGoalToolName
		}
		t.description = defaultCreateGoalToolDescription
	case toolKindUpdate:
		if t.name == "" {
			t.name = DefaultUpdateGoalToolName
		}
		t.description = defaultUpdateGoalToolDescription
	}
	if t.stateKey == "" {
		t.stateKey = DefaultStateKey
	}
	return t
}

func (t *goalTool) Declaration() *tool.Declaration {
	switch t.kind {
	case toolKindGet:
		return &tool.Declaration{
			Name:        t.name,
			Description: t.description,
			InputSchema: &tool.Schema{
				Type:                 "object",
				Properties:           map[string]*tool.Schema{},
				AdditionalProperties: false,
			},
			OutputSchema: goalOutputSchema(),
		}
	case toolKindCreate:
		return &tool.Declaration{
			Name:        t.name,
			Description: t.description,
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"objective": {
						Type:        "string",
						Description: "The concrete objective that should remain active for the session until completed or blocked.",
					},
				},
				Required: []string{"objective"},
			},
			OutputSchema: goalOutputSchema(),
		}
	case toolKindUpdate:
		return &tool.Declaration{
			Name:        t.name,
			Description: t.description,
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"status": {
						Type:        "string",
						Description: "Terminal goal status. Use complete only when the objective is achieved. Use blocked only after the same blocking condition repeats and the agent is at an impasse.",
						Enum:        []any{string(GoalStatusComplete), string(GoalStatusBlocked)},
					},
				},
				Required: []string{"status"},
			},
			OutputSchema: goalOutputSchema(),
		}
	default:
		return &tool.Declaration{Name: t.name, Description: t.description}
	}
}

func goalOutputSchema() *tool.Schema {
	return &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"message": {Type: "string"},
			"goal": {
				Type: "object",
				Properties: map[string]*tool.Schema{
					"id":               {Type: "string"},
					"objective":        {Type: "string"},
					"status":           {Type: "string"},
					"created_at_unix":  {Type: "number"},
					"updated_at_unix":  {Type: "number"},
					"terminal_at_unix": {Type: "number"},
				},
			},
		},
		Required: []string{"message"},
	}
}

func (t *goalTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	switch t.kind {
	case toolKindGet:
		return t.callGet(ctx)
	case toolKindCreate:
		return t.callCreate(ctx, jsonArgs)
	case toolKindUpdate:
		return t.callUpdate(ctx, jsonArgs)
	default:
		return nil, errors.New("goal: unknown tool kind")
	}
}

func (t *goalTool) callGet(ctx context.Context) (any, error) {
	inv, _ := agent.InvocationFromContext(ctx)
	g, ok, err := GetGoalWithStateKey(invocationSession(inv), t.stateKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return goalToolOutput{Message: "No session goal is set."}, nil
	}
	return goalToolOutput{Message: "Current session goal loaded.", Goal: g}, nil
}

func (t *goalTool) callCreate(ctx context.Context, jsonArgs []byte) (any, error) {
	in, err := decodeCreateGoalInput(jsonArgs)
	if err != nil {
		return nil, err
	}
	inv, _ := agent.InvocationFromContext(ctx)
	sess := invocationSession(inv)
	if sess == nil {
		return nil, errors.New("create_goal: invocation session is required")
	}
	if existing, ok, err := GetGoalWithStateKey(sess, t.stateKey); err != nil {
		return nil, err
	} else if ok && existing.Status == GoalStatusActive {
		return nil, fmt.Errorf("create_goal: active goal already exists: %s", existing.Objective)
	}
	g, err := NewActiveGoal(in.Objective)
	if err != nil {
		return nil, fmt.Errorf("create_goal: %w", err)
	}
	if err := writeGoalToSession(sess, t.stateKey, g); err != nil {
		return nil, err
	}
	return goalToolOutput{Message: "Session goal created.", Goal: g}, nil
}

func (t *goalTool) callUpdate(ctx context.Context, jsonArgs []byte) (any, error) {
	in, err := decodeUpdateGoalInput(jsonArgs)
	if err != nil {
		return nil, err
	}
	inv, _ := agent.InvocationFromContext(ctx)
	sess := invocationSession(inv)
	if sess == nil {
		return nil, errors.New("update_goal: invocation session is required")
	}
	g, ok, err := GetGoalWithStateKey(sess, t.stateKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("update_goal: no active goal exists")
	}
	if g.Status != GoalStatusActive {
		return nil, fmt.Errorf("update_goal: goal is already terminal: %s", g.Status)
	}
	now := time.Now().UTC().Unix()
	g.Status = in.Status
	g.UpdatedAtUnix = now
	g.TerminalAtUnix = &now
	if err := writeGoalToSession(sess, t.stateKey, g); err != nil {
		return nil, err
	}
	return goalToolOutput{Message: "Session goal updated.", Goal: g}, nil
}

func (t *goalTool) StateDeltaForInvocation(
	inv *agent.Invocation,
	_ string,
	_ []byte,
	_ []byte,
) map[string][]byte {
	if t.kind == toolKindGet {
		return nil
	}
	g, ok, err := GetGoalWithStateKey(invocationSession(inv), t.stateKey)
	if err != nil || !ok {
		return nil
	}
	raw, err := encodeGoal(g)
	if err != nil {
		return nil
	}
	return map[string][]byte{t.stateKey: raw}
}

func decodeCreateGoalInput(raw []byte) (createGoalInput, error) {
	var in createGoalInput
	if len(bytes.TrimSpace(raw)) == 0 {
		return in, errors.New("create_goal: empty arguments")
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, fmt.Errorf("create_goal: decode arguments: %w", err)
	}
	in.Objective = strings.TrimSpace(in.Objective)
	if in.Objective == "" {
		return in, errors.New("create_goal: objective is required")
	}
	return in, nil
}

func decodeUpdateGoalInput(raw []byte) (updateGoalInput, error) {
	var in updateGoalInput
	if len(bytes.TrimSpace(raw)) == 0 {
		return in, errors.New("update_goal: empty arguments")
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, fmt.Errorf("update_goal: decode arguments: %w", err)
	}
	switch in.Status {
	case GoalStatusComplete, GoalStatusBlocked:
		return in, nil
	case GoalStatusActive:
		return in, errors.New("update_goal: status must be terminal: complete or blocked")
	default:
		return in, fmt.Errorf("update_goal: invalid status %q", in.Status)
	}
}
