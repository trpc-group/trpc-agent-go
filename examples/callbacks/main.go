//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package main demonstrates multi-turn chat using the Runner with streaming output, session management,
// tool calling, and shows how to use AgentCallbacks, ModelCallbacks, and ToolCallbacks.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func main() {
	// Parse command line arguments.
	modelName := flag.String("model", "deepseek-chat", "Name of the model to use")
	flag.Parse()

	fmt.Printf("🚀 Multi-turn Chat with Runner + Tools + Callbacks\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Type 'exit' to end the conversation\n")
	fmt.Printf("Available tools: calculator, current_time\n")
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the chat.
	chat := &multiTurnChatWithCallbacks{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// multiTurnChatWithCallbacks manages the chat with callbacks.
type multiTurnChatWithCallbacks struct {
	modelName string
	runner    runner.Runner
	userID    string
	sessionID string
}

func (c *multiTurnChatWithCallbacks) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	return c.startChat(ctx)
}

func (c *multiTurnChatWithCallbacks) setup(_ context.Context) error {
	modelInstance := openai.New(c.modelName, openai.Options{
		ChannelBufferSize: 512,
	})

	// Create tools.
	calculatorTool := function.NewFunctionTool(
		c.calculate,
		function.WithName("calculator"),
		function.WithDescription("Perform basic mathematical calculations (add, subtract, multiply, divide)"),
	)
	timeTool := function.NewFunctionTool(
		c.getCurrentTime,
		function.WithName("current_time"),
		function.WithDescription("Get the current time and date for a specific timezone"),
	)

	// Construct ModelCallbacks example.
	modelCallbacks := model.NewModelCallbacks()
	modelCallbacks.RegisterBeforeModel(func(
		ctx context.Context, req *model.Request,
	) (*model.Response, error) {
		userMsg := ""
		if len(req.Messages) > 0 {
			userMsg = req.Messages[len(req.Messages)-1].Content
		}
		fmt.Printf("\n🔵 BeforeModelCallback: model=%s, lastUserMsg=%q\n",
			c.modelName,
			userMsg,
		)
		if userMsg != "" && strings.Contains(userMsg, "custom model") {
			fmt.Printf("🔵 BeforeModelCallback: triggered, returning custom response for 'custom model'.\n")
			return &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "[This is a custom response from before model callback]",
					},
				}},
			}, nil
		}
		return nil, nil
	})
	modelCallbacks.RegisterAfterModel(func(
		ctx context.Context, resp *model.Response, runErr error,
	) (*model.Response, error) {
		if resp != nil && resp.Done {
			fmt.Printf("\n🟣 AfterModelCallback: model=%s has finished\n", c.modelName)
		}
		if resp != nil && len(resp.Choices) > 0 && strings.Contains(resp.Choices[0].Message.Content, "override me") {
			fmt.Printf("🟣 AfterModelCallback: triggered, overriding response for 'override me'.\n")
			return &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "[This response was overridden by after model callback]",
					},
				}},
			}, nil
		}
		return nil, nil
	})

	// Construct ToolCallbacks example.
	toolCallbacks := tool.NewToolCallbacks()
	toolCallbacks.RegisterBeforeTool(func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
	) (any, error) {
		fmt.Printf("\n🟠 BeforeToolCallback: tool=%s, args=%s\n", toolName, string(jsonArgs))
		if toolName == "calculator" && strings.Contains(string(jsonArgs), "42") {
			fmt.Println("\n🟠 BeforeToolCallback: triggered, custom result returned for calculator with 42.")
			return map[string]any{"result": 4242, "note": "custom result from before tool callback"}, nil
		}
		return nil, nil
	})
	toolCallbacks.RegisterAfterTool(func(
		ctx context.Context,
		toolName string,
		toolDeclaration *tool.Declaration,
		jsonArgs []byte,
		result any,
		runErr error,
	) (any, error) {
		fmt.Printf("\n🟤 AfterToolCallback: tool=%s, args=%s, result=%v, err=%v\n", toolName, string(jsonArgs), result, runErr)
		if toolName == "current_time" {
			if m, ok := result.(map[string]any); ok {
				m["formatted"] = fmt.Sprintf("%s %s (%s)", m["date"], m["time"], m["timezone"])
				fmt.Println("\n🟤 AfterToolCallback: triggered, formatted result.")
				return m, nil
			}
		}
		return nil, nil
	})

	// AgentCallbacks example.
	agentCallbacks := agent.NewAgentCallbacks()
	agentCallbacks.RegisterBeforeAgent(func(
		ctx context.Context, invocation *agent.Invocation,
	) (*model.Response, error) {
		fmt.Printf("\n🟢 BeforeAgentCallback: agent=%s, invocationID=%s, userMsg=%q\n",
			invocation.AgentName,
			invocation.InvocationID,
			invocation.Message.Content,
		)
		return nil, nil
	})
	agentCallbacks.RegisterAfterAgent(func(
		ctx context.Context, invocation *agent.Invocation, runErr error,
	) (*model.Response, error) {
		respContent := "<nil>"
		if invocation != nil && invocation.Message.Content != "" {
			respContent = invocation.Message.Content
		}
		fmt.Printf("\n🟡 AfterAgentCallback: agent=%s, invocationID=%s, runErr=%v, userMsg=%q\n",
			invocation.AgentName,
			invocation.InvocationID,
			runErr,
			respContent,
		)
		return nil, nil
	})

	genConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2000),
		Temperature: floatPtr(0.7),
		Stream:      true,
	}

	agentName := "chat-assistant"
	llmAgent := llmagent.New(
		agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("A helpful AI assistant with calculator and time tools"),
		llmagent.WithInstruction("Use tools when appropriate for calculations or time queries. "+
			"Be helpful and conversational."),
		llmagent.WithGenerationConfig(genConfig),
		llmagent.WithChannelBufferSize(100),
		llmagent.WithTools([]tool.Tool{calculatorTool, timeTool}),
		llmagent.WithAgentCallbacks(agentCallbacks),
		llmagent.WithModelCallbacks(modelCallbacks),
		llmagent.WithToolCallbacks(toolCallbacks),
	)

	appName := "multi-turn-chat-callbacks"
	c.runner = runner.NewRunner(
		appName,
		llmAgent,
	)

	c.userID = "user"
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().Unix())

	fmt.Printf("✅ Chat with callbacks ready! Session: %s\n\n", c.sessionID)

	return nil
}

