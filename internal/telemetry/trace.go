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
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolorder"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// genAISystem holds the gen_ai.system value stamped on agent/tool/chat spans.
// Defaults to semconvtrace.SystemTRPCGoAgent; integrators may override via
// SetGenAISystem (e.g. langfuse.WithGenAISystem).
var genAISystem atomic.Value

func init() {
	genAISystem.Store(semconvtrace.SystemTRPCGoAgent)
}

// GenAISystem returns the gen_ai.system value used when stamping span attributes.
func GenAISystem() string {
	if v, ok := genAISystem.Load().(string); ok && v != "" {
		return v
	}
	return semconvtrace.SystemTRPCGoAgent
}

// SetGenAISystem overrides the gen_ai.system value stamped by Trace* helpers.
// An empty system restores the library default (trpc.go.agent).
func SetGenAISystem(system string) {
	if system == "" {
		genAISystem.Store(semconvtrace.SystemTRPCGoAgent)
		return
	}
	genAISystem.Store(system)
}

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
	return OperationExecuteTool + " " + toolName
}

// WorkflowType is the normalized type vocabulary used by workflow spans.
type WorkflowType string

// Standard workflow type values.
const (
	WorkflowTypeGraph    WorkflowType = "graph"
	WorkflowTypeFunction WorkflowType = "function"
	WorkflowTypeLLM      WorkflowType = "llm"
	WorkflowTypeTool     WorkflowType = "tool"
	WorkflowTypeAgent    WorkflowType = "agent"
	WorkflowTypeJoin     WorkflowType = "join"
	WorkflowTypeRouter   WorkflowType = "router"
)

// String returns the string representation of the workflow type.
func (wt WorkflowType) String() string {
	return string(wt)
}

// Workflow is the workflow information.
type Workflow struct {
	Name     string
	ID       string
	Type     WorkflowType
	Request  any
	Response any
	Error    error
}

// NewWorkflowSpanName creates a new workflow span name.
func NewWorkflowSpanName(workflowName string) string {
	return OperationWorkflow + " " + workflowName
}

type telemetryMessage struct {
	Role             model.Role          `json:"role"`
	Content          string              `json:"content,omitempty"`
	ContentParts     []model.ContentPart `json:"content_parts,omitempty"`
	ToolCallID       string              `json:"tool_call_id,omitempty"`
	Name             string              `json:"name,omitempty"`
	ToolCalls        []model.ToolCall    `json:"tool_calls,omitempty"`
	ReasoningContent string              `json:"reasoning_content,omitempty"`
}

type telemetryChoice struct {
	Index        int              `json:"index"`
	Message      telemetryMessage `json:"message,omitempty"`
	Delta        telemetryMessage `json:"delta,omitempty"`
	FinishReason *string          `json:"finish_reason,omitempty"`
}

func telemetryMessageFromModel(msg model.Message) telemetryMessage {
	return telemetryMessage{
		Role:             msg.Role,
		Content:          msg.Content,
		ContentParts:     msg.ContentParts,
		ToolCallID:       msg.ToolID,
		Name:             msg.ToolName,
		ToolCalls:        msg.ToolCalls,
		ReasoningContent: msg.ReasoningContent,
	}
}

func marshalTelemetryMessages(messages []model.Message) ([]byte, error) {
	out := make([]telemetryMessage, len(messages))
	for i, msg := range messages {
		out[i] = telemetryMessageFromModel(msg)
	}
	return json.Marshal(out)
}

func marshalTelemetryChoices(choices []model.Choice) ([]byte, error) {
	out := make([]telemetryChoice, len(choices))
	for i, choice := range choices {
		out[i] = telemetryChoice{
			Index:        choice.Index,
			Message:      telemetryMessageFromModel(choice.Message),
			Delta:        telemetryMessageFromModel(choice.Delta),
			FinishReason: choice.FinishReason,
		}
	}
	return json.Marshal(out)
}

