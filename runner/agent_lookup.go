//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/appid"
)

func (r *runner) lookupNamedAgent(
	ctx context.Context,
	agentName string,
	ro agent.RunOptions,
) (agent.Agent, bool, error) {
	if ag, ok := r.agents[agentName]; ok && ag != nil {
		selected := r.wrapSelectedAgent(ag)
		appid.RegisterRunner(r.appName, selected.Info().Name)
		return selected, true, nil
	}
	if factory, ok := r.agentFactories[agentName]; ok && factory != nil {
		created, err := factory(ctx, ro)
		if err != nil {
			return nil, false, fmt.Errorf("runner: agent factory: %w", err)
		}
		if created == nil {
			return nil, false, fmt.Errorf(
				"runner: agent factory returned nil",
			)
		}
		selected := r.wrapSelectedAgent(created)
		appid.RegisterRunner(r.appName, selected.Info().Name)
		return selected, true, nil
	}
	ag, ok := findUniqueNestedAgent(r.agents, agentName)
	if !ok || ag == nil {
		return nil, false, nil
	}
	selected := r.wrapSelectedAgent(ag)
	appid.RegisterRunner(r.appName, selected.Info().Name)
	return selected, true, nil
}

func findUniqueNestedAgent(
	roots map[string]agent.Agent,
	targetName string,
) (agent.Agent, bool) {
	if targetName == "" || len(roots) == 0 {
		return nil, false
	}
	var found agent.Agent
	visited := make(map[uintptr]struct{})
	for _, root := range roots {
		candidate, matched, ambiguous := findAgentInTree(
			root,
			targetName,
			visited,
		)
		if ambiguous {
			return nil, false
		}
		if !matched {
			continue
		}
		if found != nil {
			return nil, false
		}
		found = candidate
	}
	return found, found != nil
}

func findAgentInTree(
	root agent.Agent,
	targetName string,
	visited map[uintptr]struct{},
) (agent.Agent, bool, bool) {
	if root == nil {
		return nil, false, false
	}
	rootID, ok := comparableAgentID(root)
	if ok {
		if _, seen := visited[rootID]; seen {
			return nil, false, false
		}
		visited[rootID] = struct{}{}
	}

	var found agent.Agent
	for _, sub := range root.SubAgents() {
		if sub == nil {
			continue
		}
		if sub.Info().Name == targetName {
			if found != nil {
				return nil, false, true
			}
			found = sub
		}
		nested, matched, ambiguous := findAgentInTree(
			sub,
			targetName,
			visited,
		)
		if ambiguous {
			return nil, false, true
		}
		if matched {
			if found != nil {
				return nil, false, true
			}
			found = nested
		}
	}
	return found, found != nil, false
}

func comparableAgentID(ag agent.Agent) (uintptr, bool) {
	if ag == nil {
		return 0, false
	}
	value := reflect.ValueOf(ag)
	switch value.Kind() {
	case reflect.Chan,
		reflect.Func,
		reflect.Map,
		reflect.Pointer,
		reflect.Slice,
		reflect.UnsafePointer:
		if value.IsNil() {
			return 0, false
		}
		return value.Pointer(), true
	default:
		return 0, false
	}
}
