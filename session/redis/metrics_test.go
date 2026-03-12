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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	ametric "trpc.group/trpc-go/trpc-agent-go/telemetry/metric"
)

// resetOperationCounters resets the package-level sync.Once and counters
// so each test gets a fresh meter from the current global MeterProvider.
func resetOperationCounters() {
	operationCountersOnce = sync.Once{}
	operationCounters = nil
}

// setupMetricProvider installs an SDK MeterProvider with a ManualReader
// and returns the reader (for collecting metric data) and a cleanup function.
func setupMetricProvider(t *testing.T) (*sdkmetric.ManualReader, func()) {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	require.NoError(t, ametric.InitMeterProvider(provider))
	resetOperationCounters()

	return reader, func() {
		_ = provider.Shutdown(context.Background())
		resetOperationCounters()
	}
}

// findCounterValue returns the data-point value for the given metric name
// and storage attribute, or 0 if not found.
func findCounterValue(
	t *testing.T,
	reader *sdkmetric.ManualReader,
	metricName, storageType string,
) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				for _, attr := range dp.Attributes.ToSlice() {
					if string(attr.Key) == "storage" && attr.Value.AsString() == storageType {
						return dp.Value
					}
				}
			}
		}
	}
	return 0
}

// collectTotalForMetric collects all data points for a given metric name.
func collectTotalForMetric(t *testing.T, reader *sdkmetric.ManualReader, metricName string) int64 {
	t.Helper()
	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	var total int64
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != metricName {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				total += dp.Value
			}
		}
	}
	return total
}

func TestRecordStorageRoute_TracingDisabled(t *testing.T) {
	reader, cleanup := setupMetricProvider(t)
	defer cleanup()

	redisURL, redisCleanup := setupTestRedis(t)
	defer redisCleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(false))
	require.NoError(t, err)
	defer svc.Close()

	svc.recordStorageRoute(context.Background(), opCreateSession, "hashidx")
	svc.recordStorageRoute(context.Background(), opGetSession, "zset")

	assert.Equal(t, int64(0), collectTotalForMetric(t, reader, metricCreateSessionCount),
		"no metrics should be recorded when tracing is disabled")
	assert.Equal(t, int64(0), collectTotalForMetric(t, reader, metricGetSessionCount),
		"no metrics should be recorded when tracing is disabled")
}

func TestRecordStorageRoute_TracingEnabled(t *testing.T) {
	reader, cleanup := setupMetricProvider(t)
	defer cleanup()

	redisURL, redisCleanup := setupTestRedis(t)
	defer redisCleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	svc.recordStorageRoute(ctx, opCreateSession, "hashidx")
	svc.recordStorageRoute(ctx, opCreateSession, "hashidx")
	svc.recordStorageRoute(ctx, opGetSession, "zset")
	svc.recordStorageRoute(ctx, opAppendEvent, "hashidx")

	assert.Equal(t, int64(2), findCounterValue(t, reader, metricCreateSessionCount, "hashidx"))
	assert.Equal(t, int64(0), findCounterValue(t, reader, metricCreateSessionCount, "zset"))
	assert.Equal(t, int64(1), findCounterValue(t, reader, metricGetSessionCount, "zset"))
	assert.Equal(t, int64(0), findCounterValue(t, reader, metricGetSessionCount, "hashidx"))
	assert.Equal(t, int64(1), findCounterValue(t, reader, metricAppendEventCount, "hashidx"))
	assert.Equal(t, int64(0), findCounterValue(t, reader, metricAppendEventCount, "zset"))
}

func TestRecordStorageRoute_AllOperations(t *testing.T) {
	reader, cleanup := setupMetricProvider(t)
	defer cleanup()

	redisURL, redisCleanup := setupTestRedis(t)
	defer redisCleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	ctx := context.Background()
	ops := []struct {
		operation   string
		metricName  string
		storageType string
	}{
		{opCreateSession, metricCreateSessionCount, "hashidx"},
		{opCreateSession, metricCreateSessionCount, "zset"},
		{opGetSession, metricGetSessionCount, "hashidx"},
		{opGetSession, metricGetSessionCount, "zset"},
		{opAppendEvent, metricAppendEventCount, "hashidx"},
		{opAppendEvent, metricAppendEventCount, "zset"},
		{opCreateSessionSummary, metricCreateSessionSummaryCount, "hashidx"},
		{opCreateSessionSummary, metricCreateSessionSummaryCount, "zset"},
		{opGetSessionSummaryText, metricGetSessionSummaryCount, "hashidx"},
		{opGetSessionSummaryText, metricGetSessionSummaryCount, "zset"},
		{opAppendTrackEvent, metricAppendTrackEventCount, "hashidx"},
		{opAppendTrackEvent, metricAppendTrackEventCount, "zset"},
	}

	for _, op := range ops {
		svc.recordStorageRoute(ctx, op.operation, op.storageType)
	}

	for _, op := range ops {
		val := findCounterValue(t, reader, op.metricName, op.storageType)
		assert.Equal(t, int64(1), val, "expected 1 for %s/%s", op.operation, op.storageType)
	}
}

func TestInitOperationCounters_Idempotent(t *testing.T) {
	_, cleanup := setupMetricProvider(t)
	defer cleanup()

	c1 := initOperationCounters()
	c2 := initOperationCounters()
	assert.NotNil(t, c1)
	assert.Equal(t, c1, c2, "should return the same counters map")
}

func TestRecordStorageRoute_UnknownOperation(t *testing.T) {
	_, cleanup := setupMetricProvider(t)
	defer cleanup()

	redisURL, redisCleanup := setupTestRedis(t)
	defer redisCleanup()

	svc, err := NewService(WithRedisClientURL(redisURL), WithEnableTracing(true))
	require.NoError(t, err)
	defer svc.Close()

	// Should not panic for unknown operation
	assert.NotPanics(t, func() {
		svc.recordStorageRoute(context.Background(), "unknown_op", "hashidx")
	})
}
