//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package subagent provides tools for controlling dynamic background
// subagent runs.
package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	subagentruntime "trpc.group/trpc-go/trpc-agent-go/subagent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolSpawn  = "subagents_spawn"
	toolList   = "subagents_list"
	toolGet    = "subagents_get"
	toolCancel = "subagents_cancel"
	toolWait   = "subagents_wait"

	argAgentName      = "agent_name"
	argID             = "id"
	argTask           = "task"
	argTimeoutSeconds = "timeout_seconds"

	schemaTypeInteger = "integer"
	schemaTypeObject  = "object"
	schemaTypeString  = "string"
)

// Option configures the generated tools.
type Option func(*options)

type options struct {
	defaultAgentName        string
	runtimeState            map[string]any
	injectedContextMessages []model.Message
	allowNested             bool
}

// WithDefaultAgentName configures the agent selected by spawn when the caller
// does not provide one.
func WithDefaultAgentName(name string) Option {
	return func(opts *options) {
		opts.defaultAgentName = strings.TrimSpace(name)
	}
}

// WithRuntimeState merges static runtime state into each spawned run.
func WithRuntimeState(state map[string]any) Option {
	return func(opts *options) {
		opts.runtimeState = cloneRuntimeState(state)
	}
}

// WithInjectedContextMessages appends non-persisted context messages to each
// spawned run.
func WithInjectedContextMessages(messages []model.Message) Option {
	return func(opts *options) {
		opts.injectedContextMessages = append(
			[]model.Message(nil),
			messages...,
		)
	}
}

// WithNestedSpawns allows a subagent to spawn additional subagents.
func WithNestedSpawns(enabled bool) Option {
	return func(opts *options) {
		opts.allowNested = enabled
	}
}

// Tools contains all subagent control tools.
type Tools struct {
	state *toolState

	spawn  *spawnTool
	list   *listTool
	get    *getTool
	cancel *cancelTool
	wait   *waitTool
}

type toolState struct {
	controller subagentruntime.Controller
	options    options
}

// NewTools creates subagent control tools.
func NewTools(
	controller subagentruntime.Controller,
	opts ...Option,
) Tools {
	options := options{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	state := &toolState{
		controller: controller,
		options:    options,
	}
	t := Tools{
		state: state,
	}
	t.spawn = &spawnTool{state: state}
	t.list = &listTool{state: state}
	t.get = &getTool{state: state}
	t.cancel = &cancelTool{state: state}
	t.wait = &waitTool{state: state}
	return t
}

// SetController updates the controller used by all tools.
func (t *Tools) SetController(controller subagentruntime.Controller) {
	if t == nil {
		return
	}
	if t.state == nil {
		t.state = &toolState{}
	}
	t.state.controller = controller
}

// All returns all tool declarations.
func (t *Tools) All() []tool.Tool {
	if t == nil {
		return nil
	}
	return []tool.Tool{
		t.spawn,
		t.list,
		t.get,
		t.cancel,
		t.wait,
	}
}

type spawnTool struct {
	state *toolState
}

type listTool struct {
	state *toolState
}

type getTool struct {
	state *toolState
}

type cancelTool struct {
	state *toolState
}

type waitTool struct {
	state *toolState
}

type spawnInput struct {
	Task           string `json:"task"`
	AgentName      string `json:"agent_name"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type runIDInput struct {
	ID             string `json:"id"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type listResult struct {
	Runs []subagentruntime.Run `json:"runs,omitempty"`
}

func (t *spawnTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolSpawn,
		Description: "Spawn one dynamic background subagent for " +
			"the current session. It returns immediately with " +
			"a run id.",
		InputSchema: &tool.Schema{
			Type:     schemaTypeObject,
			Required: []string{argTask},
			Properties: map[string]*tool.Schema{
				argTask: {
					Type: schemaTypeString,
					Description: "Delegated task for the " +
						"background subagent.",
				},
				argAgentName: {
					Type: schemaTypeString,
					Description: "Optional registered agent " +
						"name to run.",
				},
				argTimeoutSeconds: {
					Type: schemaTypeInteger,
					Description: "Optional timeout in seconds " +
						"for the delegated run.",
				},
			},
		},
	}
}

func (t *spawnTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	state, err := requireTools(t.state)
	if err != nil {
		return nil, err
	}
	if !state.options.allowNested && isNestedSubagent(ctx) {
		return nil, fmt.Errorf(
			"subagent: nested subagent spawn is not supported",
		)
	}

	var in spawnInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	userID, sessionID, err := currentContext(ctx)
	if err != nil {
		return nil, err
	}
	agentName := strings.TrimSpace(in.AgentName)
	if agentName == "" {
		agentName = state.options.defaultAgentName
	}
	run, err := state.controller.Spawn(ctx, subagentruntime.SpawnRequest{
		OwnerUserID:             userID,
		ParentSessionID:         sessionID,
		AgentName:               agentName,
		Task:                    in.Task,
		Timeout:                 secondsDuration(in.TimeoutSeconds),
		RuntimeState:            cloneRuntimeState(state.options.runtimeState),
		InjectedContextMessages: state.options.injectedContextMessages,
	})
	if err != nil {
		return nil, err
	}
	return run, nil
}

func (t *listTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolList,
		Description: "List dynamic background subagents created " +
			"from the current session.",
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
		},
	}
}