// ChatTraceState keeps per-chat-span trace state for repeated streaming chunks.
//
// Request attributes are normally committed once per chat span while the
// installed span attribute policy pointer remains unchanged. Invocation and
// response attributes remain chunk-scoped because they are cheap and may be
// completed as streaming processing advances.
//
// ChatTraceState is not goroutine-safe and must not be shared across chat spans.
type ChatTraceState struct {
	requestCommitted bool
	cachedPolicy     *SpanAttributePolicy
}

// traceChunkAttributes contains per-response chunk trace inputs.
type traceChunkAttributes struct {
	Invocation       *agent.Invocation
	Response         *model.Response
	EventID          string
	TimeToFirstToken time.Duration
}

// commitRequest writes base chat and request attributes for a chat span.
//
// Request payload attributes are committed once while the global span attribute
// policy pointer remains unchanged. If the policy pointer changes, request
// attributes are rebuilt and written again under the new policy. Base chat
// attributes may be rewritten on those refreshes. When req is nil, only base
// attributes are written and request commit state is not latched.
func (s *ChatTraceState) commitRequest(span trace.Span, req *model.Request, taskType string) {
	if !span.IsRecording() {
		return
	}

	policy := spanAttributePolicy.Load()
	if s != nil && s.requestCommitted && s.cachedPolicy == policy {
		return
	}

	attrs := baseChatAttributes()
	if taskType != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAITaskType, taskType))
	}
	if req != nil {
		attrs = append(attrs, buildRequestAttributes(req)...)
	}

	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}

	if s != nil && req != nil {
		s.requestCommitted = true
		s.cachedPolicy = policy
	}
}

// traceChunk writes invocation, chunk metadata, and response attributes.
func (s *ChatTraceState) traceChunk(span trace.Span, attributes *traceChunkAttributes) {
	if !span.IsRecording() || attributes == nil {
		return
	}

	attrs := buildInvocationAttributes(attributes.Invocation)
	if attributes.EventID != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyEventID, attributes.EventID))
	}
	if attributes.TimeToFirstToken > 0 {
		attrs = append(attrs, attribute.Float64(semconvtrace.KeyTRPCAgentGoClientTimeToFirstToken, attributes.TimeToFirstToken.Seconds()))
	}
	attrs = append(attrs, buildResponseAttributes(attributes.Response, semconvtrace.ValueDefaultErrorType)...)
	if len(attrs) > 0 {
		span.SetAttributes(attrs...)
	}

	if attributes.Response != nil && attributes.Response.Error != nil {
		span.SetStatus(codes.Error, attributes.Response.Error.Message)
	}
}

// TraceChat is the single entry point for stateful streaming chat traces.
func (s *ChatTraceState) TraceChat(span trace.Span, attributes *TraceChatAttributes) {
	traceChatWithState(span, attributes, s)
}

func baseChatAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(semconvtrace.KeyGenAISystem, GenAISystem()),
		attribute.String(semconvtrace.KeyGenAIOperationName, OperationChat),
	}
}

// TraceWorkflow traces the workflow.
func TraceWorkflow(span trace.Span, workflow *Workflow) {
	if !span.IsRecording() {
		return
	}
	span.SetAttributes(
		attribute.String(semconvtrace.KeyGenAIOperationName, OperationWorkflow),
		attribute.String(semconvtrace.KeyGenAIWorkflowName, workflow.Name),
		attribute.String(semconvtrace.KeyGenAIWorkflowID, workflow.ID),
	)
	if workflow.Type != "" {
		span.SetAttributes(attribute.String(semconvtrace.KeyGenAIWorkflowType, workflow.Type.String()))
	}
	if workflow.Request != nil {
		setStringAttribute(span, OperationWorkflow, semconvtrace.KeyGenAIWorkflowRequest, "<not json serializable>", func() ([]byte, error) {
			return json.Marshal(workflow.Request)
		})
	}
	if workflow.Response != nil {
		setStringAttribute(span, OperationWorkflow, semconvtrace.KeyGenAIWorkflowResponse, "<not json serializable>", func() ([]byte, error) {
			return json.Marshal(workflow.Response)
		})
	}
	if workflow.Error != nil {
		span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, ToErrorType(workflow.Error, semconvtrace.ValueDefaultErrorType)))
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
	return operationNames + " " + requestModel
}

