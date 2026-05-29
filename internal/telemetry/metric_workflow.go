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
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

var (
	// WorkflowMeter is the meter used for recording workflow execution metrics.
	WorkflowMeter metric.Meter = MeterProvider.Meter(metrics.MeterNameWorkflow)

	// WorkflowMetricGenAIClientOperationDuration records graph workflow/node execution durations in seconds.
	WorkflowMetricGenAIClientOperationDuration *histogram.DynamicFloat64Histogram

	// WorkflowMetricGenAIWorkflowElapsedTime records relative workflow elapsed time in seconds.
	WorkflowMetricGenAIWorkflowElapsedTime *histogram.DynamicFloat64Histogram
)

// WorkflowAttributes is the attributes for workflow execution metrics.
type WorkflowAttributes struct {
	System       string
	AppName      string
	UserID       string
	AgentID      string
	AgentName    string
	WorkflowID   string
	WorkflowName string
	WorkflowType string
	ElapsedFrom  string
	ElapsedTo    string
	Error        error
	ErrorType    string
}

func (a WorkflowAttributes) toAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(semconvtrace.KeyGenAIOperationName, OperationWorkflow),
		attribute.String(semconvtrace.KeyGenAISystem, a.System),
		attribute.String(semconvtrace.KeyGenAIAppName, a.AppName),
		attribute.String(semconvtrace.KeyGenAIUserID, a.UserID),
		attribute.String(semconvtrace.KeyGenAIAgentID, a.AgentID),
		attribute.String(semconvtrace.KeyGenAIWorkflowID, a.WorkflowID),
		attribute.String(semconvtrace.KeyGenAIWorkflowName, a.WorkflowName),
		attribute.String(semconvtrace.KeyGenAIWorkflowType, a.WorkflowType),
	}
	if a.AgentName != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIAgentName, a.AgentName))
	}
	if a.ErrorType != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyErrorType, a.ErrorType))
	} else if a.Error != nil {
		attrs = append(attrs, attribute.String(semconvtrace.KeyErrorType, ToErrorType(a.Error, semconvtrace.ValueDefaultErrorType)))
	}
	return attrs
}

func (a WorkflowAttributes) toElapsedAttributes() []attribute.KeyValue {
	attrs := a.toAttributes()
	elapsedFrom := a.ElapsedFrom
	if elapsedFrom == "" {
		elapsedFrom = metrics.ValueGenAIWorkflowElapsedFromRootWorkflowStart
	}
	elapsedTo := a.ElapsedTo
	if elapsedTo == "" {
		elapsedTo = metrics.ValueGenAIWorkflowElapsedToCurrentWorkflowEnd
	}
	attrs = append(
		attrs,
		attribute.String(metrics.KeyGenAIWorkflowElapsedFrom, elapsedFrom),
		attribute.String(metrics.KeyGenAIWorkflowElapsedTo, elapsedTo),
	)
	return attrs
}

// ReportWorkflowMetrics reports the workflow execution metrics.
func ReportWorkflowMetrics(ctx context.Context, attrs WorkflowAttributes, duration time.Duration) {
	if WorkflowMetricGenAIClientOperationDuration == nil {
		return
	}
	WorkflowMetricGenAIClientOperationDuration.Record(
		ctx,
		duration.Seconds(),
		metric.WithAttributes(attrs.toAttributes()...),
	)
}

// ReportWorkflowElapsedMetrics reports relative workflow elapsed-time metrics.
func ReportWorkflowElapsedMetrics(ctx context.Context, attrs WorkflowAttributes, duration time.Duration) {
	if WorkflowMetricGenAIWorkflowElapsedTime == nil {
		return
	}
	WorkflowMetricGenAIWorkflowElapsedTime.Record(
		ctx,
		duration.Seconds(),
		metric.WithAttributes(attrs.toElapsedAttributes()...),
	)
}
