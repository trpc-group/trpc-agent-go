//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package invokeagenttelemetry provides shared invoke_agent OpenTelemetry
// helpers used by the agent runtime to emit consistent traces and metrics
// across all agent implementations.
package invokeagenttelemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	noopmetric "go.opentelemetry.io/otel/metric/noop"
	oteltrace "go.opentelemetry.io/otel/trace"
	nooptrace "go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/errs"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	metricsemconv "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

// OperationInvokeAgent is the GenAI operation name reported on every
// invoke_agent span and metric emitted by this package.
const OperationInvokeAgent = "invoke_agent"

var (
	// MeterProvider is the meter provider used to create invoke_agent metric
	// instruments. It defaults to a no-op provider until [InitMeterProvider]
	// is called by the telemetry runtime.
	MeterProvider metric.MeterProvider = noopmetric.NewMeterProvider()

	// InvokeAgentMeter is the meter used to record invoke_agent metrics. It
	// is rebuilt by [InitMeterProvider] when a real provider is installed.
	InvokeAgentMeter metric.Meter = MeterProvider.Meter(metricsemconv.MeterNameInvokeAgent)

	// InvokeAgentMetricGenAIRequestCnt counts invoke_agent requests.
	InvokeAgentMetricGenAIRequestCnt metric.Int64Counter
	// InvokeAgentMetricGenAIClientTokenUsage tracks token usage histograms
	// (prompt vs completion) for invoke_agent operations.
	InvokeAgentMetricGenAIClientTokenUsage *histogram.DynamicInt64Histogram
	// InvokeAgentMetricGenAIClientTimeToFirstToken tracks the time-to-first-token
	// histogram for streaming invoke_agent operations.
	InvokeAgentMetricGenAIClientTimeToFirstToken *histogram.DynamicFloat64Histogram
	// InvokeAgentMetricGenAIClientOperationDuration tracks the end-to-end
	// duration histogram of invoke_agent operations.
	InvokeAgentMetricGenAIClientOperationDuration *histogram.DynamicFloat64Histogram
)

// InvocationView is the immutable snapshot of an [agent.Invocation] consumed
// by invoke_agent telemetry helpers. It captures only the fields required to
// label spans and metrics so callers can pass either a live invocation or a
// derived view without coupling this package to the agent runtime.
type InvocationView struct {
	AgentName             string
	InvocationID          string
	Message               model.Message
	Session               *session.Session
	Model                 model.Model
	SpanAttributes        []attribute.KeyValue
	TraceStartedCallbacks []func(oteltrace.SpanContext)
	HasParent             bool
}

// InvokeAgentOptions carries the per-call attributes that influence the
// invoke_agent span and metrics, such as the agent description, system
// instructions and the resolved generation configuration.
type InvokeAgentOptions struct {
	Description  string
	Instructions string
	GenConfig    *model.GenerationConfig
	Stream       bool
}

// TokenUsage aggregates the token counts observed during a single
// invoke_agent operation.
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// InvokeAgentRecorder owns the lifecycle of a single invoke_agent span and
// its associated tracker, accumulating token usage and error status until
// [InvokeAgentRecorder.Finish] is called.
type InvokeAgentRecorder struct {
	started           bool
	span              oteltrace.Span
	tracker           *InvokeAgentTracker
	tokenUsage        TokenUsage
	fullRespEvent     *event.Event
	responseErrorType string
	finished          bool
}

type invokeAgentAttributes struct {
	AgentName string
	AgentID   string
	AppName   string
	UserID    string
	System    string
	Stream    bool
	ErrorType string
	Error     error
}

