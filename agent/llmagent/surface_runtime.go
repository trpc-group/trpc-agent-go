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
	toolsessionrecall "trpc.group/trpc-go/trpc-agent-go/internal/session/tool/recall"
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
	ctx context.Context,
	inv *agent.Invocation,
) skill.Repository {
	if patch, ok := a.rootSurfacePatch(inv); ok {
		if repo, ok := patch.SkillRepository(); ok {
			return repo
		}
	}
	a.mu.RLock()
	provider := a.option.skillsRepositoryProvider
	mode := a.option.skillScopeMode
	staticRepo := a.option.skillsRepository
	a.mu.RUnlock()
	if provider == nil {
		return staticRepo
	}
	scope, err := skillScopeForInvocation(mode, inv)
	if err != nil {
		if skill.NormalizeSkillScopeMode(mode) == skill.SkillScopeUser {
			return nil
		}
		return staticRepo
	}
	if scope.IsZero() {
		return staticRepo
	}
	repo, err := provider.Repository(ctx, scope)
	if err != nil {
		if skill.NormalizeSkillScopeMode(mode) == skill.SkillScopeUser {
			return nil
		}
		return staticRepo
	}
	return repo
}

func skillScopeForInvocation(
	mode skill.SkillScopeMode,
	inv *agent.Invocation,
) (skill.SkillScope, error) {
	if inv == nil || inv.Session == nil {
		return skill.SkillScope{}, nil
	}
	return skill.NewSkillScope(
		skill.NormalizeSkillScopeMode(mode),
		inv.Session.AppName,
		inv.Session.UserID,
	)
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

// InvocationSkillRepository returns the effective skill repository for the
// invocation, honoring an invocation-scoped surface patch when present. It
// implements agent.InvocationSkillRepositoryProvider so helpers such as the
// dynamic AgentTool can derive a child skill surface from a parent invocation
// without importing the llmagent package.
func (a *LLMAgent) InvocationSkillRepository(
	ctx context.Context,
	inv *agent.Invocation,
) skill.Repository {
	if a == nil {
		return nil
	}
	return a.skillRepositoryForInvocation(ctx, inv)
}

// InvocationCodeExecutor returns the effective code executor for the
// invocation, honoring a per-run override when present. It implements
// agent.InvocationCodeExecutorProvider so callers can check executor
// availability for a parent invocation without importing the llmagent package.
func (a *LLMAgent) InvocationCodeExecutor(
	_ context.Context,
	inv *agent.Invocation,
) codeexecutor.CodeExecutor {
	return a.codeExecutorForInvocation(inv)
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
	if hasUserTools, ok := llmflow.InvocationHasFilteredUserTools(inv); ok {
		if inv != nil && surfacepatch.ToolSurfaceTracingEnabled(inv.RunOptions.CustomAgentConfigs) {
			traceableToolNames, _ := llmflow.InvocationFilteredTraceableUserToolNames(inv)
			if len(traceableToolNames) == 0 && hasUserTools {
				appliedSurfaceIDs = append(
					appliedSurfaceIDs,
					astructure.SurfaceID(nodeID, astructure.SurfaceTypeTool),
				)
			}
			for _, toolName := range traceableToolNames {
				appliedSurfaceIDs = append(
					appliedSurfaceIDs,
					astructure.SurfaceID(
						nodeID,
						astructure.SurfaceTypeTool,
						toolName,
					),
				)
			}
		} else if hasUserTools {
			appliedSurfaceIDs = append(
				appliedSurfaceIDs,
				astructure.SurfaceID(nodeID, astructure.SurfaceTypeTool),
			)
		}
	}
	if a.skillRepositoryForInvocation(context.Background(), inv) != nil {
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
	userTools = applyToolDeclarationPatch(userTools, patch)
	userTools, userToolNames = filterInvocationUserTools(
		ctx,
		userTools,
		userToolNames,
		options.toolFilter,
	)

	allTools := append([]tool.Tool(nil), userTools...)
	allTools = appendKnowledgeTools(allTools, &options)
	allTools, userToolNames = appendCurrentTimeTool(
		allTools,
		userToolNames,
		&options,
	)
	effectiveSkills := a.skillRepositoryForInvocation(ctx, inv)
	effectiveExec := a.codeExecutorForInvocation(inv)
	workspaceExecEnabled := workspaceExecSurfaceEnabled(&options) &&
		codeExecutorSupportsWorkspaceExec(effectiveExec)
	workspaceExecSessions := workspaceExecEnabled &&
		codeExecutorSupportsWorkspaceExecSessions(effectiveExec)
	var workspaceRegistry *codeexecutor.WorkspaceRegistry
	if effectiveSkills != nil && effectiveExec != nil {
		workspaceRegistry = a.workspaceRegistryForInvocation(inv, effectiveExec)
	} else if workspaceExecEnabled {
		workspaceRegistry = a.workspaceRegistryForInvocation(inv, effectiveExec)
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
	if toolsessionrecall.SupportsOnDemandSession(inv) {
		allTools = appendOnDemandSessionTools(allTools, &options, inv)
	}
	if options.EnableAwaitUserReplyTool {
		allTools = append(allTools, toolawaitreply.New())
	}
	// A surface patch may suppress framework-managed sub-agent transfer for this
	// node (the dynamic AgentTool does so for short-lived sub-agents that must
	// not hand control to another agent). Treat it like having no sub-agents.
	if len(subAgents) == 0 || patch.SuppressSubAgentTransfer() {
		allTools = appendExtensionTools(allTools, &options)
		return allTools, userToolNames
	}
	agentInfos := make([]agent.Info, len(subAgents))
	for i, subAgent := range subAgents {
		agentInfos[i] = subAgent.Info()
	}
	allTools = append(allTools, transfer.New(agentInfos))
	// Extension-contributed tools (WithExtensions →
	// extension.Registry.Tools) sit at the same logical layer as
	// other framework-managed auto-injected tools: not folded into
	// userToolNames, yet present on the outbound tool surface.
	//
	// Append them after every framework tool (knowledge, workspace,
	// skills, session recall, await_user_reply and transfer) so
	// earlier-wins dedup also protects later framework declarations
	// from extension name collisions.
	allTools = appendExtensionTools(allTools, &options)
	return allTools, userToolNames
}

// InvocationKnowledgeOptions returns the options required to reproduce this
// agent's knowledge-search surface on a derived agent.
//
// Built-in presets use it to inherit a parent agent's retrieval capability
// without copying the parent's materialized (and possibly custom-named)
// knowledge tools. The invocation argument is accepted for parity with the
// other invocation-scoped accessors and reserved for future per-invocation
// knowledge resolution; it is currently unused.
func (a *LLMAgent) InvocationKnowledgeOptions(
	_ *agent.Invocation,
) []Option {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.option.Knowledge == nil {
		return nil
	}
	opts := []Option{WithKnowledge(a.option.Knowledge)}
	if a.option.KnowledgeFilter != nil {
		opts = append(opts, WithKnowledgeFilter(a.option.KnowledgeFilter))
	}
	if a.option.KnowledgeConditionedFilter != nil {
		opts = append(
			opts,
			WithKnowledgeConditionedFilter(a.option.KnowledgeConditionedFilter),
		)
	}
	if a.option.EnableKnowledgeAgenticFilter {
		opts = append(opts, WithEnableKnowledgeAgenticFilter(true))
		if a.option.AgenticFilterInfo != nil {
			opts = append(
				opts,
				WithKnowledgeAgenticFilterInfo(a.option.AgenticFilterInfo),
			)
		}
	}
	return opts
}

func (a *LLMAgent) userToolsForInvocation(
	ctx context.Context,
	patch surfacepatch.Patch,
) ([]tool.Tool, map[string]bool) {
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

	if patchedTools, ok := patch.Tools(); ok {
		return patchedTools, collectUserToolNames(patchedTools)
	}
	if !refreshToolSets {
		userTools := make([]tool.Tool, 0, len(userToolNames))
		for _, t := range staticTools {
			if userToolNames[t.Declaration().Name] {
				userTools = append(userTools, t)
			}
		}
		return applyUserToolPatch(userTools, userToolNames, patch)
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
	return applyUserToolPatch(userTools, userToolNames, patch)
}

func applyUserToolPatch(
	userTools []tool.Tool,
	userToolNames map[string]bool,
	patch surfacepatch.Patch,
) ([]tool.Tool, map[string]bool) {
	patchedTools, ok := patch.ApplyTools(userTools)
	if !ok {
		return userTools, userToolNames
	}
	return patchedTools, collectUserToolNames(patchedTools)
}

func applyToolDeclarationPatch(
	tools []tool.Tool,
	patch surfacepatch.Patch,
) []tool.Tool {
	declarations, ok := patch.ToolDeclarations()
	if !ok {
		return tools
	}
	return itool.ApplyDeclarations(tools, declarations)
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
