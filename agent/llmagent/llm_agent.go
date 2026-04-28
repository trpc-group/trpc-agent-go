//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package llmagent provides an LLM agent implementation.
package llmagent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/event"
	iagent "trpc.group/trpc-go/trpc-agent-go/internal/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/llmflow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/internal/skillprofile"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	itrace "trpc.group/trpc-go/trpc-agent-go/internal/trace"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
	"trpc.group/trpc-go/trpc-agent-go/prompt"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	toolawaitreply "trpc.group/trpc-go/trpc-agent-go/tool/awaitreply"
	toolskill "trpc.group/trpc-go/trpc-agent-go/tool/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
	toolworkspaceexec "trpc.group/trpc-go/trpc-agent-go/tool/workspaceexec"
)

// localruntimeFallback returns a simple local workspace executor used when
// no explicit executor is provided.
func defaultCodeExecutor() codeexecutor.CodeExecutor {
	return localexec.New()
}

// LLMAgent is an agent that uses an LLM to generate responses.
type LLMAgent struct {
	name                    string
	mu                      sync.RWMutex
	model                   model.Model
	models                  map[string]model.Model // Registered models for switching
	description             string
	instruction             prompt.Text
	systemPrompt            prompt.Text
	modelInstructions       map[string]prompt.Text
	modelGlobalInstructions map[string]prompt.Text
	genConfig               model.GenerationConfig
	flow                    flow.Flow
	tools                   []tool.Tool     // All tools (user tools + framework tools)
	userToolNames           map[string]bool // Names of tools explicitly registered
	// via WithTools and WithToolSets.
	codeExecutor         codeexecutor.CodeExecutor
	planner              planner.Planner
	subAgents            []agent.Agent // Sub-agents that can be delegated to
	agentCallbacks       *agent.Callbacks
	outputKey            string         // Key to store output in session state
	outputSchema         map[string]any // JSON schema for output validation
	inputSchema          map[string]any // JSON schema for input validation
	structuredOutput     *model.StructuredOutput
	structuredOutputType reflect.Type
	option               Options
}

const invalidOutputSchemaAwaitUserReply = "" +
	"Invalid LLMAgent configuration: if output_schema is set, " +
	"await_user_reply must be disabled"

// New creates a new LLMAgent with the given options.
func New(name string, opts ...Option) *LLMAgent {
	options := defaultOptions

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}
	prepareSkillsRepository(&options)
	applySkillsExecutorFallback(&options)

	// Validate output_schema configuration before registering tools.
	if options.OutputSchema != nil {
		if options.EnableAwaitUserReplyTool {
			panic(invalidOutputSchemaAwaitUserReply)
		}
		if len(options.Tools) > 0 || len(options.ToolSets) > 0 {
			panic("Invalid LLMAgent configuration: if output_schema is set, tools and toolSets must be empty")
		}
		if options.Knowledge != nil {
			panic("Invalid LLMAgent configuration: if output_schema is set, knowledge must be empty")
		}
		if len(options.SubAgents) > 0 {
			panic("Invalid LLMAgent configuration: if output_schema is set, sub_agents must be empty to disable agent transfer")
		}
	}

	// Register tools from both tools and toolsets, including knowledge search tool if provided.
	// Also track which tools are user-registered (via WithTools) for filtering purposes.
	tools, userToolNames := registerTools(&options)

	// Initialize models map and determine the initial model.
	initialModel, models := initializeModels(&options)

	// Construct the agent first so request processors can access dynamic getters.
	a := &LLMAgent{
		name:              name,
		model:             initialModel,
		models:            models,
		description:       options.Description,
		instruction:       newTextPrompt(options.Instruction),
		systemPrompt:      newTextPrompt(options.GlobalInstruction),
		modelInstructions: cloneTextPromptMap(options.ModelInstructions),
		modelGlobalInstructions: cloneTextPromptMap(
			options.ModelGlobalInstructions,
		),
		genConfig:            options.GenerationConfig,
		codeExecutor:         options.codeExecutor,
		tools:                tools,
		userToolNames:        userToolNames,
		planner:              options.Planner,
		subAgents:            options.SubAgents,
		agentCallbacks:       options.AgentCallbacks,
		outputKey:            options.OutputKey,
		outputSchema:         options.OutputSchema,
		inputSchema:          options.InputSchema,
		structuredOutput:     options.StructuredOutput,
		structuredOutputType: options.StructuredOutputType,
		option:               options,
	}

	// Prepare request processors in the correct order, wiring dynamic getters.
	requestProcessors := buildRequestProcessorsWithAgent(a, &options)

	// Prepare response processors.
	var responseProcessors []flow.ResponseProcessor

	// Add planning response processor if planner is configured.
	if options.Planner != nil {
		planningResponseProcessor := processor.NewPlanningResponseProcessor(options.Planner)
		responseProcessors = append(responseProcessors, planningResponseProcessor)
	}

	if options.EnableCodeExecutionResponseProcessor {
		responseProcessors = append(
			responseProcessors,
			processor.NewCodeExecutionResponseProcessor(),
		)
	}

	// Add output response processor if output_key or output_schema is configured or structured output is requested.
	if hasStaticOutputResponseProcessor(&options) {
		orp := processor.NewOutputResponseProcessor(options.OutputKey, options.OutputSchema)
		responseProcessors = append(responseProcessors, orp)
	} else {
		responseProcessors = append(
			responseProcessors,
			processor.NewConditionalResponseProcessor(
				hasInvocationStructuredOutput,
				processor.NewOutputResponseProcessor("", nil),
			),
		)
	}

	toolcallProcessor := processor.NewFunctionCallResponseProcessor(
		options.EnableParallelTools,
		options.ToolCallbacks,
		processor.WithToolCallRetryPolicy(options.ToolCallRetryPolicy),
	)
	// Configure default transfer message for direct sub-agent calls.
	// Default behavior (when not configured): enabled with built-in default message.
	if options.DefaultTransferMessage != nil {
		// Explicitly configured via WithDefaultTransferMessage.
		processor.SetDefaultTransferMessage(*options.DefaultTransferMessage)
	}
	responseProcessors = append(responseProcessors, toolcallProcessor)

	// Always install the transfer processor so dynamic sub-agent updates
	// (for example, via SubAgentSetter) can enable transfer_to_agent later.
	transferResponseProcessor := processor.NewTransferResponseProcessor(
		options.EndInvocationAfterTransfer,
	)
	responseProcessors = append(responseProcessors, transferResponseProcessor)

	// Create flow with the provided processors and options.
	flowOpts := llmflow.Options{
		ChannelBufferSize:               options.ChannelBufferSize,
		ModelCallbacks:                  options.ModelCallbacks,
		SyncSummaryIntraRun:             options.SyncSummaryIntraRun,
		EnableContextCompaction:         options.EnableContextCompaction,
		ContextCompactionThresholdRatio: options.ContextCompactionThresholdRatio,
	}

	a.flow = llmflow.New(
		requestProcessors, responseProcessors,
		flowOpts,
	)

	return a
}

