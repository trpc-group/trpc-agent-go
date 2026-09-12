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

const unknownMetricValue = "_unknown"

var (
	// InvokeSkillMeter is the meter used for recording skill activation metrics.
	InvokeSkillMeter metric.Meter = MeterProvider.Meter(metrics.MeterNameInvokeSkill)

	// InvokeSkillMetricGenAIRequestCnt records the number of skill activations.
	InvokeSkillMetricGenAIRequestCnt metric.Int64Counter
	// InvokeSkillMetricGenAIClientOperationDuration records skill activation/materialization durations in seconds.
	InvokeSkillMetricGenAIClientOperationDuration *histogram.DynamicFloat64Histogram
)

// InvokeSkillMetricAttributes contains metric attributes for skill activation.
type InvokeSkillMetricAttributes struct {
	SkillName    string
	SkillID      string
	SkillVersion string
	UserID       string
	AgentID      string
	AgentName    string
	Error        error
	ErrorType    string
}

func (a InvokeSkillMetricAttributes) toAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(semconvtrace.KeyGenAIOperationName, OperationInvokeSkill),
		attribute.String(semconvtrace.KeyGenAISkillName, nonEmpty(a.SkillName)),
		attribute.String(semconvtrace.KeyGenAISkillID, nonEmpty(a.SkillID)),
		attribute.String(semconvtrace.KeyGenAIUserID, nonEmpty(a.UserID)),
		attribute.String(semconvtrace.KeyGenAIAgentID, nonEmpty(a.AgentID)),
	}
	if a.AgentName != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAIAgentName, a.AgentName))
	}
	if a.SkillVersion != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyGenAISkillVersion, a.SkillVersion))
	}
	if a.ErrorType != "" {
		attrs = append(attrs, attribute.String(semconvtrace.KeyErrorType, a.ErrorType))
	} else if a.Error != nil {
		attrs = append(attrs, attribute.String(semconvtrace.KeyErrorType, ToErrorType(a.Error, semconvtrace.ValueDefaultErrorType)))
	}
	return attrs
}

// ReportInvokeSkillMetrics reports skill activation/materialization metrics.
func ReportInvokeSkillMetrics(ctx context.Context, attrs InvokeSkillMetricAttributes, duration time.Duration) {
	as := attrs.toAttributes()
	if InvokeSkillMetricGenAIRequestCnt != nil {
		InvokeSkillMetricGenAIRequestCnt.Add(ctx, 1, metric.WithAttributes(as...))
	}
	if InvokeSkillMetricGenAIClientOperationDuration != nil {
		InvokeSkillMetricGenAIClientOperationDuration.Record(ctx, duration.Seconds(), metric.WithAttributes(as...))
	}
}

func nonEmpty(value string) string {
	if value == "" {
		return unknownMetricValue
	}
	return value
}
