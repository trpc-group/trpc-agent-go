//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
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

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	cleanTrace, err := atrace.Start(
		context.Background(),
		atrace.WithEndpoint("localhost:4317"),
	)
	if err != nil {
		log.Fatalf("Failed to start trace telemetry: %v", err)
	}

	cleanMetric, err := ametric.Start(
		context.Background(),
		ametric.WithEndpoint("localhost:4317"),
	)
	if err != nil {
		log.Fatalf("Failed to start metric telemetry: %v", err)
	}

	defer func() {
		if err := cleanTrace(); err != nil {
			log.Printf("Failed to clean up trace telemetry: %v", err)
		}
		if err := cleanMetric(); err != nil {
			log.Printf("Failed to clean up metric telemetry: %v", err)
		}
	}()

	// Attributes represent additional key-value descriptors that can be bound
	// to a metric observer or recorder.
	commonAttrs := []attribute.KeyValue{
		attribute.String("attrA", "chocolate"),
		attribute.String("attrB", "raspberry"),
		attribute.String("attrC", "vanilla"),
	}

	runCount, err := ametric.Meter.Int64Counter("run", metric.WithDescription("The number of times the iteration ran"))
	if err != nil {
		log.Fatal(err)
	}

	// Work begins
	ctx, span := atrace.Tracer.Start(
		context.Background(),
		"CollectorExporter-Example",
		trace.WithAttributes(commonAttrs...))
	defer span.End()
	for i := 0; i < 10; i++ {
		_, iSpan := atrace.Tracer.Start(ctx, fmt.Sprintf("Sample-%d", i))
		runCount.Add(ctx, 1, metric.WithAttributes(commonAttrs...))
		log.Printf("Doing really hard work (%d / 10)\n", i+1)
		<-time.After(time.Second)
		iSpan.End()
	}

	// wait for the telemetry to be sent
	log.Println("Waiting 1min for Meter to be sent...")
	time.Sleep(1 * time.Minute)

	log.Printf("Done!")
}