// buildRequestProcessors constructs the request processors in the required order.
func buildRequestProcessorsWithAgent(a *LLMAgent, options *Options) []flow.RequestProcessor {
	var requestProcessors []flow.RequestProcessor

	// 1. Basic processor - handles generation config.
	basicOptions := []processor.BasicOption{
		processor.WithGenerationConfig(options.GenerationConfig),
	}
	basicProcessor := processor.NewBasicRequestProcessor(basicOptions...)
	requestProcessors = append(requestProcessors, basicProcessor)

	// 2. Planning processor - handles planning instructions if planner is configured.
	if options.Planner != nil {
		planningProcessor := processor.NewPlanningRequestProcessor(options.Planner)
		requestProcessors = append(requestProcessors, planningProcessor)
	}

	// 3. Instruction processor - adds instruction content and system prompt.
	instructionOpts := []processor.InstructionRequestProcessorOption{
		processor.WithOutputSchema(options.OutputSchema),
	}
	// Fallback injection for structured output when the provider doesn't enforce JSON Schema natively.
	if options.StructuredOutput != nil && options.StructuredOutput.JSONSchema != nil {
		instructionOpts = append(instructionOpts,
			processor.WithStructuredOutputSchema(options.StructuredOutput.JSONSchema.Schema),
		)
	}
	instructionOpts = append(instructionOpts,
		processor.WithInstructionResolver(a.instructionForInvocation),
		processor.WithSystemPromptResolver(a.systemPromptForInvocation),
	)
	instructionProcessor := processor.NewInstructionRequestProcessor(
		"", // static value unused when resolver is present
		"", // static value unused when resolver is present
		instructionOpts...,
	)
	requestProcessors = append(requestProcessors, instructionProcessor)

	// 4. Identity processor - sets agent identity.
	if a.name != "" || options.Description != "" {
		identityProcessor := processor.NewIdentityRequestProcessor(
			a.name,
			options.Description,
			processor.WithAddNameToInstruction(options.AddNameToInstruction),
		)
		requestProcessors = append(requestProcessors, identityProcessor)
	}

	// 5. Skills processor - injects skill overview and loaded contents
	// when a skills repository is configured statically or resolved at
	// invocation time. This ensures the model sees available skills
	// (names/descriptions) and any loaded SKILL.md/doc texts before
	// deciding on tool calls.
	skillFlags := mustResolveSkillToolFlags(options)
	var skillsOpts []processor.SkillsRequestProcessorOption
	if options.skillsCapabilityGuidance != nil {
		skillsOpts = append(
			skillsOpts,
			processor.WithSkillsCapabilityGuidance(
				*options.skillsCapabilityGuidance,
			),
		)
	}
	if options.skillsProtocolGuidance != nil {
		skillsOpts = append(
			skillsOpts,
			processor.WithSkillsProtocolGuidance(
				*options.skillsProtocolGuidance,
			),
		)
	}
	if options.skillsToolingGuidance != nil {
		skillsOpts = append(
			skillsOpts,
			processor.WithSkillsToolingGuidance(
				*options.skillsToolingGuidance,
			),
		)
	}
	skillsOpts = append(
		skillsOpts,
		processor.WithSkillsRepositoryResolver(
			a.skillRepositoryForInvocation,
		),
		processor.WithSkillLoadMode(options.SkillLoadMode),
		processor.WithSkillToolFlags(skillFlags),
		processor.WithSkillToolFlagsResolver(
			a.skillToolFlagsForInvocation,
		),
		processor.WithSkillsDirectoryHints(
			options.skillsDirectoryHints,
		),
		processor.WithSkillsFilePathHints(
			options.skillsFilePathHints,
		),
	)
	if options.MaxLoadedSkills > 0 {
		skillsOpts = append(
			skillsOpts,
			processor.WithMaxLoadedSkills(
				options.MaxLoadedSkills,
			),
		)
	}
	if options.SkillsLoadedContentInToolResults {
		skillsOpts = append(
			skillsOpts,
			processor.WithSkillsLoadedContentInToolResults(true),
		)
	}
	skillsProcessor := processor.NewSkillsRequestProcessor(
		options.skillsRepository,
		skillsOpts...,
	)
	requestProcessors = append(requestProcessors, skillsProcessor)

	// 6. Workspace exec processor - injects executor/workspace guidance
	// when the current invocation exposes workspace_exec capability.
	workspaceOpts := []processor.WorkspaceExecRequestProcessorOption{
		processor.WithWorkspaceExecEnabledResolver(
			a.supportsWorkspaceExecForInvocation,
		),
		processor.WithWorkspaceExecSessionsResolver(
			a.supportsWorkspaceExecSessionsForInvocation,
		),
		processor.WithWorkspaceExecSkillsRepositoryResolver(
			a.skillRepositoryForInvocation,
		),
	}
	requestProcessors = append(
		requestProcessors,
		processor.NewWorkspaceExecRequestProcessor(workspaceOpts...),
	)

	// 7. Content processor - appends conversation/context history.
	contentOpts := []processor.ContentOption{
		processor.WithAddContextPrefix(options.AddContextPrefix),
		processor.WithAddSessionSummary(options.AddSessionSummary),
		processor.WithSessionSummaryInjectionMode(options.SessionSummaryInjectionMode),
		processor.WithMaxHistoryRuns(options.MaxHistoryRuns),
		processor.WithEnableContextCompaction(options.EnableContextCompaction),
		processor.WithContextCompactionKeepRecentRequests(
			options.ContextCompactionKeepRecentRequests,
		),
		processor.WithContextCompactionToolResultMaxTokens(
			options.ContextCompactionToolResultMaxTokens,
		),
		processor.WithContextCompactionOversizedToolResultMaxTokens(
			options.ContextCompactionOversizedToolResultMaxTokens,
		),
		processor.WithPreserveSameBranch(options.PreserveSameBranch),
		processor.WithPreserveForeignMessages(options.PreserveForeignMessages),
		processor.WithTimelineFilterMode(options.messageTimelineFilterMode),
		processor.WithBranchFilterMode(options.messageBranchFilterMode),
		processor.WithPreloadMemory(options.PreloadMemory),
		processor.WithPreloadSessionRecall(options.PreloadSessionRecall),
		processor.WithPreloadSessionRecallMinScore(
			options.PreloadSessionRecallMinScore,
		),
		processor.WithPreloadSessionRecallSearchMode(
			options.PreloadSessionRecallSearchMode,
		),
		processor.WithEventMessageProjector(
			processor.EventMessageProjector(
				options.EventMessageProjector,
			),
		),
		processor.WithFewShotResolver(a.fewShotForInvocation),
	}
	if options.ReasoningContentMode != "" {
		contentOpts = append(contentOpts,
			processor.WithReasoningContentMode(options.ReasoningContentMode))
	}
	if options.summaryFormatter != nil {
		contentOpts = append(contentOpts,
			processor.WithSummaryFormatter(options.summaryFormatter))
	}
	contentProcessor := processor.NewContentRequestProcessor(contentOpts...)
	requestProcessors = append(requestProcessors, contentProcessor)

	// 8. Post-tool processor - injects dynamic prompt after tool results.
	requestProcessors = appendPostToolProcessor(options, requestProcessors)

	// 9. Skills tool result processor - materializes loaded skill content
	// into tool result messages.
	requestProcessors = appendSkillsToolResultProcessor(a, options, requestProcessors)

	// 10. Time processor - adds current time information if enabled.
	// Moved after content processor to avoid invalidating system message cache.
	// Time information changes frequently, so placing it last allows previous
	// stable content (instructions, identity, skills, history) to be cached.
	requestProcessors = appendTimeProcessor(options, requestProcessors)

	return requestProcessors
}

func hasStaticOutputResponseProcessor(options *Options) bool {
	return options.OutputKey != "" || options.OutputSchema != nil || options.StructuredOutput != nil
}

func hasInvocationStructuredOutput(ctx context.Context, invocation *agent.Invocation) bool {
	if invocation == nil {
		return false
	}
	return invocation.StructuredOutput != nil || invocation.StructuredOutputType != nil
}

