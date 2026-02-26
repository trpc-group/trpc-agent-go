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
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// grpcDial is a package-level variable to allow test injection of a custom dialer.
// In production, this points to grpc.Dial.
var grpcDial = grpc.Dial

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
	OperationWorkflow        = "workflow"
)

// NewChatSpanName creates a new chat span name.
func NewChatSpanName(requestModel string) string {
	return newInferenceSpanName(OperationChat, requestModel)
}

// NewExecuteToolSpanName creates a new execute tool span name.
func NewExecuteToolSpanName(toolName string) string {
	return fmt.Sprintf("%s %s", OperationExecuteTool, toolName)
}

const (
	// KeyGenAIWorkflowName is the name of the workflow.
	KeyGenAIWorkflowName = "gen_ai.workflow.name"
	// KeyGenAIWorkflowID is the id of the workflow.
	KeyGenAIWorkflowID = "gen_ai.workflow.id"
)

var (
	// KeyGenAIWorkflowRequest is the request of the workflow.
	KeyGenAIWorkflowRequest = semconvtrace.KeyGenAIWorkflowRequest
	// KeyGenAIWorkflowResponse is the response of the workflow.
	KeyGenAIWorkflowResponse = semconvtrace.KeyGenAIWorkflowResponse
)

// Workflow is the workflow information.
type Workflow struct {
	Name     string
	ID       string
	Request  any
	Response any
	Error    error
}

// NewWorkflowSpanName creates a new workflow span name.
func NewWorkflowSpanName(workflowName string) string {
	return fmt.Sprintf("%s %s", OperationWorkflow, workflowName)
}

