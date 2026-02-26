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
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent/internal/jsonschema"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/searchfilter"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/planner"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultChannelBufferSize = 256

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

	// ReasoningContentModeKeepAll keeps all reasoning_content in history.
	// Use this for debugging or when you need to retain thinking chains.
	ReasoningContentModeKeepAll = processor.ReasoningContentModeKeepAll
	// ReasoningContentModeDiscardPreviousTurns discards reasoning_content from
	// messages that belong to previous request turns. Messages within the current
	// request retain their reasoning_content (for tool call scenarios).
	// This is the default mode, recommended for DeepSeek models.
	ReasoningContentModeDiscardPreviousTurns = processor.ReasoningContentModeDiscardPreviousTurns
	// ReasoningContentModeDiscardAll discards all reasoning_content from history.
	// Use this for maximum bandwidth savings when reasoning history is not needed.
	ReasoningContentModeDiscardAll = processor.ReasoningContentModeDiscardAll

	// SkillLoadModeOnce injects loaded skill content for the next model
	// request, then offloads it from session state.
	SkillLoadModeOnce = processor.SkillLoadModeOnce
	// SkillLoadModeTurn keeps loaded skill content available for all model
	// requests within the current invocation, and offloads it when the next
	// invocation begins.
	SkillLoadModeTurn = processor.SkillLoadModeTurn
	// SkillLoadModeSession keeps loaded skill content available across
	// invocations until cleared or the session expires.
	SkillLoadModeSession = processor.SkillLoadModeSession
)

// MessageFilterMode is the mode for filtering messages.
type MessageFilterMode int

const (
	// FullContext Includes all messages with prefix matching (including historical messages).
	// equivalent to TimelineFilterAll + BranchFilterModePrefix.
	FullContext MessageFilterMode = iota
	// RequestContext includes only messages from the current request cycle that match the branch prefix.
	// equivalent to TimelineFilterCurrentRequest + BranchFilterModePrefix.
	RequestContext
	// IsolatedRequest includes only messages from the current request cycle that exactly match the branch.
	// equivalent to TimelineFilterCurrentRequest + BranchFilterModeExact.
	IsolatedRequest
	// IsolatedInvocation includes only messages from current invocation session that exactly match the branch,
	// equivalent to TimelineFilterCurrentInvocation + BranchFilterModeExact.
	IsolatedInvocation
)

var (
	defaultOptions = Options{
		ChannelBufferSize:                    defaultChannelBufferSize,
		EnableCodeExecutionResponseProcessor: true,
		EndInvocationAfterTransfer:           true,
		// Default to rewriting same-branch lineage events to user context so
		// that downstream agents see a consolidated user message stream unless
		// explicitly opted into preserving assistant/tool roles.
		PreserveSameBranch: false,
		// Default to disable memory preloading (use tools instead).
		// PreloadMemory configuration values:
		//   - 0 (default): Disable preloading (use tools instead).
		//   - N > 0: Load the most recent N memories.
		//   - -1: Load all memories.
		//     WARNING: Loading all memories may significantly increase token usage
		//     and API costs, especially for users with many stored memories.
		//     Consider using a positive limit (e.g., 10-50) for production use.
		PreloadMemory: 0,

		SkillLoadMode: SkillLoadModeTurn,

		SkipSkillsFallbackOnSessionSummary: true,
	}
)