const (
	// ProtocolGRPC uses gRPC protocol for OTLP exporter.
	ProtocolGRPC string = "grpc"
	// ProtocolHTTP uses HTTP protocol for OTLP exporter.
	ProtocolHTTP string = "http"
)

// TraceToolCall traces the invocation of a tool call.
func TraceToolCall(span trace.Span, sess *session.Session, declaration *tool.Declaration, args []byte, rspEvent *event.Event, err error) {
	span.SetAttributes(
		attribute.String(semconvtrace.KeyGenAISystem, GenAISystem()),
		attribute.String(semconvtrace.KeyGenAIOperationName, OperationExecuteTool),
		attribute.String(semconvtrace.KeyGenAIToolName, declaration.Name),
		attribute.String(semconvtrace.KeyGenAIToolDescription, declaration.Description),
	)
	if rspEvent != nil {
		span.SetAttributes(attribute.String(semconvtrace.KeyEventID, rspEvent.ID))
	}
	if sess != nil {
		span.SetAttributes(
			attribute.String(semconvtrace.KeyGenAIConversationID, sess.ID),
			attribute.String(semconvtrace.KeyRunnerUserID, sess.UserID),
		)
	}

	// args is json-encoded.
	setBytesAttribute(span, OperationExecuteTool, semconvtrace.KeyGenAIToolCallArguments, args)
	if rspEvent != nil && rspEvent.Response != nil {
		if e := rspEvent.Response.Error; e != nil {
			span.SetStatus(codes.Error, e.Message)
			span.SetAttributes(responseErrorAttributes(e, semconvtrace.ValueDefaultErrorType)...)
		} else if err != nil {
			span.SetStatus(codes.Error, err.Error())
			span.SetAttributes(attribute.String(semconvtrace.KeyErrorType, ToErrorType(err, semconvtrace.ValueDefaultErrorType)), attribute.String(semconvtrace.KeyErrorMessage, err.Error()))
		}

		if callIDs := rspEvent.Response.GetToolCallIDs(); len(callIDs) > 0 {
			span.SetAttributes(attribute.String(semconvtrace.KeyGenAIToolCallID, callIDs[0]))
		}
		setStringAttribute(span, OperationExecuteTool, semconvtrace.KeyGenAIToolCallResult, "<not json serializable>", func() ([]byte, error) {
			return json.Marshal(rspEvent.Response)
		})
	}

	// Setting empty llm request and response (as UI expect these) while not
	// applicable for tool_response.
	span.SetAttributes(
		attribute.String(semconvtrace.KeyLLMRequest, "{}"),
		attribute.String(semconvtrace.KeyLLMResponse, "{}"),
	)
}

// ToolNameMergedTools is the name of the merged tools.
const ToolNameMergedTools = "(merged tools)"

