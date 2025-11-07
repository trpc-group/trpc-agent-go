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
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent/internal/jsonschema"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/llmflow"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	itelemetry "trpc.group/trpc-go/trpc-agent-go/internal/telemetry"
	itool "trpc.group/trpc-go/trpc-agent-go/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	knowledgetool "trpc.group/trpc-go/trpc-agent-go/knowledge/tool"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/transfer"
)

var defaultChannelBufferSize = 256

const (
	// defaultModelName is the model name used when only WithModel is set
	// without WithModels.
	defaultModelName = "__default__"

	// BranchFilterModePrefix Prefix matching pattern
	BranchFilterModePrefix = processor.BranchFilterModePrefix
	// BranchFilterModeAll include all
	BranchFilterModeAll = processor.BranchFilterModeAll
	// BranchFilterModeExact exact match
	BranchFilterModeExact = processor.BranchFilterModeExact

	// TimelineFilterAll includes all historical message records
	// Suitable for scenarios requiring full conversation context
	TimelineFilterAll = processor.TimelineFilterAll
	// TimelineFilterCurrentRequest only includes messages within the current request cycle
	// Filters out previous historical records, keeping only messages related to this request
	TimelineFilterCurrentRequest = processor.TimelineFilterCurrentRequest
	// TimelineFilterCurrentInvocation only includes messages within the current invocation session
	// Suitable for scenarios requiring isolation between different invocation cycles in long-running sessions
	TimelineFilterCurrentInvocation = processor.TimelineFilterCurrentInvocation
)

// Option is a function that configures an LLMAgent.
type Option func(*Options)

// WithModel sets the model to use.
func WithModel(model model.Model) Option {
	return func(opts *Options) {
		opts.Model = model
	}
}

// WithModels registers a map of models that can be switched by name.
// The map key is the model name, and the value is the model.Model instance.
// If both WithModel and WithModels are set, WithModel specifies the initial
// model. If only WithModels is set, the first model in the map will be used
// as the initial model (note: map iteration order is not guaranteed).
func WithModels(models map[string]model.Model) Option {
	return func(opts *Options) {
		opts.Models = models
	}
}

// WithDescription sets the description of the agent.
func WithDescription(description string) Option {
	return func(opts *Options) {
		opts.Description = description
	}
}

// WithInstruction sets the instruction of the agent.
func WithInstruction(instruction string) Option {
	return func(opts *Options) {
		opts.Instruction = instruction
	}
}

// WithGlobalInstruction sets the global instruction of the agent.
func WithGlobalInstruction(instruction string) Option {
	return func(opts *Options) {
		opts.GlobalInstruction = instruction
	}
}

// WithGenerationConfig sets the generation configuration.
func WithGenerationConfig(config model.GenerationConfig) Option {
	return func(opts *Options) {
		opts.GenerationConfig = config
	}
}

// WithChannelBufferSize sets the buffer size for event channels.
func WithChannelBufferSize(size int) Option {
	return func(opts *Options) {
		opts.ChannelBufferSize = size
	}
}

// WithCodeExecutor sets the code executor to use for executing code blocks.
func WithCodeExecutor(ce codeexecutor.CodeExecutor) Option {
	return func(opts *Options) {
		opts.codeExecutor = ce
	}
}

// WithTools sets the list of tools available to the agent.
func WithTools(tools []tool.Tool) Option {
	return func(opts *Options) {
		opts.Tools = tools
	}
}

// WithToolSets sets the list of tool sets available to the agent.
func WithToolSets(toolSets []tool.ToolSet) Option {
	return func(opts *Options) {
		opts.ToolSets = toolSets
	}
}

// WithPlanner sets the planner to use for planning instructions.
func WithPlanner(planner planner.Planner) Option {
	return func(opts *Options) {
		opts.Planner = planner
	}
}

// WithSubAgents sets the list of sub-agents available to the agent.
func WithSubAgents(subAgents []agent.Agent) Option {
	return func(opts *Options) {
		opts.SubAgents = subAgents
	}
}

// WithAgentCallbacks sets the agent callbacks.
func WithAgentCallbacks(callbacks *agent.Callbacks) Option {
	return func(opts *Options) {
		opts.AgentCallbacks = callbacks
	}
}

// WithModelCallbacks sets the model callbacks.
func WithModelCallbacks(callbacks *model.Callbacks) Option {
	return func(opts *Options) {
		opts.ModelCallbacks = callbacks
	}
}

