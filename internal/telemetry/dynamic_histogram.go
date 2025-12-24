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
	"sync"

	"go.opentelemetry.io/otel/metric"
)

// DynamicFloat64Histogram wraps a Float64Histogram with dynamic bucket configuration.
// It allows updating histogram bucket boundaries at runtime by recreating the underlying
// histogram instrument.
type DynamicFloat64Histogram struct {
	mu          sync.RWMutex
	histogram   metric.Float64Histogram
	meter       metric.Meter
	name        string
	description string
	unit        string
	boundaries  []float64
}

// NewDynamicFloat64Histogram creates a new dynamic histogram with the given options.
// The meter, name, description, and unit are stored for later use when recreating
// the histogram with new bucket boundaries.
func NewDynamicFloat64Histogram(
	meter metric.Meter,
	name string,
	description string,
	unit string,
	boundaries []float64,
) (*DynamicFloat64Histogram, error) {
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
		meter:       meter,
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

	opts := []metric.Float64HistogramOption{
		metric.WithDescription(d.description),
		metric.WithUnit(d.unit),
	}
	if len(boundaries) > 0 {
		opts = append(opts, metric.WithExplicitBucketBoundaries(boundaries...))
	}

	h, err := d.meter.Float64Histogram(d.name, opts...)
	if err != nil {
		return err
	}
	d.histogram = h
	d.boundaries = boundaries
	return nil
}