// TraceWorkflow traces the workflow.
func TraceWorkflow(span trace.Span, workflow *Workflow) {
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(attribute.String(KeyGenAIOperationName, OperationWorkflow))
	span.SetAttributes(attribute.String(KeyGenAIWorkflowName, workflow.Name))
	span.SetAttributes(attribute.String(KeyGenAIWorkflowID, workflow.ID))
	if workflow.Request != nil {
		request, err := json.Marshal(workflow.Request)
		if err != nil {
			span.SetAttributes(attribute.String(KeyGenAIWorkflowRequest, fmt.Sprintf("<not json serializable: %v>", err)))
		} else {
			span.SetAttributes(attribute.String(KeyGenAIWorkflowRequest, string(request)))
		}
	}
	if workflow.Response != nil {
		response, err := json.Marshal(workflow.Response)
		if err != nil {
			span.SetAttributes(attribute.String(KeyGenAIWorkflowResponse, fmt.Sprintf("<not json serializable>: %v", err)))
		} else {
			span.SetAttributes(attribute.String(KeyGenAIWorkflowResponse, string(response)))
		}
	}
	if workflow.Error != nil {
		span.SetAttributes(attribute.String(KeyErrorType, ToErrorType(workflow.Error, ValueDefaultErrorType)))
		span.SetStatus(codes.Error, workflow.Error.Error())
		span.RecordError(workflow.Error)
	}
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

// Telemetry attribute keys aliases from semconv package.
var (
	ResourceServiceNamespace = semconvtrace.ResourceServiceNamespace
	ResourceServiceName      = semconvtrace.ResourceServiceName
	ResourceServiceVersion   = semconvtrace.ResourceServiceVersion

	KeyEventID      = semconvtrace.KeyEventID
	KeyInvocationID = semconvtrace.KeyInvocationID
	KeyLLMRequest   = semconvtrace.KeyLLMRequest
	KeyLLMResponse  = semconvtrace.KeyLLMResponse

	KeyRunnerName      = semconvtrace.KeyRunnerName
	KeyRunnerUserID    = semconvtrace.KeyRunnerUserID
	KeyRunnerSessionID = semconvtrace.KeyRunnerSessionID
	KeyRunnerInput     = semconvtrace.KeyRunnerInput
	KeyRunnerOutput    = semconvtrace.KeyRunnerOutput

	KeyTRPCAgentGoAppName                = semconvtrace.KeyTRPCAgentGoAppName
	KeyTRPCAgentGoUserID                 = semconvtrace.KeyTRPCAgentGoUserID
	KeyTRPCAgentGoClientTimeToFirstToken = semconvtrace.KeyTRPCAgentGoClientTimeToFirstToken

	KeyGenAIOperationName = semconvtrace.KeyGenAIOperationName
	KeyGenAISystem        = semconvtrace.KeyGenAISystem

	KeyGenAIRequestModel                  = semconvtrace.KeyGenAIRequestModel
	KeyGenAIRequestIsStream               = semconvtrace.KeyGenAIRequestIsStream
	KeyGenAIRequestChoiceCount            = semconvtrace.KeyGenAIRequestChoiceCount
	KeyGenAIInputMessages                 = semconvtrace.KeyGenAIInputMessages
	KeyGenAIOutputMessages                = semconvtrace.KeyGenAIOutputMessages
	KeyGenAIAgentName                     = semconvtrace.KeyGenAIAgentName
	KeyGenAIConversationID                = semconvtrace.KeyGenAIConversationID
	KeyGenAIUsageOutputTokens             = semconvtrace.KeyGenAIUsageOutputTokens
	KeyGenAIUsageInputTokens              = semconvtrace.KeyGenAIUsageInputTokens
	KeyGenAIUsageInputTokensCached        = semconvtrace.KeyGenAIUsageInputTokensCached
	KeyGenAIUsageInputTokensCacheRead     = semconvtrace.KeyGenAIUsageInputTokensCacheRead
	KeyGenAIUsageInputTokensCacheCreation = semconvtrace.KeyGenAIUsageInputTokensCacheCreation
	KeyGenAIProviderName                  = semconvtrace.KeyGenAIProviderName
	KeyGenAIAgentDescription              = semconvtrace.KeyGenAIAgentDescription
	KeyGenAIResponseFinishReasons         = semconvtrace.KeyGenAIResponseFinishReasons
	KeyGenAIResponseID                    = semconvtrace.KeyGenAIResponseID
	KeyGenAIResponseModel                 = semconvtrace.KeyGenAIResponseModel
	KeyGenAIRequestStopSequences          = semconvtrace.KeyGenAIRequestStopSequences
	KeyGenAIRequestFrequencyPenalty       = semconvtrace.KeyGenAIRequestFrequencyPenalty
	KeyGenAIRequestMaxTokens              = semconvtrace.KeyGenAIRequestMaxTokens
	KeyGenAIRequestPresencePenalty        = semconvtrace.KeyGenAIRequestPresencePenalty
	KeyGenAIRequestTemperature            = semconvtrace.KeyGenAIRequestTemperature
	KeyGenAIRequestTopP                   = semconvtrace.KeyGenAIRequestTopP
	KeyGenAISystemInstructions            = semconvtrace.KeyGenAISystemInstructions
	KeyGenAITokenType                     = semconvtrace.KeyGenAITokenType
	KeyGenAITaskType                      = semconvtrace.KeyGenAITaskType
	KeyGenAIRequestThinkingEnabled        = semconvtrace.KeyGenAIRequestThinkingEnabled
	KeyGenAIRequestToolDefinitions        = semconvtrace.KeyGenAIRequestToolDefinitions

	KeyGenAIToolName          = semconvtrace.KeyGenAIToolName
	KeyGenAIToolDescription   = semconvtrace.KeyGenAIToolDescription
	KeyGenAIToolCallID        = semconvtrace.KeyGenAIToolCallID
	KeyGenAIToolCallArguments = semconvtrace.KeyGenAIToolCallArguments
	KeyGenAIToolCallResult    = semconvtrace.KeyGenAIToolCallResult

	KeyErrorType          = semconvtrace.KeyErrorType
	KeyErrorMessage       = semconvtrace.KeyErrorMessage
	ValueDefaultErrorType = semconvtrace.ValueDefaultErrorType

	SystemTRPCGoAgent = semconvtrace.SystemTRPCGoAgent
)

// TraceToolCall traces the invocation of a tool call.
func TraceToolCall(span trace.Span, sess *session.Session, declaration *tool.Declaration, args []byte, rspEvent *event.Event, err error) {
	span.SetAttributes(
		attribute.String(KeyGenAISystem, SystemTRPCGoAgent),
		attribute.String(KeyGenAIOperationName, OperationExecuteTool),
		attribute.String(KeyGenAIToolName, declaration.Name),
		attribute.String(KeyGenAIToolDescription, declaration.Description),
	)
	if rspEvent != nil {
		span.SetAttributes(attribute.String(KeyEventID, rspEvent.ID))
	}
	if sess != nil {
		span.SetAttributes(
			attribute.String(KeyGenAIConversationID, sess.ID),
			attribute.String(KeyRunnerUserID, sess.UserID),
		)
	}

	// args is json-encoded.
	span.SetAttributes(attribute.String(KeyGenAIToolCallArguments, string(args)))
	if rspEvent != nil && rspEvent.Response != nil {
		if e := rspEvent.Response.Error; e != nil {
			span.SetStatus(codes.Error, e.Message)
			span.SetAttributes(attribute.String(KeyErrorType, e.Type), attribute.String(KeyErrorMessage, e.Message))
		} else if err != nil {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(KeyErrorType, ToErrorType(err, ValueDefaultErrorType)), attribute.String(KeyErrorMessage, err.Error()))
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
// for preventing trace-query requests typically sent by web UIs.
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
			span.SetAttributes(attribute.String(KeyErrorType, e.Type), attribute.String(KeyErrorMessage, e.Message))
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
	if !span.IsRecording() {
		return
	}
	if invoke != nil && len(invoke.RunOptions.SpanAttributes) > 0 {
		span.SetAttributes(invoke.RunOptions.SpanAttributes...)
	}
	if bts, err := json.Marshal([]model.Message{invoke.Message}); err == nil {
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
		if te := genConfig.ThinkingEnabled; te != nil {
			span.SetAttributes(attribute.Bool(KeyGenAIRequestThinkingEnabled, *te))
		}
	}

	if invoke.Session != nil {
		span.SetAttributes(
			attribute.String(KeyRunnerUserID, invoke.Session.UserID),
			attribute.String(KeyGenAIConversationID, invoke.Session.ID),
		)
	}
}

// TokenUsage is token usage information.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// TraceAfterInvokeAgent traces the after invocation of an agent.
func TraceAfterInvokeAgent(span trace.Span, rspEvent *event.Event, tokenUsage *TokenUsage, timeToFirstToken time.Duration) {
	if !span.IsRecording() {
		return
	}
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
	if tokenUsage != nil {
		span.SetAttributes(attribute.Int(KeyGenAIUsageInputTokens, tokenUsage.PromptTokens))
		span.SetAttributes(attribute.Int(KeyGenAIUsageOutputTokens, tokenUsage.CompletionTokens))
	}
	span.SetAttributes(attribute.String(KeyGenAIResponseID, rsp.ID))

	if e := rsp.Error; e != nil {
		span.SetStatus(codes.Error, e.Message)
		span.SetAttributes(attribute.String(KeyErrorType, e.Type), attribute.String(KeyErrorMessage, e.Message))
	}
	if timeToFirstToken > 0 {
		span.SetAttributes(attribute.Float64(KeyTRPCAgentGoClientTimeToFirstToken, timeToFirstToken.Seconds()))
	}
}

// TraceChatAttributes contains TraceChat inputs other than span.
//
// It is used to keep TraceChat signatures stable as parameters evolve.
type TraceChatAttributes struct {
	Invocation       *agent.Invocation
	Request          *model.Request
	Response         *model.Response
	EventID          string
	TimeToFirstToken time.Duration
	TaskType         string
}

// NewSummarizeTaskType creates a task type for summarize.
func NewSummarizeTaskType(name string) string {
	taskType := "summarize"
	if name == "" {
		return taskType
	}
	return taskType + " " + name
}

// TraceChat traces the invocation of an LLM call.
func TraceChat(span trace.Span, attributes *TraceChatAttributes) {
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String(KeyGenAISystem, SystemTRPCGoAgent),
		attribute.String(KeyGenAIOperationName, OperationChat),
	}
	if attributes == nil {
		span.SetAttributes(attrs...)
		return
	}

	if attributes.EventID != "" {
		attrs = append(attrs, attribute.String(KeyEventID, attributes.EventID))
	}
	if attributes.TimeToFirstToken > 0 {
		attrs = append(attrs, attribute.Float64(KeyTRPCAgentGoClientTimeToFirstToken, attributes.TimeToFirstToken.Seconds()))
	}
	if attributes.TaskType != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAITaskType, attributes.TaskType))
	}

	// Add invocation attributes
	attrs = append(attrs, buildInvocationAttributes(attributes.Invocation)...)

	// Add request attributes
	attrs = append(attrs, buildRequestAttributes(attributes.Request)...)

	// Add response attributes
	attrs = append(attrs, buildResponseAttributes(attributes.Response)...)

	// Set all attributes at once
	span.SetAttributes(attrs...)

	// Handle response error status
	if attributes.Response != nil && attributes.Response.Error != nil {
		span.SetStatus(codes.Error, attributes.Response.Error.Message)
	}
}