// WithToolCallbacks sets the tool callbacks.
func WithToolCallbacks(callbacks *tool.Callbacks) Option {
	return func(opts *Options) {
		opts.ToolCallbacks = callbacks
	}
}

// WithKnowledge sets the knowledge base for the agent.
// If provided, the knowledge search tool will be automatically added to the agent's tools.
func WithKnowledge(kb knowledge.Knowledge) Option {
	return func(opts *Options) {
		opts.Knowledge = kb
	}
}

// WithOutputKey sets the key in session state to store the output of the agent.
func WithOutputKey(outputKey string) Option {
	return func(opts *Options) {
		opts.OutputKey = outputKey
	}
}

// WithOutputSchema sets the JSON schema for validating agent output.
// When this is set, the agent can ONLY reply and CANNOT use any tools,
// such as function tools, RAGs, agent transfer, etc.
func WithOutputSchema(schema map[string]any) Option {
	return func(opts *Options) {
		opts.OutputSchema = schema
	}
}

// WithInputSchema sets the JSON schema for validating agent input.
// When this is set, the agent's input will be validated against this schema
// when used as a tool or when receiving input from other agents.
func WithInputSchema(schema map[string]any) Option {
	return func(opts *Options) {
		opts.InputSchema = schema
	}
}

// WithAddNameToInstruction adds the agent name to the instruction if true.
func WithAddNameToInstruction(addNameToInstruction bool) Option {
	return func(opts *Options) {
		opts.AddNameToInstruction = addNameToInstruction
	}
}

// WithEnableParallelTools enables parallel tool execution if set to true.
// By default, tools execute serially for safety and compatibility.
func WithEnableParallelTools(enable bool) Option {
	return func(opts *Options) {
		opts.EnableParallelTools = enable
	}
}

// WithDefaultTransferMessage configures the default message used when the model
// calls a sub-agent without providing a message. If msg is an empty string,
// the default message injection is disabled; if non-empty, it is enabled and msg is used.
func WithDefaultTransferMessage(msg string) Option {
	return func(opts *Options) {
		opts.DefaultTransferMessage = &msg
	}
}

// WithStructuredOutputJSON sets a JSON schema structured output for normal runs.
// The schema is constructed automatically from the provided example type.
// Provide a typed zero-value pointer like: new(MyStruct) or (*MyStruct)(nil) and we infer the type.
func WithStructuredOutputJSON(examplePtr any, strict bool, description string) Option {
	return func(opts *Options) {
		// Infer reflect.Type from examplePtr.
		var t reflect.Type
		if examplePtr == nil {
			return
		}
		if rt := reflect.TypeOf(examplePtr); rt.Kind() == reflect.Pointer {
			t = rt
		} else {
			t = reflect.PointerTo(rt)
		}
		// Generate a robust JSON schema via the generator.
		gen := jsonschema.New()
		schema := gen.Generate(t.Elem())
		name := t.Elem().Name()
		opts.StructuredOutput = &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:        name,
				Schema:      schema,
				Strict:      strict,
				Description: description,
			},
		}
		opts.StructuredOutputType = t
	}
}

// WithAddCurrentTime adds the current time to the system prompt if true.
func WithAddCurrentTime(addCurrentTime bool) Option {
	return func(opts *Options) {
		opts.AddCurrentTime = addCurrentTime
	}
}

// WithTimezone specifies the timezone to use for time display.
func WithTimezone(timezone string) Option {
	return func(opts *Options) {
		opts.Timezone = timezone
	}
}

// WithTimeFormat specifies the format for time display.
// The format should be a valid Go time format string.
// See https://pkg.go.dev/time#Time.Format for more details.
func WithTimeFormat(timeFormat string) Option {
	return func(opts *Options) {
		opts.TimeFormat = timeFormat
	}
}

// WithAddContextPrefix controls whether to add "For context:" prefix when converting foreign events.
// When false, foreign agent events are passed directly without the prefix.
// This is useful for chain agents where you want to pass formatted data between agents.
func WithAddContextPrefix(addPrefix bool) Option {
	return func(opts *Options) {
		opts.AddContextPrefix = addPrefix
	}
}

// WithAddSessionSummary controls whether to prepend the current-branch summary
// as a system message in the request context when available.
func WithAddSessionSummary(addSummary bool) Option {
	return func(opts *Options) {
		opts.AddSessionSummary = addSummary
	}
}

