//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package subagentrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	openclawsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolSubagentsSpawn  = "subagents_spawn"
	toolSubagentsList   = "subagents_list"
	toolSubagentsGet    = "subagents_get"
	toolSubagentsCancel = "subagents_cancel"
	toolSubagentsWait   = "subagents_wait"

	toolSessionsSpawn  = "sessions_spawn"
	toolSessionsList   = "sessions_list"
	toolSessionsGet    = "sessions_get"
	toolSessionsCancel = "sessions_cancel"

	argID             = "id"
	argTask           = "task"
	argTimeoutSeconds = "timeout_seconds"

	schemaTypeInteger = "integer"
	schemaTypeObject  = "object"
	schemaTypeString  = "string"
)

type Tools struct {
	spawn  *spawnTool
	list   *listTool
	get    *getTool
	cancel *cancelTool
	wait   *waitTool

	spawnAlias  *spawnTool
	listAlias   *listTool
	getAlias    *getTool
	cancelAlias *cancelTool
}

func NewTools(svc *Service) Tools {
	return Tools{
		spawn:       &spawnTool{name: toolSubagentsSpawn, svc: svc},
		list:        &listTool{name: toolSubagentsList, svc: svc},
		get:         &getTool{name: toolSubagentsGet, svc: svc},
		cancel:      &cancelTool{name: toolSubagentsCancel, svc: svc},
		wait:        &waitTool{name: toolSubagentsWait, svc: svc},
		spawnAlias:  &spawnTool{name: toolSessionsSpawn, alias: true, svc: svc},
		listAlias:   &listTool{name: toolSessionsList, alias: true, svc: svc},
		getAlias:    &getTool{name: toolSessionsGet, alias: true, svc: svc},
		cancelAlias: &cancelTool{name: toolSessionsCancel, alias: true, svc: svc},
	}
}

func (t *Tools) SetService(svc *Service) {
	if t == nil {
		return
	}
	for _, item := range []serviceAwareTool{
		t.spawn,
		t.list,
		t.get,
		t.cancel,
		t.wait,
		t.spawnAlias,
		t.listAlias,
		t.getAlias,
		t.cancelAlias,
	} {
		if item != nil {
			item.setService(svc)
		}
	}
}

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
		t.spawnAlias,
		t.listAlias,
		t.getAlias,
		t.cancelAlias,
	}
}

type serviceAwareTool interface {
	setService(svc *Service)
}

type spawnTool struct {
	name  string
	alias bool
	svc   *Service
}

type listTool struct {
	name  string
	alias bool
	svc   *Service
}

type getTool struct {
	name  string
	alias bool
	svc   *Service
}

type cancelTool struct {
	name  string
	alias bool
	svc   *Service
}

type waitTool struct {
	name string
	svc  *Service
}