// buildInvocationAttributes extracts attributes from the invocation.
func buildInvocationAttributes(invoke *agent.Invocation) []attribute.KeyValue {
	if invoke == nil {
		return nil
	}

	attrs := []attribute.KeyValue{
		attribute.String(KeyInvocationID, invoke.InvocationID),
	}

	if invoke.Session != nil {
		attrs = append(attrs,
			attribute.String(KeyGenAIConversationID, invoke.Session.ID),
			attribute.String(KeyRunnerUserID, invoke.Session.UserID),
		)
	}

	if invoke.Model != nil {
		attrs = append(attrs, attribute.String(KeyGenAIRequestModel, invoke.Model.Info().Name))
	}

	return attrs
}

// buildRequestAttributes builds request-related attributes.
func buildRequestAttributes(req *model.Request) []attribute.KeyValue {
	if req == nil {
		return nil
	}

	attrs := []attribute.KeyValue{
		attribute.StringSlice(KeyGenAIRequestStopSequences, req.GenerationConfig.Stop),
		attribute.Int(KeyGenAIRequestChoiceCount, 1),
	}

	// Add generation config attributes
	genConfig := req.GenerationConfig
	// Add stream attribute only when it's true
	if genConfig.Stream {
		attrs = append(attrs, attribute.Bool(KeyGenAIRequestIsStream, true))
	}
	if fp := genConfig.FrequencyPenalty; fp != nil {
		attrs = append(attrs, attribute.Float64(KeyGenAIRequestFrequencyPenalty, *fp))
	}
	if mt := genConfig.MaxTokens; mt != nil {
		attrs = append(attrs, attribute.Int(KeyGenAIRequestMaxTokens, *mt))
	}
	if pp := genConfig.PresencePenalty; pp != nil {
		attrs = append(attrs, attribute.Float64(KeyGenAIRequestPresencePenalty, *pp))
	}
	if tp := genConfig.Temperature; tp != nil {
		attrs = append(attrs, attribute.Float64(KeyGenAIRequestTemperature, *tp))
	}
	if tp := genConfig.TopP; tp != nil {
		attrs = append(attrs, attribute.Float64(KeyGenAIRequestTopP, *tp))
	}
	if te := genConfig.ThinkingEnabled; te != nil {
		attrs = append(attrs, attribute.Bool(KeyGenAIRequestThinkingEnabled, *te))
	}

	// Add request body
	if bts, err := json.Marshal(req); err == nil {
		attrs = append(attrs, attribute.String(KeyLLMRequest, string(bts)))
	} else {
		attrs = append(attrs, attribute.String(KeyLLMRequest, "<not json serializable>"))
	}

	// Add tool definitions as best-effort structured array (JSON string fallback)
	if len(req.Tools) > 0 {
		definitions := make([]*tool.Declaration, 0, len(req.Tools))
		for _, t := range req.Tools {
			if t == nil {
				continue
			}
			if decl := t.Declaration(); decl != nil {
				definitions = append(definitions, decl)
			}
		}

		if len(definitions) > 0 {
			if bts, err := json.Marshal(definitions); err == nil {
				attrs = append(attrs, attribute.String(KeyGenAIRequestToolDefinitions, string(bts)))
			}
		}
	}

	// Add messages
	if bts, err := json.Marshal(req.Messages); err == nil {
		attrs = append(attrs, attribute.String(KeyGenAIInputMessages, string(bts)))
	} else {
		attrs = append(attrs, attribute.String(KeyGenAIInputMessages, "<not json serializable>"))
	}

	return attrs
}

