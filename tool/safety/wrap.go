//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// WrapCallableTool places Guard directly in front of a CallableTool.
//
// The normal agent runner should install Guard as a tool.PermissionPolicy.
// This wrapper is for hosts that invoke CallableTool.Call directly and would
// otherwise bypass the runner permission phase. Deny and ask decisions return
// the same structured permission result used by the runner and do not call the
// wrapped tool.
func WrapCallableTool(callable tool.CallableTool, guard *Guard) (tool.CallableTool, error) {
	if callable == nil {
		return nil, errors.New("tool safety: callable tool is nil")
	}
	if guard == nil {
		return nil, errors.New("tool safety: guard is nil")
	}
	if callable.Declaration() == nil {
		return nil, errors.New("tool safety: callable tool declaration is nil")
	}
	return &guardedCallableTool{callable: callable, guard: guard}, nil
}

type guardedCallableTool struct {
	callable tool.CallableTool
	guard    *Guard
}

func (g *guardedCallableTool) Declaration() *tool.Declaration {
	return g.callable.Declaration()
}

func (g *guardedCallableTool) ToolMetadata() tool.ToolMetadata {
	return tool.MetadataOf(g.callable)
}

func (g *guardedCallableTool) Call(ctx context.Context, arguments []byte) (any, error) {
	declaration := g.callable.Declaration()
	request := &tool.PermissionRequest{
		Tool:        g.callable,
		ToolName:    declaration.Name,
		Declaration: declaration,
		Arguments:   arguments,
		Metadata:    tool.MetadataOf(g.callable),
	}
	decision, err := g.guard.CheckToolPermission(ctx, request)
	if err != nil {
		return nil, err
	}
	decision, err = tool.NormalizePermissionDecision(decision)
	if err != nil {
		return nil, err
	}
	if decision.Action != tool.PermissionActionAllow {
		return tool.PermissionResultFor(declaration.Name, decision), nil
	}
	result, err := g.callable.Call(ctx, arguments)
	if err != nil {
		return nil, err
	}
	redacted, _ := g.guard.redactor.RedactValue(result)
	return redacted, nil
}

var _ tool.CallableTool = (*guardedCallableTool)(nil)
var _ tool.MetadataProvider = (*guardedCallableTool)(nil)