func (t *listTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	state, err := requireTools(t.state)
	if err != nil {
		return nil, err
	}
	if err := validateEmptyArgs(args); err != nil {
		return nil, err
	}
	userID, sessionID, err := currentContext(ctx)
	if err != nil {
		return nil, err
	}
	runs, err := state.controller.List(ctx, subagentruntime.ListFilter{
		OwnerUserID:     userID,
		ParentSessionID: sessionID,
	})
	if err != nil {
		return nil, err
	}
	return listResult{Runs: runs}, nil
}

func (t *getTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolGet,
		Description: "Get the latest status and result for one " +
			"dynamic background subagent run.",
		InputSchema: runIDSchema(),
	}
}

func (t *getTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	state, err := requireTools(t.state)
	if err != nil {
		return nil, err
	}
	runID, userID, err := decodeRunIDArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	run, err := state.controller.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !sameOwner(run, userID) {
		return nil, subagentruntime.ErrRunNotFound
	}
	return run, nil
}

func (t *cancelTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolCancel,
		Description: "Cancel one dynamic background subagent run. " +
			"This is best-effort.",
		InputSchema: runIDSchema(),
	}
}

func (t *cancelTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	state, err := requireTools(t.state)
	if err != nil {
		return nil, err
	}
	runID, userID, err := decodeRunIDArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	run, err := state.controller.Get(ctx, runID)
	if err != nil {
		return nil, err
	}
	if !sameOwner(run, userID) {
		return nil, subagentruntime.ErrRunNotFound
	}
	canceled, _, err := state.controller.Cancel(ctx, runID)
	if err != nil {
		return nil, err
	}
	return canceled, nil
}

func (t *waitTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolWait,
		Description: "Wait until one dynamic background subagent " +
			"run reaches a terminal status.",
		InputSchema: waitSchema(),
	}
}

func (t *waitTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	state, err := requireTools(t.state)
	if err != nil {
		return nil, err
	}
	in, userID, err := decodeWaitArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	run, err := state.controller.Get(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if !sameOwner(run, userID) {
		return nil, subagentruntime.ErrRunNotFound
	}
	waitCtx := ctx
	var cancel context.CancelFunc
	if in.TimeoutSeconds > 0 {
		waitCtx, cancel = context.WithTimeout(
			ctx,
			secondsDuration(in.TimeoutSeconds),
		)
		defer cancel()
	}
	return state.controller.Wait(waitCtx, in.ID)
}

func runIDSchema() *tool.Schema {
	return &tool.Schema{
		Type:     schemaTypeObject,
		Required: []string{argID},
		Properties: map[string]*tool.Schema{
			argID: {
				Type:        schemaTypeString,
				Description: "Subagent run id returned by spawn.",
			},
		},
	}
}

func waitSchema() *tool.Schema {
	schema := runIDSchema()
	schema.Properties[argTimeoutSeconds] = &tool.Schema{
		Type:        schemaTypeInteger,
		Description: "Optional wait timeout in seconds.",
	}
	return schema
}

func requireTools(state *toolState) (*toolState, error) {
	if state == nil || state.controller == nil {
		return nil, fmt.Errorf("subagent: controller unavailable")
	}
	return state, nil
}

func validateEmptyArgs(args []byte) error {
	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" || trimmed == "{}" {
		return nil
	}
	var ignored map[string]any
	return json.Unmarshal(args, &ignored)
}

func decodeRunIDArgs(
	ctx context.Context,
	args []byte,
) (string, string, error) {
	var in runIDInput
	if err := json.Unmarshal(args, &in); err != nil {
		return "", "", err
	}
	userID, _, err := currentContext(ctx)
	if err != nil {
		return "", "", err
	}
	runID := strings.TrimSpace(in.ID)
	if runID == "" {
		return "", "", fmt.Errorf("subagent: empty run id")
	}
	return runID, userID, nil
}

func decodeWaitArgs(
	ctx context.Context,
	args []byte,
) (runIDInput, string, error) {
	var in runIDInput
	if err := json.Unmarshal(args, &in); err != nil {
		return runIDInput{}, "", err
	}
	userID, _, err := currentContext(ctx)
	if err != nil {
		return runIDInput{}, "", err
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		return runIDInput{}, "", fmt.Errorf("subagent: empty run id")
	}
	return in, userID, nil
}

func currentContext(ctx context.Context) (string, string, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return "", "", fmt.Errorf(
			"subagent: current session context is unavailable",
		)
	}
	userID := strings.TrimSpace(inv.Session.UserID)
	if userID == "" {
		return "", "", fmt.Errorf(
			"subagent: current user id is unavailable",
		)
	}
	sessionID := strings.TrimSpace(inv.Session.ID)
	if sessionID == "" {
		return "", "", fmt.Errorf(
			"subagent: current session id is unavailable",
		)
	}
	return userID, sessionID, nil
}

func isNestedSubagent(ctx context.Context) bool {
	nested, ok := agent.GetRuntimeStateValueFromContext[bool](
		ctx,
		subagentruntime.RuntimeStateKeyRun,
	)
	return ok && nested
}

func sameOwner(run *subagentruntime.Run, userID string) bool {
	return run != nil &&
		strings.TrimSpace(run.OwnerUserID) == strings.TrimSpace(userID)
}

func secondsDuration(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

func cloneRuntimeState(state map[string]any) map[string]any {
	if len(state) == 0 {
		return nil
	}
	out := make(map[string]any, len(state))
	for key, value := range state {
		out[key] = value
	}
	return out
}