// WithMaxHistoryRuns sets the maximum number of history messages when AddSessionSummary is false.
// When 0 (default), no limit is applied.
func WithMaxHistoryRuns(maxRuns int) Option {
	return func(opts *Options) {
		opts.MaxHistoryRuns = maxRuns
	}
}

// WithPreserveSameBranch controls whether messages from the same invocation
// branch lineage (ancestor/descendant) should preserve their original roles
// instead of being rewritten into user context when used as history.
// Default is true.
func WithPreserveSameBranch(preserve bool) Option {
	return func(opts *Options) {
		opts.PreserveSameBranch = preserve
	}
}

// WithKnowledgeFilter sets the knowledge filter for the knowledge base.
func WithKnowledgeFilter(filter map[string]any) Option {
	return func(opts *Options) {
		opts.KnowledgeFilter = filter
	}
}

// WithKnowledgeAgenticFilterInfo sets the knowledge agentic filter info for the knowledge base.
func WithKnowledgeAgenticFilterInfo(filter map[string][]any) Option {
	return func(opts *Options) {
		opts.AgenticFilterInfo = filter
	}
}

// WithEnableKnowledgeAgenticFilter sets whether enable llm generate filter for the knowledge base.
func WithEnableKnowledgeAgenticFilter(agenticFilter bool) Option {
	return func(opts *Options) {
		opts.EnableKnowledgeAgenticFilter = agenticFilter
	}
}

// WithEndInvocationAfterTransfer sets whether end invocation after transfer.
func WithEndInvocationAfterTransfer(end bool) Option {
	return func(opts *Options) {
		opts.EndInvocationAfterTransfer = end
	}
}

// WithMessageTimelineFilterMode sets the message timeline filter mode.
func WithMessageTimelineFilterMode(mode string) Option {
	return func(opts *Options) {
		opts.messageTimelineFilterMode = mode
	}
}

// WithMessageBranchFilterMode sets the message branch filter mode.
func WithMessageBranchFilterMode(mode string) Option {
	return func(opts *Options) {
		opts.messageBranchFilterMode = mode
	}
}

// Options contains configuration options for creating an LLMAgent.
type Options struct {
	// Name is the name of the agent.
	Name string
	// Model is the model to use for generating responses.
	Model model.Model
	// Models is a map of models that can be switched by name at runtime.
	Models map[string]model.Model
	// Description is a description of the agent.
	Description string
	// Instruction is the instruction for the agent.
	Instruction string
	// GlobalInstruction is the global instruction for the agent.
	// It will be used for all agents in the agent tree.
	GlobalInstruction string
	// GenerationConfig contains the generation configuration.
	GenerationConfig model.GenerationConfig
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
	codeExecutor      codeexecutor.CodeExecutor
	// Tools is the list of tools available to the agent.
	Tools []tool.Tool
	// ToolSets is the list of tool sets available to the agent.
	ToolSets []tool.ToolSet
	// Planner is the planner to use for planning instructions.
	Planner planner.Planner
	// SubAgents is the list of sub-agents available to the agent.
	SubAgents []agent.Agent
	// AgentCallbacks contains callbacks for agent operations.
	AgentCallbacks *agent.Callbacks
	// ModelCallbacks contains callbacks for model operations.
	ModelCallbacks *model.Callbacks
	// ToolCallbacks contains callbacks for tool operations.
	ToolCallbacks *tool.Callbacks
	// Knowledge is the knowledge base for the agent.
	// If provided, the knowledge search tool will be automatically added.
	Knowledge knowledge.Knowledge
	// KnowledgeFilter is the filter for the knowledge search tool.
	KnowledgeFilter map[string]any
	// EnableKnowledgeAgenticFilter enables agentic filter mode for knowledge search.
	// When true, allows the LLM to dynamically decide whether to pass filter parameters.
	EnableKnowledgeAgenticFilter bool
	// KnowledgeAgenticFilter is the knowledge agentic filter for the knowledge search tool.
	AgenticFilterInfo map[string][]any
	// AddNameToInstruction adds the agent name to the instruction if true.
	AddNameToInstruction bool
	// EnableParallelTools enables parallel tool execution if true.
	// If false (default), tools will execute serially for safety.
	EnableParallelTools bool
	// AddCurrentTime adds the current time to the system prompt if true.
	AddCurrentTime bool
	// Timezone specifies the timezone to use for time display.
	Timezone string
	// TimeFormat specifies the format for time display.
	TimeFormat string
	// OutputKey is the key in session state to store the output of the agent.
	OutputKey string
	// OutputSchema is the JSON schema for validating agent output.
	// When this is set, the agent can ONLY reply and CANNOT use any tools.
	OutputSchema map[string]any
	// InputSchema is the JSON schema for validating agent input.
	// When this is set, the agent's input will be validated against this schema
	// when used as a tool or when receiving input from other agents.
	InputSchema map[string]any
	// AddContextPrefix controls whether to add "For context:" prefix when converting foreign events.
	// When false, foreign agent events are passed directly without the prefix.
	AddContextPrefix bool

	// AddSessionSummary controls whether to prepend the current branch summary
	// as a system message when available (default: false).
	AddSessionSummary bool

	// MaxHistoryRuns sets the maximum number of history messages when AddSessionSummary is false.
	// When 0 (default), no limit is applied.
	MaxHistoryRuns int

	// PreserveSameBranch controls whether the content request processor
	// should preserve original roles (assistant/tool) for events that
	// belong to the same invocation branch lineage (ancestor/descendant).
	// When true, messages emitted within the same branch tree will not be
	// rewritten into user context, keeping their original roles intact.
	// Default is true (correct-by-default for multi-agent flows).
	PreserveSameBranch bool
	// StructuredOutput defines how the model should produce structured output in normal runs.
	StructuredOutput *model.StructuredOutput
	// StructuredOutputType is the reflect.Type of the example pointer used to generate the schema.
	StructuredOutputType reflect.Type
	// EndInvocationAfterTransfer controls whether to end the current agent invocation after transfer.
	// If true, the current agent will end the invocation after transfer, else the current agent will continue to run
	// when the transfer is complete. Defaults to true.
	EndInvocationAfterTransfer bool

	// DefaultTransferMessage holds the message to inject when the model directly
	// calls a sub-agent without providing a message. Configured via WithDefaultTransferMessage.
	// Behavior:
	//   - Not configured: use built-in default message.
	//   - Configured with empty string: use built-in default message.
	//   - Configured with non-empty: use the provided message.
	DefaultTransferMessage *string

	messageTimelineFilterMode string
	messageBranchFilterMode   string
}

