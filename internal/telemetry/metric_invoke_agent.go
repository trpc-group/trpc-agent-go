package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

var (
	// InvokeAgentMeter is the meter used for recording agent invocation metrics.
	InvokeAgentMeter metric.Meter = MeterProvider.Meter(metrics.MeterNameInvokeAgent)

	// InvokeAgentMetricGenAIRequestCnt records the number of invoke agent requests made.
	InvokeAgentMetricGenAIRequestCnt metric.Int64Counter = noop.Int64Counter{}
	// InvokeMetricGenAIClientTokenUsage records the distribution of input and output token usage.
	InvokeMetricGenAIClientTokenUsage metric.Int64Histogram = noop.Int64Histogram{}
	// InvokeAgentMetricGenAIClientTimeToFirstToken records the distribution of time to first token latency in seconds.
	InvokeAgentMetricGenAIClientTimeToFirstToken metric.Float64Histogram = noop.Float64Histogram{}
	// InvokeAgentMetricGenAIClientOperationDuration records the distribution of total agent invocation durations in seconds.
	InvokeAgentMetricGenAIClientOperationDuration metric.Float64Histogram = noop.Float64Histogram{}
	// InvokeAgentMetricGenAIClientTimePerOutputToken records the distribution of time per output token in seconds.
	// This metric measures the decode phase performance by calculating (total_duration - time_to_first_token) / (output_tokens - first_token_count).
	InvokeAgentMetricGenAIClientTimePerOutputToken metric.Float64Histogram = noop.Float64Histogram{}
	// InvokeAgentMetricGenAIClientOutputTokenPerTime records the distribution of output token per time for client.
	// 1 / InvokeAgentMetricGenAIClientTimePerOutputToken.
	InvokeAgentMetricGenAIClientOutputTokenPerTime metric.Float64Histogram = noop.Float64Histogram{}
)

// invokeAgentAttributes is the attributes for invoke agent metrics.
// It is a subset of chat attributes.
type invokeAgentAttributes struct {
	AgentName string
	AppName   string
	UserID    string
	System    string
	Stream    bool
	ErrorType string
	Error     error
}

func (a invokeAgentAttributes) toAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(KeyGenAIOperationName, OperationInvokeAgent),
		attribute.Bool(metrics.KeyTRPCAgentGoStream, a.Stream),
		attribute.String(KeyGenAISystem, a.System),
	}
	if a.AppName != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoAppName, a.AppName))
	}
	if a.UserID != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoUserID, a.UserID))
	}
	if a.AgentName != "" {
		attrs = append(attrs, attribute.String(KeyGenAIAgentName, a.AgentName))
	}
	if a.ErrorType != "" {
		attrs = append(attrs, attribute.String(KeyErrorType, a.ErrorType))
	} else if a.Error != nil {
		attrs = append(attrs, attribute.String(KeyErrorType, ValueDefaultErrorType))
	}

	return attrs
}

// keep compile-time reference to avoid unused warning until wired.
var _ = invokeAgentAttributes{}

// InvokeAgentTracker tracks metrics for a single agent invocation lifecycle.
type InvokeAgentTracker struct {
	ctx                    context.Context
	start                  time.Time
	isFirstToken           bool
	firstTokenTimeDuration time.Duration
	firstCompleteToken     int
	totalCompletionTokens  int
	totalPromptTokens      int

	invocation *agent.Invocation
	stream     bool // indicates whether the request is streaming

	err               *error // pointer to capture final error
	responseErrorType string
}

// NewInvokeAgentTracker creates a new telemetry tracker for agent invocation.
func NewInvokeAgentTracker(
	ctx context.Context,
	invocation *agent.Invocation,
	stream bool,
	err *error,
) *InvokeAgentTracker {
	return &InvokeAgentTracker{
		ctx:          ctx,
		start:        time.Now(),
		isFirstToken: true,
		invocation:   invocation,
		stream:       stream,
		err:          err,
	}
}

// TrackResponse updates telemetry state for each response chunk.
func (t *InvokeAgentTracker) TrackResponse(response *model.Response) {
	if response == nil {
		return
	}

	// Track first token timing
	if t.isFirstToken {
		t.firstTokenTimeDuration = time.Since(t.start)
		t.isFirstToken = false

		if response.Usage != nil {
			t.firstCompleteToken = response.Usage.CompletionTokens
		}
	}

	// Track token usage
	if !response.IsPartial && response.Usage != nil {
		t.totalPromptTokens += response.Usage.PromptTokens
		t.totalCompletionTokens += response.Usage.CompletionTokens
	}
}