// TraceMergedToolCalls traces the invocation of a merged tool call.
// Calling this function is not needed for telemetry purposes. This is provided
// for preventing trace-query requests typically sent by web UIs.
func TraceMergedToolCalls(span trace.Span, rspEvent *event.Event) {
	span.SetAttributes(
		attribute.String(semconvtrace.KeyGenAISystem, GenAISystem()),
		attribute.String(semconvtrace.KeyGenAIOperationName, OperationExecuteTool),
		attribute.String(semconvtrace.KeyGenAIToolName, ToolNameMergedTools),
		attribute.String(semconvtrace.KeyGenAIToolDescription, "(merged tools)"),
		attribute.String(semconvtrace.KeyGenAIToolCallArguments, "N/A"),
	)
	if rspEvent != nil && rspEvent.Response != nil {
		if callIDs := rspEvent.Response.GetToolCallIDs(); len(callIDs) > 0 {
			span.SetAttributes(attribute.String(semconvtrace.KeyGenAIToolCallID, callIDs[0]))
		}
		if e := rspEvent.Response.Error; e != nil {
			span.SetStatus(codes.Error, e.Message)
			span.SetAttributes(responseErrorAttributes(e, semconvtrace.ValueDefaultErrorType)...)
		}
		span.SetAttributes(attribute.String(semconvtrace.KeyEventID, rspEvent.ID))

		setStringAttribute(span, OperationExecuteTool, semconvtrace.KeyGenAIToolCallResult, "<not json serializable>", func() ([]byte, error) {
			return json.Marshal(rspEvent.Response)
		})
	}

	// Setting empty llm request and response (as UI expect these) while not
	// applicable for tool_response.
	span.SetAttributes(
		attribute.String(semconvtrace.KeyLLMRequest, "{}"),
		attribute.String(semconvtrace.KeyLLMResponse, "{}"),
	)
}

func resolveInvocationAgentIdentity(invoke *agent.Invocation) (string, string) {
	if invoke == nil {
		return "", ""
	}
	// Invocation does not carry a canonical agent ID today, so use
	// Invocation.AgentName as the fallback for gen_ai.agent.id.
	return invoke.AgentName, invoke.AgentName
}

// TraceBeforeInvokeAgent traces the before invocation of an agent.
func TraceBeforeInvokeAgent(span trace.Span, invoke *agent.Invocation, agentDescription, instructions string, genConfig *model.GenerationConfig) {
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String(semconvtrace.KeyGenAISystem, GenAISystem()),
		attribute.String(semconvtrace.KeyGenAIOperationName, OperationInvokeAgent),
		attribute.String(semconvtrace.KeyGenAIAgentDescription, agentDescription),
		attribute.String(semconvtrace.KeyGenAISystemInstructions, instructions),
	}
	if invoke != nil {
		traceBeforeInvokeAgentInvocation(span, invoke)
		attrs = append(attrs, beforeInvokeAgentAttributes(invoke)...)
	}
	span.SetAttributes(attrs...)
	setInvokeAgentGenerationConfigAttributes(span, genConfig)
}

func traceBeforeInvokeAgentInvocation(span trace.Span, invoke *agent.Invocation) {
	if len(invoke.RunOptions.SpanAttributes) > 0 {
		span.SetAttributes(invoke.RunOptions.SpanAttributes...)
	}
	if invoke.GetParentInvocation() == nil &&
		len(invoke.RunOptions.TraceStartedCallbacks) > 0 {
		spanContext := span.SpanContext()
		for _, callback := range invoke.RunOptions.TraceStartedCallbacks {
			if callback == nil {
				continue
			}
			callback(spanContext)
		}
	}
	setInvokeAgentInputMessageAttributes(span, invoke.Message)
}

func setInvokeAgentInputMessageAttributes(span trace.Span, msg model.Message) {
	setStringAttribute(span, OperationInvokeAgent, semconvtrace.KeyGenAIInputMessages, "<not json serializable>", func() ([]byte, error) {
		return marshalTelemetryMessages([]model.Message{msg})
	})
	setStringAttribute(span, OperationInvokeAgent, semconvtrace.KeyGenAIInputMessagesOTel, "<not json serializable>", func() ([]byte, error) {
		return marshalOTelTelemetryMessages([]model.Message{msg})
	})
}

func beforeInvokeAgentAttributes(invoke *agent.Invocation) []attribute.KeyValue {
	var attrs []attribute.KeyValue
	agentName, agentID := resolveInvocationAgentIdentity(invoke)
	if agentName != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIAgentName, agentName))
	}
	if agentID != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIAgentID, agentID))
	}
	attrs = append(attrs, attribute.String(semconvtrace.KeyInvocationID, invoke.InvocationID))
	if invoke.Session != nil {
		attrs = append(attrs,
			attribute.String(semconvtrace.KeyRunnerUserID, invoke.Session.UserID),
			attribute.String(semconvtrace.KeyGenAIConversationID, invoke.Session.ID),
		)
	}
	return attrs
}

