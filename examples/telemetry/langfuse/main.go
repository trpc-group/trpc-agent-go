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

	"trpc.group/trpc-go/trpc-agent-go/examples/telemetry/agent"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	// Start trace with Langfuse integration using environment variables
	clean, err := langfuse.Start(context.Background())
	if err != nil {
		log.Fatalf("Failed to start trace telemetry: %v", err)
	}
	defer func() {
		if err := clean(context.Background()); err != nil {
			log.Printf("Failed to clean up trace telemetry: %v", err)
		}
	}()

	const agentName = "multi-tool-assistant"
	// Parse command line arguments
	modelName := flag.String("model", "deepseek-chat", "Model name to use")
	flag.Parse()
	printGuideMessage(*modelName)
	a := agent.NewMultiToolChatAgent("multi-tool-assistant", *modelName)
	userMessage := []string{
		"Calculate 123 + 456 * 789",
		"What day of the week is today?",
		"'Hello World' to uppercase",
		"Create a test file in the current directory",
		"Find information about Tesla company",
	}

	for _, msg := range userMessage {
		func() {
			// Attributes represent additional key-value descriptors that can be bound to a metric observer or recorder.
			commonAttrs := []attribute.KeyValue{
				attribute.String("agentName", agentName),
				attribute.String("modelName", *modelName),
				attribute.String("langfuse.environment", "development"),
				attribute.String("langfuse.trace.input", msg),
			}

			// Put trace-level Langfuse attributes(userId, sessionId, metadata, version, release, tags) into baggage so they can be propagated to all spans.
			// https://langfuse.com/integrations/native/opentelemetry#propagating-attributes
			mSession, err := baggage.NewMemberRaw("langfuse.session.id", "session-1")
			if err != nil {
				log.Fatalf("Failed to create baggage member: %v", err)
			}
			mUser, err := baggage.NewMemberRaw("langfuse.user.id", "user-1")
			if err != nil {
				log.Fatalf("Failed to create baggage member: %v", err)
			}
			mURL, err := baggage.NewMemberRaw("langfuse.trace.metadata.url", "https://www.google.com")
			if err != nil {
				log.Fatalf("Failed to create baggage member: %v", err)
			}

			bag, err := baggage.New(mSession, mUser, mURL)
			if err != nil {
				log.Fatalf("Failed to create baggage: %v", err)
			}
			ctx := baggage.ContextWithBaggage(context.Background(), bag)

			ctx, span := atrace.Tracer.Start(
				ctx,
				agentName,
				trace.WithAttributes(commonAttrs...),
			)

			defer span.End()

			result, err := a.ProcessMessage(ctx, msg)
			if result != "" {
				span.SetAttributes(attribute.String("langfuse.trace.output", result))
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
