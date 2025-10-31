package trace

// https://github.com/open-telemetry/semantic-conventions/blob/main/docs/gen-ai/gen-ai-agent-spans.md#spans
// telemetry attributes constants.
var (
	ResourceServiceNamespace = "trpc-go-agent"
	ResourceServiceName      = "telemetry"
	ResourceServiceVersion   = "v0.1.0"

	KeyEventID      = "trpc.go.agent.event_id"
	KeyInvocationID = "trpc.go.agent.invocation_id"
	KeyLLMRequest   = "trpc.go.agent.llm_request"
	KeyLLMResponse  = "trpc.go.agent.llm_response"

	// Runner-related attributes
	KeyRunnerName      = "trpc.go.agent.runner.name"
	KeyRunnerUserID    = "trpc.go.agent.runner.user_id"
	KeyRunnerSessionID = "trpc.go.agent.runner.session_id"
	KeyRunnerInput     = "trpc.go.agent.runner.input"
	KeyRunnerOutput    = "trpc.go.agent.runner.output"

	KeyTRPCAgentGoAppName                = "trpc_go_agent.app.name"
	KeyTRPCAgentGoUserID                 = "trpc_go_agent.user.id"
	KeyTRPCAgentGoClientTimeToFirstToken = "trpc_agent_go.client.time_to_first_token" // #nosec G101 - this is a metric key name, not a credential.

	// GenAI operation attributes
	KeyGenAIOperationName = "gen_ai.operation.name"
	KeyGenAISystem        = "gen_ai.system"

	KeyGenAIRequestModel            = "gen_ai.request.model"
	KeyGenAIRequestChoiceCount      = "gen_ai.request.choice.count"
	KeyGenAIInputMessages           = "gen_ai.input.messages"
	KeyGenAIOutputMessages          = "gen_ai.output.messages"
	KeyGenAIAgentName               = "gen_ai.agent.name"
	KeyGenAIConversationID          = "gen_ai.conversation.id"
	KeyGenAIUsageOutputTokens       = "gen_ai.usage.output_tokens" // #nosec G101 - this is a metric key name, not a credential.
	KeyGenAIUsageInputTokens        = "gen_ai.usage.input_tokens"  // #nosec G101 - this is a metric key name, not a credential.
	KeyGenAIProviderName            = "gen_ai.provider.name"
	KeyGenAIAgentDescription        = "gen_ai.agent.description"
	KeyGenAIResponseFinishReasons   = "gen_ai.response.finish_reasons"
	KeyGenAIResponseID              = "gen_ai.response.id"
	KeyGenAIResponseModel           = "gen_ai.response.model"
	KeyGenAIRequestStopSequences    = "gen_ai.request.stop_sequences"
	KeyGenAIRequestFrequencyPenalty = "gen_ai.request.frequency_penalty"
	KeyGenAIRequestMaxTokens        = "gen_ai.request.max_tokens" // #nosec G101 - this is a metric key name, not a credential.
	KeyGenAIRequestPresencePenalty  = "gen_ai.request.presence_penalty"
	KeyGenAIRequestTemperature      = "gen_ai.request.temperature"
	KeyGenAIRequestTopP             = "gen_ai.request.top_p"
	KeyGenAISystemInstructions      = "gen_ai.system_instructions"
	KeyGenAITokenType               = "gen_ai.token.type" // #nosec G101 - this is a metric key name, not a credential.

	KeyGenAIToolName          = "gen_ai.tool.name"
	KeyGenAIToolDescription   = "gen_ai.tool.description"
	KeyGenAIToolCallID        = "gen_ai.tool.call.id"
	KeyGenAIToolCallArguments = "gen_ai.tool.call.arguments"
	KeyGenAIToolCallResult    = "gen_ai.tool.call.result"

	KeyGenAIRequestEncodingFormats = "gen_ai.request.encoding_formats"

	// https://github.com/open-telemetry/semantic-conventions/blob/main/docs/general/recording-errors.md#recording-errors-on-spans
	KeyErrorType          = "error.type"
	KeyErrorMessage       = "error.message"
	ValueDefaultErrorType = "_OTHER"

	// System value
	SystemTRPCGoAgent = "trpc.go.agent"
)
