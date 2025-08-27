//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
	"log"
	"time"

	ametric "trpc.group/trpc-go/trpc-agent-go/telemetry/metric"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

	"go.opentelemetry.io/otel/metric"
)

// initTelemetry initializes OpenTelemetry trace and metric exporters.
func initTelemetry() error {
	// Start trace telemetry.
	cleanTrace, err := atrace.Start(
		context.Background(),
		atrace.WithEndpoint("localhost:4317"),
	)
	if err != nil {
		return fmt.Errorf("failed to start trace telemetry: %w", err)
	}

	// Start metric telemetry.
	cleanMetric, err := ametric.Start(
		context.Background(),
		ametric.WithEndpoint("localhost:4317"),
	)
	if err != nil {
		return fmt.Errorf("failed to start metric telemetry: %w", err)
	}

	// Register cleanup functions.
	// Note: In a real application, you would want to handle cleanup more gracefully.
	go func() {
		// Wait for the application to finish, then cleanup.
		time.Sleep(5 * time.Minute)
		if err := cleanTrace(); err != nil {
			log.Printf("Failed to clean up trace telemetry: %v", err)
		}
		if err := cleanMetric(); err != nil {
			log.Printf("Failed to clean up metric telemetry: %v", err)
		}
	}()

	return nil
}

// initMetrics initializes OpenTelemetry metrics.
func (e *toolTimerExample) initMetrics() error {
	var err error

	// Initialize histograms for duration measurements.
	e.agentDurationHistogram, err = ametric.Meter.Float64Histogram(
		"agent_duration_seconds",
		metric.WithDescription("Duration of agent execution in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("failed to create agent duration histogram: %w", err)
	}

	e.toolDurationHistogram, err = ametric.Meter.Float64Histogram(
		"tool_duration_seconds",
		metric.WithDescription("Duration of tool execution in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("failed to create tool duration histogram: %w", err)
	}

	e.modelDurationHistogram, err = ametric.Meter.Float64Histogram(
		"model_duration_seconds",
		metric.WithDescription("Duration of model inference in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		return fmt.Errorf("failed to create model duration histogram: %w", err)
	}

	// Initialize counters for execution counts.
	e.agentCounter, err = ametric.Meter.Int64Counter(
		"agent_executions_total",
		metric.WithDescription("Total number of agent executions"),
	)
	if err != nil {
		return fmt.Errorf("failed to create agent counter: %w", err)
	}

	e.toolCounter, err = ametric.Meter.Int64Counter(
		"tool_executions_total",
		metric.WithDescription("Total number of tool executions"),
	)
	if err != nil {
		return fmt.Errorf("failed to create tool counter: %w", err)
	}

	e.modelCounter, err = ametric.Meter.Int64Counter(
		"model_inferences_total",
		metric.WithDescription("Total number of model inferences"),
	)
	if err != nil {
		return fmt.Errorf("failed to create model counter: %w", err)
	}

	return nil
}
