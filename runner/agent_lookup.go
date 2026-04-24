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
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func (r *runner) loadRegisteredAgent(
	ctx context.Context,
	agentName string,
	ro agent.RunOptions,
) (agent.Agent, bool, error) {
	if ag, ok := r.agents[agentName]; ok && ag != nil {
		return ag, true, nil
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
		return created, true, nil
	}
	return nil, false, nil
}

func (r *runner) resolveAwaitUserReplyRoute(
	ctx context.Context,
	route agent.AwaitUserReplyRoute,
	ro agent.RunOptions,
) (agent.Agent, string, bool, error) {
	if r == nil {
		return nil, "", false, nil
	}
	segments := splitAgentPath(route.LookupPath)
	if len(segments) == 0 {
		return nil, "", false, nil
	}

	rootName := segments[0]
	current, ok, err := r.loadRegisteredAgent(ctx, rootName, ro)
	if err != nil {
		return nil, "", false, err
	}
	if !ok || current == nil {
		return nil, "", false, nil
	}

	for _, segment := range segments[1:] {
		current = current.FindSubAgent(segment)
		if current == nil {
			return nil, "", false, nil
		}
	}

	return current, rootName, true, nil
}

func splitAgentPath(path string) []string {
	parts := strings.Split(path, agent.BranchDelimiter)
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		segments = append(segments, part)
	}
	return segments
}
