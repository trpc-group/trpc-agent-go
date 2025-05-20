// Package main provides an example of using streaming thoughts with the ReAct agent.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/agents/react"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/models"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/tools"
)

func main() {
	// Setup logging
	log.SetLevel(log.LevelDebug)
	log.Infof("Starting streaming thought example")

	// Get API key from environment or fail
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Println("Error: OPENAI_API_KEY environment variable not set")
		os.Exit(1)
	}

	// Create OpenAI streaming model
	openaiModel := models.NewOpenAIStreamingModel(
		"gpt-4-0125-preview",
		models.WithOpenAIAPIKey(apiKey),
		models.WithOpenAIBaseURL("https://api.openai.com/v1"),
	)

	// Create tools
	calculatorTool := tools.NewCalculatorTool()
	httpTool := tools.NewHTTPClientTool()
	toolSet := []tool.Tool{calculatorTool, httpTool}

	// Create and run both types of agents
	runThoughtStreamingAgent(openaiModel, toolSet)
	fmt.Println("\n\n===== Now trying with model streaming =====\n\n")
	runModelStreamingAgent(openaiModel, toolSet)
}

// runThoughtStreamingAgent runs a ReAct agent using thought generator streaming.
func runThoughtStreamingAgent(model model.StreamingModel, tools []tool.Tool) {
	// Create a thought prompt strategy that will be used for both agents
	thoughtPromptStrategy := react.NewDefaultThoughtPromptStrategy()

	// Explicitly create a streaming thought generator
	thoughtGenerator := react.NewStreamingLLMThoughtGenerator(
		model,
		thoughtPromptStrategy,
		react.ThoughtFormatFree,
	)

	// Create ReAct agent with streaming thought generator
	agentConfig := react.AgentConfig{
		Name:             "ThoughtStreamingAgent",
		Description:      "An agent that uses streaming thoughts",
		Model:            model,
		Tools:            tools,
		ThoughtGenerator: thoughtGenerator,
		MaxIterations:    5,
		EnableStreaming:  true,
	}

	agent, err := react.NewAgent(agentConfig)
	if err != nil {
		fmt.Printf("Error creating ReAct agent: %v\n", err)
		os.Exit(1)
	}

	// Create a user query that requires reasoning and tool use
	userQuery := "What's 125 * 37? Then add 42 to that result."
	msg := message.NewUserMessage(userQuery)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run agent in async streaming mode
	fmt.Println("Running agent with thought generator streaming...")
	fmt.Printf("User: %s\n\n", userQuery)

	eventCh, err := agent.RunAsync(ctx, msg)
	if err != nil {
		fmt.Printf("Error running agent: %v\n", err)
		os.Exit(1)
	}

	// Process streaming events
	for evt := range eventCh {
		processStreamingEvent(evt)
	}
}

// runModelStreamingAgent runs a ReAct agent using model streaming.
func runModelStreamingAgent(model model.StreamingModel, tools []tool.Tool) {
	// Create ReAct agent with default configuration that uses model streaming
	agentConfig := react.AgentConfig{
		Name:            "ModelStreamingAgent",
		Description:     "An agent that uses model streaming",
		Model:           model,
		Tools:           tools,
		MaxIterations:   5,
		EnableStreaming: true,
		// Use default ThoughtGenerator which will be standard LLMThoughtGenerator
	}

	agent, err := react.NewAgent(agentConfig)
	if err != nil {
		fmt.Printf("Error creating ReAct agent: %v\n", err)
		os.Exit(1)
	}

	// Create a user query that requires reasoning and tool use
	userQuery := "What's 84 / 7? Then multiply that result by 3."
	msg := message.NewUserMessage(userQuery)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Run agent in async streaming mode
	fmt.Println("Running agent with model streaming...")
	fmt.Printf("User: %s\n\n", userQuery)

	eventCh, err := agent.RunAsync(ctx, msg)
	if err != nil {
		fmt.Printf("Error running agent: %v\n", err)
		os.Exit(1)
	}

	// Process streaming events
	for evt := range eventCh {
		processStreamingEvent(evt)
	}
}

// processStreamingEvent handles the different types of streaming events.
func processStreamingEvent(evt *event.Event) {
	switch evt.Type {
	case event.TypeStreamChunk:
		// Print streaming chunks as they arrive
		if content, ok := evt.GetMetadata("content"); ok {
			if contentStr, ok := content.(string); ok {
				fmt.Print(contentStr)
			}
		}
	case event.TypeStreamEnd:
		fmt.Println("\n[Stream ended]")
	case event.TypeMessage:
		if message, ok := evt.Data.(*message.Message); ok {
			fmt.Printf("\nFinal response: %s\n", message.Content)
		}
	case event.TypeTool:
		// Print tool events
		if toolName, ok := evt.GetMetadata("tool_name"); ok {
			fmt.Printf("\n[Using tool: %s]\n", toolName)
		}
	case event.TypeStreamToolCall:
		// Print tool call events
		if name, ok := evt.GetMetadata("name"); ok {
			fmt.Printf("\n[Called tool: %s]\n", name)
		}
	case event.TypeStreamToolResult:
		// Print tool result events
		if result, ok := evt.GetMetadata("result"); ok {
			fmt.Printf("[Tool result: %s]\n", result)
		}
	case event.TypeError:
		// Print error events
		if errMsg, ok := evt.GetMetadata("error"); ok {
			fmt.Printf("\nError: %s\n", errMsg)
		}
	case event.TypeCustom:
		// Handle custom events
		if eventType, ok := evt.GetMetadata("type"); ok {
			if eventType == "thinking" {
				if data, ok := evt.Data.(string); ok {
					fmt.Printf("\n[Thinking: %s]\n", data)
				}
			}
		}
	}
}
