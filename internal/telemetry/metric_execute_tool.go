//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/metric/histogram"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

var (
	// ExecuteToolMeter is the meter used for recording tool execution metrics.
	ExecuteToolMeter = MeterProvider.Meter(metrics.MeterNameExecuteTool)

	// ExecuteToolMetricTRPCAgentGoClientRequestCnt records the number of tool execution requests made.
	ExecuteToolMetricTRPCAgentGoClientRequestCnt metric.Int64Counter
	// ExecuteToolMetricGenAIClientOperationDuration records the distribution of tool execution durations in seconds.
	ExecuteToolMetricGenAIClientOperationDuration *histogram.DynamicFloat64Histogram
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
		attrs = append(attrs, attribute.String(KeyErrorType, ToErrorType(a.Error, ValueDefaultErrorType)))
	}
	return attrs
}

// ReportExecuteToolMetrics reports the tool execution metrics.
func ReportExecuteToolMetrics(ctx context.Context, attrs ExecuteToolAttributes, duration time.Duration) {
	as := attrs.toAttributes()
	if ExecuteToolMetricTRPCAgentGoClientRequestCnt != nil {
		ExecuteToolMetricTRPCAgentGoClientRequestCnt.Add(ctx, 1, metric.WithAttributes(as...))
	}
	if ExecuteToolMetricGenAIClientOperationDuration != nil {
		ExecuteToolMetricGenAIClientOperationDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(as...))
	}
}
