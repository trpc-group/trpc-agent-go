//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package telemetry provides telemetry and observability functionality for the trpc-agent-go framework.
// It includes tracing, metrics, and monitoring capabilities for agent operations.
package telemetry

import (
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// telemetry service constants.
const (
	ServiceName      = "telemetry"
	ServiceVersion   = "v0.1.0"
	ServiceNamespace = "trpc-go-agent"
	InstrumentName   = "trpc.agent.go"

	SpanNamePrefixExecuteTool = "execute_tool"

	OperationExecuteTool     = "execute_tool"
	OperationChat            = "chat"
	OperationGenerateContent = "generate_content"
	OperationInvokeAgent     = "invoke_agent"
	OperationCreateAgent     = "create_agent"
	OperationEmbeddings      = "embeddings"
)

// NewChatSpanName creates a new chat span name.
func NewChatSpanName(requestModel string) string {
	return newInferenceSpanName(OperationChat, requestModel)
}

// NewExecuteToolSpanName creates a new execute tool span name.
func NewExecuteToolSpanName(toolName string) string {
	return fmt.Sprintf("%s %s", OperationExecuteTool, toolName)
}

// newInferenceSpanName creates a new inference span name.
// inference operation name: "chat" for openai, "generate_content" for gemini.
// For example, "chat gpt-4.0".
func newInferenceSpanName(operationNames, requestModel string) string {
	if requestModel == "" {
		return operationNames
	}
	return fmt.Sprintf("%s %s", operationNames, requestModel)
}

const (
	// ProtocolGRPC uses gRPC protocol for OTLP exporter.
	ProtocolGRPC string = "grpc"
	// ProtocolHTTP uses HTTP protocol for OTLP exporter.
	ProtocolHTTP string = "http"
)

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

	KeyGenAIToolName          = "gen_ai.tool.name"
	KeyGenAIToolDescription   = "gen_ai.tool.description"
	KeyGenAIToolCallID        = "gen_ai.tool.call.id"
	KeyGenAIToolCallArguments = "gen_ai.tool.call.arguments"
	KeyGenAIToolCallResult    = "gen_ai.tool.call.result"

	KeyGenAIRequestEncodingFormats = "gen_ai.request.encoding_formats"

	// https://github.com/open-telemetry/semantic-conventions/blob/main/docs/general/recording-errors.md#recording-errors-on-spans
	KeyErrorType          = "error.type"
	ValueDefaultErrorType = "_OTHER"

	// System value
	SystemTRPCGoAgent = "trpc.go.agent"
)

// TraceToolCall traces the invocation of a tool call.
func TraceToolCall(span trace.Span, declaration *tool.Declaration, args []byte, rspEvent *event.Event) {
	span.SetAttributes(
		attribute.String(KeyGenAISystem, SystemTRPCGoAgent),
		attribute.String(KeyGenAIOperationName, OperationExecuteTool),
		attribute.String(KeyGenAIToolName, declaration.Name),
		attribute.String(KeyGenAIToolDescription, declaration.Description),
	)
	if rspEvent != nil {
		span.SetAttributes(attribute.String(KeyEventID, rspEvent.ID))
	}

	// args is json-encoded.
	span.SetAttributes(attribute.String(KeyGenAIToolCallArguments, string(args)))
	if rspEvent != nil && rspEvent.Response != nil {

		if e := rspEvent.Response.Error; e != nil {
			span.SetStatus(codes.Error, e.Message)
			span.SetAttributes(attribute.String(KeyErrorType, e.Type))
		}

		if callIDs := rspEvent.Response.GetToolCallIDs(); len(callIDs) > 0 {
			span.SetAttributes(attribute.String(KeyGenAIToolCallID, callIDs[0]))
		}
		if bts, err := json.Marshal(rspEvent.Response); err == nil {
			span.SetAttributes(attribute.String(KeyGenAIToolCallResult, string(bts)))
		} else {
			span.SetAttributes(attribute.String(KeyGenAIToolCallResult, "<not json serializable>"))
		}
	}

	// Setting empty llm request and response (as UI expect these) while not
	// applicable for tool_response.
	span.SetAttributes(
		attribute.String(KeyLLMRequest, "{}"),
		attribute.String(KeyLLMResponse, "{}"),
	)
}

// ToolNameMergedTools is the name of the merged tools.
const ToolNameMergedTools = "(merged tools)"

