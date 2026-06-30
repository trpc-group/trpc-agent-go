//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	ocbrowser "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/browser"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

type deferredToolSurfaceConfig struct {
	Model         model.Model
	Config        agentConfig
	Instruction   string
	SystemPrompt  string
	Generation    model.GenerationConfig
	Repository    *ocskills.Repository
	RepoProvider  skill.RepositoryProvider
	Tools         []tool.Tool
	ToolSets      []tool.ToolSet
	CodeExecutor  codeexecutor.CodeExecutor
	ToolCallbacks *tool.Callbacks
}

var openClawDeferredToolAliases = map[string]string{
	"browser-runtime":           ocbrowser.ToolName,
	"browser_runtime":           ocbrowser.ToolName,
	"trpc-claw-browser-runtime": ocbrowser.ToolName,
	"trpc_claw_browser_runtime": ocbrowser.ToolName,
}

func baseLLMAgentOptions(
	mdl model.Model,
	cfg agentConfig,
	instruction string,
	systemPrompt string,
	genConfig model.GenerationConfig,
	repo *ocskills.Repository,
) []llmagent.Option {
	return []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithInstruction(instruction),
		llmagent.WithGlobalInstruction(systemPrompt),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithAddSessionSummary(cfg.AddSessionSummary),
		llmagent.WithEnableContextCompaction(cfg.EnableContextCompaction),
		llmagent.WithContextCompactionOversizedToolResultMaxTokens(
			cfg.ContextCompactionOversizedToolResultMaxTokens,
		),
		llmagent.WithMaxHistoryRuns(cfg.MaxHistoryRuns),
		llmagent.WithMaxLLMCalls(cfg.MaxLLMCalls),
		llmagent.WithMaxToolIterations(cfg.MaxToolIterations),
		llmagent.WithPreloadMemory(cfg.PreloadMemory),
		llmagent.WithEnableOnDemandSession(true),
		llmagent.WithEventMessageProjector(
			conversation.ProjectEventMessage,
		),
		llmagent.WithToolResultCompactionConfig(
			openClawToolResultCompactionConfig(),
		),
		llmagent.WithEnableParallelTools(cfg.EnableParallelTools),
		llmagent.WithPostToolPrompt(openClawPostToolPrompt),
		llmagent.WithSkillFilter(
			runtimeprofile.SkillVisibilityFilterForRepository(repo),
		),
	}
}

func openClawToolResultCompactionConfig() *llmagent.ToolResultCompactionConfig {
	return &llmagent.ToolResultCompactionConfig{
		KeepToolNames: []string{
			agenttool.DefaultDynamicToolName,
		},
	}
}

func appendOpenClawSkillOptions(
	opts []llmagent.Option,
	cfg agentConfig,
	repo *ocskills.Repository,
	repoProvider skill.RepositoryProvider,
) []llmagent.Option {
	opts = append(opts, llmagent.WithSkills(repo))
	opts = append(
		opts,
		llmagent.WithSkillRepositoryProvider(repoProvider),
		llmagent.WithSkillScopeMode(cfg.EvolutionSkillScopeMode),
	)
	opts = append(
		opts,
		llmagent.WithSkillToolProfile(
			llmagent.SkillToolProfile(cfg.SkillsToolProfile),
		),
		llmagent.WithSkillsFilePathHints(true),
		llmagent.WithSkillsDirectoryHints(true),
		llmagent.WithSkillLoadMode(cfg.SkillsLoadMode),
		llmagent.WithSkillsLoadedContentInToolResults(
			cfg.SkillsToolResults,
		),
		llmagent.WithSkillLoadToolDescription(
			openClawSkillLoadToolDescription,
		),
		llmagent.WithWorkspaceExecSurfaceEnabled(false),
		llmagent.WithSkipSkillsFallbackOnSessionSummary(
			cfg.SkillsSkipFallback,
		),
		llmagent.WithSkillsProtocolGuidance(
			buildOpenClawSkillsGuidance(cfg),
		),
	)
	if cfg.SkillsMaxLoaded > 0 {
		opts = append(
			opts,
			llmagent.WithMaxLoadedSkills(cfg.SkillsMaxLoaded),
		)
	}
	return opts
}

func appendCodeExecutionOptions(
	opts []llmagent.Option,
	exec codeexecutor.CodeExecutor,
	cfg codeExecutorOptions,
) []llmagent.Option {
	if exec != nil {
		opts = append(opts, llmagent.WithCodeExecutor(exec))
	}
	if cfg.AutoExecuteCodeBlocks != nil {
		opts = append(
			opts,
			llmagent.WithEnableCodeExecutionResponseProcessor(
				*cfg.AutoExecuteCodeBlocks,
			),
		)
	}
	return opts
}

