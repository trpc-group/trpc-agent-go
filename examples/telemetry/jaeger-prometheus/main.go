//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates telemetry (tracing and metrics) usage with OpenTelemetry.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/telemetry/agent"
	ametric "trpc.group/trpc-go/trpc-agent-go/telemetry/metric"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	// Initialize meter provider
	mp, err := ametric.NewMeterProvider(
		context.Background(),
		ametric.WithEndpoint("localhost:4318"),
		ametric.WithProtocol("http"),
	)
	if err != nil {
		log.Fatalf("Failed to create meter provider: %v", err)
	}
	defer mp.Shutdown(context.Background())
	ametric.InitMeterProvider(mp)
	meter := mp.Meter("trpc_agent_go.app")

	// Strat trace
	clean, err := atrace.Start(
		context.Background(),
		atrace.WithEndpoint("localhost:4318"),
		atrace.WithProtocol("http"),
	)
	if err != nil {
		log.Fatalf("Failed to start trace telemetry: %v", err)
	}
	defer func() {
		if err := clean(); err != nil {
			log.Printf("Failed to clean up metric telemetry: %v", err)
		}
	}()

	const agentName = "multi-tool-assistant"
	// Parse command line arguments
	modelName := flag.String("model", "deepseek-chat", "Model name to use")
	flag.Parse()
	printGuideMessage(*modelName)
	a := agent.NewMultiToolChatAgent("multi-tool-assistant", *modelName)
	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer a.Close()
	userMessage := []string{
		"Calculate 123 + 456 * 789",
		"What day of the week is today?",
		"'Hello World' to uppercase",
		"Create a test file in the current directory",
		"Find information about Tesla company",
	}
	// Attributes represent additional key-value descriptors that can be bound to a metric observer or recorder.
	commonAttrs := []attribute.KeyValue{
		attribute.String("agentName", agentName),
		attribute.String("modelName", *modelName),
	}

	userMessageCount, err := meter.Int64Counter("run",
		metric.WithDescription("the number of user message that the agent processed"))

	ctx, span := atrace.Tracer.Start(
		context.Background(),
		agentName,
		trace.WithAttributes(commonAttrs...),
	)
	defer span.End()

	for _, msg := range userMessage {
		func() {
			ctx, cancel := context.WithTimeout(ctx, time.Minute)
			defer cancel()
			userMessageCount.Add(ctx, 1, metric.WithAttributes(commonAttrs...))
			ctx, span := atrace.Tracer.Start(ctx, "process-message")
			span.SetAttributes(attribute.String("user-message", msg))
			defer span.End()
			result, err := a.ProcessMessage(ctx, msg)
			if result != "" {
				span.SetAttributes(attribute.String("output", result))
			}
			if err != nil {
				span.SetAttributes(attribute.String("error", err.Error()))
				log.Fatalf("Chat system failed to run: %v", err)
			}
			span.SetAttributes(attribute.String("error", "<nil>"))
		}()
	}
}

func printGuideMessage(modelName string) {
	fmt.Printf("🚀 Multi-Tool Intelligent Assistant Demo\n")
	fmt.Printf("Model: %s\n", modelName)
	fmt.Printf("Available tools: calculator, time_tool, text_tool, file_tool, duckduckgo_search\n")
	// Print welcome message and examples
	fmt.Println("💡 Try asking these questions:")
	fmt.Println("   [Calculator] Calculate 123 + 456 * 789")
	fmt.Println("   [Calculator] Calculate the square root of pi")
	fmt.Println("   [Time] What time is it now?")
	fmt.Println("   [Time] What day of the week is today?")
	fmt.Println("   [Text] Convert 'Hello World' to uppercase")
	fmt.Println("   [Text] Count characters in 'Hello World'")
	fmt.Println("   [File] Read the README.md file")
	fmt.Println("   [File] Create a test file in the current directory")
	fmt.Println("   [Search] Search for information about Steve Jobs")
	fmt.Println("   [Search] Find information about Tesla company")
	fmt.Println()
	fmt.Println(strings.Repeat("=", 60))
}
