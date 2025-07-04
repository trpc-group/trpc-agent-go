package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/telemetry"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	clean, err := telemetry.Start(
		context.Background(),
		telemetry.WithTracesEndpoint("localhost:4317"),
		telemetry.WithMetricsEndpoint("localhost:4317"),
	)
	if err != nil {
		log.Fatalf("Failed to start telemetry: %v", err)
	}
	defer func() {
		if err := clean(); err != nil {
			log.Printf("Failed to clean up telemetry: %v", err)
		}
	}()

	// Attributes represent additional key-value descriptors that can be bound
	// to a metric observer or recorder.
	commonAttrs := []attribute.KeyValue{
		attribute.String("attrA", "chocolate"),
		attribute.String("attrB", "raspberry"),
		attribute.String("attrC", "vanilla"),
	}

	runCount, err := telemetry.Meter.Int64Counter("run", metric.WithDescription("The number of times the iteration ran"))
	if err != nil {
		log.Fatal(err)
	}

	// Work begins
	ctx, span := telemetry.Tracer.Start(
		context.Background(),
		"CollectorExporter-Example",
		trace.WithAttributes(commonAttrs...))
	defer span.End()
	for i := 0; i < 10; i++ {
		_, iSpan := telemetry.Tracer.Start(ctx, fmt.Sprintf("Sample-%d", i))
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