// SetResponseErrorType updates the response error type seen (for extracting error info).
func (t *InvokeAgentTracker) SetResponseErrorType(errorType string) {
	t.responseErrorType = errorType
}

// RecordMetrics returns a defer function that records all telemetry metrics.
// Should be called with defer immediately after creating the tracker.
func (t *InvokeAgentTracker) RecordMetrics() func() {
	return func() {
		attrs := t.buildAttributes()
		requestDuration := time.Since(t.start)
		otelAttrs := attrs.toAttributes()

		// Increment request counter
		InvokeAgentMetricGenAIRequestCnt.Add(t.ctx, 1, metric.WithAttributes(otelAttrs...))

		// Record request duration
		InvokeAgentMetricGenAIClientOperationDuration.Record(t.ctx, requestDuration.Seconds(), metric.WithAttributes(otelAttrs...))

		// Record time to first token
		InvokeAgentMetricGenAIClientTimeToFirstToken.Record(t.ctx, t.firstTokenTimeDuration.Seconds(),
			metric.WithAttributes(otelAttrs...))

		// Record input token usage
		InvokeMetricGenAIClientTokenUsage.Record(t.ctx, int64(t.totalPromptTokens),
			metric.WithAttributes(append(otelAttrs, attribute.String(KeyGenAITokenType, metrics.KeyTRPCAgentGoInputTokenType))...))

		// Record output token usage
		InvokeMetricGenAIClientTokenUsage.Record(t.ctx, int64(t.totalCompletionTokens),
			metric.WithAttributes(append(otelAttrs, attribute.String(KeyGenAITokenType, metrics.KeyTRPCAgentGoOutputTokenType))...))

		// Calculate and record derived metrics
		t.recordDerivedMetrics(otelAttrs, requestDuration)
	}
}

// buildAttributes constructs invokeAgentAttributes from tracked state.
func (t *InvokeAgentTracker) buildAttributes() invokeAgentAttributes {
	attrs := invokeAgentAttributes{}

	// Extract error
	if t.err != nil && *t.err != nil {
		attrs.Error = *t.err
	}
	if t.responseErrorType != "" {
		attrs.ErrorType = t.responseErrorType
	}

	// Extract request attributes
	attrs.Stream = t.stream

	// Extract invocation attributes (with nil safety)
	if t.invocation != nil {
		if t.invocation.AgentName != "" {
			attrs.AgentName = t.invocation.AgentName
		}
		if t.invocation.Model != nil {
			attrs.System = t.invocation.Model.Info().Name
		}
		if t.invocation.Session != nil {
			attrs.UserID = t.invocation.Session.UserID
			attrs.AppName = t.invocation.Session.AppName
		}
	}

	return attrs
}

// recordDerivedMetrics calculates and records time-per-token and token-per-time metrics.
func (t *InvokeAgentTracker) recordDerivedMetrics(otelAttrs []attribute.KeyValue, requestDuration time.Duration) {
	tokens := t.totalCompletionTokens - t.firstCompleteToken
	duration := requestDuration - t.firstTokenTimeDuration

	if tokens > 0 && duration > 0 {
		// Record time per output token
		InvokeAgentMetricGenAIClientTimePerOutputToken.Record(t.ctx, (duration / time.Duration(tokens)).Seconds(),
			metric.WithAttributes(otelAttrs...))
		// Record output token per time
		InvokeAgentMetricGenAIClientOutputTokenPerTime.Record(t.ctx, float64(tokens)/duration.Seconds(),
			metric.WithAttributes(otelAttrs...))
	} else if tokens == 0 && t.totalCompletionTokens > 0 && requestDuration > 0 {
		// Record time per output token
		InvokeAgentMetricGenAIClientTimePerOutputToken.Record(t.ctx, (requestDuration / time.Duration(t.totalCompletionTokens)).Seconds(),
			metric.WithAttributes(otelAttrs...))
		// Record output token per time
		InvokeAgentMetricGenAIClientOutputTokenPerTime.Record(t.ctx, float64(t.totalCompletionTokens)/requestDuration.Seconds(),
			metric.WithAttributes(otelAttrs...))
	}
}
