//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package histogram provides dynamic histogram types for OpenTelemetry metrics.
package histogram

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/metrics"
)

// DynamicFloat64Histogram wraps a Float64Histogram with dynamic bucket configuration.
// It allows updating histogram bucket boundaries at runtime by recreating the underlying
// histogram instrument.
type DynamicFloat64Histogram struct {
	mu         sync.RWMutex
	histogram  metric.Float64Histogram
	meterName  string
	mp         metric.MeterProvider
	metricName string
	options    []metric.Float64HistogramOption
}

// NewDynamicFloat64Histogram creates a new dynamic histogram with the given options.
// The meter provider, meter name, histogram name, and options are stored for later use when recreating
// the histogram with new bucket boundaries.
func NewDynamicFloat64Histogram(
	mp metric.MeterProvider,
	meterName string,
	metricName string,
	options ...metric.Float64HistogramOption,
) (*DynamicFloat64Histogram, error) {
	if mp == nil {
		return nil, fmt.Errorf("meter provider is nil")
	}

	meter := mp.Meter(meterName, metric.WithInstrumentationAttributes(attribute.String(metrics.KeyMetricName, metricName)))

	h, err := meter.Float64Histogram(metricName, options...)
	if err != nil {
		return nil, err
	}
	return &DynamicFloat64Histogram{
		histogram:  h,
		mp:         mp,
		meterName:  meterName,
		metricName: metricName,
		options:    options,
	}, nil
}

// Record records a value with the current histogram.
// This method is thread-safe.
func (d *DynamicFloat64Histogram) Record(ctx context.Context, value float64, opts ...metric.RecordOption) {
	d.mu.RLock()
	h := d.histogram
	d.mu.RUnlock()
	h.Record(ctx, value, opts...)
}

// SetBuckets updates bucket boundaries by recreating the histogram.
// Note: This creates a new histogram instrument; old data is not migrated.
// This method is thread-safe.
func (d *DynamicFloat64Histogram) SetBuckets(boundaries []float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.mp == nil {
		return fmt.Errorf("meter provider is nil")
	}
	// Create a new Meter each time buckets change (required for some SDK/provider implementations).
	meter := d.mp.Meter(d.meterName, metric.WithInstrumentationAttributes(attribute.String(metrics.KeyMetricName, d.metricName)))

	opts := make([]metric.Float64HistogramOption, 0, len(d.options)+1)
	opts = append(opts, d.options...)
	if len(boundaries) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(boundaries...))
	}

	h, err := meter.Float64Histogram(d.metricName, opts...)
	if err != nil {
		return err
	}
	d.histogram = h
	return nil
}

// DynamicInt64Histogram wraps an Int64Histogram with dynamic bucket configuration.
// It allows updating histogram bucket boundaries at runtime by recreating the underlying
// histogram instrument.
type DynamicInt64Histogram struct {
	mu         sync.RWMutex
	histogram  metric.Int64Histogram
	meterName  string
	mp         metric.MeterProvider
	metricName string
	options    []metric.Int64HistogramOption
}

// NewDynamicInt64Histogram creates a new dynamic histogram with the given options.
// The meter provider, meter name, histogram name, and options are stored for later use when recreating
// the histogram with new bucket boundaries.
func NewDynamicInt64Histogram(
	mp metric.MeterProvider,
	meterName string,
	metricName string,
	options ...metric.Int64HistogramOption,
) (*DynamicInt64Histogram, error) {
	if mp == nil {
		return nil, fmt.Errorf("meter provider is nil")
	}

	meter := mp.Meter(meterName, metric.WithInstrumentationAttributes(attribute.String(metrics.KeyMetricName, metricName)))

	h, err := meter.Int64Histogram(metricName, options...)
	if err != nil {
		return nil, err
	}
	return &DynamicInt64Histogram{
		histogram:  h,
		mp:         mp,
		meterName:  meterName,
		metricName: metricName,
		options:    options,
	}, nil
}

// Record records a value with the current histogram.
// This method is thread-safe.
func (d *DynamicInt64Histogram) Record(ctx context.Context, value int64, opts ...metric.RecordOption) {
	d.mu.RLock()
	h := d.histogram
	d.mu.RUnlock()
	h.Record(ctx, value, opts...)
}

// SetBuckets updates bucket boundaries by recreating the histogram.
// Note: This creates a new histogram instrument; old data is not migrated.
// This method is thread-safe.
func (d *DynamicInt64Histogram) SetBuckets(boundaries []float64) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.mp == nil {
		return fmt.Errorf("meter provider is nil")
	}
	// Create a new Meter each time buckets change (required for some SDK/provider implementations).
	meter := d.mp.Meter(d.meterName, metric.WithInstrumentationAttributes(attribute.String(metrics.KeyMetricName, d.metricName)))

	opts := make([]metric.Int64HistogramOption, 0, len(d.options)+1)
	opts = append(opts, d.options...)
	if len(boundaries) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(boundaries...))
	}

	h, err := meter.Int64Histogram(d.metricName, opts...)
	if err != nil {
		return err
	}
	d.histogram = h
	return nil
}