func (c *multiTurnChatWithCallbacks) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}

		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}

		if strings.ToLower(userInput) == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

func (c *multiTurnChatWithCallbacks) processMessage(ctx context.Context, userMessage string) error {
	message := model.NewUserMessage(userMessage)
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	return c.processStreamingResponse(eventChan)
}

func (c *multiTurnChatWithCallbacks) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		fullContent       string
		toolCallsDetected bool
		assistantStarted  bool
	)

	for event := range eventChan {
		if event.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", event.Error.Message)
			continue
		}

		if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 CallableTool calls initiated:\n")
			for _, toolCall := range event.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", toolCall.Function.Name, toolCall.ID)
				if len(toolCall.Function.Arguments) > 0 {
					fmt.Printf("     Args: %s\n", string(toolCall.Function.Arguments))
				}
			}
			fmt.Printf("\n🔄 Executing tools...\n")
		}

		if event.Response != nil && len(event.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range event.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ CallableTool response (ID: %s): %s\n",
						choice.Message.ToolID,
						strings.TrimSpace(choice.Message.Content))
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		if len(event.Choices) > 0 {
			choice := event.Choices[0]
			if choice.Delta.Content != "" {
				if !assistantStarted {
					if toolCallsDetected {
						fmt.Printf("\n🤖 Assistant: ")
					}
					assistantStarted = true
				}
				fmt.Print(choice.Delta.Content)
				fullContent += choice.Delta.Content
			}
		}

		if event.Done && !c.isToolEvent(event) {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}

func (c *multiTurnChatWithCallbacks) isToolEvent(event *event.Event) bool {
	if event.Response == nil {
		return false
	}
	if len(event.Choices) > 0 && len(event.Choices[0].Message.ToolCalls) > 0 {
		return true
	}
	if len(event.Choices) > 0 && event.Choices[0].Message.ToolID != "" {
		return true
	}
	for _, choice := range event.Response.Choices {
		if choice.Message.Role == model.RoleTool {
			return true
		}
	}
	return false
}

// CallableTool implementations.

func (c *multiTurnChatWithCallbacks) calculate(args calculatorArgs) map[string]any {
	var result float64
	switch strings.ToLower(args.Operation) {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		if args.B != 0 {
			result = args.A / args.B
		} else {
			result = 0
		}
	default:
		result = 0
	}
	return map[string]any{
		"operation": args.Operation,
		"a":         args.A,
		"b":         args.B,
		"result":    result,
	}
}

func (c *multiTurnChatWithCallbacks) getCurrentTime(args timeArgs) map[string]any {
	now := time.Now()
	var t time.Time
	timezone := args.Timezone
	switch strings.ToUpper(args.Timezone) {
	case "UTC":
		t = now.UTC()
	case "EST", "EASTERN":
		t = now.Add(-5 * time.Hour)
	case "PST", "PACIFIC":
		t = now.Add(-8 * time.Hour)
	case "CST", "CENTRAL":
		t = now.Add(-6 * time.Hour)
	case "":
		t = now
		timezone = "Local"
	default:
		t = now.UTC()
		timezone = "UTC"
	}
	return map[string]any{
		"timezone": timezone,
		"time":     t.Format("15:04:05"),
		"date":     t.Format("2006-01-02"),
		"weekday":  t.Weekday().String(),
	}
}

type calculatorArgs struct {
	Operation string  `json:"operation" description:"The operation: add, subtract, multiply, divide"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type timeArgs struct {
	Timezone string `json:"timezone" description:"Timezone (UTC, EST, PST, CST) or leave empty for local"`
}

func intPtr(i int) *int           { return &i }
func floatPtr(f float64) *float64 { return &f }
