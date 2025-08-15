//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates telemetry (tracing and metrics) usage with OpenTelemetry.
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/telemetry/agent"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	// https://langfuse.com/integrations/native/opentelemetry
	langFuseSecretKey := "sk-lf-c3d93ca5-dbc1-41cd-8b9c-4b29781f620e"
	langFusePublicKey := "pk-lf-a3e8b2fe-074a-4afa-a215-b9b1e11e69c5"
	langFuseHost := "http://localhost:3000"
	otelEndpointPath := "/api/public/otel/v1/traces"

	// Start trace
	clean, err := atrace.Start(
		context.Background(),
		atrace.WithEndpointURL(langFuseHost+otelEndpointPath),
		atrace.WithProtocol("http"),
		atrace.WithHeaders(map[string]string{
			"Authorization": fmt.Sprintf("Basic %s", encodeAuth(langFusePublicKey, langFuseSecretKey)),
		}),
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
			ctx, span := atrace.Tracer.Start(ctx, "process-message")
			span.SetAttributes(attribute.String("user-message", msg))
			defer span.End()
			err := a.ProcessMessage(ctx, msg)
			if err != nil {
				span.SetAttributes(attribute.String("error", err.Error()))
				log.Fatalf("Chat system failed to run: %v", err)
			}
			span.SetAttributes(attribute.String("error", "<nil>"))
		}()
	}
}

func encodeAuth(pk, sk string) string {
	auth := pk + ":" + sk
	return base64.StdEncoding.EncodeToString([]byte(auth))
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
