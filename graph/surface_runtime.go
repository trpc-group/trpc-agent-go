//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"trpc.group/trpc-go/trpc-agent-go/agent"
	iflow "trpc.group/trpc-go/trpc-agent-go/internal/flow"
	istructure "trpc.group/trpc-go/trpc-agent-go/internal/structure"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func graphInvocationFromState(state State) *agent.Invocation {
	if execVal, ok := state[StateKeyExecContext]; ok {
		if execCtx, ok := execVal.(*ExecutionContext); ok && execCtx != nil {
			return execCtx.Invocation
		}
	}
	return nil
}

func graphSurfacePatch(
	invocation *agent.Invocation,
	localNodeID string,
) (surfacepatch.Patch, bool) {
	if invocation == nil || localNodeID == "" {
		return surfacepatch.Patch{}, false
	}
	rootNodeID := agent.InvocationSurfaceRootNodeID(invocation)
	absNodeID := istructure.JoinNodeID(
		rootNodeID,
		localNodeID,
	)
	return surfacepatch.PatchForNode(
		invocation.RunOptions.CustomAgentConfigs,
		absNodeID,
	)
}

func graphPatchedModel(
	invocation *agent.Invocation,
	localNodeID string,
	fallback model.Model,
) model.Model {
	patch, ok := graphSurfacePatch(invocation, localNodeID)
	if !ok {
		return fallback
	}
	patchedModel, ok := patch.Model()
	if !ok {
		return fallback
	}
	return patchedModel
}

func toolSliceToMap(tools []tool.Tool) map[string]tool.Tool {
	if len(tools) == 0 {
		return map[string]tool.Tool{}
	}
	out := make(map[string]tool.Tool, len(tools))
	for _, t := range tools {
		if t == nil || t.Declaration() == nil {
			continue
		}
		out[t.Declaration().Name] = t
	}
	return out
}

func (r *llmRunner) currentNodeID(state State) string {
	if v, ok := state[StateKeyCurrentNodeID].(string); ok && v != "" {
		return v
	}
	return r.nodeID
}

func (r *llmRunner) insertFewShot(
	state State,
	messages []model.Message,
) []model.Message {
	invocation := graphInvocationFromState(state)
	if invocation == nil {
		return messages
	}
	patch, ok := graphSurfacePatch(invocation, r.currentNodeID(state))
	if !ok {
		return messages
	}
	examples, ok := patch.FewShot()
	if !ok || len(examples) == 0 {
		return messages
	}
	return iflow.InsertFewShotMessages(
		messages,
		examples,
	)
}
