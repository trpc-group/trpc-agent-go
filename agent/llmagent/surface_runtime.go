//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/llmflow"
	"trpc.group/trpc-go/trpc-agent-go/internal/skillprofile"
	"trpc.group/trpc-go/trpc-agent-go/internal/surfacepatch"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	toolawaitreply "trpc.group/trpc-go/trpc-agent-go/tool/awaitreply"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

func (a *LLMAgent) rootSurfacePatch(
	inv *agent.Invocation,
) (surfacepatch.Patch, bool) {
	if inv == nil {
		return surfacepatch.Patch{}, false
	}
	nodeID := agent.InvocationSurfaceRootNodeID(inv)
	if nodeID == "" {
		return surfacepatch.Patch{}, false
	}
	return surfacepatch.PatchForNode(
		inv.RunOptions.CustomAgentConfigs,
		nodeID,
	)
}

func (a *LLMAgent) fewShotForInvocation(
	inv *agent.Invocation,
) [][]model.Message {
	patch, ok := a.rootSurfacePatch(inv)
	if !ok {
		return nil
	}
	examples, ok := patch.FewShot()
	if !ok {
		return nil
	}
	return examples
}

func (a *LLMAgent) skillRepositoryForInvocation(
	inv *agent.Invocation,
) skill.Repository {
	if patch, ok := a.rootSurfacePatch(inv); ok {
		if repo, ok := patch.SkillRepository(); ok {
			return repo
		}
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.option.skillsRepository
}

func (a *LLMAgent) modelSurfaceForInvocation(
	inv *agent.Invocation,
) (model.Model, bool) {
	patch, ok := a.rootSurfacePatch(inv)
	if !ok {
		return nil, false
	}
	return patch.Model()
}

func (a *LLMAgent) codeExecutorForInvocation(
	inv *agent.Invocation,
) codeexecutor.CodeExecutor {
	if inv != nil && inv.RunOptions.CodeExecutor != nil {
		return inv.RunOptions.CodeExecutor
	}
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.codeExecutor != nil {
		return a.codeExecutor
	}
	return a.option.codeExecutor
}

func (a *LLMAgent) supportsWorkspaceExecForInvocation(
	inv *agent.Invocation,
) bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	options := a.option
	a.mu.RUnlock()
	if !workspaceExecSurfaceEnabled(&options) {
		return false
	}
	return codeExecutorSupportsWorkspaceExec(a.codeExecutorForInvocation(inv))
}

func (a *LLMAgent) supportsWorkspaceExecSessionsForInvocation(
	inv *agent.Invocation,
) bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	options := a.option
	a.mu.RUnlock()
	if !workspaceExecSurfaceEnabled(&options) {
		return false
	}
	return codeExecutorSupportsWorkspaceExecSessions(
		a.codeExecutorForInvocation(inv),
	)
}

func (a *LLMAgent) skillToolFlagsForInvocation(
	inv *agent.Invocation,
) skillprofile.Flags {
	if a == nil {
		return skillprofile.Flags{}
	}
	a.mu.RLock()
	options := a.option
	a.mu.RUnlock()
	return mustResolveSkillToolFlagsWithExecutor(
		&options,
		a.codeExecutorForInvocation(inv),
	)
}

// ExecutionTraceAppliedSurfaceIDs reports the effective surfaces that affected one invocation step.
func (a *LLMAgent) ExecutionTraceAppliedSurfaceIDs(inv *agent.Invocation) []string {
	nodeID := agent.InvocationSurfaceRootNodeID(inv)
	if nodeID == "" {
		return nil
	}
	appliedSurfaceIDs := make([]string, 0, 6)
	if a.instructionForInvocation(inv) != "" {
		appliedSurfaceIDs = append(appliedSurfaceIDs, astructure.SurfaceID(nodeID, astructure.SurfaceTypeInstruction))
	}
	if a.systemPromptForInvocation(inv) != "" {
		appliedSurfaceIDs = append(appliedSurfaceIDs, astructure.SurfaceID(nodeID, astructure.SurfaceTypeGlobalInstruction))
	}
	if examples := a.fewShotForInvocation(inv); len(examples) > 0 {
		appliedSurfaceIDs = append(appliedSurfaceIDs, astructure.SurfaceID(nodeID, astructure.SurfaceTypeFewShot))
	}
	if inv != nil && inv.Model != nil {
		appliedSurfaceIDs = append(appliedSurfaceIDs, astructure.SurfaceID(nodeID, astructure.SurfaceTypeModel))
	}
	if hasUserTools, ok := llmflow.InvocationHasFilteredUserTools(inv); ok && hasUserTools {
		appliedSurfaceIDs = append(appliedSurfaceIDs, astructure.SurfaceID(nodeID, astructure.SurfaceTypeTool))
	}
	if a.skillRepositoryForInvocation(inv) != nil {
		appliedSurfaceIDs = append(appliedSurfaceIDs, astructure.SurfaceID(nodeID, astructure.SurfaceTypeSkill))
	}
	return appliedSurfaceIDs
}