func setInvokeAgentGenerationConfigAttributes(span trace.Span, genConfig *model.GenerationConfig) {
	if genConfig != nil {
		span.SetAttributes(attribute.Bool(semconvtrace.KeyGenAIRequestIsStream, genConfig.Stream))
		if len(genConfig.Stop) > 0 {
			span.SetAttributes(attribute.StringSlice(semconvtrace.KeyGenAIRequestStopSequences, genConfig.Stop))
		}
		if fp := genConfig.FrequencyPenalty; fp != nil {
			span.SetAttributes(attribute.Float64(semconvtrace.KeyGenAIRequestFrequencyPenalty, *fp))
		}
		if mt := genConfig.MaxTokens; mt != nil {
			span.SetAttributes(attribute.Int(semconvtrace.KeyGenAIRequestMaxTokens, *mt))
		}
		if pp := genConfig.PresencePenalty; pp != nil {
			span.SetAttributes(attribute.Float64(semconvtrace.KeyGenAIRequestPresencePenalty, *pp))
		}
		if tp := genConfig.Temperature; tp != nil {
			span.SetAttributes(attribute.Float64(semconvtrace.KeyGenAIRequestTemperature, *tp))
		}
		if tp := genConfig.TopP; tp != nil {
			span.SetAttributes(attribute.Float64(semconvtrace.KeyGenAIRequestTopP, *tp))
		}
		if te := genConfig.ThinkingEnabled; te != nil {
			span.SetAttributes(attribute.Bool(semconvtrace.KeyGenAIRequestThinkingEnabled, *te))
		}
	}
}

// TokenUsage is token usage information.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// TraceAfterInvokeAgent traces the after invocation of an agent.
func TraceAfterInvokeAgent(
	span trace.Span,
	rspEvent *event.Event,
	tokenUsage *TokenUsage,
	timeToFirstToken time.Duration,
	errorTypeFallback string,
) {
	if !span.IsRecording() {
		return
	}
	if tokenUsage != nil {
		span.SetAttributes(attribute.Int(semconvtrace.KeyGenAIUsageInputTokens, tokenUsage.PromptTokens))
		span.SetAttributes(attribute.Int(semconvtrace.KeyGenAIUsageOutputTokens, tokenUsage.CompletionTokens))
	}
	if timeToFirstToken > 0 {
		span.SetAttributes(attribute.Float64(semconvtrace.KeyTRPCAgentGoClientTimeToFirstToken, timeToFirstToken.Seconds()))
	}
	if rspEvent == nil {
		return
	}
	rsp := rspEvent.Response
	if rsp == nil {
		return
	}
	if len(rsp.Choices) > 0 {
		setStringAttribute(span, OperationInvokeAgent, semconvtrace.KeyGenAIOutputMessages, "", func() ([]byte, error) {
			return marshalTelemetryChoices(rsp.Choices)
		})
		setStringAttribute(span, OperationInvokeAgent, semconvtrace.KeyGenAIOutputMessagesOTel, "", func() ([]byte, error) {
			return marshalOTelTelemetryChoices(rsp.Choices)
		})
		var finishReasons []string
		for _, choice := range rsp.Choices {
			if choice.FinishReason != nil {
				finishReasons = append(finishReasons, *choice.FinishReason)
			} else {
				finishReasons = append(finishReasons, "")
			}
		}
		span.SetAttributes(attribute.StringSlice(semconvtrace.KeyGenAIResponseFinishReasons, finishReasons))
	}
	span.SetAttributes(attribute.String(semconvtrace.KeyGenAIResponseModel, rsp.Model))
	span.SetAttributes(attribute.String(semconvtrace.KeyGenAIResponseID, rsp.ID))

	if e := rsp.Error; e != nil {
		span.SetStatus(codes.Error, e.Message)
		span.SetAttributes(responseErrorAttributes(e, errorTypeFallback)...)
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
	traceChatWithState(span, attributes, nil)
}

func traceChatWithState(span trace.Span, attributes *TraceChatAttributes, state *ChatTraceState) {
	if !span.IsRecording() {
		return
	}
	if attributes == nil {
		span.SetAttributes(baseChatAttributes()...)
		return
	}

	state.commitRequest(span, attributes.Request, attributes.TaskType)
	state.traceChunk(span, &traceChunkAttributes{
		Invocation:       attributes.Invocation,
		Response:         attributes.Response,
		EventID:          attributes.EventID,
		TimeToFirstToken: attributes.TimeToFirstToken,
	})
}

// buildInvocationAttributes extracts attributes from the invocation.
func buildInvocationAttributes(invoke *agent.Invocation) []attribute.KeyValue {
	if invoke == nil {
		return nil
	}

	attrs := []attribute.KeyValue{
		attribute.String(semconvtrace.KeyInvocationID, invoke.InvocationID),
	}

	if invoke.Session != nil {
		attrs = append(attrs,
			attribute.String(semconvtrace.KeyGenAIConversationID, invoke.Session.ID),
			attribute.String(semconvtrace.KeyRunnerUserID, invoke.Session.UserID),
		)
	}

	if invoke.Model != nil {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIRequestModel, invoke.Model.Info().Name))
	}

	return attrs
}

