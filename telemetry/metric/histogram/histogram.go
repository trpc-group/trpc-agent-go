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
	mu          sync.RWMutex
	histogram   metric.Float64Histogram
	meterName   string
	mp          metric.MeterProvider
	name        string
	description string
	unit        string
	boundaries  []float64
}

// NewDynamicFloat64Histogram creates a new dynamic histogram with the given options.
// The meter provider, meter name, histogram name, description, and unit are stored for later use when recreating
// the histogram with new bucket boundaries.
func NewDynamicFloat64Histogram(
	mp metric.MeterProvider,
	meterName string,
	meter metric.Meter,
	name string,
	description string,
	unit string,
	boundaries []float64,
) (*DynamicFloat64Histogram, error) {
	if mp == nil {
		return nil, fmt.Errorf("meter provider is nil")
	}

	if meter == nil {
		meter = mp.Meter(meterName, metric.WithInstrumentationAttributes(attribute.String(metrics.KeyMeterName, meterName)))
	}
	opts := []metric.Float64HistogramOption{
		metric.WithDescription(description),
		metric.WithUnit(unit),
	}
	if len(boundaries) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(boundaries...))
	}

	h, err := meter.Float64Histogram(name, opts...)
	if err != nil {
		return nil, err
	}
	return &DynamicFloat64Histogram{
		histogram:   h,
		mp:          mp,
		meterName:   meterName,
		name:        name,
		description: description,
		unit:        unit,
		boundaries:  boundaries,
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
	meter := d.mp.Meter(d.meterName, metric.WithInstrumentationAttributes(attribute.String(metrics.KeyMeterName, d.meterName)))
	opts := []metric.Float64HistogramOption{
		metric.WithDescription(d.description),
		metric.WithUnit(d.unit),
	}
	if len(boundaries) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(boundaries...))
	}

	h, err := meter.Float64Histogram(d.name, opts...)
	if err != nil {
		return err
	}
	d.histogram = h
	d.boundaries = boundaries
	return nil
}

// GetBuckets returns the current bucket boundaries.
// This method is thread-safe.
func (d *DynamicFloat64Histogram) GetBuckets() []float64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]float64, len(d.boundaries))
	copy(result, d.boundaries)
	return result
}

// DynamicInt64Histogram wraps an Int64Histogram with dynamic bucket configuration.
// It allows updating histogram bucket boundaries at runtime by recreating the underlying
// histogram instrument.
type DynamicInt64Histogram struct {
	mu          sync.RWMutex
	histogram   metric.Int64Histogram
	meterName   string
	mp          metric.MeterProvider
	name        string
	description string
	unit        string
	boundaries  []float64
}

// NewDynamicInt64Histogram creates a new dynamic histogram with the given options.
// The meter provider, meter name, histogram name, description, and unit are stored for later use when recreating
// the histogram with new bucket boundaries.
func NewDynamicInt64Histogram(
	mp metric.MeterProvider,
	meterName string,
	meter metric.Meter,
	name string,
	description string,
	unit string,
	boundaries []float64,
) (*DynamicInt64Histogram, error) {
	if mp == nil {
		return nil, fmt.Errorf("meter provider is nil")
	}

	if meter == nil {
		meter = mp.Meter(meterName, metric.WithInstrumentationAttributes(attribute.String(metrics.KeyMeterName, meterName)))
	}
	opts := []metric.Int64HistogramOption{
		metric.WithDescription(description),
		metric.WithUnit(unit),
	}
	if len(boundaries) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(boundaries...))
	}

	h, err := meter.Int64Histogram(name, opts...)
	if err != nil {
		return nil, err
	}
	return &DynamicInt64Histogram{
		histogram:   h,
		mp:          mp,
		meterName:   meterName,
		name:        name,
		description: description,
		unit:        unit,
		boundaries:  boundaries,
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
	meter := d.mp.Meter(d.meterName, metric.WithInstrumentationAttributes(attribute.String(metrics.KeyMeterName, d.meterName)))
	opts := []metric.Int64HistogramOption{
		metric.WithDescription(d.description),
		metric.WithUnit(d.unit),
	}
	if len(boundaries) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(boundaries...))
	}

	h, err := meter.Int64Histogram(d.name, opts...)
	if err != nil {
		return err
	}
	d.histogram = h
	d.boundaries = boundaries
	return nil
}

// GetBuckets returns the current bucket boundaries.
// This method is thread-safe.
func (d *DynamicInt64Histogram) GetBuckets() []float64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]float64, len(d.boundaries))
	copy(result, d.boundaries)
	return result
}