func newDeferredToolSurfaceTool(
	cfg deferredToolSurfaceConfig,
) tool.Tool {
	templateOpts := baseLLMAgentOptions(
		cfg.Model,
		cfg.Config,
		cfg.Instruction,
		cfg.SystemPrompt,
		cfg.Generation,
		cfg.Repository,
	)
	if cfg.Config.PostToolPromptEnabled != nil {
		templateOpts = append(
			templateOpts,
			llmagent.WithEnablePostToolPrompt(
				*cfg.Config.PostToolPromptEnabled,
			),
		)
	}
	templateOpts = appendOpenClawSkillOptions(
		templateOpts,
		cfg.Config,
		cfg.Repository,
		cfg.RepoProvider,
	)
	if len(cfg.Tools) > 0 {
		templateOpts = append(templateOpts, llmagent.WithTools(cfg.Tools))
	}
	if len(cfg.ToolSets) > 0 {
		templateOpts = append(
			templateOpts,
			llmagent.WithToolSets(cfg.ToolSets),
		)
	}
	if cfg.Config.RefreshToolSetsOnRun {
		templateOpts = append(
			templateOpts,
			llmagent.WithRefreshToolSetsOnRun(true),
		)
	}
	templateOpts = appendCodeExecutionOptions(
		templateOpts,
		cfg.CodeExecutor,
		cfg.Config.CodeExecutor,
	)
	if cfg.ToolCallbacks != nil {
		templateOpts = append(
			templateOpts,
			llmagent.WithToolCallbacks(cfg.ToolCallbacks),
		)
	}

	template := llmagent.New(defaultAgentName+"-tool-worker", templateOpts...)
	dynamicOpts := []agenttool.Option{
		agenttool.WithDescription(openClawDeferredToolDescription),
		agenttool.WithTemplateAgent(template),
		agenttool.WithCapabilityProvider(
			deferredCapabilityProvider(cfg.Tools, cfg.ToolSets),
		),
		agenttool.WithCapabilityToolAliases(openClawDeferredToolAliases),
		agenttool.WithCapabilitySkillsProvider(
			deferredCapabilitySkillsProvider(
				cfg.Repository,
				cfg.RepoProvider,
				cfg.Config.EvolutionSkillScopeMode,
			),
		),
		agenttool.WithExposeSkillSelection(true),
	}
	if cfg.Config.DynamicAgentTimeout > 0 {
		dynamicOpts = append(
			dynamicOpts,
			agenttool.WithDynamicTimeout(cfg.Config.DynamicAgentTimeout),
		)
	}

	dynamicOpts = append(
		dynamicOpts,
		agenttool.WithRequestDescription(
			"Self-contained tool-backed task for the OpenClaw worker.",
		),
		agenttool.WithInstructionDescription(
			"Optional role or constraints for this worker call.",
		),
		agenttool.WithToolsDescription(
			"Optional exact tool names, for example web_fetch, "+
				"browser, or exec_command. Omit to let the "+
				"worker choose from all permitted tools. Do not "+
				"put tool names in skills.",
		),
		agenttool.WithSkillsDescription(
			"Optional exact skill names if already known. Use only "+
				"real skill names here; put tool names in tools. "+
				"Omit to let the worker choose from all permitted "+
				"skills.",
		),
	)
	return agenttool.NewDynamicTool(dynamicOpts...)
}

func newDeferredCapabilitySearchTool(
	cfg deferredToolSurfaceConfig,
) tool.Tool {
	return agenttool.NewCapabilitySearchTool(
		agenttool.WithCapabilitySearchProvider(
			deferredCapabilityProvider(cfg.Tools, cfg.ToolSets),
		),
		agenttool.WithCapabilitySearchToolAliases(
			openClawDeferredToolAliases,
		),
		agenttool.WithCapabilitySearchSkillsProvider(
			deferredCapabilitySkillsProvider(
				cfg.Repository,
				cfg.RepoProvider,
				cfg.Config.EvolutionSkillScopeMode,
			),
		),
	)
}

func deferredCapabilitySkillsProvider(
	staticRepo skill.Repository,
	repoProvider skill.RepositoryProvider,
	mode skill.SkillScopeMode,
) agenttool.CapabilitySkillsProvider {
	return func(ctx context.Context, parentInv *agent.Invocation) skill.Repository {
		if repoProvider == nil {
			return staticRepo
		}
		scope, err := deferredSkillScopeForInvocation(mode, parentInv)
		if err != nil {
			if skill.NormalizeSkillScopeMode(mode) == skill.SkillScopeUser {
				return nil
			}
			return staticRepo
		}
		if scope.IsZero() {
			return staticRepo
		}
		if ctx == nil {
			ctx = context.Background()
		}
		repo, err := repoProvider.Repository(ctx, scope)
		if err != nil {
			if skill.NormalizeSkillScopeMode(mode) == skill.SkillScopeUser {
				return nil
			}
			return staticRepo
		}
		return repo
	}
}

func deferredSkillScopeForInvocation(
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

func deferredCapabilityProvider(
	baseTools []tool.Tool,
	toolSets []tool.ToolSet,
) agenttool.CapabilitySurfaceProvider {
	copiedBase := append([]tool.Tool(nil), baseTools...)
	copiedSets := append([]tool.ToolSet(nil), toolSets...)
	return func(
		ctx context.Context,
		_ *agent.Invocation,
	) ([]tool.Tool, map[string]bool) {
		tools := deferredCapabilityTools(ctx, copiedBase, copiedSets)
		return tools, deferredToolNameSet(tools)
	}
}

func deferredCapabilityTools(
	ctx context.Context,
	baseTools []tool.Tool,
	toolSets []tool.ToolSet,
) []tool.Tool {
	if ctx == nil {
		ctx = context.Background()
	}
	out := make([]tool.Tool, 0, len(baseTools))
	seen := map[string]bool{}
	appendTool := func(t tool.Tool) {
		name := toolDeclName(t)
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, t)
	}
	for _, t := range baseTools {
		appendTool(t)
	}
	for _, toolSet := range toolSets {
		if toolSet == nil {
			continue
		}
		for _, t := range itool.NewNamedToolSet(toolSet).Tools(ctx) {
			appendTool(t)
		}
	}
	return out
}

func deferredToolNameSet(tools []tool.Tool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		if name := toolDeclName(t); name != "" {
			names[name] = true
		}
	}
	return names
}

func toolDeclName(t tool.Tool) string {
	if t == nil {
		return ""
	}
	decl := t.Declaration()
	if decl == nil {
		return ""
	}
	return decl.Name
}