// buildRequestAttributes builds request-related attributes.
func buildRequestAttributes(req *model.Request) []attribute.KeyValue {
	if req == nil {
		return nil
	}

	attrs := []attribute.KeyValue{
		attribute.StringSlice(semconvtrace.KeyGenAIRequestStopSequences, req.GenerationConfig.Stop),
		attribute.Int(semconvtrace.KeyGenAIRequestChoiceCount, 1),
	}

	// Add generation config attributes
	genConfig := req.GenerationConfig
	// Add stream attribute only when it's true
	if genConfig.Stream {
		attrs = append(attrs, attribute.Bool(semconvtrace.KeyGenAIRequestIsStream, true))
	}
	if fp := genConfig.FrequencyPenalty; fp != nil {
		attrs = append(attrs, attribute.Float64(semconvtrace.KeyGenAIRequestFrequencyPenalty, *fp))
	}
	if mt := genConfig.MaxTokens; mt != nil {
		attrs = append(attrs, attribute.Int(semconvtrace.KeyGenAIRequestMaxTokens, *mt))
	}
	if pp := genConfig.PresencePenalty; pp != nil {
		attrs = append(attrs, attribute.Float64(semconvtrace.KeyGenAIRequestPresencePenalty, *pp))
	}
	if tp := genConfig.Temperature; tp != nil {
		attrs = append(attrs, attribute.Float64(semconvtrace.KeyGenAIRequestTemperature, *tp))
	}
	if tp := genConfig.TopP; tp != nil {
		attrs = append(attrs, attribute.Float64(semconvtrace.KeyGenAIRequestTopP, *tp))
	}
	if te := genConfig.ThinkingEnabled; te != nil {
		attrs = append(attrs, attribute.Bool(semconvtrace.KeyGenAIRequestThinkingEnabled, *te))
	}

	// Add request body
	attrs = appendStringAttribute(attrs, OperationChat, semconvtrace.KeyLLMRequest, "<not json serializable>", func() ([]byte, error) {
		return json.Marshal(req)
	})

	// Add tool definitions as best-effort structured array (JSON string fallback)
	if len(req.Tools) > 0 {
		definitions := make([]*tool.Declaration, 0, len(req.Tools))
		for _, t := range toolorder.SortedTools(req.Tools) {
			definitions = append(definitions, t.Declaration())
		}
		if len(definitions) > 0 {
			attrs = appendStringAttribute(attrs, OperationChat, semconvtrace.KeyGenAIRequestToolDefinitions, "", func() ([]byte, error) {
				return json.Marshal(definitions)
			})
		}
	}

	// Add messages
	attrs = appendStringAttribute(attrs, OperationChat, semconvtrace.KeyGenAIInputMessages, "<not json serializable>", func() ([]byte, error) {
		return marshalTelemetryMessages(req.Messages)
	})
	attrs = appendStringAttribute(attrs, OperationChat, semconvtrace.KeyGenAIInputMessagesOTel, "<not json serializable>", func() ([]byte, error) {
		return marshalOTelTelemetryMessages(req.Messages)
	})

	return attrs
}