func appendPostToolProcessor(options *Options, requestProcessors []flow.RequestProcessor) []flow.RequestProcessor {
	if options.postToolPromptEnabled != nil &&
		!*options.postToolPromptEnabled {
		return requestProcessors
	}

	var postToolOpts []processor.PostToolOption
	if options.PostToolPrompt != "" {
		postToolOpts = append(
			postToolOpts,
			processor.WithPostToolPrompt(options.PostToolPrompt),
		)
	}
	postToolProcessor := processor.NewPostToolRequestProcessor(postToolOpts...)
	return append(requestProcessors, postToolProcessor)
}

func appendSkillsToolResultProcessor(a *LLMAgent, options *Options, requestProcessors []flow.RequestProcessor) []flow.RequestProcessor {
	if !options.SkillsLoadedContentInToolResults {
		return requestProcessors
	}
	skillsToolResultProcessor :=
		processor.NewSkillsToolResultRequestProcessor(
			options.skillsRepository,
			processor.WithSkillsToolResultRepositoryResolver(
				a.skillRepositoryForInvocation,
			),
			processor.WithSkillsToolResultLoadMode(
				options.SkillLoadMode,
			),
			processor.WithSkillsToolResultDirectoryHints(
				options.skillsDirectoryHints,
			),
			processor.WithSkillsToolResultFilePathHints(
				options.skillsFilePathHints,
			),
			processor.WithSkipSkillsFallbackOnSessionSummary(
				options.SkipSkillsFallbackOnSessionSummary,
			),
		)
	return append(requestProcessors, skillsToolResultProcessor)
}

func appendTimeProcessor(options *Options, requestProcessors []flow.RequestProcessor) []flow.RequestProcessor {
	if !options.AddCurrentTime {
		return requestProcessors
	}
	timeProcessor := processor.NewTimeRequestProcessor(
		processor.WithAddCurrentTime(true),
		processor.WithTimezone(options.Timezone),
		processor.WithTimeFormat(options.TimeFormat),
	)
	return append(requestProcessors, timeProcessor)
}

// buildRequestProcessors preserves the original helper signature for tests and
// legacy callers. It constructs a temporary agent instance and forwards to
// buildRequestProcessorsWithAgent. Dynamic updates are not supported when using
// this legacy function; use New() which wires the real agent for runtime getters.
func buildRequestProcessors(name string, options *Options) []flow.RequestProcessor { // nolint:deadcode
	prepareSkillsRepository(options)
	dummy := &LLMAgent{
		name:                    name,
		instruction:             newTextPrompt(options.Instruction),
		systemPrompt:            newTextPrompt(options.GlobalInstruction),
		modelInstructions:       cloneTextPromptMap(options.ModelInstructions),
		modelGlobalInstructions: cloneTextPromptMap(options.ModelGlobalInstructions),
		option:                  *options,
	}
	return buildRequestProcessorsWithAgent(dummy, options)
}

func newTextPrompt(template string) prompt.Text {
	return prompt.Text{Template: template}
}

func cloneTextPromptMap(src map[string]string) map[string]prompt.Text {
	if src == nil {
		return nil
	}
	dst := make(map[string]prompt.Text, len(src))
	for key, value := range src {
		dst[key] = newTextPrompt(value)
	}
	return dst
}

func prepareSkillsRepository(options *Options) {
	if options == nil ||
		options.skillsRepository == nil ||
		options.skillFilter == nil {
		return
	}
	options.skillsRepository = skill.NewFilteredRepository(
		options.skillsRepository,
		options.skillFilter,
	)
}

// applySkillsExecutorFallback auto-wires a local code executor when the
// caller enabled skills via WithSkills but did not provide an executor.
// This preserves the zero-config upgrade path (WithSkills alone should
// keep working) while still letting callers fully opt out.
//
// The fallback is intentionally skipped when:
//   - an executor was already configured via WithCodeExecutor,
//   - the caller used WithAllowedSkillTools to drive fine-grained tool
//     selection (they are being explicit about what they want), or
//   - the caller explicitly selected SkillToolProfileKnowledgeOnly,
//     which is the opt-out signal for "no convenience execution wiring
//     from the framework".
//
// Note: the distinction between the unconfigured default and an
// explicit KnowledgeOnly profile is intentional; both normalize to the
// same built-in skill tool set, but only an explicit opt-in disables
// the fallback.
//
// Scope of the fallback: the auto-injected CodeExecutor exists to power
// execution tools such as workspace_exec. It must not silently expand
// the agent's execution surface to also auto-execute fenced code from
// assistant replies. Therefore, when this path injects an executor and
// the caller has NOT explicitly configured
// WithEnableCodeExecutionResponseProcessor, the function also disables
// EnableCodeExecutionResponseProcessor. Callers who explicitly set
// that option (true or false) keep their configured value.
func applySkillsExecutorFallback(options *Options) {
	if options == nil ||
		options.skillsRepository == nil ||
		options.codeExecutor != nil ||
		options.allowedSkillTools != nil ||
		skillprofile.IsExplicitKnowledgeOnly(options.skillToolProfile) {
		return
	}
	options.codeExecutor = defaultCodeExecutor()
	if !options.codeExecutionResponseProcessorExplicit {
		options.EnableCodeExecutionResponseProcessor = false
	}
}

// initializeModels initializes the models map and determines the initial
// model based on WithModel and WithModels options.
func initializeModels(options *Options) (model.Model, map[string]model.Model) {
	models := make(map[string]model.Model)

	// Case 1: No models configured at all.
	if options.Model == nil && len(options.Models) == 0 {
		return nil, models
	}

	// Case 2: Only WithModel is set, no WithModels.
	if len(options.Models) == 0 {
		models[defaultModelName] = options.Model
		return options.Model, models
	}

	// Case 3: WithModels is set (with or without WithModel).
	models = options.Models

	// If WithModel is also set, use it as the initial model.
	if options.Model != nil {
		// Check if the model is already in the models map.
		found := false
		for _, m := range models {
			if m == options.Model {
				found = true
				break
			}
		}
		// If not found, add it with the default name.
		if !found {
			models[defaultModelName] = options.Model
		}
		return options.Model, models
	}

	// WithModels is set but WithModel is not, use the first model from map.
	// Note: map iteration order is not guaranteed.
	for _, m := range models {
		return m, models
	}

	// Should not reach here, but return nil for safety.
	return nil, models
}

func registerTools(options *Options) ([]tool.Tool, map[string]bool) {
	userToolNames := collectUserToolNames(options.Tools)
	allTools := append([]tool.Tool(nil), options.Tools...)
	allTools, userToolNames = appendStaticToolSetTools(
		allTools, userToolNames, options,
	)
	allTools = appendKnowledgeTools(allTools, options)
	var runTool *toolskill.RunTool
	var workspaceRegistry *codeexecutor.WorkspaceRegistry
	if options.skillsRepository != nil {
		if options.codeExecutor != nil {
			workspaceRegistry = buildWorkspaceRegistry()
		}
		runTool = buildSkillRunTool(options, workspaceRegistry)
	} else if executorSupportsWorkspaceExec(options) {
		workspaceRegistry = buildWorkspaceRegistry()
	}
	allTools = appendWorkspaceExecTool(
		allTools,
		options,
		workspaceRegistry,
		nil,
	)
	allTools = appendSkillTools(allTools, options, runTool)
	return allTools, userToolNames
}

func collectUserToolNames(tools []tool.Tool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, t := range tools {
		names[t.Declaration().Name] = true
	}
	return names
}