// Option is a function that configures an LLMAgent.
type Option func(*Options)

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
	// ModelInstructions maps model.Info().Name to a model-specific instruction.
	// When present, it overrides Instruction for matching models.
	ModelInstructions map[string]string
	// ModelGlobalInstructions maps model.Info().Name to a model-specific system
	// prompt.
	// When present, it overrides GlobalInstruction for matching models.
	ModelGlobalInstructions map[string]string
	// GenerationConfig contains the generation configuration.
	GenerationConfig model.GenerationConfig
	// ChannelBufferSize is the buffer size for event channels (default: 256).
	ChannelBufferSize int
	codeExecutor      codeexecutor.CodeExecutor
	// EnableCodeExecutionResponseProcessor controls whether the agent
	// auto-executes fenced code blocks from model responses.
	//
	// Default: true (preserves existing behavior).
	EnableCodeExecutionResponseProcessor bool
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
	// KnowledgeFilter is the metadata filter for the knowledge search tool.
	KnowledgeFilter map[string]any
	// KnowledgeConditionedFilter is the complex condition filter for the knowledge search tool.
	KnowledgeConditionedFilter *searchfilter.UniversalFilterCondition
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
	// summaryFormatter allows custom formatting of session summary content.
	// When nil (default), uses the default formatSummaryContent function.
	summaryFormatter func(summary string) string

	// MaxLLMCalls is an optional upper bound on the number of LLM calls
	// allowed per invocation for this agent. When the value is:
	//   - > 0: the limit is enforced per invocation.
	//   - <= 0: no limit is applied (default, preserves existing behavior).
	MaxLLMCalls int

	// MaxToolIterations is an optional upper bound on how many tool-call
	// iterations are allowed per invocation for this agent. A "tool iteration"
	// is defined as an assistant response that contains tool calls and reaches
	// the FunctionCallResponseProcessor. When the value is:
	//   - > 0: the limit is enforced per invocation.
	//   - <= 0: no limit is applied (default, preserves existing behavior).
	MaxToolIterations int

	// PreserveSameBranch controls whether the content request processor
	// should preserve original roles (assistant/tool) for events that
	// belong to the same invocation branch lineage (ancestor/descendant).
	// When true, messages emitted within the same branch tree will not be
	// rewritten into user context, keeping their original roles intact.
	// Default is false, so same-branch events are merged into user context
	// unless explicitly opted into preserving roles.
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

	// RefreshToolSetsOnRun controls whether tools from ToolSets are
	// refreshed from the underlying ToolSet on each run.
	// When false (default), tools from ToolSets are resolved once at
	// construction time. When true, the agent will call ToolSet.Tools
	// again when building the tools list for each invocation.
	RefreshToolSetsOnRun bool

	// SkillLoadMode controls how long loaded skill bodies/docs remain
	// available in the system prompt.
	SkillLoadMode string

	// SkillsLoadedContentInToolResults controls where loaded skill bodies
	// and selected docs are materialized.
	//
	// When false (default), loaded content is appended to the system
	// message (legacy behavior).
	//
	// When true, loaded content is appended to the corresponding tool
	// result messages (skill_load / skill_select_docs). This keeps the
	// system prompt more stable for prompt caching.
	SkillsLoadedContentInToolResults bool

	// SkipSkillsFallbackOnSessionSummary controls whether the framework
	// skips the "Loaded skill context" system-message fallback when a
	// session summary is present in the request.
	//
	// Default: true.
	SkipSkillsFallbackOnSessionSummary bool

	// skillsRepository enables agent skills when non-nil.
	skillsRepository skill.Repository
	// skillsToolingGuidance overrides the built-in skills guidance block.
	skillsToolingGuidance *string
	// skillRunAllowedCommands restricts skill_run to allowlisted commands.
	skillRunAllowedCommands []string
	// skillRunDeniedCommands rejects denylisted commands for skill_run.
	skillRunDeniedCommands    []string
	messageTimelineFilterMode string
	messageBranchFilterMode   string

	// ReasoningContentMode controls how reasoning_content is handled in
	// multi-turn conversations. This is particularly important for DeepSeek
	// models where reasoning_content should be discarded from previous turns.
	ReasoningContentMode string

	toolFilter tool.FilterFunc

	// PreloadMemory sets the number of memories to preload into system prompt.
	// When > 0, the specified number of most recent memories are loaded.
	// When 0 (default), no memories are preloaded (use tools instead).
	// When < 0, all memories are loaded.
	PreloadMemory int

	// PostToolPrompt overrides the default dynamic prompt injected when
	// tool results are detected. When empty, the built-in default prompt
	// from processor.DefaultPostToolPrompt is used. Set to a non-empty
	// string to customize the guidance given to the model after tool calls.
	PostToolPrompt string
}

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

// WithModelInstructions sets model-specific instruction overrides.
// Key: model.Info().Name, Value: instruction text.
func WithModelInstructions(instructions map[string]string) Option {
	return func(opts *Options) {
		opts.ModelInstructions = cloneStringMap(instructions)
	}
}

// WithModelGlobalInstructions sets model-specific system prompt overrides.
// Key: model.Info().Name, Value: system prompt text.
func WithModelGlobalInstructions(prompts map[string]string) Option {
	return func(opts *Options) {
		opts.ModelGlobalInstructions = cloneStringMap(prompts)
	}
}

// WithGenerationConfig sets the generation configuration.
func WithGenerationConfig(config model.GenerationConfig) Option {
	return func(opts *Options) {
		opts.GenerationConfig = config
	}
}

