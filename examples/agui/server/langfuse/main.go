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
	"flag"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/telemetry/langfuse"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	agentName = "agui-agent"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model to use")
	isStream  = flag.Bool("stream", true, "Whether to stream the response")
	address   = flag.String("address", "127.0.0.1:8080", "Listen address")
	path      = flag.String("path", "/agui", "HTTP path")
)

func main() {
	// Start trace with Langfuse integration using environment variables.
	clean, err := langfuse.Start(context.Background())
	if err != nil {
		log.Fatalf("Failed to start trace telemetry: %v", err)
	}
	defer func() {
		if err := clean(context.Background()); err != nil {
			log.Fatalf("Failed to clean up trace telemetry: %v", err)
		}
	}()
	// Parse command line arguments.
	flag.Parse()
	// Build agent and runner.
	agent := newAgent()
	runner := runner.NewRunner(agent.Info().Name, agent)
	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer runner.Close()
	// Build AG-UI server.
	callbacks := translator.NewCallbacks().RegisterAfterTranslate(langfuseCallback())
	server, err := agui.New(runner,
		agui.WithPath(*path),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithUserIDResolver(userIDResolver),
			aguirunner.WithTranslateCallbacks(callbacks),
			aguirunner.WithStartSpan(startSpan),
		),
	)
	if err != nil {
		log.Fatalf("failed to create AG-UI server: %v", err)
	}
	// Start AG-UI server.
	log.Infof("AG-UI: serving agent %q on http://%s%s", agent.Info().Name, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

// startSpan starts a span for the agent.
func startSpan(ctx context.Context, input *adapter.RunAgentInput) (context.Context, trace.Span, error) {
	userID, err := userIDResolver(ctx, input)
	if err != nil {
		return nil, nil, fmt.Errorf("userIDResolver: %w", err)
	}
	commonAttrs := []attribute.KeyValue{
		attribute.String("agentName", agentName),
		attribute.String("modelName", *modelName),
		attribute.String("langfuse.environment", "development"),
		attribute.String("langfuse.session.id", input.ThreadID),
		attribute.String("langfuse.user.id", userID),
		attribute.String("langfuse.trace.input", input.Messages[len(input.Messages)-1].Content),
	}
	ctx, span := atrace.Tracer.Start(ctx, agentName, trace.WithAttributes(commonAttrs...))
	return ctx, span, nil
}

// langfuseCallback is a callback that sends the output to Langfuse.
func langfuseCallback() translator.AfterTranslateCallback {
	// Store the output for each trace ID.
	langfuseOutputs := sync.Map{}
	// Get the output for a given trace ID, default to empty string.
	getOutputBuilder := func(traceID string) *strings.Builder {
		data, ok := langfuseOutputs.Load(traceID)
		if !ok {
			return &strings.Builder{}
		}
		output, ok := data.(*strings.Builder)
		if !ok {
			return &strings.Builder{}
		}
		return output
	}
	// Return the callback that sends the output to Langfuse.
	return func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
		span := trace.SpanFromContext(ctx)
		traceID := span.SpanContext().TraceID().String()
		switch e := event.(type) {
		// Reset the output.
		case *aguievents.RunStartedEvent:
			langfuseOutputs.Store(traceID, &strings.Builder{})
		// Report the output.
		case *aguievents.RunFinishedEvent, *aguievents.RunErrorEvent:
			outputBuilder := getOutputBuilder(traceID)
			span.SetAttributes(attribute.String("langfuse.trace.output", outputBuilder.String()))
			langfuseOutputs.Delete(traceID)
		// Aggregate the output.
		case *aguievents.TextMessageContentEvent:
			outputBuilder := getOutputBuilder(traceID)
			outputBuilder.WriteString(e.Delta)
			langfuseOutputs.Store(traceID, outputBuilder)
		}
		return nil, nil
	}
}

// userIDResolver resolves the user ID from the AG-UI input.
func userIDResolver(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
	return "user", nil
}

// newAgent creates a new agent.
func newAgent() agent.Agent {
	modelInstance := openai.New(*modelName)
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.7),
		Stream:      *isStream,
	}
	calculatorTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription("A calculator tool, you can use it to calculate the result of the operation. "+
			"a is the first number, b is the second number, "+
			"the operation can be add, subtract, multiply, divide, power."),
	)
	return llmagent.New(
		agentName,
		llmagent.WithTools([]tool.Tool{calculatorTool}),
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction("You are a helpful assistant."),
	)
}

func calculator(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch args.Operation {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		result = args.A / args.B
	case "power", "^":
		result = math.Pow(args.A, args.B)
	default:
		return calculatorResult{Result: 0}, fmt.Errorf("invalid operation: %s", args.Operation)
	}
	return calculatorResult{Result: result}, nil
}

type calculatorArgs struct {
	Operation string  `json:"operation" description:"add, subtract, multiply, divide, power"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type calculatorResult struct {
	Result float64 `json:"result"`
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