// TraceMergedToolCalls traces the invocation of a merged tool call.
// Calling this function is not needed for telemetry purposes. This is provided
// for preventing /debug/trace requests (typically sent by web UI).
func TraceMergedToolCalls(span trace.Span, rspEvent *event.Event) {
	span.SetAttributes(
		attribute.String(KeyGenAISystem, SystemTRPCGoAgent),
		attribute.String(KeyGenAIOperationName, OperationExecuteTool),
		attribute.String(KeyGenAIToolName, ToolNameMergedTools),
		attribute.String(KeyGenAIToolDescription, "(merged tools)"),
		attribute.String(KeyGenAIToolCallArguments, "N/A"),
	)
	if rspEvent != nil && rspEvent.Response != nil {
		if callIDs := rspEvent.Response.GetToolCallIDs(); len(callIDs) > 0 {
			span.SetAttributes(attribute.String(KeyGenAIToolCallID, callIDs[0]))
		}
		if e := rspEvent.Response.Error; e != nil {
			span.SetStatus(codes.Error, e.Message)
			span.SetAttributes(attribute.String(KeyErrorType, e.Type))
		}
		span.SetAttributes(attribute.String(KeyEventID, rspEvent.ID))

		if bts, err := json.Marshal(rspEvent.Response); err == nil {
			span.SetAttributes(attribute.String(KeyGenAIToolCallResult, string(bts)))
		} else {
			span.SetAttributes(attribute.String(KeyGenAIToolCallResult, "<not json serializable>"))
		}
	}

	// Setting empty llm request and response (as UI expect these) while not
	// applicable for tool_response.
	span.SetAttributes(
		attribute.String(KeyLLMRequest, "{}"),
		attribute.String(KeyLLMResponse, "{}"),
	)
}

// TraceBeforeInvokeAgent traces the before invocation of an agent.
func TraceBeforeInvokeAgent(span trace.Span, invoke *agent.Invocation, agentDescription, instructions string, genConfig *model.GenerationConfig) {
	if bts, err := json.Marshal(&model.Request{Messages: []model.Message{invoke.Message}}); err == nil {
		span.SetAttributes(
			attribute.String(KeyGenAIInputMessages, string(bts)),
		)
	} else {
		span.SetAttributes(attribute.String(KeyGenAIInputMessages, "<not json serializable>"))
	}
	span.SetAttributes(
		attribute.String(KeyGenAISystem, SystemTRPCGoAgent),
		attribute.String(KeyGenAIOperationName, OperationInvokeAgent),
		attribute.String(KeyGenAIAgentName, invoke.AgentName),
		attribute.String(KeyInvocationID, invoke.InvocationID),
		attribute.String(KeyGenAIAgentDescription, agentDescription),
		attribute.String(KeyGenAISystemInstructions, instructions),
	)
	if genConfig != nil {
		span.SetAttributes(attribute.StringSlice(KeyGenAIRequestStopSequences, genConfig.Stop))
		if fp := genConfig.FrequencyPenalty; fp != nil {
			span.SetAttributes(attribute.Float64(KeyGenAIRequestFrequencyPenalty, *fp))
		}
		if mt := genConfig.MaxTokens; mt != nil {
			span.SetAttributes(attribute.Int(KeyGenAIRequestMaxTokens, *mt))
		}
		if pp := genConfig.PresencePenalty; pp != nil {
			span.SetAttributes(attribute.Float64(KeyGenAIRequestPresencePenalty, *pp))
		}
		if tp := genConfig.Temperature; tp != nil {
			span.SetAttributes(attribute.Float64(KeyGenAIRequestTemperature, *tp))
		}
		if tp := genConfig.TopP; tp != nil {
			span.SetAttributes(attribute.Float64(KeyGenAIRequestTopP, *tp))
		}
	}

	if invoke.Session != nil {
		span.SetAttributes(
			attribute.String(KeyRunnerUserID, invoke.Session.UserID),
			attribute.String(KeyGenAIConversationID, invoke.Session.ID),
		)
	}
}

// TraceAfterInvokeAgent traces the after invocation of an agent.
func TraceAfterInvokeAgent(span trace.Span, rspEvent *event.Event) {
	if rspEvent == nil {
		return
	}
	rsp := rspEvent.Response
	if rsp == nil {
		return
	}
	if len(rsp.Choices) > 0 {
		if bts, err := json.Marshal(rsp.Choices); err == nil {
			span.SetAttributes(
				attribute.String(KeyGenAIOutputMessages, string(bts)),
			)
		}
		var finishReasons []string
		for _, choice := range rsp.Choices {
			if choice.FinishReason != nil {
				finishReasons = append(finishReasons, *choice.FinishReason)
			} else {
				finishReasons = append(finishReasons, "")
			}
		}
		span.SetAttributes(attribute.StringSlice(KeyGenAIResponseFinishReasons, finishReasons))

	}
	span.SetAttributes(attribute.String(KeyGenAIResponseModel, rsp.Model))
	if rsp.Usage != nil {
		span.SetAttributes(attribute.Int(KeyGenAIUsageInputTokens, rsp.Usage.PromptTokens))
		span.SetAttributes(attribute.Int(KeyGenAIUsageOutputTokens, rsp.Usage.CompletionTokens))
	}
	span.SetAttributes(attribute.String(KeyGenAIResponseID, rsp.ID))

	if e := rsp.Error; e != nil {
		span.SetStatus(codes.Error, e.Message)
		span.SetAttributes(attribute.String(KeyErrorType, e.Type))
	}
}