// LLMAgent is an agent that uses an LLM to generate responses.
type LLMAgent struct {
	name                 string
	mu                   sync.RWMutex
	model                model.Model
	models               map[string]model.Model // Registered models for switching
	description          string
	instruction          string
	systemPrompt         string
	genConfig            model.GenerationConfig
	flow                 flow.Flow
	tools                []tool.Tool     // All tools (user tools + framework tools)
	userToolNames        map[string]bool // Names of tools explicitly registered by user via WithTools and WithToolSets
	codeExecutor         codeexecutor.CodeExecutor
	planner              planner.Planner
	subAgents            []agent.Agent // Sub-agents that can be delegated to
	agentCallbacks       *agent.Callbacks
	outputKey            string         // Key to store output in session state
	outputSchema         map[string]any // JSON schema for output validation
	inputSchema          map[string]any // JSON schema for input validation
	structuredOutput     *model.StructuredOutput
	structuredOutputType reflect.Type
}

// New creates a new LLMAgent with the given options.
func New(name string, opts ...Option) *LLMAgent {
	var options Options = Options{
		ChannelBufferSize:          defaultChannelBufferSize,
		EndInvocationAfterTransfer: true,
		// Default to preserving same-branch lineage so assistant/tool roles
		// from parent/child branches are retained for downstream agents.
		PreserveSameBranch: true,
	}

	// Apply function options.
	for _, opt := range opts {
		opt(&options)
	}

	// Validate output_schema configuration before registering tools.
	if options.OutputSchema != nil {
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
		name:                 name,
		model:                initialModel,
		models:               models,
		description:          options.Description,
		instruction:          options.Instruction,
		systemPrompt:         options.GlobalInstruction,
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

	responseProcessors = append(responseProcessors, processor.NewCodeExecutionResponseProcessor())

	// Add output response processor if output_key or output_schema is configured or structured output is requested.
	if options.OutputKey != "" || options.OutputSchema != nil || options.StructuredOutput != nil {
		orp := processor.NewOutputResponseProcessor(options.OutputKey, options.OutputSchema)
		responseProcessors = append(responseProcessors, orp)
	}

	toolcallProcessor := processor.NewFunctionCallResponseProcessor(options.EnableParallelTools, options.ToolCallbacks)
	// Configure default transfer message for direct sub-agent calls.
	// Default behavior (when not configured): enabled with built-in default message.
	if options.DefaultTransferMessage != nil {
		// Explicitly configured via WithDefaultTransferMessage.
		processor.SetDefaultTransferMessage(*options.DefaultTransferMessage)
	}
	responseProcessors = append(responseProcessors, toolcallProcessor)

	// Add transfer response processor if sub-agents are configured.
	if len(options.SubAgents) > 0 {
		transferResponseProcessor := processor.NewTransferResponseProcessor(options.EndInvocationAfterTransfer)
		responseProcessors = append(responseProcessors, transferResponseProcessor)
	}

	// Create flow with the provided processors and options.
	flowOpts := llmflow.Options{
		ChannelBufferSize: options.ChannelBufferSize,
		ModelCallbacks:    options.ModelCallbacks,
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
	if options.Instruction != "" || options.GlobalInstruction != "" ||
		(options.StructuredOutput != nil && options.StructuredOutput.JSONSchema != nil) {
		instructionOpts := []processor.InstructionRequestProcessorOption{
			processor.WithOutputSchema(options.OutputSchema),
		}
		// Fallback injection for structured output when the provider doesn't enforce JSON Schema natively.
		if options.StructuredOutput != nil && options.StructuredOutput.JSONSchema != nil {
			instructionOpts = append(instructionOpts,
				processor.WithStructuredOutputSchema(options.StructuredOutput.JSONSchema.Schema),
			)
		}
		// Always wire dynamic getters so instructions can be updated at runtime.
		instructionOpts = append(instructionOpts,
			processor.WithInstructionGetter(func() string { return a.getInstruction() }),
			processor.WithSystemPromptGetter(func() string { return a.getSystemPrompt() }),
		)
		instructionProcessor := processor.NewInstructionRequestProcessor(
			"", // static value unused when getters are present
			"", // static value unused when getters are present
			instructionOpts...,
		)
		requestProcessors = append(requestProcessors, instructionProcessor)
	}

	// 4. Identity processor - sets agent identity.
	if a.name != "" || options.Description != "" {
		identityProcessor := processor.NewIdentityRequestProcessor(
			a.name,
			options.Description,
			processor.WithAddNameToInstruction(options.AddNameToInstruction),
		)
		requestProcessors = append(requestProcessors, identityProcessor)
	}

	// 5. Time processor - adds current time information if enabled.
	if options.AddCurrentTime {
		timeProcessor := processor.NewTimeRequestProcessor(
			processor.WithAddCurrentTime(true),
			processor.WithTimezone(options.Timezone),
			processor.WithTimeFormat(options.TimeFormat),
		)
		requestProcessors = append(requestProcessors, timeProcessor)
	}

	contentProcessor := processor.NewContentRequestProcessor(
		processor.WithAddContextPrefix(options.AddContextPrefix),
		processor.WithAddSessionSummary(options.AddSessionSummary),
		processor.WithMaxHistoryRuns(options.MaxHistoryRuns),
		processor.WithPreserveSameBranch(options.PreserveSameBranch),
		processor.WithTimelineFilterMode(options.messageTimelineFilterMode),
		processor.WithBranchFilterMode(options.messageBranchFilterMode),
	)
	requestProcessors = append(requestProcessors, contentProcessor)

	return requestProcessors
}

// buildRequestProcessors preserves the original helper signature for tests and
// legacy callers. It constructs a temporary agent instance and forwards to
// buildRequestProcessorsWithAgent. Dynamic updates are not supported when using
// this legacy function; use New() which wires the real agent for runtime getters.
func buildRequestProcessors(name string, options *Options) []flow.RequestProcessor { // nolint:deadcode
	dummy := &LLMAgent{
		name:         name,
		instruction:  options.Instruction,
		systemPrompt: options.GlobalInstruction,
	}
	return buildRequestProcessorsWithAgent(dummy, options)
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
	// Track user-registered tool names from WithTools and WithToolSets.
	// These are tools explicitly registered by the user and can be subject to filtering.
	userToolNames := make(map[string]bool)

	// Tools from WithTools are user tools.
	for _, t := range options.Tools {
		userToolNames[t.Declaration().Name] = true
	}

	// Start with direct tools.
	allTools := make([]tool.Tool, 0, len(options.Tools))
	allTools = append(allTools, options.Tools...)

	// Add tools from each toolset with automatic namespacing.
	// Tools from WithToolSets are also user tools (user explicitly added them).
	ctx := context.Background()
	for _, toolSet := range options.ToolSets {
		// Create named toolset wrapper to avoid name conflicts.
		namedToolSet := itool.NewNamedToolSet(toolSet)
		setTools := namedToolSet.Tools(ctx)
		for _, t := range setTools {
			allTools = append(allTools, t)
			// Mark toolset tools as user tools.
			userToolNames[t.Declaration().Name] = true
		}
	}

	// Add knowledge search tool if knowledge base is provided.
	// This is a FRAMEWORK tool (auto-added by framework), NOT a user tool.
	// It should never be filtered out by user tool filters.
	if options.Knowledge != nil {
		if options.EnableKnowledgeAgenticFilter {
			agenticKnowledge := knowledgetool.NewAgenticFilterSearchTool(
				options.Knowledge, options.AgenticFilterInfo, knowledgetool.WithFilter(options.KnowledgeFilter),
			)
			allTools = append(allTools, agenticKnowledge)
			// Do NOT add to userToolNames - this is a framework tool.
		} else {
			knowledgeTool := knowledgetool.NewKnowledgeSearchTool(
				options.Knowledge, knowledgetool.WithFilter(options.KnowledgeFilter),
			)
			allTools = append(allTools, knowledgeTool)
			// Do NOT add to userToolNames - this is a framework tool.
		}
	}

	return allTools, userToolNames
}

// Run implements the agent.Agent interface.
// It executes the LLM agent flow and returns a channel of events.
func (a *LLMAgent) Run(ctx context.Context, invocation *agent.Invocation) (e <-chan *event.Event, err error) {
	a.setupInvocation(invocation)

	ctx, span := trace.Tracer.Start(ctx, fmt.Sprintf("%s %s", itelemetry.OperationInvokeAgent, a.name))
	itelemetry.TraceBeforeInvokeAgent(span, invocation, a.description, a.systemPrompt+a.instruction, &a.genConfig)

	flowEventChan, err := a.executeAgentFlow(ctx, invocation)
	if err != nil {
		// Check if this is a custom response error (early return)
		var customErr *haveCustomResponseError
		if errors.As(err, &customErr) {
			span.End()
			return customErr.EventChan, nil
		}
		// Handle actual errors
		span.SetStatus(codes.Error, err.Error())
		span.SetAttributes(attribute.String(itelemetry.KeyErrorType, itelemetry.ValueDefaultErrorType))
		span.End()
		return nil, err
	}

	return a.wrapEventChannel(ctx, invocation, flowEventChan, span), nil
}

// executeAgentFlow executes the agent flow with before agent callbacks.
// Returns the event channel and any error that occurred.
func (a *LLMAgent) executeAgentFlow(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if a.agentCallbacks != nil {
		customResponse, err := a.agentCallbacks.RunBeforeAgent(ctx, invocation)
		if err != nil {
			return nil, fmt.Errorf("before agent callback failed: %w", err)
		}
		if customResponse != nil {
			// Create a channel that returns the custom response and then closes.
			eventChan := make(chan *event.Event, 1)
			// Create an event from the custom response.
			customEvent := event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
			agent.EmitEvent(ctx, invocation, eventChan, customEvent)
			close(eventChan)
			return nil, &haveCustomResponseError{EventChan: eventChan}
		}
	}

	// Use the underlying flow to execute the agent logic.
	flowEventChan, err := a.flow.Run(ctx, invocation)
	if err != nil {
		return nil, err
	}

	return flowEventChan, nil

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
	// Set model: prioritize RunOptions.Model, then RunOptions.ModelName, then agent's default model.
	a.mu.RLock()
	// Check if a per-request model is specified.
	if invocation.RunOptions.Model != nil {
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

	// Set agent and agent name
	invocation.Agent = a
	invocation.AgentName = a.name

	// Propagate structured output configuration into invocation and request path.
	invocation.StructuredOutputType = a.structuredOutputType
	invocation.StructuredOutput = a.structuredOutput
}

// wrapEventChannel wraps the event channel to apply after agent callbacks.
func (a *LLMAgent) wrapEventChannel(
	ctx context.Context,
	invocation *agent.Invocation,
	originalChan <-chan *event.Event,
	span sdktrace.Span,
) <-chan *event.Event {
	wrappedChan := make(chan *event.Event, 256) // Use default buffer size

	go func() {
		var fullRespEvent *event.Event
		tokenUsage := &itelemetry.TokenUsage{}
		defer func() {
			if fullRespEvent != nil {
				itelemetry.TraceAfterInvokeAgent(span, fullRespEvent, tokenUsage)
			}
			span.End()
			close(wrappedChan)
		}()

		// Forward all events from the original channel
		for evt := range originalChan {
			if evt != nil && evt.Response != nil {
				if evt.Response.Usage != nil {
					tokenUsage.PromptTokens += evt.Response.Usage.PromptTokens
					tokenUsage.CompletionTokens += evt.Response.Usage.CompletionTokens
					tokenUsage.TotalTokens += evt.Response.Usage.TotalTokens
				}
				if !evt.Response.IsPartial {
					fullRespEvent = evt
				}

			}
			if err := event.EmitEvent(ctx, wrappedChan, evt); err != nil {
				return
			}
		}

		// After all events are processed, run after agent callbacks
		if a.agentCallbacks != nil {
			customResponse, err := a.agentCallbacks.RunAfterAgent(ctx, invocation, nil)
			var evt *event.Event
			if err != nil {
				// Send error event.
				evt = event.NewErrorEvent(
					invocation.InvocationID,
					invocation.AgentName,
					agent.ErrorTypeAgentCallbackError,
					err.Error(),
				)
			} else if customResponse != nil {
				// Create an event from the custom response.
				evt = event.NewResponseEvent(invocation.InvocationID, invocation.AgentName, customResponse)
			}
			if evt != nil {
				fullRespEvent = evt
			}

			agent.EmitEvent(ctx, invocation, wrappedChan, evt)
		}
	}()

	return wrappedChan
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

// Tools implements the agent.Agent interface.
// It returns the list of tools available to the agent, including transfer tools.
func (a *LLMAgent) Tools() []tool.Tool {
	if len(a.subAgents) == 0 {
		return a.tools
	}

	// Create agent info for sub-agents.
	agentInfos := make([]agent.Info, len(a.subAgents))
	for i, subAgent := range a.subAgents {
		agentInfos[i] = subAgent.Info()
	}

	transferTool := transfer.New(agentInfos)
	return append(a.tools, transferTool)
}

// SubAgents returns the list of sub-agents for this agent.
func (a *LLMAgent) SubAgents() []agent.Agent {
	return a.subAgents
}

// FindSubAgent finds a sub-agent by name.
// Returns nil if no sub-agent with the given name is found.
func (a *LLMAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range a.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}

// UserTools returns the list of tools that were explicitly registered by the user
// via WithTools and WithToolSets options.
//
// User tools (can be filtered):
//   - Tools registered via WithTools
//   - Tools registered via WithToolSets
//
// Framework tools (never filtered, not included in this list):
//   - knowledge_search / agentic_knowledge_search (auto-added when WithKnowledge is set)
//   - transfer_to_agent (auto-added when WithSubAgents is set)
//
// This method is used by the tool filtering logic to distinguish user tools from framework tools.
func (a *LLMAgent) UserTools() []tool.Tool {
	// Filter user tools from all tools
	userTools := make([]tool.Tool, 0, len(a.userToolNames))
	for _, t := range a.tools {
		if a.userToolNames[t.Declaration().Name] {
			userTools = append(userTools, t)
		}
	}
	return userTools
}

// CodeExecutor returns the code executor used by this agent.
// implements the agent.CodeExecutor interface.
// This allows the agent to execute code blocks in different environments.
func (a *LLMAgent) CodeExecutor() codeexecutor.CodeExecutor {
	return a.codeExecutor
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
	a.instruction = instruction
	a.mu.Unlock()
}

// SetGlobalInstruction updates the agent's global system prompt at runtime.
// This affects the system-level prompt prepended to requests.
func (a *LLMAgent) SetGlobalInstruction(systemPrompt string) {
	a.mu.Lock()
	a.systemPrompt = systemPrompt
	a.mu.Unlock()
}

// getInstruction returns the current instruction with read lock.
func (a *LLMAgent) getInstruction() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.instruction
}

// getSystemPrompt returns the current system prompt with read lock.
func (a *LLMAgent) getSystemPrompt() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.systemPrompt
}