// InvokeAgentTracker accumulates per-call telemetry state for a single
// invoke_agent operation and is responsible for emitting the associated
// metrics when [InvokeAgentTracker.RecordMetrics] is invoked.
type InvokeAgentTracker struct {
	ctx                    context.Context
	start                  time.Time
	isFirstToken           bool
	firstTokenTimeDuration time.Duration
	totalCompletionTokens  int
	totalPromptTokens      int

	attributes invokeAgentAttributes
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

// InitMeterProvider rebuilds the package-level invoke_agent metric
// instruments using the supplied [metric.MeterProvider]. It must be called
// once when the telemetry runtime is initialized so that subsequent recorders
// emit data through the configured exporter.
func InitMeterProvider(mp metric.MeterProvider) error {
	MeterProvider = mp
	InvokeAgentMeter = mp.Meter(metricsemconv.MeterNameInvokeAgent)

	var err error
	InvokeAgentMetricGenAIRequestCnt, err = InvokeAgentMeter.Int64Counter(
		metricsemconv.MetricTRPCAgentGoClientRequestCnt,
		metric.WithDescription("Total number of invoke_agent requests"),
		metric.WithUnit("1"),
	)
	if err != nil {
		return fmt.Errorf("failed to create invoke_agent request count metric: %w", err)
	}
	InvokeAgentMetricGenAIClientTokenUsage, err = histogram.NewDynamicInt64Histogram(
		mp,
		metricsemconv.MeterNameInvokeAgent,
		metricsemconv.MetricGenAIClientTokenUsage,
		metric.WithDescription("Token usage for invoke_agent"),
		metric.WithUnit("{token}"),
	)
	if err != nil {
		return fmt.Errorf("failed to create invoke_agent token usage metric: %w", err)
	}
	InvokeAgentMetricGenAIClientTimeToFirstToken, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metricsemconv.MeterNameInvokeAgent,
		metricsemconv.MetricTRPCAgentGoClientTimeToFirstToken,
		metric.WithDescription("Time to first token for invoke_agent"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("failed to create invoke_agent ttft metric: %w", err)
	}
	InvokeAgentMetricGenAIClientOperationDuration, err = histogram.NewDynamicFloat64Histogram(
		mp,
		metricsemconv.MeterNameInvokeAgent,
		metricsemconv.MetricGenAIClientOperationDuration,
		metric.WithDescription("Duration of invoke_agent operations"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("failed to create invoke_agent duration metric: %w", err)
	}
	return nil
}

// InvokeAgentSpanName returns the canonical span name for an invoke_agent
// operation. When the agent name is unknown, it falls back to the generic
// operation name so spans remain attributable to invoke_agent.
func InvokeAgentSpanName(invocation *InvocationView) string {
	if invocation == nil || invocation.AgentName == "" {
		return OperationInvokeAgent
	}
	return fmt.Sprintf("%s %s", OperationInvokeAgent, invocation.AgentName)
}

// StartInvokeAgent attaches an [InvokeAgentRecorder] to the supplied span
// and seeds the underlying tracker with invocation metadata. When started is
// true, the pre-execution span attributes are written immediately via
// [TraceBeforeInvokeAgent].
func StartInvokeAgent(
	ctx context.Context,
	invocation *InvocationView,
	span oteltrace.Span,
	started bool,
	opts InvokeAgentOptions,
) *InvokeAgentRecorder {
	if span == nil {
		span = nooptrace.Span{}
		started = false
	}

	if started {
		genConfig := opts.GenConfig
		if genConfig == nil {
			genConfig = &model.GenerationConfig{Stream: opts.Stream}
		}
		TraceBeforeInvokeAgent(
			span,
			invocation,
			opts.Description,
			opts.Instructions,
			genConfig,
		)
	}

	var trackerErr error
	tracker := NewInvokeAgentTracker(ctx, invocation, opts.Stream, &trackerErr)
	return &InvokeAgentRecorder{
		started: started,
		span:    span,
		tracker: tracker,
	}
}

// Observe folds a single agent event into the recorder, updating the
// running token usage, the most recent non-partial response event and the
// derived response error type. It is safe to call on a nil recorder.
func (r *InvokeAgentRecorder) Observe(evt *event.Event) {
	if r == nil || evt == nil {
		return
	}
	resp := evt.Response
	if resp == nil {
		return
	}
	r.tracker.TrackResponse(resp)
	if !resp.IsPartial {
		if usage := resp.Usage; usage != nil {
			r.tokenUsage.PromptTokens += usage.PromptTokens
			r.tokenUsage.CompletionTokens += usage.CompletionTokens
			r.tokenUsage.TotalTokens += usage.TotalTokens
		}
		r.fullRespEvent = evt
	}
	if resp.Error != nil {
		r.responseErrorType = FormatResponseErrorLabel(
			resp.Error,
			model.ErrorTypeRunError,
		)
	}
}

// SetResponseErrorType overrides the response error type that will be
// reported on the span and metrics when the recorder is finished.
func (r *InvokeAgentRecorder) SetResponseErrorType(errorType string) {
	if r == nil {
		return
	}
	r.responseErrorType = errorType
}

// Finish closes the invoke_agent span and emits the accumulated metrics.
// It is idempotent and safe to call on a nil recorder.
func (r *InvokeAgentRecorder) Finish() {
	if r == nil || r.finished {
		return
	}
	r.finished = true

	if r.fullRespEvent != nil && r.fullRespEvent.Response != nil {
		if respErr := r.fullRespEvent.Response.Error; respErr != nil {
			r.responseErrorType = FormatResponseErrorLabel(
				respErr,
				model.ErrorTypeRunError,
			)
		} else {
			r.responseErrorType = ""
		}
	}

	if r.started {
		if r.fullRespEvent != nil {
			TraceAfterInvokeAgent(
				r.span,
				r.fullRespEvent,
				&r.tokenUsage,
				r.tracker.FirstTokenTimeDuration(),
				model.ErrorTypeRunError,
			)
		} else if r.responseErrorType != "" {
			r.span.SetStatus(codes.Error, r.responseErrorType)
			r.span.SetAttributes(
				attribute.String(semconvtrace.KeyErrorType, r.responseErrorType),
			)
		}
	}

	if r.tracker != nil {
		r.tracker.SetResponseErrorType(r.responseErrorType)
		r.tracker.RecordMetrics()()
	}

	if r.started {
		r.span.End()
	}
}

// Span returns the underlying OpenTelemetry span. A no-op span is returned
// when the recorder is nil so callers can chain attribute writes safely.
func (r *InvokeAgentRecorder) Span() oteltrace.Span {
	if r == nil {
		return nooptrace.Span{}
	}
	return r.span
}

// TraceStarted reports whether tracing was enabled when the recorder was
// created. It is safe to call on a nil recorder, in which case it returns
// false.
func (r *InvokeAgentRecorder) TraceStarted() bool {
	if r == nil {
		return false
	}
	return r.started
}

// TraceBeforeInvokeAgent writes the pre-execution invoke_agent span
// attributes derived from the invocation view and generation configuration.
// It is a no-op when the span is not recording.
func TraceBeforeInvokeAgent(
	span oteltrace.Span,
	invoke *InvocationView,
	agentDescription string,
	instructions string,
	genConfig *model.GenerationConfig,
) {
	if !span.IsRecording() {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String(semconvtrace.KeyGenAISystem, semconvtrace.SystemTRPCGoAgent),
		attribute.String(semconvtrace.KeyGenAIOperationName, OperationInvokeAgent),
		attribute.String(semconvtrace.KeyGenAIAgentDescription, agentDescription),
		attribute.String(semconvtrace.KeyGenAISystemInstructions, instructions),
	}
	if invoke != nil {
		if len(invoke.SpanAttributes) > 0 {
			span.SetAttributes(invoke.SpanAttributes...)
		}
		if !invoke.HasParent && len(invoke.TraceStartedCallbacks) > 0 {
			spanContext := span.SpanContext()
			for _, callback := range invoke.TraceStartedCallbacks {
				if callback == nil {
					continue
				}
				callback(spanContext)
			}
		}
		if bts, err := marshalTelemetryMessages([]model.Message{invoke.Message}); err == nil {
			span.SetAttributes(
				attribute.String(semconvtrace.KeyGenAIInputMessages, string(bts)),
			)
		} else {
			span.SetAttributes(attribute.String(semconvtrace.KeyGenAIInputMessages, "<not json serializable>"))
		}
		if invoke.AgentName != "" {
			attrs = append(attrs,
				attribute.String(semconvtrace.KeyGenAIAgentName, invoke.AgentName),
				attribute.String(semconvtrace.KeyGenAIAgentID, invoke.AgentName),
			)
		}
		attrs = append(attrs, attribute.String(semconvtrace.KeyInvocationID, invoke.InvocationID))
		if invoke.Session != nil {
			attrs = append(attrs,
				attribute.String(semconvtrace.KeyRunnerUserID, invoke.Session.UserID),
				attribute.String(semconvtrace.KeyGenAIConversationID, invoke.Session.ID),
			)
		}
	}
	span.SetAttributes(attrs...)
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
		if topP := genConfig.TopP; topP != nil {
			span.SetAttributes(attribute.Float64(semconvtrace.KeyGenAIRequestTopP, *topP))
		}
		if te := genConfig.ThinkingEnabled; te != nil {
			span.SetAttributes(attribute.Bool(semconvtrace.KeyGenAIRequestThinkingEnabled, *te))
		}
	}
}

// TraceAfterInvokeAgent writes the post-execution invoke_agent span
// attributes including token usage, output messages, finish reasons and
// error status. It is a no-op when the span is not recording.
func TraceAfterInvokeAgent(
	span oteltrace.Span,
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
	if rspEvent == nil || rspEvent.Response == nil {
		return
	}
	rsp := rspEvent.Response
	if len(rsp.Choices) > 0 {
		if bts, err := marshalTelemetryChoices(rsp.Choices); err == nil {
			span.SetAttributes(attribute.String(semconvtrace.KeyGenAIOutputMessages, string(bts)))
		}
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
	span.SetAttributes(
		attribute.String(semconvtrace.KeyGenAIResponseModel, rsp.Model),
		attribute.String(semconvtrace.KeyGenAIResponseID, rsp.ID),
	)
	if e := rsp.Error; e != nil {
		span.SetStatus(codes.Error, e.Message)
		span.SetAttributes(responseErrorAttributes(e, errorTypeFallback)...)
	}
}

// NewInvokeAgentTracker constructs an [InvokeAgentTracker] populated with
// the static metric attributes derived from the invocation view. The err
// pointer captures the latest error observed by the caller and is read when
// metrics are recorded.
func NewInvokeAgentTracker(
	ctx context.Context,
	invocation *InvocationView,
	stream bool,
	err *error,
) *InvokeAgentTracker {
	attributes := invokeAgentAttributes{Stream: stream, Error: *err}
	if invocation != nil {
		if invocation.AgentName != "" {
			attributes.AgentName = invocation.AgentName
			attributes.AgentID = invocation.AgentName
		}
		if invocation.Model != nil {
			attributes.System = invocation.Model.Info().Name
		}
		if invocation.Session != nil {
			attributes.UserID = invocation.Session.UserID
			attributes.AppName = invocation.Session.AppName
		}
	}
	return &InvokeAgentTracker{
		ctx:          ctx,
		start:        time.Now(),
		isFirstToken: true,
		attributes:   attributes,
	}
}

// TrackResponse updates the tracker with a single model response, recording
// time-to-first-token on the first valid streaming chunk and accumulating
// total token usage when a non-partial response carries usage information.
func (t *InvokeAgentTracker) TrackResponse(response *model.Response) {
	if response == nil {
		return
	}
	if t.isFirstToken && response.IsValidContent() {
		t.firstTokenTimeDuration = time.Since(t.start)
		t.isFirstToken = false
	}
	if !response.IsPartial && response.Usage != nil {
		t.totalPromptTokens += response.Usage.PromptTokens
		t.totalCompletionTokens += response.Usage.CompletionTokens
	}
}

// SetResponseErrorType records the response error type to be exposed as a
// metric attribute when [InvokeAgentTracker.RecordMetrics] runs.
func (t *InvokeAgentTracker) SetResponseErrorType(errorType string) {
	t.attributes.ErrorType = errorType
}

// RecordMetrics returns a closure that, when invoked, emits the request
// count, total duration, time-to-first-token and token usage metrics for the
// completed invoke_agent operation.
func (t *InvokeAgentTracker) RecordMetrics() func() {
	return func() {
		requestDuration := time.Since(t.start)
		otelAttrs := t.attributes.toAttributes()

		if InvokeAgentMetricGenAIRequestCnt != nil {
			InvokeAgentMetricGenAIRequestCnt.Add(t.ctx, 1, metric.WithAttributes(otelAttrs...))
		}
		if InvokeAgentMetricGenAIClientOperationDuration != nil {
			InvokeAgentMetricGenAIClientOperationDuration.Record(
				t.ctx,
				requestDuration.Seconds(),
				metric.WithAttributes(otelAttrs...),
			)
		}
		if t.firstTokenTimeDuration > 0 && InvokeAgentMetricGenAIClientTimeToFirstToken != nil {
			InvokeAgentMetricGenAIClientTimeToFirstToken.Record(
				t.ctx,
				t.firstTokenTimeDuration.Seconds(),
				metric.WithAttributes(otelAttrs...),
			)
		}
		if InvokeAgentMetricGenAIClientTokenUsage != nil {
			InvokeAgentMetricGenAIClientTokenUsage.Record(
				t.ctx,
				int64(t.totalPromptTokens),
				metric.WithAttributes(append(otelAttrs, attribute.String(semconvtrace.KeyGenAITokenType, metricsemconv.KeyTRPCAgentGoInputTokenType))...),
			)
			InvokeAgentMetricGenAIClientTokenUsage.Record(
				t.ctx,
				int64(t.totalCompletionTokens),
				metric.WithAttributes(append(otelAttrs, attribute.String(semconvtrace.KeyGenAITokenType, metricsemconv.KeyTRPCAgentGoOutputTokenType))...),
			)
		}
	}
}

// FirstTokenTimeDuration returns the time elapsed between the start of the
// invocation and the first valid streaming response chunk. It returns zero
// if no qualifying chunk has been observed.
func (t *InvokeAgentTracker) FirstTokenTimeDuration() time.Duration {
	return t.firstTokenTimeDuration
}

func (a invokeAgentAttributes) toAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(semconvtrace.KeyGenAIOperationName, OperationInvokeAgent),
		attribute.Bool(metricsemconv.KeyTRPCAgentGoStream, a.Stream),
		attribute.String(semconvtrace.KeyGenAISystem, a.System),
	}
	if a.AppName != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyTRPCAgentGoAppName, a.AppName))
	}
	if a.UserID != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyTRPCAgentGoUserID, a.UserID))
	}
	if a.AgentName != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIAgentName, a.AgentName))
	}
	if a.AgentID != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIAgentID, a.AgentID))
	}
	if a.ErrorType != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyErrorType, a.ErrorType))
	} else if a.Error != nil {
		attrs = append(attrs, attribute.String(semconvtrace.KeyErrorType, ToErrorType(a.Error, semconvtrace.ValueDefaultErrorType)))
	}
	return attrs
}

// FormatResponseErrorLabel renders a stable error label suitable for use as
// a metric attribute. When respErr is nil the supplied fallback is returned
// unchanged; otherwise the response error type is preferred and the optional
// error code is appended.
func FormatResponseErrorLabel(respErr *model.ResponseError, fallback string) string {
	if respErr == nil {
		return fallback
	}
	label := fallback
	if respErr.Type != "" {
		label = respErr.Type
	}
	if respErr.Code != nil && *respErr.Code != "" {
		return fmt.Sprintf("%s_%s", label, *respErr.Code)
	}
	return label
}

// ToErrorType derives a stable error type label from a Go error by routing
// it through [errs.ToResponseError] and [FormatResponseErrorLabel].
func ToErrorType(err error, errorType string) string {
	return FormatResponseErrorLabel(errs.ToResponseError(err), errorType)
}

func responseErrorAttributes(respErr *model.ResponseError, fallback string) []attribute.KeyValue {
	if respErr == nil {
		return nil
	}
	attrs := []attribute.KeyValue{
		attribute.String(semconvtrace.KeyErrorType, FormatResponseErrorLabel(respErr, fallback)),
	}
	if respErr.Message != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyErrorMessage, respErr.Message))
	}
	return attrs
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