type spawnInput struct {
	Task           string `json:"task"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type runIDInput struct {
	ID             string `json:"id"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type listResult struct {
	Runs []openclawsubagent.Run `json:"runs,omitempty"`
}

func (t *spawnTool) setService(svc *Service) {
	t.svc = svc
}

func (t *listTool) setService(svc *Service) {
	t.svc = svc
}

func (t *getTool) setService(svc *Service) {
	t.svc = svc
}

func (t *cancelTool) setService(svc *Service) {
	t.svc = svc
}

func (t *waitTool) setService(svc *Service) {
	t.svc = svc
}

func (t *spawnTool) Declaration() *tool.Declaration {
	description := "Spawn one OpenClaw background subagent for " +
		"the current session. Use this for long-running work, " +
		"parallelizable work, or independent verification. It " +
		"returns immediately with a run id."
	if t.alias {
		description = "Compatibility alias for " +
			toolSubagentsSpawn + ". " + description
	}
	return &tool.Declaration{
		Name:        t.name,
		Description: description,
		InputSchema: &tool.Schema{
			Type:     schemaTypeObject,
			Required: []string{argTask},
			Properties: map[string]*tool.Schema{
				argTask: {
					Type: schemaTypeString,
					Description: "Delegated task for the " +
						"background subagent.",
				},
				argTimeoutSeconds: {
					Type: schemaTypeInteger,
					Description: "Optional timeout in " +
						"seconds for the delegated run.",
				},
			},
		},
	}
}

func (t *spawnTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.svc == nil {
		return nil, fmt.Errorf("subagent: service unavailable")
	}
	if isNestedSubagent(ctx) {
		return nil, fmt.Errorf(
			"subagent: nested subagent spawn is not supported",
		)
	}

	var in spawnInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	userID, sess, err := currentContext(ctx)
	if err != nil {
		return nil, err
	}
	delivery, err := outbound.ResolveTarget(
		ctx,
		outbound.DeliveryTarget{},
	)
	if err != nil {
		return nil, fmt.Errorf(
			"subagent: resolve delivery target: %w",
			err,
		)
	}

	return t.svc.Spawn(ctx, SpawnRequest{
		OwnerUserID:     userID,
		ParentSessionID: sess.ID,
		Task:            in.Task,
		TimeoutSeconds:  in.TimeoutSeconds,
		Delivery: deliveryTarget{
			Channel: delivery.Channel,
			Target:  delivery.Target,
		},
	})
}

func (t *listTool) Declaration() *tool.Declaration {
	description := "List OpenClaw background subagents created " +
		"from the current session."
	if t.alias {
		description = "Compatibility alias for " +
			toolSubagentsList + ". " + description
	}
	return &tool.Declaration{
		Name:        t.name,
		Description: description,
		InputSchema: &tool.Schema{
			Type: schemaTypeObject,
		},
	}
}

func (t *listTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.svc == nil {
		return nil, fmt.Errorf("subagent: service unavailable")
	}
	if err := validateEmptyArgs(args); err != nil {
		return nil, err
	}
	userID, sess, err := currentContext(ctx)
	if err != nil {
		return nil, err
	}
	return listResult{
		Runs: t.svc.ListForUser(
			userID,
			openclawsubagent.ListFilter{
				ParentSessionID: sess.ID,
			},
		),
	}, nil
}

func (t *getTool) Declaration() *tool.Declaration {
	description := "Get the latest status and result for one " +
		"OpenClaw background subagent run."
	if t.alias {
		description = "Compatibility alias for " +
			toolSubagentsGet + ". " + description
	}
	return &tool.Declaration{
		Name:        t.name,
		Description: description,
		InputSchema: runIDSchema(),
	}
}

func (t *getTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.svc == nil {
		return nil, fmt.Errorf("subagent: service unavailable")
	}
	runID, userID, err := decodeRunIDArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	return t.svc.GetForUser(userID, runID)
}

func (t *cancelTool) Declaration() *tool.Declaration {
	description := "Cancel one OpenClaw background subagent run. " +
		"This is best-effort."
	if t.alias {
		description = "Compatibility alias for " +
			toolSubagentsCancel + ". " + description
	}
	return &tool.Declaration{
		Name:        t.name,
		Description: description,
		InputSchema: runIDSchema(),
	}
}

func (t *cancelTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.svc == nil {
		return nil, fmt.Errorf("subagent: service unavailable")
	}
	runID, userID, err := decodeRunIDArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	run, _, err := t.svc.CancelForUser(userID, runID)
	if err != nil {
		return nil, err
	}
	return run, nil
}

func (t *waitTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: t.name,
		Description: "Wait until one OpenClaw background subagent " +
			"run reaches a terminal status.",
		InputSchema: waitSchema(),
	}
}

func (t *waitTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.svc == nil {
		return nil, fmt.Errorf("subagent: service unavailable")
	}
	in, userID, err := decodeWaitArgs(ctx, args)
	if err != nil {
		return nil, err
	}
	waitCtx := ctx
	var cancel context.CancelFunc
	if in.TimeoutSeconds > 0 {
		waitCtx, cancel = context.WithTimeout(
			ctx,
			time.Duration(in.TimeoutSeconds)*time.Second,
		)
		defer cancel()
	}
	return t.svc.WaitForUser(waitCtx, userID, in.ID)
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
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		return "", "", fmt.Errorf("subagent: empty run id")
	}
	return in.ID, userID, nil
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

func currentContext(
	ctx context.Context,
) (string, *session.Session, error) {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return "", nil, fmt.Errorf(
			"subagent: current session context is unavailable",
		)
	}
	userID := strings.TrimSpace(inv.Session.UserID)
	if userID == "" {
		return "", nil, fmt.Errorf(
			"subagent: current user id is unavailable",
		)
	}
	if strings.TrimSpace(inv.Session.ID) == "" {
		return "", nil, fmt.Errorf(
			"subagent: current session id is unavailable",
		)
	}
	return userID, inv.Session, nil
}

func isNestedSubagent(ctx context.Context) bool {
	nested, ok := agent.GetRuntimeStateValueFromContext[bool](
		ctx,
		openclawsubagent.RuntimeStateKeyRun,
	)
	return ok && nested
}