// InvocationToolSurface returns the invocation-scoped tool surface and user tool names.
func (a *LLMAgent) InvocationToolSurface(
	ctx context.Context,
	inv *agent.Invocation,
) ([]tool.Tool, map[string]bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	patch, _ := a.rootSurfacePatch(inv)
	userTools, userToolNames := a.userToolsForInvocation(ctx, patch)
	a.mu.RLock()
	options := a.option
	subAgents := append([]agent.Agent(nil), a.subAgents...)
	a.mu.RUnlock()
	userTools, userToolNames = filterInvocationUserTools(
		ctx,
		userTools,
		userToolNames,
		options.toolFilter,
	)

	allTools := append([]tool.Tool(nil), userTools...)
	allTools = appendKnowledgeTools(allTools, &options)

	effectiveSkills := a.skillRepositoryForInvocation(inv)
	effectiveExec := a.codeExecutorForInvocation(inv)
	workspaceExecEnabled := workspaceExecSurfaceEnabled(&options) &&
		codeExecutorSupportsWorkspaceExec(effectiveExec)
	workspaceExecSessions := workspaceExecEnabled &&
		codeExecutorSupportsWorkspaceExecSessions(effectiveExec)
	var workspaceRegistry *codeexecutor.WorkspaceRegistry
	if effectiveSkills != nil && effectiveExec != nil {
		workspaceRegistry = buildWorkspaceRegistry()
	} else if workspaceExecEnabled {
		workspaceRegistry = buildWorkspaceRegistry()
	}
	// Pass effectiveSkills so workspace_exec's loaded-skills
	// reconcile reads the same repository that skill tools and the
	// skills request processor use on this invocation. Without this
	// alignment, a surface-patch repo override would be honored by
	// the skill tools but silently ignored by the reconciler path
	// added in this change set, causing the model context and the
	// materialized skill working copy to drift apart.
	allTools = appendWorkspaceExecToolWithExecutor(
		allTools,
		effectiveExec,
		workspaceExecEnabled,
		workspaceExecSessions,
		workspaceRegistry,
		inv,
		&options,
		effectiveSkills,
	)
	allTools = appendSkillToolsWithRepoAndFlags(
		allTools,
		&options,
		effectiveSkills,
		workspaceRegistry,
		nil,
		effectiveExec,
		mustResolveSkillToolFlagsWithExecutor(
			&options,
			effectiveExec,
		),
	)
	if options.EnableAwaitUserReplyTool {
		allTools = append(allTools, toolawaitreply.New())
	}
	if len(subAgents) == 0 {
		return allTools, userToolNames
	}
	agentInfos := make([]agent.Info, len(subAgents))
	for i, subAgent := range subAgents {
		agentInfos[i] = subAgent.Info()
	}
	allTools = append(allTools, transfer.New(agentInfos))
	return allTools, userToolNames
}

func (a *LLMAgent) userToolsForInvocation(
	ctx context.Context,
	patch surfacepatch.Patch,
) ([]tool.Tool, map[string]bool) {
	if patchedTools, ok := patch.Tools(); ok {
		return patchedTools, collectUserToolNames(patchedTools)
	}
	a.mu.RLock()
	refreshToolSets := a.option.RefreshToolSetsOnRun
	staticTools := append([]tool.Tool(nil), a.tools...)
	userToolNames := make(map[string]bool, len(a.userToolNames))
	for name, isUser := range a.userToolNames {
		userToolNames[name] = isUser
	}
	baseTools := append([]tool.Tool(nil), a.option.Tools...)
	toolSets := append([]tool.ToolSet(nil), a.option.ToolSets...)
	a.mu.RUnlock()

	if !refreshToolSets {
		userTools := make([]tool.Tool, 0, len(userToolNames))
		for _, t := range staticTools {
			if userToolNames[t.Declaration().Name] {
				userTools = append(userTools, t)
			}
		}
		return userTools, userToolNames
	}
	userTools := append([]tool.Tool(nil), baseTools...)
	userToolNames = collectUserToolNames(baseTools)
	for _, toolSet := range toolSets {
		namedToolSet := itool.NewNamedToolSet(toolSet)
		for _, t := range namedToolSet.Tools(ctx) {
			userTools = append(userTools, t)
			userToolNames[t.Declaration().Name] = true
		}
	}
	return userTools, userToolNames
}

func filterInvocationUserTools(
	ctx context.Context,
	userTools []tool.Tool,
	userToolNames map[string]bool,
	filter tool.FilterFunc,
) ([]tool.Tool, map[string]bool) {
	if filter == nil || len(userTools) == 0 {
		return userTools, userToolNames
	}
	filtered := make([]tool.Tool, 0, len(userTools))
	filteredNames := make(map[string]bool, len(userToolNames))
	for _, tl := range userTools {
		if tl == nil || tl.Declaration() == nil {
			continue
		}
		if filter(ctx, tl) {
			filtered = append(filtered, tl)
			filteredNames[tl.Declaration().Name] = true
		}
	}
	return filtered, filteredNames
}
