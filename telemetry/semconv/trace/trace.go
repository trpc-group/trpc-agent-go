//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package trace defines trace name constants following OpenTelemetry semantic conventions.
package trace

// Telemetry attributes constants for tracing spans.
// Reference: https://github.com/open-telemetry/semantic-conventions/blob/main/docs/gen-ai/gen-ai-agent-spans.md#spans
const (
	// ResourceServiceNamespace defines the service namespace for trpc-go-agent.
	ResourceServiceNamespace = "trpc-go-agent"
	// ResourceServiceName defines the service name for telemetry.
	ResourceServiceName = "telemetry"
	// ResourceServiceVersion defines the version of the telemetry service.
	ResourceServiceVersion = "v0.1.0"

	// KeyEventID is the attribute key for event ID.
	KeyEventID = "trpc.go.agent.event_id"
	// KeyInvocationID is the attribute key for invocation ID.
	KeyInvocationID = "trpc.go.agent.invocation_id"
	// KeyLLMRequest is the attribute key for LLM request payload.
	KeyLLMRequest = "trpc.go.agent.llm_request"
	// KeyLLMResponse is the attribute key for LLM response payload.
	KeyLLMResponse = "trpc.go.agent.llm_response"

	// KeyRunnerName is the attribute key for runner name.
	KeyRunnerName = "trpc.go.agent.runner.name"
	// KeyRunnerUserID is the attribute key for runner user ID.
	KeyRunnerUserID = "trpc.go.agent.runner.user_id"
	// KeyRunnerSessionID is the attribute key for runner session ID.
	KeyRunnerSessionID = "trpc.go.agent.runner.session_id"
	// KeyRunnerInput is the attribute key for runner input.
	KeyRunnerInput = "trpc.go.agent.runner.input"
	// KeyRunnerOutput is the attribute key for runner output.
	KeyRunnerOutput = "trpc.go.agent.runner.output"

	// KeyTRPCAgentGoAppName is the attribute key for application name.
	KeyTRPCAgentGoAppName = "trpc_go_agent.app.name"
	// KeyTRPCAgentGoUserID is the attribute key for user ID.
	KeyTRPCAgentGoUserID = "trpc_go_agent.user.id"
	// KeyTRPCAgentGoClientTimeToFirstToken is the attribute key for time to first token metric.
	KeyTRPCAgentGoClientTimeToFirstToken = "trpc_agent_go.client.time_to_first_token" // #nosec G101 - this is a metric key name, not a credential.

	// KeyGenAIOperationName is the attribute key for GenAI operation name.
	KeyGenAIOperationName = "gen_ai.operation.name"
	// KeyGenAISystem is the attribute key for GenAI system identifier.
	KeyGenAISystem = "gen_ai.system"

	// KeyGenAIRequestModel is the attribute key for the model used in the request.
	KeyGenAIRequestModel = "gen_ai.request.model"
	// KeyGenAIRequestIsStream is the attribute key for whether the request is streaming.
	KeyGenAIRequestIsStream = "gen_ai.request.is_stream"
	// KeyGenAIRequestChoiceCount is the attribute key for the number of choices in the request.
	KeyGenAIRequestChoiceCount = "gen_ai.request.choice.count"
	// KeyGenAIInputMessages is the attribute key for input messages.
	KeyGenAIInputMessages = "gen_ai.input.messages"
	// KeyGenAIOutputMessages is the attribute key for output messages.
	KeyGenAIOutputMessages = "gen_ai.output.messages"
	// KeyGenAIAgentName is the attribute key for agent name.
	KeyGenAIAgentName = "gen_ai.agent.name"
	// KeyGenAIConversationID is the attribute key for conversation ID.
	KeyGenAIConversationID = "gen_ai.conversation.id"
	// KeyGenAIUsageOutputTokens is the attribute key for output token count.
	KeyGenAIUsageOutputTokens = "gen_ai.usage.output_tokens" // #nosec G101 - this is a metric key name, not a credential.
	// KeyGenAIUsageInputTokens is the attribute key for input token count.
	KeyGenAIUsageInputTokens = "gen_ai.usage.input_tokens" // #nosec G101 - this is a metric key name, not a credential.
	// KeyGenAIProviderName is the attribute key for provider name.
	KeyGenAIProviderName = "gen_ai.provider.name"
	// KeyGenAIAgentDescription is the attribute key for agent description.
	KeyGenAIAgentDescription = "gen_ai.agent.description"
	// KeyGenAIResponseFinishReasons is the attribute key for response finish reasons.
	KeyGenAIResponseFinishReasons = "gen_ai.response.finish_reasons"
	// KeyGenAIResponseID is the attribute key for response ID.
	KeyGenAIResponseID = "gen_ai.response.id"
	// KeyGenAIResponseModel is the attribute key for the model used in the response.
	KeyGenAIResponseModel = "gen_ai.response.model"
	// KeyGenAIRequestStopSequences is the attribute key for stop sequences in the request.
	KeyGenAIRequestStopSequences = "gen_ai.request.stop_sequences"
	// KeyGenAIRequestFrequencyPenalty is the attribute key for frequency penalty parameter.
	KeyGenAIRequestFrequencyPenalty = "gen_ai.request.frequency_penalty"
	// KeyGenAIRequestMaxTokens is the attribute key for maximum tokens parameter.
	KeyGenAIRequestMaxTokens = "gen_ai.request.max_tokens" // #nosec G101 - this is a metric key name, not a credential.
	// KeyGenAIRequestPresencePenalty is the attribute key for presence penalty parameter.
	KeyGenAIRequestPresencePenalty = "gen_ai.request.presence_penalty"
	// KeyGenAIRequestTemperature is the attribute key for temperature parameter.
	KeyGenAIRequestTemperature = "gen_ai.request.temperature"
	// KeyGenAIRequestTopP is the attribute key for top-p sampling parameter.
	KeyGenAIRequestTopP = "gen_ai.request.top_p"

	// KeyGenAIRequestThinkingEnabled is the attribute key for thinking enabled parameter.
	// Note: This is a custom attribute not defined in OpenTelemetry semantic conventions for GenAI.
	KeyGenAIRequestThinkingEnabled = "gen_ai.request.thinking_enabled"

	// KeyGenAISystemInstructions is the attribute key for system instructions.
	KeyGenAISystemInstructions = "gen_ai.system_instructions"
	// KeyGenAITokenType is the attribute key for token type.
	KeyGenAITokenType = "gen_ai.token.type" // #nosec G101 - this is a metric key name, not a credential.

	// KeyGenAIToolName is the attribute key for tool name.
	KeyGenAIToolName = "gen_ai.tool.name"
	// KeyGenAIToolDescription is the attribute key for tool description.
	KeyGenAIToolDescription = "gen_ai.tool.description"
	// KeyGenAIToolCallID is the attribute key for tool call ID.
	KeyGenAIToolCallID = "gen_ai.tool.call.id"
	// KeyGenAIToolCallArguments is the attribute key for tool call arguments.
	KeyGenAIToolCallArguments = "gen_ai.tool.call.arguments"
	// KeyGenAIToolCallResult is the attribute key for tool call result.
	KeyGenAIToolCallResult = "gen_ai.tool.call.result"

	// KeyGenAIRequestEncodingFormats is the attribute key for request encoding formats.
	KeyGenAIRequestEncodingFormats = "gen_ai.request.encoding_formats"

	// KeyErrorType is the attribute key for error type.
	// Reference: https://github.com/open-telemetry/semantic-conventions/blob/main/docs/general/recording-errors.md#recording-errors-on-spans
	KeyErrorType = "error.type"
	// KeyErrorMessage is the attribute key for error message.
	// Reference: https://github.com/open-telemetry/semantic-conventions/blob/main/docs/general/recording-errors.md#recording-errors-on-spans
	KeyErrorMessage = "error.message"
	// ValueDefaultErrorType is the default error type value for unknown errors.
	ValueDefaultErrorType = "_OTHER"

	// SystemTRPCGoAgent is the system identifier for trpc-go-agent.
	SystemTRPCGoAgent = "trpc.go.agent"
)
