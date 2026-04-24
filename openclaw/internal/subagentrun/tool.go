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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	publicsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	toolSubagentsSpawn  = "subagents_spawn"
	toolSubagentsList   = "subagents_list"
	toolSubagentsGet    = "subagents_get"
	toolSubagentsCancel = "subagents_cancel"

	toolSessionsSpawn  = "sessions_spawn"
	toolSessionsList   = "sessions_list"
	toolSessionsGet    = "sessions_get"
	toolSessionsCancel = "sessions_cancel"

	argTask           = "task"
	argID             = "id"
	argTimeoutSeconds = "timeout_seconds"

	schemaTypeInteger = "integer"
	schemaTypeObject  = "object"
	schemaTypeString  = "string"
)

type Tools struct {
	spawn       *spawnTool
	list        *listTool
	get         *getTool
	cancel      *cancelTool
	spawnAlias  *spawnTool
	listAlias   *listTool
	getAlias    *getTool
	cancelAlias *cancelTool
}

func NewTools(svc *Service) Tools {
	return Tools{
		spawn:       newSpawnTool(toolSubagentsSpawn, false, svc),
		list:        newListTool(toolSubagentsList, false, svc),
		get:         newGetTool(toolSubagentsGet, false, svc),
		cancel:      newCancelTool(toolSubagentsCancel, false, svc),
		spawnAlias:  newSpawnTool(toolSessionsSpawn, true, svc),
		listAlias:   newListTool(toolSessionsList, true, svc),
		getAlias:    newGetTool(toolSessionsGet, true, svc),
		cancelAlias: newCancelTool(toolSessionsCancel, true, svc),
	}
}

func (t *Tools) SetService(svc *Service) {
	if t == nil {
		return
	}
	for _, item := range []*ServiceAwareTool{
		t.spawn.base(),
		t.list.base(),
		t.get.base(),
		t.cancel.base(),
		t.spawnAlias.base(),
		t.listAlias.base(),
		t.getAlias.base(),
		t.cancelAlias.base(),
	} {
		if item == nil {
			continue
		}
		item.svc = svc
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
		t.spawnAlias,
		t.listAlias,
		t.getAlias,
		t.cancelAlias,
	}
}

type ServiceAwareTool struct {
	name        string
	compatAlias bool
	svc         *Service
}

type spawnTool struct {
	ServiceAwareTool
}

type listTool struct {
	ServiceAwareTool
}

type getTool struct {
	ServiceAwareTool
}

type cancelTool struct {
	ServiceAwareTool
}

type spawnInput struct {
	Task           string `json:"task"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type runIDInput struct {
	ID string `json:"id"`
}

type listResult struct {
	Runs []publicsubagent.Run `json:"runs,omitempty"`
}

func newSpawnTool(
	name string,
	compatAlias bool,
	svc *Service,
) *spawnTool {
	return &spawnTool{
		ServiceAwareTool: ServiceAwareTool{
			name:        name,
			compatAlias: compatAlias,
			svc:         svc,
		},
	}
}

func newListTool(
	name string,
	compatAlias bool,
	svc *Service,
) *listTool {
	return &listTool{
		ServiceAwareTool: ServiceAwareTool{
			name:        name,
			compatAlias: compatAlias,
			svc:         svc,
		},
	}
}

func newGetTool(
	name string,
	compatAlias bool,
	svc *Service,
) *getTool {
	return &getTool{
		ServiceAwareTool: ServiceAwareTool{
			name:        name,
			compatAlias: compatAlias,
			svc:         svc,
		},
	}
}

func newCancelTool(
	name string,
	compatAlias bool,
	svc *Service,
) *cancelTool {
	return &cancelTool{
		ServiceAwareTool: ServiceAwareTool{
			name:        name,
			compatAlias: compatAlias,
			svc:         svc,
		},
	}
}

func (t *spawnTool) base() *ServiceAwareTool {
	if t == nil {
		return nil
	}
	return &t.ServiceAwareTool
}

func (t *listTool) base() *ServiceAwareTool {
	if t == nil {
		return nil
	}
	return &t.ServiceAwareTool
}

func (t *getTool) base() *ServiceAwareTool {
	if t == nil {
		return nil
	}
	return &t.ServiceAwareTool
}

func (t *cancelTool) base() *ServiceAwareTool {
	if t == nil {
		return nil
	}
	return &t.ServiceAwareTool
}

func (t *spawnTool) Declaration() *tool.Declaration {
	description := "Spawn one background subagent for the current " +
		"session. Use this for long-running work, parallelizable " +
		"work, or independent verification. It returns " +
		"immediately with a run id."
	if t.compatAlias {
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

	run, err := t.svc.Spawn(ctx, SpawnRequest{
		OwnerUserID:     userID,
		ParentSessionID: sess.ID,
		Task:            in.Task,
		TimeoutSeconds:  in.TimeoutSeconds,
		Delivery: deliveryTarget{
			Channel: delivery.Channel,
			Target:  delivery.Target,
		},
	})
	if err != nil {
		return nil, err
	}
	return run, nil
}

func (t *listTool) Declaration() *tool.Declaration {
	description := "List background subagents created from the " +
		"current session."
	if t.compatAlias {
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
	if len(args) > 0 && strings.TrimSpace(string(args)) != "" &&
		strings.TrimSpace(string(args)) != "{}" {
		var ignored map[string]any
		if err := json.Unmarshal(args, &ignored); err != nil {
			return nil, err
		}
	}

	userID, sess, err := currentContext(ctx)
	if err != nil {
		return nil, err
	}
	return listResult{
		Runs: t.svc.ListForUser(
			userID,
			publicsubagent.ListFilter{
				ParentSessionID: sess.ID,
			},
		),
	}, nil
}

func (t *getTool) Declaration() *tool.Declaration {
	description := "Get the latest status and result for one " +
		"background subagent run."
	if t.compatAlias {
		description = "Compatibility alias for " +
			toolSubagentsGet + ". " + description
	}
	return &tool.Declaration{
		Name:        t.name,
		Description: description,
		InputSchema: &tool.Schema{
			Type:     schemaTypeObject,
			Required: []string{argID},
			Properties: map[string]*tool.Schema{
				argID: {
					Type: schemaTypeString,
					Description: "Subagent run id returned by " +
						"spawn.",
				},
			},
		},
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
	description := "Cancel one background subagent run. This is " +
		"best-effort."
	if t.compatAlias {
		description = "Compatibility alias for " +
			toolSubagentsCancel + ". " + description
	}
	return &tool.Declaration{
		Name:        t.name,
		Description: description,
		InputSchema: &tool.Schema{
			Type:     schemaTypeObject,
			Required: []string{argID},
			Properties: map[string]*tool.Schema{
				argID: {
					Type: schemaTypeString,
					Description: "Subagent run id returned by " +
						"spawn.",
				},
			},
		},
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
		runtimeStateSubagentRun,
	)
	return ok && nested
}