// WithMaxLLMCalls sets the optional upper bound on the number of LLM calls
// allowed per invocation for this agent. When limit is:
//   - > 0: the limit is enforced per invocation.
//   - <= 0: no limit is applied (default behavior).
func WithMaxLLMCalls(limit int) Option {
	return func(opts *Options) {
		opts.MaxLLMCalls = limit
	}
}

// WithMaxToolIterations sets the optional upper bound on how many tool-call
// iterations are allowed per invocation for this agent. When limit is:
//   - > 0: the limit is enforced per invocation.
//   - <= 0: no limit is applied (default behavior).
func WithMaxToolIterations(limit int) Option {
	return func(opts *Options) {
		opts.MaxToolIterations = limit
	}
}

// WithChannelBufferSize sets the buffer size for event channels.
func WithChannelBufferSize(size int) Option {
	return func(opts *Options) {
		if size < 0 {
			size = defaultChannelBufferSize
		}
		opts.ChannelBufferSize = size
	}
}

// WithCodeExecutor sets the code executor to use for executing code blocks.
func WithCodeExecutor(ce codeexecutor.CodeExecutor) Option {
	return func(opts *Options) {
		opts.codeExecutor = ce
	}
}

// WithEnableCodeExecutionResponseProcessor controls whether the agent
// auto-executes fenced code blocks found in model responses.
func WithEnableCodeExecutionResponseProcessor(enable bool) Option {
	return func(opts *Options) {
		opts.EnableCodeExecutionResponseProcessor = enable
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

// WithRefreshToolSetsOnRun controls whether tools from ToolSets are
// refreshed from the underlying ToolSet on each run.
// When enabled, the agent will call ToolSet.Tools again when building
// the tools list for each invocation instead of using a fixed snapshot.
// This is useful when ToolSets provide a dynamic tool list (for example,
// MCP ToolSets that support ListTools at runtime).
func WithRefreshToolSetsOnRun(refresh bool) Option {
	return func(opts *Options) {
		opts.RefreshToolSetsOnRun = refresh
	}
}

// WithSkills enables model-agnostic Agent Skills support using the
// provided repository. The processor will inject a small overview
// and on-demand content according to session state.
func WithSkills(repo skill.Repository) Option {
	return func(opts *Options) {
		opts.skillsRepository = repo
	}
}

// WithSkillLoadMode sets how long skill bodies/docs loaded via skill_load
// remain available in the system prompt.
//
// Supported modes:
//   - SkillLoadModeTurn (default)
//   - SkillLoadModeOnce
//   - SkillLoadModeSession (legacy)
func WithSkillLoadMode(mode string) Option {
	return func(opts *Options) {
		opts.SkillLoadMode = mode
	}
}

// WithSkillsLoadedContentInToolResults enables an alternative injection
// mode where loaded skill bodies/docs are materialized into tool result
// messages (skill_load / skill_select_docs) instead of being appended
// to the system prompt.
func WithSkillsLoadedContentInToolResults(enable bool) Option {
	return func(opts *Options) {
		opts.SkillsLoadedContentInToolResults = enable
	}
}

// WithSkipSkillsFallbackOnSessionSummary controls whether the agent
// skips the "Loaded skill context" system-message fallback when a session
// summary is present in the request.
//
// Default: true.
func WithSkipSkillsFallbackOnSessionSummary(
	skip bool,
) Option {
	return func(opts *Options) {
		opts.SkipSkillsFallbackOnSessionSummary = skip
	}
}

// WithSkillsToolingGuidance overrides the tooling/workspace guidance
// block appended to the skills overview in the system message.
//
// Behavior:
//   - Not configured: use the built-in default guidance.
//   - Configured with empty string: omit the guidance block.
//   - Configured with non-empty string: append the provided text.
func WithSkillsToolingGuidance(
	guidance string,
) Option {
	return func(opts *Options) {
		text := guidance
		opts.skillsToolingGuidance = &text
	}
}

// WithSkillRunAllowedCommands restricts skill_run to a single,
// allowlisted command (no shell syntax) when non-empty.
func WithSkillRunAllowedCommands(cmds ...string) Option {
	return func(opts *Options) {
		opts.skillRunAllowedCommands = append(
			[]string(nil), cmds...,
		)
	}
}

// WithSkillRunDeniedCommands rejects a single, denylisted command (no shell
// syntax) when non-empty.
func WithSkillRunDeniedCommands(cmds ...string) Option {
	return func(opts *Options) {
		opts.skillRunDeniedCommands = append(
			[]string(nil), cmds...,
		)
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

// WithStructuredOutputJSONSchema sets a JSON schema structured output for normal runs.
//
// Unlike WithOutputSchema, this uses the model-native response_format json_schema mechanism
// (when supported by the provider) and can be used together with tools/toolsets.
//
// name should be a short identifier for the schema. Some providers (e.g. OpenAI) require it.
func WithStructuredOutputJSONSchema(name string, schema map[string]any, strict bool, description string) Option {
	return func(opts *Options) {
		if schema == nil {
			return
		}
		if name == "" {
			name = "output"
		}
		opts.StructuredOutput = &model.StructuredOutput{
			Type: model.StructuredOutputJSONSchema,
			JSONSchema: &model.JSONSchemaConfig{
				Name:        name,
				Schema:      schema,
				Strict:      strict,
				Description: description,
			},
		}
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
		if name == "" {
			name = "output"
		}
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
// Default is false.
func WithPreserveSameBranch(preserve bool) Option {
	return func(opts *Options) {
		opts.PreserveSameBranch = preserve
	}
}

// WithKnowledgeFilter sets the metadata filter for the knowledge base.
func WithKnowledgeFilter(filter map[string]any) Option {
	return func(opts *Options) {
		opts.KnowledgeFilter = filter
	}
}

// WithKnowledgeConditionedFilter sets the complex condition filter for the knowledge base.
func WithKnowledgeConditionedFilter(filter *searchfilter.UniversalFilterCondition) Option {
	return func(opts *Options) {
		opts.KnowledgeConditionedFilter = filter
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

// WithReasoningContentMode controls how reasoning_content is handled in
// multi-turn conversations. This is particularly important for DeepSeek models
// where reasoning_content should be discarded from previous request turns.
//
// Available modes:
//   - ReasoningContentModeDiscardPreviousTurns: Discard reasoning_content from
//     previous requests, keep for current request (default, recommended).
//   - ReasoningContentModeKeepAll: Keep all reasoning_content (for debugging).
//   - ReasoningContentModeDiscardAll: Discard all reasoning_content from history.
func WithReasoningContentMode(mode string) Option {
	return func(opts *Options) {
		opts.ReasoningContentMode = mode
	}
}

// WithSummaryFormatter sets a custom formatter for session summary content.
// This allows users to customize how summaries are presented to the model.
// Example:
//
//	llmagent.WithSummaryFormatter(func(summary string) string {
//	    return fmt.Sprintf("## Previous Context\n\n%s", summary)
//	})
func WithSummaryFormatter(formatter func(summary string) string) Option {
	return func(opts *Options) {
		opts.summaryFormatter = formatter
	}
}

// WithToolFilter sets the tool filter function.
func WithToolFilter(filter tool.FilterFunc) Option {
	return func(opts *Options) {
		opts.toolFilter = filter
	}
}

// WithMessageFilterMode sets the message filter mode.
func WithMessageFilterMode(mode MessageFilterMode) Option {
	return func(opts *Options) {
		switch mode {
		case FullContext:
			opts.messageBranchFilterMode = BranchFilterModePrefix
			opts.messageTimelineFilterMode = TimelineFilterAll
		case RequestContext:
			opts.messageBranchFilterMode = BranchFilterModePrefix
			opts.messageTimelineFilterMode = TimelineFilterCurrentRequest
		case IsolatedRequest:
			opts.messageBranchFilterMode = BranchFilterModeExact
			opts.messageTimelineFilterMode = TimelineFilterCurrentRequest
		case IsolatedInvocation:
			opts.messageBranchFilterMode = BranchFilterModeExact
			opts.messageTimelineFilterMode = TimelineFilterCurrentInvocation
		default:
			panic("invalid option value")
		}
	}
}

// WithPreloadMemory sets the number of memories to preload into system prompt.
//   - Set to 0 (default) to disable preloading (use tools instead).
//   - Set to N (N > 0) to load the most recent N memories.
//   - Set to -1 to load all memories.
//     WARNING: Loading all memories may significantly increase token usage
//     and API costs, especially for users with many stored memories.
//     Consider using a positive limit (e.g., 10-50) for production use.
func WithPreloadMemory(limit int) Option {
	return func(opts *Options) {
		opts.PreloadMemory = limit
	}
}

// WithPostToolPrompt overrides the default dynamic prompt injected when tool
// results are detected in the conversation. The default prompt guides the
// model to synthesize results naturally without meta-commentary.
//
// Example usage:
//
//	llmagent.WithPostToolPrompt("[Dynamic Prompt] Summarize the tool output concisely.")
func WithPostToolPrompt(prompt string) Option {
	return func(opts *Options) {
		opts.PostToolPrompt = prompt
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	if src == nil {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
