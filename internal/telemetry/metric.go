package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

var (
	// MeterProvider is the global OpenTelemetry meter provider used for creating meters.
	// It defaults to a no-op implementation and should be initialized with a real provider.
	MeterProvider metric.MeterProvider = noop.NewMeterProvider()

	// ChatMeter is the meter used for recording chat-related metrics.
	ChatMeter metric.Meter = MeterProvider.Meter(metrics.MeterNameChat)

	// ChatMetricTRPCAgentGoClientRequestCnt records the number of chat requests made.
	ChatMetricTRPCAgentGoClientRequestCnt metric.Int64Counter = noop.Int64Counter{}
	// ChatMetricGenAIClientTokenUsage records the distribution of token usage (both input and output tokens).
	ChatMetricGenAIClientTokenUsage metric.Int64Histogram = noop.Int64Histogram{}
	// ChatMetricGenAIClientOperationDuration records the distribution of total chat operation durations in seconds.
	ChatMetricGenAIClientOperationDuration metric.Float64Histogram = noop.Float64Histogram{}
	// ChatMetricTRPCAgentGoClientTimeToFirstToken records the distribution of time to first token latency in seconds.
	// This measures the time from request start until the first token is received.
	ChatMetricTRPCAgentGoClientTimeToFirstToken metric.Float64Histogram = noop.Float64Histogram{}
	// ChatMetricTRPCAgentGoClientTimePerOutputToken records the distribution of average time per output token in seconds.
	// This metric measures the decode phase performance by calculating (total_duration - time_to_first_token) / (output_tokens - first_token_count).
	ChatMetricTRPCAgentGoClientTimePerOutputToken metric.Float64Histogram = noop.Float64Histogram{}
)

// ChatAttributes is the attributes for chat metrics.
type ChatAttributes struct {
	RequestModelName string
	AgentName        string

	AppName   string
	UserID    string
	SessionID string

	ErrorType string
	Error     error
}

func (a ChatAttributes) toAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(KeyGenAIOperationName, OperationChat),
		attribute.String(KeyGenAISystem, a.RequestModelName),
	}
	if a.AppName != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoAppName, a.AppName))
	}
	if a.UserID != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoUserID, a.UserID))
	}
	if a.SessionID != "" {
		attrs = append(attrs, attribute.String(KeyGenAIConversationID, a.SessionID))
	}
	if a.ErrorType != "" {
		attrs = append(attrs, attribute.String(KeyErrorType, a.ErrorType))
	} else if a.Error != nil {
		attrs = append(attrs, attribute.String(KeyErrorType, a.Error.Error()))
	}
	return attrs
}

// IncChatRequestCnt increments the chat request counter by 1 with the provided model name and session attributes.
func IncChatRequestCnt(ctx context.Context, attrs ChatAttributes) {
	ChatMetricTRPCAgentGoClientRequestCnt.Add(ctx, 1, metric.WithAttributes(attrs.toAttributes()...))
}

// RecordChatTimePerOutputTokenDuration records the average time per output token for a chat operation.
// The duration represents the time spent per token during the decode phase of LLM inference.
func RecordChatTimePerOutputTokenDuration(ctx context.Context, attrs ChatAttributes, duration time.Duration) {
	ChatMetricTRPCAgentGoClientTimePerOutputToken.Record(ctx, duration.Seconds(),
		metric.WithAttributes(attrs.toAttributes()...))
}

// RecordChatInputTokenUsage records the number of input (prompt) tokens used in a chat operation.
func RecordChatInputTokenUsage(ctx context.Context, attrs ChatAttributes, usage int64) {
	ChatMetricGenAIClientTokenUsage.Record(ctx, usage, metric.WithAttributes(append(attrs.toAttributes(), attribute.String(KeyGenAITokenType, metrics.KeyTRPCAgentGoInputTokenType))...))
}

// RecordChatOutputTokenUsage records the number of output (completion) tokens generated in a chat operation.
func RecordChatOutputTokenUsage(ctx context.Context, attrs ChatAttributes, usage int64) {
	ChatMetricGenAIClientTokenUsage.Record(ctx, usage, metric.WithAttributes(append(attrs.toAttributes(), attribute.String(KeyGenAITokenType, metrics.KeyTRPCAgentGoOutputTokenType))...))
}

// RecordChatTimeToFirstTokenDuration records the time taken from request start until the first token is received.
// This metric is important for measuring the prefill/prompt processing latency of LLM inference.
func RecordChatTimeToFirstTokenDuration(ctx context.Context, attrs ChatAttributes, duration time.Duration) {
	ChatMetricTRPCAgentGoClientTimeToFirstToken.Record(ctx, duration.Seconds(),
		metric.WithAttributes(attrs.toAttributes()...))
}

// RecordChatRequestDuration records the total duration of a chat request from start to completion.
func RecordChatRequestDuration(ctx context.Context, attrs ChatAttributes, duration time.Duration) {
	ChatMetricGenAIClientOperationDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(attrs.toAttributes()...))
}

var (
	// ExecuteToolMeter is the meter used for recording tool execution metrics.
	ExecuteToolMeter = MeterProvider.Meter(metrics.MeterNameExecuteTool)

	// ExecuteToolMetricTRPCAgentGoClientRequestCnt records the number of tool execution requests made.
	ExecuteToolMetricTRPCAgentGoClientRequestCnt metric.Int64Counter = noop.Int64Counter{}
	// ExecuteToolMetricGenAIClientOperationDuration records the distribution of tool execution durations in seconds.
	ExecuteToolMetricGenAIClientOperationDuration metric.Float64Histogram = noop.Float64Histogram{}
)

// ExecuteToolAttributes is the attributes for tool execution metrics.
type ExecuteToolAttributes struct {
	RequestModelName string
	ToolName         string
	AppName          string
	AgentName        string
	UserID           string
	SessionID        string
	Error            error
	ErrorType        string
}

func (a ExecuteToolAttributes) toAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(KeyGenAIOperationName, OperationExecuteTool),
		attribute.String(KeyGenAISystem, a.RequestModelName),
		attribute.String(KeyGenAIToolName, a.ToolName),
	}
	if a.AppName != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoAppName, a.AppName))
	}
	if a.UserID != "" {
		attrs = append(attrs, attribute.String(KeyTRPCAgentGoUserID, a.UserID))
	}
	if a.SessionID != "" {
		attrs = append(attrs, attribute.String(KeyGenAIConversationID, a.SessionID))
	}
	if a.AgentName != "" {
		attrs = append(attrs, attribute.String(KeyGenAIAgentName, a.AgentName))
	}
	if a.ErrorType != "" {
		attrs = append(attrs, attribute.String(KeyErrorType, a.ErrorType))
	} else if a.Error != nil {
		attrs = append(attrs, attribute.String(KeyErrorType, a.Error.Error()))
	}
	return attrs
}

// ReportExecuteToolMetrics reports the tool execution metrics.
func ReportExecuteToolMetrics(ctx context.Context, attrs ExecuteToolAttributes, duration time.Duration) {
	as := attrs.toAttributes()
	ExecuteToolMetricTRPCAgentGoClientRequestCnt.Add(ctx, 1, metric.WithAttributes(as...))
	ExecuteToolMetricGenAIClientOperationDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(as...))
}
