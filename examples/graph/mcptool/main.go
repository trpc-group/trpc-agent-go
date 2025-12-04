//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how to use MCP tools inside a graph‑based
// workflow. The example focuses on calling MCP tools like get_weather
// from a graph, and streaming the intermediate steps.
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

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/mcp"
	tmcp "trpc.group/trpc-go/trpc-mcp-go"
)

const (
	defaultModelName = "deepseek-chat"
)

var (
	modelName = flag.String("model", defaultModelName,
		"Name of the model to use")
)

func main() {
	flag.Parse()

	fmt.Printf("🚀 Graph + MCP (STDIO) example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Println(strings.Repeat("=", 50))
	fmt.Println("This example shows how a graph can call MCP tools")
	fmt.Println("like get_weather via an MCP STDIO server.")
	fmt.Println()
	fmt.Println("💡 Hint:")
	fmt.Println("   1) cd examples/graph/mcptool")
	fmt.Println("   2) go run . -model deepseek-chat")
	fmt.Println("      (the graph will spawn the STDIO MCP server automatically)")
	fmt.Println()

	chat := &mcpGraphChat{
		modelName: *modelName,
	}

	if err := chat.run(); err != nil {
		log.Fatalf("graph+mcp chat failed: %v", err)
	}
}

// mcpGraphChat manages the graph workflow that calls MCP tools.
type mcpGraphChat struct {
	modelName string

	runner runner.Runner

	userID    string
	sessionID string

	mcpToolSet *mcp.ToolSet
}

// run sets up the graph and starts the interactive loop.
func (c *mcpGraphChat) run() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer c.runner.Close()

	// Ensure MCP toolset is closed when finished.
	if c.mcpToolSet != nil {
		defer c.mcpToolSet.Close()
	}

	return c.startInteractiveMode(ctx)
}

// setup builds the graph, wraps it into a GraphAgent and Runner, and
// initializes the MCP toolset.
func (c *mcpGraphChat) setup(ctx context.Context) error {
	workflowGraph, toolSet, err := c.createMCPGraph(ctx)
	if err != nil {
		return err
	}

	// Hold the toolset for cleanup.
	c.mcpToolSet = toolSet

	graphAgent, err := graphagent.New(
		"mcp-graph-assistant",
		workflowGraph,
		graphagent.WithDescription("Graph example that calls MCP tools such as get_weather"),
		graphagent.WithInitialState(graph.State{}),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	sessionService := inmemory.NewSessionService()

	appName := "graph-mcp-example"
	c.runner = runner.NewRunner(
		appName,
		graphAgent,
		runner.WithSessionService(sessionService),
	)

	c.userID = "user"
	c.sessionID = fmt.Sprintf("graph-mcp-session-%d", time.Now().Unix())

	fmt.Printf("✅ Graph + MCP ready! Session: %s\n\n", c.sessionID)
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("💡 Hint: OPENAI_API_KEY is not set. If your model provider requires it, export it or configure base URL/API key accordingly.")
	}

	return nil
}

// createMCPGraph creates a simple graph that lets an LLM decide when to call
// MCP tools, executes those tools via a Tools node, and then formats the final
// answer for the user.
func (c *mcpGraphChat) createMCPGraph(ctx context.Context) (*graph.Graph, *mcp.ToolSet, error) {
	// Use the standard messages state schema so that we can rely on
	// graph.StateKeyMessages/graph.StateKeyLastResponse, etc.
	schema := graph.MessagesStateSchema()

	// Create model instance.
	modelInstance := openai.New(c.modelName)

	// Create MCP toolset: start the local STDIO MCP server defined in
	// stdioserver/main.go via "go run".
	toolSet := mcp.NewMCPToolSet(
		mcp.ConnectionConfig{
			Transport: "stdio",
			Command:   "go",
			Args:      []string{"run", "./stdioserver/main.go"},
			Timeout:   10 * time.Second,
		},
		mcp.WithMCPOptions(
			tmcp.WithSimpleRetry(3),
		),
	)

	if err := toolSet.Init(ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to initialize MCP toolset: %w", err)
	}

	mcpTools := toolSet.Tools(ctx)
	if len(mcpTools) == 0 {
		return nil, nil, fmt.Errorf("no MCP tools discovered from server")
	}

	tools := make(map[string]tool.Tool, len(mcpTools))
	fmt.Println("🔧 MCP tools registered in graph:")
	for _, t := range mcpTools {
		name := t.Declaration().Name
		tools[name] = t
		fmt.Printf("   • %s\n", name)
	}
	fmt.Println()

	stateGraph := graph.NewStateGraph(schema)

	// LLM node that can decide whether to call MCP tools and also produce the final answer.
	stateGraph.AddLLMNode(
		"assistant",
		modelInstance,
		`You are a helpful assistant.

You may have access to external tools via the tools list.

Guidelines:
1. Before answering, think about whether calling a tool could help you give a more accurate or up‑to‑date answer.
2. If a tool seems useful, call it with appropriate arguments, then read its output carefully.
3. Prefer calling a tool at most once per request unless new information is needed.
4. When you have enough information, stop calling tools and answer the user directly.`,
		tools,
	)

	// Tools node executes MCP tool calls emitted by the assistant node.
	stateGraph.AddToolsNode("tools", tools)

	// Final no-op node used only to terminate the graph; the visible answer
	// is the last response produced by the assistant node.
	stateGraph.AddNode("finish", func(ctx context.Context, state graph.State) (any, error) {
		return nil, nil
	})

	// Wiring: assistant -> tools (if tool_calls present) or directly to finish.
	stateGraph.AddToolsConditionalEdges("assistant", "tools", "finish")

	// After tools finish, go back to assistant so that it can read tool responses
	// and, if needed, decide whether to call tools again or answer the user.
	stateGraph.AddEdge("tools", "assistant")

	// Entry and finish points.
	stateGraph.SetEntryPoint("assistant").SetFinishPoint("finish")

	compiled, err := stateGraph.Compile()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to compile MCP graph: %w", err)
	}

	return compiled, toolSet, nil
}