func appendStaticToolSetTools(
	allTools []tool.Tool,
	userToolNames map[string]bool,
	options *Options,
) ([]tool.Tool, map[string]bool) {
	if options.RefreshToolSetsOnRun {
		return allTools, userToolNames
	}

	ctx := context.Background()
	for _, toolSet := range options.ToolSets {
		namedToolSet := itool.NewNamedToolSet(toolSet)
		for _, t := range namedToolSet.Tools(ctx) {
			allTools = append(allTools, t)
			userToolNames[t.Declaration().Name] = true
		}
	}
	return allTools, userToolNames
}

func appendKnowledgeTools(
	allTools []tool.Tool,
	options *Options,
) []tool.Tool {
	if options.Knowledge == nil {
		return allTools
	}

	toolOpts := []knowledgetool.Option{
		knowledgetool.WithFilter(options.KnowledgeFilter),
	}
	if options.KnowledgeConditionedFilter != nil {
		toolOpts = append(
			toolOpts,
			knowledgetool.WithConditionedFilter(
				options.KnowledgeConditionedFilter,
			),
		)
	}

	if options.EnableKnowledgeAgenticFilter {
		return append(allTools, knowledgetool.NewAgenticFilterSearchTool(
			options.Knowledge, options.AgenticFilterInfo, toolOpts...,
		))
	}
	return append(allTools, knowledgetool.NewKnowledgeSearchTool(
		options.Knowledge, toolOpts...,
	))
}

func appendSkillTools(
	allTools []tool.Tool,
	options *Options,
	runTool *toolskill.RunTool,
) []tool.Tool {
	var exec codeexecutor.CodeExecutor
	if options != nil {
		exec = options.codeExecutor
	}
	return appendSkillToolsWithRepoAndFlags(
		allTools,
		options,
		options.skillsRepository,
		nil,
		runTool,
		exec,
		mustResolveSkillToolFlags(options),
	)
}

func appendSkillToolsWithRepo(
	allTools []tool.Tool,
	options *Options,
	repo skill.Repository,
	reg *codeexecutor.WorkspaceRegistry,
	runTool *toolskill.RunTool,
) []tool.Tool {
	var exec codeexecutor.CodeExecutor
	if options != nil {
		exec = options.codeExecutor
	}
	return appendSkillToolsWithRepoAndFlags(
		allTools,
		options,
		repo,
		reg,
		runTool,
		exec,
		mustResolveSkillToolFlags(options),
	)
}

func appendSkillToolsWithRepoAndFlags(
	allTools []tool.Tool,
	options *Options,
	repo skill.Repository,
	reg *codeexecutor.WorkspaceRegistry,
	runTool *toolskill.RunTool,
	exec codeexecutor.CodeExecutor,
	skillFlags skillprofile.Flags,
) []tool.Tool {
	if repo == nil {
		return allTools
	}
	if skillFlags.Load {
		loadOpts := []toolskill.LoadToolOption{}
		if options.skillLoadToolDescription != nil {
			loadOpts = append(
				loadOpts,
				toolskill.WithLoadToolDescription(
					*options.skillLoadToolDescription,
				),
			)
		}
		allTools = append(
			allTools,
			toolskill.NewLoadToolWithOptions(
				repo,
				loadOpts...,
			),
		)
	}
	if skillFlags.SelectDocs {
		allTools = append(
			allTools,
			toolskill.NewSelectDocsTool(repo),
		)
	}
	if skillFlags.ListDocs {
		allTools = append(
			allTools,
			toolskill.NewListDocsTool(repo),
		)
	}
	if !skillFlags.RequiresExecutionTools() {
		return allTools
	}

	if runTool == nil {
		runTool = buildSkillRunToolWithRepo(options, repo, reg, exec)
	}
	if skillFlags.Run {
		allTools = append(allTools, runTool)
	}
	if !skillFlags.RequiresExecSessionTools() {
		return allTools
	}
	execTool := toolskill.NewExecTool(runTool)
	if skillFlags.Exec {
		allTools = append(allTools, execTool)
	}
	if skillFlags.WriteStdin {
		allTools = append(
			allTools,
			toolskill.NewWriteStdinTool(execTool),
		)
	}
	if skillFlags.PollSession {
		allTools = append(
			allTools,
			toolskill.NewPollSessionTool(execTool),
		)
	}
	if skillFlags.KillSession {
		allTools = append(
			allTools,
			toolskill.NewKillSessionTool(execTool),
		)
	}
	return allTools
}

func mustResolveSkillToolFlags(options *Options) skillprofile.Flags {
	var exec codeexecutor.CodeExecutor
	if options != nil {
		exec = options.codeExecutor
	}
	return mustResolveSkillToolFlagsWithExecutor(options, exec)
}

func mustResolveSkillToolFlagsWithExecutor(
	options *Options,
	exec codeexecutor.CodeExecutor,
) skillprofile.Flags {
	if options == nil {
		return skillprofile.Flags{}
	}
	flags, err := skillprofile.ResolveFlags(
		options.skillToolProfile,
		options.allowedSkillTools,
	)
	if err != nil {
		panic(fmt.Sprintf(
			"Invalid LLMAgent configuration: %v",
			err,
		))
	}

	if options.skillRunRequireSkillLoaded &&
		(flags.Run || flags.Exec) &&
		!flags.Load {
		panic("Invalid LLMAgent configuration: " +
			"skill_run and skill_exec require skill_load when " +
			"WithSkillRunRequireSkillLoaded is enabled")
	}
	if !codeExecutorSupportsInteractive(exec) {
		flags = flags.WithoutInteractiveExecution()
	}
	return flags
}

func appendWorkspaceExecTool(
	allTools []tool.Tool,
	options *Options,
	reg *codeexecutor.WorkspaceRegistry,
	inv *agent.Invocation,
) []tool.Tool {
	var exec codeexecutor.CodeExecutor
	var loadedSkillsRepo skill.Repository
	if options != nil {
		exec = options.codeExecutor
		loadedSkillsRepo = options.skillsRepository
	}
	return appendWorkspaceExecToolWithExecutor(
		allTools,
		exec,
		executorSupportsWorkspaceExec(options),
		executorSupportsWorkspaceExecSessions(options),
		reg,
		inv,
		options,
		loadedSkillsRepo,
	)
}

// appendWorkspaceExecToolWithExecutor wires workspace_exec and its
// companion tools into allTools.
//
// loadedSkillsRepo is the effective skill repository for the current
// invocation. Callers on the invocation-scoped path pass the result
// of skillRepositoryForInvocation so that surface-patch repo
// overrides propagate into workspace_exec's loaded-skills reconcile;
// static callers (agent construction time) pass
// options.skillsRepository because no invocation context exists yet.
// Passing nil disables loaded-skills reconcile for this ExecTool.
func appendWorkspaceExecToolWithExecutor(
	allTools []tool.Tool,
	exec codeexecutor.CodeExecutor,
	enabled bool,
	sessional bool,
	reg *codeexecutor.WorkspaceRegistry,
	inv *agent.Invocation,
	options *Options,
	loadedSkillsRepo skill.Repository,
) []tool.Tool {
	if !enabled {
		return allTools
	}
	toolOpts := []func(*toolworkspaceexec.ExecTool){
		toolworkspaceexec.WithWorkspaceRegistry(reg),
	}
	toolOpts = append(
		toolOpts,
		workspacePrepOptions(options, loadedSkillsRepo)...,
	)
	execTool := toolworkspaceexec.NewExecTool(exec, toolOpts...)
	allTools = append(
		allTools,
		execTool,
	)
	if toolworkspaceexec.SupportsArtifactSave(inv) {
		allTools = append(
			allTools,
			toolworkspaceexec.NewSaveArtifactTool(execTool),
		)
	}
	if sessional {
		allTools = append(
			allTools,
			toolworkspaceexec.NewWriteStdinTool(execTool),
			toolworkspaceexec.NewKillSessionTool(execTool),
		)
	}
	return allTools
}