// buildResponseAttributes builds response-related attributes.
func buildResponseAttributes(rsp *model.Response, errorTypeFallback string) []attribute.KeyValue {
	if rsp == nil {
		return nil
	}

	attrs := []attribute.KeyValue{
		attribute.String(semconvtrace.KeyGenAIResponseModel, rsp.Model),
		attribute.String(semconvtrace.KeyGenAIResponseID, rsp.ID),
	}

	// Add error type if present
	if e := rsp.Error; e != nil {
		attrs = append(attrs, responseErrorAttributes(e, errorTypeFallback)...)
	}

	// Add usage attributes
	if rsp.Usage != nil {
		attrs = append(attrs,
			attribute.Int(semconvtrace.KeyGenAIUsageInputTokens, rsp.Usage.PromptTokens),
			attribute.Int(semconvtrace.KeyGenAIUsageOutputTokens, rsp.Usage.CompletionTokens),
		)
		// Prompt cache tokens (if provided by the model provider)
		if cached := rsp.Usage.PromptTokensDetails.CachedTokens; cached != 0 {
			// OpenAI: cached_tokens
			attrs = append(attrs, attribute.Int(semconvtrace.KeyGenAIUsageInputTokensCached, cached))
		}
		if cacheRead := rsp.Usage.PromptTokensDetails.CacheReadTokens; cacheRead != 0 {
			// Anthropic: cache_read_tokens
			attrs = append(attrs, attribute.Int(semconvtrace.KeyGenAIUsageInputTokensCacheRead, cacheRead))
		}
		if cacheCreation := rsp.Usage.PromptTokensDetails.CacheCreationTokens; cacheCreation != 0 {
			// Anthropic: cache_creation_tokens
			attrs = append(attrs, attribute.Int(semconvtrace.KeyGenAIUsageInputTokensCacheCreation, cacheCreation))
		}
	}

	// Add choices attributes
	if len(rsp.Choices) > 0 {
		attrs = appendStringAttribute(attrs, OperationChat, semconvtrace.KeyGenAIOutputMessages, "", func() ([]byte, error) {
			return marshalTelemetryChoices(rsp.Choices)
		})
		attrs = appendStringAttribute(attrs, OperationChat, semconvtrace.KeyGenAIOutputMessagesOTel, "", func() ([]byte, error) {
			return marshalOTelTelemetryChoices(rsp.Choices)
		})

		// Extract finish reasons
		finishReasons := make([]string, 0, len(rsp.Choices))
		for _, choice := range rsp.Choices {
			if choice.FinishReason != nil {
				finishReasons = append(finishReasons, *choice.FinishReason)
			} else {
				finishReasons = append(finishReasons, "")
			}
		}
		attrs = append(attrs, attribute.StringSlice(semconvtrace.KeyGenAIResponseFinishReasons, finishReasons))
	}

	// Add response body
	attrs = appendStringAttribute(attrs, OperationChat, semconvtrace.KeyLLMResponse, "<not json serializable>", func() ([]byte, error) {
		return json.Marshal(rsp)
	})

	return attrs
}

func responseErrorAttributes(respErr *model.ResponseError, fallback string) []attribute.KeyValue {
	if respErr == nil {
		return nil
	}
	return []attribute.KeyValue{
		attribute.String(
			semconvtrace.KeyErrorType,
			FormatResponseErrorLabel(respErr, fallback),
		),
		attribute.String(semconvtrace.KeyErrorMessage, respErr.Message),
	}
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