// buildResponseAttributes builds response-related attributes.
func buildResponseAttributes(rsp *model.Response) []attribute.KeyValue {
	if rsp == nil {
		return nil
	}

	attrs := []attribute.KeyValue{
		attribute.String(KeyGenAIResponseModel, rsp.Model),
		attribute.String(KeyGenAIResponseID, rsp.ID),
	}

	// Add error type if present
	if e := rsp.Error; e != nil {
		attrs = append(attrs, attribute.String(KeyErrorType, e.Type), attribute.String(KeyErrorMessage, e.Message))
	}

	// Add usage attributes
	if rsp.Usage != nil {
		attrs = append(attrs,
			attribute.Int(KeyGenAIUsageInputTokens, rsp.Usage.PromptTokens),
			attribute.Int(KeyGenAIUsageOutputTokens, rsp.Usage.CompletionTokens),
		)
		// Prompt cache tokens (if provided by the model provider)
		if cached := rsp.Usage.PromptTokensDetails.CachedTokens; cached != 0 {
			// OpenAI: cached_tokens
			attrs = append(attrs, attribute.Int(KeyGenAIUsageInputTokensCached, cached))
		}
		if cacheRead := rsp.Usage.PromptTokensDetails.CacheReadTokens; cacheRead != 0 {
			// Anthropic: cache_read_tokens
			attrs = append(attrs, attribute.Int(KeyGenAIUsageInputTokensCacheRead, cacheRead))
		}
		if cacheCreation := rsp.Usage.PromptTokensDetails.CacheCreationTokens; cacheCreation != 0 {
			// Anthropic: cache_creation_tokens
			attrs = append(attrs, attribute.Int(KeyGenAIUsageInputTokensCacheCreation, cacheCreation))
		}
	}

	// Add choices attributes
	if len(rsp.Choices) > 0 {
		if bts, err := json.Marshal(rsp.Choices); err == nil {
			attrs = append(attrs, attribute.String(KeyGenAIOutputMessages, string(bts)))
		}

		// Extract finish reasons
		finishReasons := make([]string, 0, len(rsp.Choices))
		for _, choice := range rsp.Choices {
			if choice.FinishReason != nil {
				finishReasons = append(finishReasons, *choice.FinishReason)
			} else {
				finishReasons = append(finishReasons, "")
			}
		}
		attrs = append(attrs, attribute.StringSlice(KeyGenAIResponseFinishReasons, finishReasons))
	}

	// Add response body
	if bts, err := json.Marshal(rsp); err == nil {
		attrs = append(attrs, attribute.String(KeyLLMResponse, string(bts)))
	} else {
		attrs = append(attrs, attribute.String(KeyLLMResponse, "<not json serializable>"))
	}

	return attrs
}

// NewGRPCConn creates a new gRPC connection to the OpenTelemetry Collector.
func NewGRPCConn(endpoint string) (*grpc.ClientConn, error) {
	// It connects the OpenTelemetry Collector through gRPC connection.
	// You can customize the endpoint using SetConfig() or environment variables.
	conn, err := grpcDial(endpoint,
		// Note the use of insecure transport here. TLS is recommended in production.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	return conn, err
}