func buildWorkspaceRegistry() *codeexecutor.WorkspaceRegistry {
	return codeexecutor.NewWorkspaceRegistry()
}

// workspacePrepOptions translates llmagent-level workspace options
// (WithWorkspaceBootstrap, invocation-scoped loaded-skills wiring,
// explicit disable switch) into the public workspaceexec options.
// The workspaceexec package owns reconciler construction and
// conversation-files wiring, so no internal workspaceprep type ever
// crosses this boundary.
//
// loadedSkillsRepo is the repository the caller has already resolved
// for the current invocation; see appendWorkspaceExecToolWithExecutor
// for how callers pick between invocation-scoped and agent-default
// repos. Passing a nil repo skips the loaded-skills wiring entirely,
// which is what we want when the agent has no skill support
// configured at all.
func workspacePrepOptions(
	opts *Options,
	loadedSkillsRepo skill.Repository,
) []func(*toolworkspaceexec.ExecTool) {
	if opts == nil || opts.disableWorkspacePreparers {
		return nil
	}
	var out []func(*toolworkspaceexec.ExecTool)
	if len(opts.workspaceBootstrap.Files) > 0 ||
		len(opts.workspaceBootstrap.Commands) > 0 {
		out = append(out, toolworkspaceexec.WithWorkspaceBootstrap(
			opts.workspaceBootstrap,
		))
	}
	if loadedSkillsRepo != nil {
		out = append(out, toolworkspaceexec.WithLoadedSkills(
			loadedSkillsRepo,
		))
	}
	return out
}

func buildSkillRunTool(
	options *Options,
	reg *codeexecutor.WorkspaceRegistry,
) *toolskill.RunTool {
	return buildSkillRunToolWithRepo(
		options,
		options.skillsRepository,
		reg,
		nil,
	)
}

func buildSkillRunToolWithRepo(
	options *Options,
	repo skill.Repository,
	reg *codeexecutor.WorkspaceRegistry,
	exec codeexecutor.CodeExecutor,
) *toolskill.RunTool {
	if exec == nil && options != nil {
		exec = options.codeExecutor
	}
	if exec == nil {
		exec = defaultCodeExecutor()
	}

	runOpts := make([]func(*toolskill.RunTool), 0, 6)
	if len(options.skillRunAllowedCommands) > 0 {
		runOpts = append(
			runOpts,
			toolskill.WithAllowedCommands(
				options.skillRunAllowedCommands...,
			),
		)
	}
	if len(options.skillRunDeniedCommands) > 0 {
		runOpts = append(
			runOpts,
			toolskill.WithDeniedCommands(
				options.skillRunDeniedCommands...,
			),
		)
	}
	runOpts = append(
		runOpts,
		toolskill.WithRunOutputLimits(
			options.skillRunOutputLimits,
		),
	)
	if options.skillRunForceSaveArtifacts {
		runOpts = append(runOpts, toolskill.WithForceSaveArtifacts(true))
	}
	if options.skillRunRequireSkillLoaded {
		runOpts = append(
			runOpts,
			toolskill.WithRequireSkillLoaded(true),
		)
	}
	if options.skillRunStager != nil {
		runOpts = append(
			runOpts,
			toolskill.WithSkillStager(options.skillRunStager),
		)
	}
	if reg != nil {
		runOpts = append(runOpts, toolskill.WithWorkspaceRegistry(reg))
	}

	return toolskill.NewRunTool(
		repo,
		exec,
		runOpts...,
	)
}

// executorSupportsInteractive reports whether the effective engine
// behind the configured code executor exposes an
// InteractiveProgramRunner.  The check mirrors the fallback logic in
// RunTool.ensureEngine: when the executor does not implement
// EngineProvider (or returns a nil engine), the runtime falls back to
// a local engine which does support interactive sessions, so we
// return true in those cases.
func executorSupportsInteractive(options *Options) bool {
	var exec codeexecutor.CodeExecutor
	if options != nil {
		exec = options.codeExecutor
	}
	return codeExecutorSupportsInteractive(exec)
}

func codeExecutorSupportsInteractive(exec codeexecutor.CodeExecutor) bool {
	if exec == nil {
		exec = defaultCodeExecutor()
	}
	ep, ok := exec.(codeexecutor.EngineProvider)
	if !ok {
		// ensureEngine falls back to localexec which supports interactive.
		return true
	}
	eng := ep.Engine()
	if eng == nil {
		return true
	}
	_, interactive := eng.Runner().(codeexecutor.InteractiveProgramRunner)
	return interactive
}

// executorSupportsWorkspaceExec reports whether the agent has an
// explicit executor that exposes a live workspace engine suitable for
// generic workspace-side command execution. Unlike skill_run, this does
// not fall back to the local engine because that would silently move
// commands onto the agent host instead of the configured executor.
func executorSupportsWorkspaceExec(options *Options) bool {
	if !workspaceExecSurfaceEnabled(options) {
		return false
	}
	return codeExecutorSupportsWorkspaceExec(options.codeExecutor)
}

func codeExecutorSupportsWorkspaceExec(exec codeexecutor.CodeExecutor) bool {
	if exec == nil {
		return false
	}
	ep, ok := exec.(codeexecutor.EngineProvider)
	if !ok || ep == nil {
		return false
	}
	eng := ep.Engine()
	if eng == nil {
		return false
	}
	return eng.Manager() != nil && eng.FS() != nil && eng.Runner() != nil
}

// executorSupportsWorkspaceExecSessions reports whether workspace_exec can
// expose interactive session helpers such as workspace_write_stdin.
func executorSupportsWorkspaceExecSessions(options *Options) bool {
	if !workspaceExecSurfaceEnabled(options) {
		return false
	}
	return codeExecutorSupportsWorkspaceExecSessions(options.codeExecutor)
}

func workspaceExecSurfaceEnabled(options *Options) bool {
	if options == nil {
		return false
	}
	if options.workspaceExecSurfaceEnabled == nil {
		return true
	}
	return *options.workspaceExecSurfaceEnabled
}

func codeExecutorSupportsWorkspaceExecSessions(
	exec codeexecutor.CodeExecutor,
) bool {
	if !codeExecutorSupportsWorkspaceExec(exec) {
		return false
	}
	ep := exec.(codeexecutor.EngineProvider)
	eng := ep.Engine()
	if eng == nil || eng.Runner() == nil {
		return false
	}
	_, ok := eng.Runner().(codeexecutor.InteractiveProgramRunner)
	return ok
}

// Run implements the agent.Agent interface.
// It executes the LLM agent flow and returns a channel of events.
func (a *LLMAgent) Run(ctx context.Context, invocation *agent.Invocation) (e <-chan *event.Event, err error) {
	a.setupInvocation(invocation)
	ctx, span, startedSpan := itrace.StartSpan(
		ctx,
		invocation,
		fmt.Sprintf(
			"%s %s",
			itelemetry.OperationInvokeAgent,
			invocation.AgentName,
		),
	)
	effectiveGenConfig := a.genConfig
	effectiveGenConfig.Stream = iagent.ResolveInvokeAgentStream(invocation, &effectiveGenConfig)
	promptText := a.systemPromptForInvocation(invocation) +
		a.instructionForInvocation(invocation)
	if startedSpan {
		itelemetry.TraceBeforeInvokeAgent(
			span,
			invocation,
			a.description,
			promptText,
			&effectiveGenConfig,
		)
	}
	tracker := itelemetry.NewInvokeAgentTracker(
		ctx,
		invocation,
		effectiveGenConfig.Stream,
		&err,
	)
	ctx, flowEventChan, err := a.executeAgentFlow(ctx, invocation)
	if err != nil {
		// Check if this is a custom response error (early return)
		var customErr *haveCustomResponseError
		if errors.As(err, &customErr) {
			if startedSpan {
				span.End()
			}
			return customErr.EventChan, nil
		}
		// Handle actual errors
		if startedSpan {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, itelemetry.ToErrorType(err, model.ErrorTypeRunError)))
			span.End()
		}
		return nil, err
	}
	return a.wrapEventChannelWithTelemetry(ctx, invocation, flowEventChan, span, tracker, startedSpan), nil
}