// startInteractiveMode starts a simple REPL where each line is processed by
// the graph. It shows LLM streaming output, MCP tool calls and tool results.
func (c *mcpGraphChat) startInteractiveMode(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)

	fmt.Println("💡 MCP Graph Interactive Mode")
	fmt.Println("   Try inputs like:")
	fmt.Println("   • Use get_weather to check the weather in Beijing")
	fmt.Println("   • What is the weather in London today? Use get_weather.")
	fmt.Println("   Type 'exit' to quit.")
	fmt.Println()

	for {
		fmt.Print("📄 Request: ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "exit", "quit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "help":
			fmt.Println("Commands: help, exit")
			continue
		}

		if err := c.processRequest(ctx, input); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}

		fmt.Println()
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}

	return nil
}

// processRequest runs a single request through the graph and streams the
// intermediate events.
func (c *mcpGraphChat) processRequest(ctx context.Context, content string) error {
	message := model.NewUserMessage(content)

	eventChan, err := c.runner.Run(
		ctx,
		c.userID,
		c.sessionID,
		message,
	)
	if err != nil {
		return fmt.Errorf("failed to run graph: %w", err)
	}

	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse streams events from the graph run and prints:
// - tool calls emitted by the LLM
// - tool responses coming back from MCP tools
// - the assistant's streamed answer
func (c *mcpGraphChat) processStreamingResponse(eventChan <-chan *event.Event) error {
	fmt.Print("🤖 Assistant: ")

	var (
		toolCallsDetected bool
		assistantStarted  bool
	)

	for ev := range eventChan {
		if ev == nil {
			continue
		}

		// Handle errors.
		if ev.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
			continue
		}

		// Detect and display tool calls emitted by the LLM.
		if ev.Response != nil && len(ev.Response.Choices) > 0 &&
			len(ev.Response.Choices[0].Message.ToolCalls) > 0 {
			toolCallsDetected = true
			if assistantStarted {
				fmt.Printf("\n")
			}
			fmt.Printf("🔧 MCP tool calls:\n")
			for _, tc := range ev.Response.Choices[0].Message.ToolCalls {
				fmt.Printf("   • %s (ID: %s)\n", tc.Function.Name, tc.ID)
				if len(tc.Function.Arguments) > 0 {
					fmt.Printf("     Args: %s\n", string(tc.Function.Arguments))
				}
			}
			fmt.Printf("\n🔄 Waiting for MCP tools to finish...\n")
		}

		// Detect tool responses (messages with role=tool).
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			hasToolResponse := false
			for _, choice := range ev.Response.Choices {
				if choice.Message.Role == model.RoleTool && choice.Message.ToolID != "" {
					fmt.Printf("✅ Tool response (ID: %s): %s\n",
						choice.Message.ToolID,
						strings.TrimSpace(choice.Message.Content))
					hasToolResponse = true
				}
			}
			if hasToolResponse {
				continue
			}
		}

		// Stream assistant tokens.
		if ev.Response != nil && len(ev.Response.Choices) > 0 {
			choice := ev.Response.Choices[0]
			if choice.Delta.Content != "" {
				if !assistantStarted {
					if toolCallsDetected {
						fmt.Printf("\n🤖 Assistant: ")
					}
					assistantStarted = true
				}
				fmt.Print(choice.Delta.Content)
			}
		}

		if ev.IsFinalResponse() {
			fmt.Printf("\n")
			break
		}
	}

	return nil
}
