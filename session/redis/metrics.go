//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"context"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	ametric "trpc.group/trpc-go/trpc-agent-go/telemetry/metric"
)

const (
	meterName = "trpc_agent_go.session.redis"

	metricPrefix = "trpc_agent_go.session.redis."
)

// Operation name constants used as keys for per-operation counters.
const (
	opCreateSession         = "create_session"
	opGetSession            = "get_session"
	opAppendEvent           = "append_event"
	opCreateSessionSummary  = "create_session_summary"
	opGetSessionSummaryText = "get_session_summary_text"
	opAppendTrackEvent      = "append_track_event"
)

// Per-operation metric names.
const (
	metricCreateSessionCount        = metricPrefix + opCreateSession + "_count"
	metricGetSessionCount           = metricPrefix + opGetSession + "_count"
	metricAppendEventCount          = metricPrefix + opAppendEvent + "_count"
	metricCreateSessionSummaryCount = metricPrefix + opCreateSessionSummary + "_count"
	metricGetSessionSummaryCount    = metricPrefix + opGetSessionSummaryText + "_count"
	metricAppendTrackEventCount     = metricPrefix + opAppendTrackEvent + "_count"
)

var (
	operationCounters     map[string]metric.Int64Counter
	operationCountersOnce sync.Once
)

// operationMetrics maps operation name to its metric name and description.
var operationMetrics = []struct {
	operation   string
	metricName  string
	description string
}{
	{opCreateSession, metricCreateSessionCount, "Number of create_session operations"},
	{opGetSession, metricGetSessionCount, "Number of get_session operations"},
	{opAppendEvent, metricAppendEventCount, "Number of append_event operations"},
	{opCreateSessionSummary, metricCreateSessionSummaryCount, "Number of create_session_summary operations"},
	{opGetSessionSummaryText, metricGetSessionSummaryCount, "Number of get_session_summary_text operations"},
	{opAppendTrackEvent, metricAppendTrackEventCount, "Number of append_track_event operations"},
}

// initOperationCounters lazily initializes per-operation counters.
func initOperationCounters() map[string]metric.Int64Counter {
	operationCountersOnce.Do(func() {
		mp := ametric.GetMeterProvider()
		if mp == nil {
			return
		}
		meter := mp.Meter(meterName)
		operationCounters = make(map[string]metric.Int64Counter, len(operationMetrics))
		for _, om := range operationMetrics {
			c, err := meter.Int64Counter(
				om.metricName,
				metric.WithDescription(om.description),
				metric.WithUnit("1"),
			)
			if err != nil {
				c, _ = meter.Int64Counter(om.metricName)
			}
			operationCounters[om.operation] = c
		}
	})
	return operationCounters
}

// recordStorageRoute increments the per-operation counter with storage as attribute.
// operation: "create_session", "get_session", "append_event",
//
//	"get_session_summary_text", "create_session_summary", "append_track_event"
//
// storageType: util.StorageTypeHashIdx ("hashidx") or util.StorageTypeZset ("zset")
func (s *Service) recordStorageRoute(ctx context.Context, operation, storageType string) {
	if !s.opts.enableTracing {
		return
	}
	counters := initOperationCounters()
	if counters == nil {
		return
	}
	c, ok := counters[operation]
	if !ok || c == nil {
		return
	}
	c.Add(ctx, 1,
		metric.WithAttributes(
			attribute.String("storage", storageType),
		),
	)
}