// executeAgentFlow executes the agent flow with before agent callbacks.
// Returns the updated context, event channel, and any error that occurred.
func (a *LLMAgent) executeAgentFlow(ctx context.Context, invocation *agent.Invocation) (context.Context, <-chan *event.Event, error) {
	if a.agentCallbacks != nil {
		result, err := a.agentCallbacks.RunBeforeAgent(ctx, &agent.BeforeAgentArgs{
			Invocation: invocation,
		})
		if err != nil {
			return ctx, nil, fmt.Errorf("before agent callback failed: %w", err)
		}
		// Use the context from result if provided.
		if result != nil && result.Context != nil {
			ctx = result.Context
		}
		if result != nil && result.CustomResponse != nil {
			// Create a channel that returns the custom response and then closes.
			eventChan := make(chan *event.Event, 1)
			// Create an event from the custom response.
			customEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, result.CustomResponse)
			agent.EmitEvent(ctx, invocation, eventChan, customEvent)
			close(eventChan)
			return ctx, nil, &haveCustomResponseError{EventChan: eventChan}
		}
	}

	// Use the underlying flow to execute the agent logic.
	flowEventChan, err := a.flow.Run(ctx, invocation)
	if err != nil {
		return ctx, nil, err
	}

	return ctx, flowEventChan, nil

}

// haveCustomResponseError represents an early return due to a custom response from before agent callbacks.
// This is not an actual error but a signal to return early with the custom response.
type haveCustomResponseError struct {
	EventChan <-chan *event.Event
}

func (e *haveCustomResponseError) Error() string {
	return "custom response provided, returning early"
}

// setupInvocation sets up the invocation
func (a *LLMAgent) setupInvocation(invocation *agent.Invocation) {
	// Set agent identity before resolving node-scoped surfaces.
	invocation.Agent = a
	invocation.AgentName = a.name

	// Set model: prioritize RunOptions.Model, then RunOptions.ModelName, then agent's default model.
	a.mu.RLock()
	if patchedModel, ok := a.modelSurfaceForInvocation(invocation); ok {
		invocation.Model = patchedModel
	} else if invocation.RunOptions.Model != nil {
		// Check if a per-request model is specified.
		// Use the model directly from RunOptions.
		invocation.Model = invocation.RunOptions.Model
	} else if invocation.RunOptions.ModelName != "" {
		// Look up model by name from registered models.
		if m, ok := a.models[invocation.RunOptions.ModelName]; ok {
			invocation.Model = m
		} else {
			// If model name not found, fall back to agent's default model.
			// Log a warning but don't fail the request.
			invocation.Model = a.model
		}
	} else {
		// Use agent's default model.
		invocation.Model = a.model
	}
	a.mu.RUnlock()

	// Lift run-scoped structured output into the current invocation once.
	if invocation.StructuredOutput == nil {
		invocation.StructuredOutput = invocation.RunOptions.StructuredOutput
	}
	if invocation.StructuredOutputType == nil {
		invocation.StructuredOutputType = invocation.RunOptions.StructuredOutputType
	}
	// Keep run-scoped values on RunOptions so clone-based handoffs can reuse the same output contract.
	if invocation.StructuredOutput == nil {
		invocation.StructuredOutput = a.structuredOutput
	}
	if invocation.StructuredOutputType == nil {
		invocation.StructuredOutputType = a.structuredOutputType
	}

	// Propagate per-agent safety limits into the invocation. These limits are
	// evaluated by the Invocation helpers (IncLLMCallCount / IncToolIteration)
	// and enforced at the flow layer. When the values are <= 0, the helpers
	// treat them as "no limit", preserving existing behavior.
	invocation.MaxLLMCalls = a.option.MaxLLMCalls
	invocation.MaxToolIterations = a.option.MaxToolIterations
}

// wrapEventChannel wraps the event channel to apply after agent callbacks.
func (a *LLMAgent) wrapEventChannelWithTelemetry(
	ctx context.Context,
	invocation *agent.Invocation,
	originalChan <-chan *event.Event,
	span sdktrace.Span,
	tracker *itelemetry.InvokeAgentTracker,
	startedSpan bool,
) <-chan *event.Event {
	// Create a new channel with the same capacity as the original channel
	wrappedChan := make(chan *event.Event, cap(originalChan))
	runCtx := agent.CloneContext(ctx)
	go func(ctx context.Context) {
		var fullRespEvent *event.Event
		var responseErrorType string
		tokenUsage := &itelemetry.TokenUsage{}
		defer func() {
			finalizeWrappedTelemetry(
				span,
				tracker,
				fullRespEvent,
				responseErrorType,
				tokenUsage,
				startedSpan,
				wrappedChan,
			)
		}()
		// Forward all events from the original channel
		for evt := range originalChan {
			if trackedEvent := recordWrappedEventTelemetry(
				evt,
				tracker,
				tokenUsage,
				&responseErrorType,
			); trackedEvent != nil {
				fullRespEvent = trackedEvent
			}
			if err := event.EmitEvent(ctx, wrappedChan, evt); err != nil {
				return
			}
		}
		if ctx, evt := a.runAfterAgentCallback(ctx, invocation, fullRespEvent); evt != nil {
			fullRespEvent = evt
			agent.EmitEvent(ctx, invocation, wrappedChan, evt)
		}
	}(runCtx)

	return wrappedChan
}

func finalizeWrappedTelemetry(
	span sdktrace.Span,
	tracker *itelemetry.InvokeAgentTracker,
	fullRespEvent *event.Event,
	responseErrorType string,
	tokenUsage *itelemetry.TokenUsage,
	startedSpan bool,
	wrappedChan chan *event.Event,
) {
	responseErrorType = resolveWrappedResponseErrorType(fullRespEvent, responseErrorType)
	if startedSpan {
		if fullRespEvent != nil {
			itelemetry.TraceAfterInvokeAgent(
				span,
				fullRespEvent,
				tokenUsage,
				tracker.FirstTokenTimeDuration(),
				model.ErrorTypeRunError,
			)
		} else if responseErrorType != "" {
			span.SetStatus(codes.Error, responseErrorType)
			span.SetAttributes(
				attribute.String(semconvtrace.KeyErrorType, responseErrorType),
			)
		}
	}
	tracker.SetResponseErrorType(responseErrorType)
	tracker.RecordMetrics()()
	if startedSpan {
		span.End()
	}
	close(wrappedChan)
}

func resolveWrappedResponseErrorType(fullRespEvent *event.Event, responseErrorType string) string {
	if fullRespEvent == nil || fullRespEvent.Response == nil {
		return responseErrorType
	}
	if fullRespEvent.Response.Error == nil {
		return ""
	}
	return itelemetry.FormatResponseErrorLabel(
		fullRespEvent.Response.Error,
		model.ErrorTypeRunError,
	)
}