// TraceChat traces the invocation of an LLM call.
func TraceChat(span trace.Span, invoke *agent.Invocation, req *model.Request, rsp *model.Response, eventID string) {
	attrs := []attribute.KeyValue{
		attribute.String(KeyGenAISystem, SystemTRPCGoAgent),
		attribute.String(KeyGenAIOperationName, OperationChat),
		attribute.String(KeyInvocationID, invoke.InvocationID),
		attribute.String(KeyEventID, eventID),
	}

	// Add session ID if session exists
	if invoke.Session != nil {
		attrs = append(attrs, attribute.String(KeyGenAIConversationID, invoke.Session.ID))
	} else {
		attrs = append(attrs, attribute.String(KeyGenAIConversationID, ""))
	}

	// Add model name if model exists
	if invoke.Model != nil {
		attrs = append(attrs, attribute.String(KeyGenAIRequestModel, invoke.Model.Info().Name))
	} else {
		attrs = append(attrs, attribute.String(KeyGenAIRequestModel, ""))
	}

	span.SetAttributes(attrs...)

	if req != nil {
		genConfig := req.GenerationConfig
		span.SetAttributes(
			attribute.StringSlice(KeyGenAIRequestStopSequences, genConfig.Stop),
		)
		if fp := genConfig.FrequencyPenalty; fp != nil {
			span.SetAttributes(attribute.Float64(KeyGenAIRequestFrequencyPenalty, *fp))
		}
		if mt := genConfig.MaxTokens; mt != nil {
			span.SetAttributes(attribute.Int(KeyGenAIRequestMaxTokens, *mt))
		}
		if pp := genConfig.PresencePenalty; pp != nil {
			span.SetAttributes(attribute.Float64(KeyGenAIRequestPresencePenalty, *pp))
		}
		if tp := genConfig.Temperature; tp != nil {
			span.SetAttributes(attribute.Float64(KeyGenAIRequestTemperature, *tp))
		}
		if tp := genConfig.TopP; tp != nil {
			span.SetAttributes(attribute.Float64(KeyGenAIRequestTopP, *tp))
		}
		if bts, err := json.Marshal(req); err == nil {
			span.SetAttributes(
				attribute.String(KeyLLMRequest, string(bts)),
			)
		} else {
			span.SetAttributes(attribute.String(KeyLLMRequest, "<not json serializable>"))
		}
		span.SetAttributes(attribute.Int(KeyGenAIRequestChoiceCount, 1))
		if bts, err := json.Marshal(req.Messages); err == nil {
			span.SetAttributes(
				attribute.String(KeyGenAIInputMessages, string(bts)),
			)
		} else {
			span.SetAttributes(attribute.String(KeyGenAIInputMessages, "<not json serializable>"))
		}
		span.SetAttributes(attribute.String(KeyGenAIResponseModel, rsp.Model))
		if rsp.Usage != nil {
			span.SetAttributes(attribute.Int(KeyGenAIUsageInputTokens, rsp.Usage.PromptTokens))
			span.SetAttributes(attribute.Int(KeyGenAIUsageOutputTokens, rsp.Usage.CompletionTokens))
		}
		span.SetAttributes(attribute.String(KeyGenAIResponseID, rsp.ID))
	}

	if rsp != nil {
		if e := rsp.Error; e != nil {
			span.SetStatus(codes.Error, e.Message)
			span.SetAttributes(attribute.String(KeyErrorType, e.Type))
		}
		if len(rsp.Choices) > 0 {
			if bts, err := json.Marshal(rsp.Choices); err == nil {
				span.SetAttributes(
					attribute.String(KeyGenAIOutputMessages, string(bts)),
				)
			}
			var finishReasons []string
			for _, choice := range rsp.Choices {
				if choice.FinishReason != nil {
					finishReasons = append(finishReasons, *choice.FinishReason)
				} else {
					finishReasons = append(finishReasons, "")
				}
			}
			span.SetAttributes(attribute.StringSlice(KeyGenAIResponseFinishReasons, finishReasons))
		}
		if bts, err := json.Marshal(rsp); err == nil {
			span.SetAttributes(
				attribute.String(KeyLLMResponse, string(bts)),
			)
		} else {
			span.SetAttributes(attribute.String(KeyLLMResponse, "<not json serializable>"))
		}
	}
}

// TraceEmbedding traces the invocation of an embedding call.
func TraceEmbedding(span trace.Span, requestEncodingFormat, requestModel string, inputToken *int64, err error) {
	span.SetAttributes(
		attribute.String(KeyGenAIOperationName, OperationEmbeddings),
		attribute.String(KeyGenAIRequestModel, requestModel),
		attribute.StringSlice(KeyGenAIRequestEncodingFormats, []string{requestEncodingFormat}),
	)
	if err != nil {
		span.SetAttributes(attribute.String(KeyErrorType, ValueDefaultErrorType))
		span.SetStatus(codes.Error, err.Error())
	}
	if inputToken != nil {
		span.SetAttributes(attribute.Int64(KeyGenAIUsageInputTokens, *inputToken))
	}
}

// NewGRPCConn creates a new gRPC connection to the OpenTelemetry Collector.
func NewGRPCConn(endpoint string) (*grpc.ClientConn, error) {
	// It connects the OpenTelemetry Collector through gRPC connection.
	// You can customize the endpoint using SetConfig() or environment variables.
	conn, err := grpc.Dial(endpoint,
		// Note the use of insecure transport here. TLS is recommended in production.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	return conn, err
}
