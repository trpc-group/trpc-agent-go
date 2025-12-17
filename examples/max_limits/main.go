//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how to use per-agent limits for LLM calls and
// tool iterations. It intentionally sets very small limits so you can observe
// how the framework terminates the flow when the limits are exceeded for a
// single LLMAgent invocation.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

// maxLimitsDemo shows how MaxLLMCalls and MaxToolIterations affect a single run.
// You should see the agent start a tool call and then terminate with an error
// once the tool iteration limit is exceeded.
func main() {
	ctx := context.Background()

	// Configure a simple OpenAI model. Adjust the model name / variant as needed.
	modelInstance := openai.New(
		"deepseek-chat",
		openai.WithVariant(openai.VariantOpenAI),
	)

	sessionService := sessioninmemory.NewSessionService()

	// Simple calculator tool so the model has something to call repeatedly.
	calculatorTool := function.NewFunctionTool(
		func(_ context.Context, args struct {
			Operation string  `json:"operation" description:"The operation to perform: add or multiply"`
			A         float64 `json:"a" description:"First operand"`
			B         float64 `json:"b" description:"Second operand"`
		}) (map[string]any, error) {
			var result float64
			switch strings.ToLower(args.Operation) {
			case "add", "+":
				result = args.A + args.B
			case "multiply", "*":
				result = args.A * args.B
			default:
				// Fallback to multiply so the model still gets a result.
				result = args.A * args.B
			}
			return map[string]any{"result": result}, nil
		},
		function.WithName("calculator"),
		function.WithDescription("Perform basic arithmetic (addition and multiplication)."),
	)

	genConfig := model.GenerationConfig{
		Stream: true,
	}

	llmAgent := llmagent.New(
		"limits-demo-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Agent used to demonstrate per-agent MaxLLMCalls and MaxToolIterations."),
		llmagent.WithInstruction(
			"You are a math assistant that MUST NOT do any addition or multiplication in your head. "+
				"All arithmetic must go through the `calculator` tool, and you must not mentally derive any intermediate or final results.\n\n"+
				"When asked to compute a^n, use a step-by-step multiplication process and complete all steps within this single conversation turn sequence:\n"+
				"1) Maintain a `current` variable starting from 1.\n"+
				"2) In each assistant turn, you may produce at most ONE `calculator` tool call that multiplies `current` by the base to obtain the new `current`.\n"+
				"3) After receiving the tool result, briefly explain in the same reply what you just computed and what the new `current` is, then immediately continue to the next turn and call `calculator` again, without waiting for new user input.\n"+
				"4) Repeat this \"single tool_call → receive result → brief explanation\" loop until all multiplications are completed.\n"+
				"5) Only after all multiplications are finished may you return a final reply summarizing the whole process and the final value of a^n.",
		),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
		// Configure small per-invocation limits so we can observe how the
		// agent stops when the budgets are exhausted.
		llmagent.WithMaxLLMCalls(5),
		llmagent.WithMaxToolIterations(2),
	)

	appName := "limits-demo-app"
	r := runner.NewRunner(
		appName,
		llmAgent,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	userID := "limits-user"
	sessionID := fmt.Sprintf("limits-session-%d", time.Now().Unix())

	// Craft a prompt that encourages multiple sequential tool calls (one
	// calculator call per assistant turn) to compute an exponent, without
	// waiting for additional user input.
	message := model.NewUserMessage(
		"Let's do a step-by-step exponentiation exercise: please automatically complete the computation of 2^8 in this conversation, " +
			"without waiting for any new user messages.\n\n" +
			"Requirements:\n" +
			"1) Start from current=1 and ONLY use the `calculator` tool for multiplications; do not do any arithmetic in your head.\n" +
			"2) In each assistant turn, call `calculator` at most once to multiply `current` by 2 and obtain the new `current`.\n" +
			"3) After receiving the tool result, briefly explain in the same reply what you did in this step and what the new `current` is, then immediately start the next turn and call `calculator` again.\n" +
			"4) You must keep calling the tool yourself until all 8 multiplications are finished; do not skip steps or jump directly to the final answer.\n" +
			"5) After all multiplications are done, send one final reply summarizing the entire process and clearly stating the value of 2^8.",
	)

	fmt.Println("Running demo with per-agent MaxLLMCalls=5 and MaxToolIterations=2 ...")
	fmt.Println()
	fmt.Println("This demo automatically sends a fixed user message asking the agent to compute 2^8.")
	fmt.Println("If you want to try other inputs, edit `main.go` and change the `message` to something like:")
	fmt.Println("  - Compute 3^10, still requiring every step to go through the `calculator` tool.")
	fmt.Println("  - Call the `calculator` tool multiple times in one conversation, but only give a summary at the end.")
	fmt.Println()

	requestID := uuid.New().String()
	eventChan, err := r.Run(
		ctx,
		userID,
		sessionID,
		message,
		agent.WithRequestID(requestID),
	)
	if err != nil {
		log.Fatalf("Run failed: %v", err)
	}

	for evt := range eventChan {
		printEvent(evt)
		if evt.IsFinalResponse() {
			break
		}
	}
}

func printEvent(evt *event.Event) {
	if evt == nil {
		return
	}

	if evt.Error != nil {
		fmt.Printf("❌ Error event: type=%s message=%s\n",
			evt.Error.Type, evt.Error.Message)
		return
	}

	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return
	}

	rsp := evt.Response
	ch := rsp.Choices[0]

	// Show tool calls if present.
	if len(ch.Message.ToolCalls) > 0 {
		fmt.Println("🔧 Tool calls:")
		for _, tc := range ch.Message.ToolCalls {
			fmt.Printf("   • %s (ID: %s) args=%s\n",
				tc.Function.Name, tc.ID, string(tc.Function.Arguments))
		}
		return
	}

	// Show tool responses.
	if ch.Message.Role == model.RoleTool && ch.Message.ToolID != "" {
		fmt.Printf("🛠️  Tool response (ID: %s): %s\n",
			ch.Message.ToolID, ch.Message.Content)
		return
	}

	// Show assistant content.
	if ch.Delta.Content != "" {
		fmt.Printf("🤖 Delta: %s\n", ch.Delta.Content)
	} else if ch.Message.Content != "" {
		fmt.Printf("🤖 Message: %s\n", ch.Message.Content)
	}
}