func recordWrappedEventTelemetry(
	evt *event.Event,
	tracker *itelemetry.InvokeAgentTracker,
	tokenUsage *itelemetry.TokenUsage,
	responseErrorType *string,
) *event.Event {
	if evt == nil {
		return nil
	}
	var fullRespEvent *event.Event
	if evt.Response != nil {
		tracker.TrackResponse(evt.Response)
		if !evt.Response.IsPartial {
			addWrappedTokenUsage(tokenUsage, evt.Response.Usage)
			fullRespEvent = evt
		}
	}
	if evt.Error != nil {
		*responseErrorType = itelemetry.FormatResponseErrorLabel(
			evt.Error,
			model.ErrorTypeRunError,
		)
	}
	return fullRespEvent
}

func addWrappedTokenUsage(tokenUsage *itelemetry.TokenUsage, usage *model.Usage) {
	if usage == nil {
		return
	}
	tokenUsage.PromptTokens += usage.PromptTokens
	tokenUsage.CompletionTokens += usage.CompletionTokens
	tokenUsage.TotalTokens += usage.TotalTokens
}

func (a *LLMAgent) runAfterAgentCallback(
	ctx context.Context,
	invocation *agent.Invocation,
	fullRespEvent *event.Event,
) (context.Context, *event.Event) {
	if a.agentCallbacks == nil {
		return ctx, nil
	}
	result, err := a.agentCallbacks.RunAfterAgent(ctx, &agent.AfterAgentArgs{
		Invocation:        invocation,
		Error:             wrappedAgentError(fullRespEvent),
		FullResponseEvent: fullRespEvent,
	})
	if result != nil && result.Context != nil {
		ctx = result.Context
	}
	return ctx, wrappedAfterAgentEvent(invocation, result, err)
}

func wrappedAgentError(fullRespEvent *event.Event) error {
	if fullRespEvent == nil || fullRespEvent.Response == nil || fullRespEvent.Response.Error == nil {
		return nil
	}
	return fmt.Errorf(
		"%s: %s",
		fullRespEvent.Response.Error.Type,
		fullRespEvent.Response.Error.Message,
	)
}

func wrappedAfterAgentEvent(
	invocation *agent.Invocation,
	result *agent.AfterAgentResult,
	callbackErr error,
) *event.Event {
	if callbackErr != nil {
		return event.NewErrorEvent(
			invocation.InvocationID,
			invocation.AgentName,
			agent.ErrorTypeAgentCallbackError,
			callbackErr.Error(),
		)
	}
	if result == nil || result.CustomResponse == nil {
		return nil
	}
	return event.NewResponseEvent(
		invocation.InvocationID,
		invocation.AgentName,
		result.CustomResponse,
	)
}

// Info implements the agent.Agent interface.
// It returns the basic information about this agent.
func (a *LLMAgent) Info() agent.Info {
	return agent.Info{
		Name:         a.name,
		Description:  a.description,
		InputSchema:  a.inputSchema,
		OutputSchema: a.outputSchema,
	}
}

// getAllToolsLocked builds the full tool list.
// It combines user tools with framework tools like transfer_to_agent
// under the caller's read lock. It always returns a fresh slice so
// callers can safely use it after releasing the lock without data
// races.
//
// This variant is used by methods that don't accept a context (for
// example, Tools()). It uses context.Background() when refreshing tools
// from ToolSets.
func (a *LLMAgent) getAllToolsLocked() []tool.Tool {
	return a.getAllToolsLockedWithContext(context.Background())
}

func (a *LLMAgent) getAllToolsLockedWithContext(
	ctx context.Context,
) []tool.Tool {
	if ctx == nil {
		ctx = context.Background()
	}

	base := make([]tool.Tool, len(a.tools))
	copy(base, a.tools)

	// When RefreshToolSetsOnRun is enabled, rebuild tools from ToolSets
	// on each call to keep ToolSet-provided tools in sync with their
	// underlying dynamic source (for example, MCP ListTools).
	if a.option.RefreshToolSetsOnRun && len(a.option.ToolSets) > 0 {
		dynamic := make([]tool.Tool, 0)
		for _, toolSet := range a.option.ToolSets {
			namedToolSet := itool.NewNamedToolSet(toolSet)
			setTools := namedToolSet.Tools(ctx)
			dynamic = append(dynamic, setTools...)
		}

		if len(dynamic) > 0 {
			combined := make([]tool.Tool, 0, len(base)+len(dynamic))
			combined = append(combined, base...)
			combined = append(combined, dynamic...)
			base = combined
		}
	}

	if a.option.EnableAwaitUserReplyTool {
		base = append(base, toolawaitreply.New())
	}

	if len(a.subAgents) == 0 {
		return base
	}

	agentInfos := make([]agent.Info, len(a.subAgents))
	for i, subAgent := range a.subAgents {
		agentInfos[i] = subAgent.Info()
	}

	transferTool := transfer.New(agentInfos)
	return append(base, transferTool)
}

// Tools implements the agent.Agent interface.
// It returns the list of tools available to the agent, including
// transfer tools.
func (a *LLMAgent) Tools() []tool.Tool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.getAllToolsLocked()
}

// SubAgents returns the list of sub-agents for this agent.
func (a *LLMAgent) SubAgents() []agent.Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if len(a.subAgents) == 0 {
		return nil
	}

	subAgents := make([]agent.Agent, len(a.subAgents))
	copy(subAgents, a.subAgents)
	return subAgents
}

// FindSubAgent finds a sub-agent by name.
// Returns nil if no sub-agent with the given name is found.
func (a *LLMAgent) FindSubAgent(name string) agent.Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, subAgent := range a.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}

// UserTools returns the list of tools that were explicitly registered
// by the user via WithTools and WithToolSets options.
//
// User tools (can be filtered):
//   - Tools registered via WithTools
//   - Tools registered via WithToolSets
//
// Framework tools (never filtered, not included in this list):
//   - knowledge_search / agentic_knowledge_search (auto-added when
//     WithKnowledge is set)
//   - transfer_to_agent (auto-added when WithSubAgents is set)
//   - await_user_reply (auto-added when WithAwaitUserReplyTool(true))
//
// This method is used by the tool filtering logic to distinguish user
// tools from framework tools.
func (a *LLMAgent) UserTools() []tool.Tool {
	a.mu.RLock()
	defer a.mu.RUnlock()

	// When ToolSets are static, user tool tracking is based on the
	// snapshot captured at construction time.
	if !a.option.RefreshToolSetsOnRun {
		userTools := make([]tool.Tool, 0, len(a.userToolNames))
		for _, t := range a.tools {
			if a.userToolNames[t.Declaration().Name] {
				userTools = append(userTools, t)
			}
		}
		return userTools
	}

	// When ToolSets are refreshed on each run, user tools include:
	//   - Tools from WithTools (tracked in userToolNames)
	//   - Tools coming from ToolSets (wrapped as NamedTool).
	// Framework tools (knowledge_search, transfer_to_agent,
	// await_user_reply, etc.)
	// remain excluded.
	allTools := a.getAllToolsLocked()
	userTools := make([]tool.Tool, 0, len(allTools))

	for _, t := range allTools {
		name := t.Declaration().Name

		if a.userToolNames[name] {
			userTools = append(userTools, t)
			continue
		}

		if _, ok := t.(*itool.NamedTool); ok {
			userTools = append(userTools, t)
		}
	}

	return userTools
}

