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
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

var (
	// MeterProvider is the global OpenTelemetry meter provider used for creating meters.
	// It defaults to a no-op implementation and should be initialized with a real provider.
	MeterProvider metric.MeterProvider = noop.NewMeterProvider()
)