// FilterTools filters the list of tools based on the provided filter
// function.
func (a *LLMAgent) FilterTools(ctx context.Context) []tool.Tool {
	a.mu.RLock()
	tools := a.getAllToolsLockedWithContext(ctx)
	userToolNames := make(map[string]bool, len(a.userToolNames))
	for name, isUser := range a.userToolNames {
		userToolNames[name] = isUser
	}
	refreshToolSets := a.option.RefreshToolSetsOnRun
	filter := a.option.toolFilter
	a.mu.RUnlock()

	filtered := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		name := t.Declaration().Name
		isUser := userToolNames[name]

		if refreshToolSets {
			if _, ok := t.(*itool.NamedTool); ok {
				isUser = true
			}
		}

		if !isUser {
			filtered = append(filtered, t)
			continue
		}

		if filter == nil || filter(ctx, t) {
			filtered = append(filtered, t)
		}
	}

	return filtered
}

// CodeExecutor returns the code executor used by this agent.
// implements the agent.CodeExecutor interface.
// This allows the agent to execute code blocks in different environments.
func (a *LLMAgent) CodeExecutor() codeexecutor.CodeExecutor {
	return a.codeExecutor
}

// SetSubAgents replaces the sub-agents for this agent in a
// concurrency-safe way. This enables dynamic sub-agent discovery from
// registries without recreating the agent instance.
func (a *LLMAgent) SetSubAgents(subAgents []agent.Agent) {
	a.mu.Lock()
	a.subAgents = subAgents
	a.mu.Unlock()
}

// refreshToolsLocked recomputes the aggregated tool list and user tool
// tracking map from the current options. Caller must hold a.mu.Lock.
func (a *LLMAgent) refreshToolsLocked() {
	tools, userToolNames := registerTools(&a.option)
	a.tools = tools
	a.userToolNames = userToolNames
}

// AddToolSet adds or replaces a tool set at runtime in a
// concurrency-safe way. If another ToolSet with the same Name()
// already exists, it will be replaced. Subsequent invocations see the
// updated tool list without recreating the agent.
func (a *LLMAgent) AddToolSet(toolSet tool.ToolSet) {
	if toolSet == nil {
		return
	}

	name := toolSet.Name()

	a.mu.Lock()
	defer a.mu.Unlock()

	replaced := false
	for i, ts := range a.option.ToolSets {
		if name != "" && ts.Name() == name {
			a.option.ToolSets[i] = toolSet
			replaced = true
			break
		}
	}
	if !replaced {
		a.option.ToolSets = append(a.option.ToolSets, toolSet)
	}

	a.refreshToolsLocked()
}

// RemoveToolSet removes all tool sets whose Name() matches the given
// name. It returns true if at least one ToolSet was removed. Tools
// from the removed tool sets will no longer be exposed on future
// invocations.
func (a *LLMAgent) RemoveToolSet(name string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.option.ToolSets) == 0 {
		return false
	}

	dst := a.option.ToolSets[:0]
	removed := false
	for _, ts := range a.option.ToolSets {
		if ts.Name() == name {
			removed = true
			continue
		}
		dst = append(dst, ts)
	}
	if !removed {
		return false
	}
	a.option.ToolSets = dst

	a.refreshToolsLocked()

	return true
}

// SetToolSets replaces the agent ToolSets with the provided slice in a
// concurrency-safe way. Subsequent invocations will see tools from
// exactly these ToolSets plus framework tools (knowledge, skills).
func (a *LLMAgent) SetToolSets(toolSets []tool.ToolSet) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(toolSets) == 0 {
		a.option.ToolSets = nil
	} else {
		copied := make([]tool.ToolSet, len(toolSets))
		copy(copied, toolSets)
		a.option.ToolSets = copied
	}

	a.refreshToolsLocked()
}

// SetModel sets the model for this agent in a concurrency-safe way.
// This allows callers to manage multiple models externally and switch
// dynamically during runtime.
func (a *LLMAgent) SetModel(m model.Model) {
	a.mu.Lock()
	a.model = m
	a.mu.Unlock()
}

// SetModelByName switches the model by name in a concurrency-safe way.
// The model must be registered via WithModels option when creating the agent.
// Returns an error if the specified model name is not found.
func (a *LLMAgent) SetModelByName(modelName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	m, ok := a.models[modelName]
	if !ok {
		return fmt.Errorf("model %q not found in registered models", modelName)
	}

	a.model = m
	return nil
}

// SetInstruction updates the agent's instruction at runtime in a concurrency-safe way.
// Subsequent requests will use the new instruction without recreating the agent.
func (a *LLMAgent) SetInstruction(instruction string) {
	a.mu.Lock()
	a.instruction = newTextPrompt(instruction)
	a.mu.Unlock()
}

// SetGlobalInstruction updates the agent's global system prompt at runtime.
// This affects the system-level prompt prepended to requests.
func (a *LLMAgent) SetGlobalInstruction(systemPrompt string) {
	a.mu.Lock()
	a.systemPrompt = newTextPrompt(systemPrompt)
	a.mu.Unlock()
}

// SetPrompts updates the instruction and global system prompt together.
// Subsequent requests observe the pair from a single agent lock.
func (a *LLMAgent) SetPrompts(
	instruction string,
	systemPrompt string,
) {
	a.mu.Lock()
	a.instruction = newTextPrompt(instruction)
	a.systemPrompt = newTextPrompt(systemPrompt)
	a.mu.Unlock()
}

// SetModelInstructions updates the model-specific instruction overrides.
// Key: model.Info().Name, Value: instruction text.
func (a *LLMAgent) SetModelInstructions(instructions map[string]string) {
	copied := cloneTextPromptMap(instructions)
	a.mu.Lock()
	a.modelInstructions = copied
	a.mu.Unlock()
}

// SetModelGlobalInstructions updates the model-specific system prompt
// overrides.
// Key: model.Info().Name, Value: system prompt text.
func (a *LLMAgent) SetModelGlobalInstructions(prompts map[string]string) {
	copied := cloneTextPromptMap(prompts)
	a.mu.Lock()
	a.modelGlobalInstructions = copied
	a.mu.Unlock()
}

// getInstruction returns the current instruction with read lock.
func (a *LLMAgent) getInstruction() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.instruction.Template
}

// getSystemPrompt returns the current system prompt with read lock.
func (a *LLMAgent) getSystemPrompt() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.systemPrompt.Template
}

func (a *LLMAgent) instructionForInvocation(inv *agent.Invocation) string {
	return a.instructionPromptForInvocation(inv).Template
}

func (a *LLMAgent) instructionPromptForInvocation(inv *agent.Invocation) prompt.Text {
	if patch, ok := a.rootSurfacePatch(inv); ok {
		if instruction, ok := patch.Instruction(); ok {
			return newTextPrompt(instruction)
		}
	}
	if inv != nil && inv.RunOptions.Instruction != "" {
		return newTextPrompt(inv.RunOptions.Instruction)
	}
	modelName := ""
	if inv != nil && inv.Model != nil {
		modelName = inv.Model.Info().Name
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	if modelName != "" {
		if ins, ok := a.modelInstructions[modelName]; ok {
			return ins
		}
	}
	return a.instruction
}

func (a *LLMAgent) systemPromptForInvocation(inv *agent.Invocation) string {
	return a.systemPromptTextForInvocation(inv).Template
}

func (a *LLMAgent) systemPromptTextForInvocation(inv *agent.Invocation) prompt.Text {
	if patch, ok := a.rootSurfacePatch(inv); ok {
		if prompt, ok := patch.GlobalInstruction(); ok {
			return newTextPrompt(prompt)
		}
	}
	if inv != nil && inv.RunOptions.GlobalInstruction != "" {
		return newTextPrompt(inv.RunOptions.GlobalInstruction)
	}
	modelName := ""
	if inv != nil && inv.Model != nil {
		modelName = inv.Model.Info().Name
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	if modelName != "" {
		if prompt, ok := a.modelGlobalInstructions[modelName]; ok {
			return prompt
		}
	}
	return a.systemPrompt
}
