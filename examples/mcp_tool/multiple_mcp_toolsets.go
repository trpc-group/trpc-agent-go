// Package main demonstrates using multiple MCPToolsets for multi-session management.
// This approach aligns with ADK Python's design where each MCPToolset manages
// one independent MCP connection/session.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// MultipleMCPToolsetsDemo demonstrates the ADK Python approach:
// Use multiple MCPToolsets to manage multiple MCP sessions.
func MultipleMCPToolsetsDemo() {
	fmt.Println("=== Multiple MCPToolsets Demo (ADK Python Style) ===")
	fmt.Println("Each MCPToolset = one independent MCP session")
	fmt.Println()

	ctx := context.Background()

	// MCPToolset 1: Streamable HTTP MCP server (file system tools).
	httpToolSet := tool.NewMCPToolSet(tool.MCPConnectionConfig{
		Transport: "streamable_http",
		ServerURL: "http://localhost:3000/mcp",
		Timeout:   10 * time.Second,
	},
		tool.WithToolFilter(tool.NewIncludeFilter("echo", "greet", "current_time")),
	)
	defer httpToolSet.Close()

	// MCPToolset 2: STDIO MCP server (local tools).
	stdioToolSet := tool.NewMCPToolSet(tool.MCPConnectionConfig{
		Transport: "stdio",
		Command:   "./stdio_server/stdio_server",
		Args:      []string{},
		Timeout:   5 * time.Second,
	},
		tool.WithToolFilter(tool.NewIncludeFilter("echo", "text_transform", "calculator")),
	)
	defer stdioToolSet.Close()

	// MCPToolset 3: Can connect to other MCP servers.
	// This demonstrates the concept, and in practice, it can connect to different servers.
	// otherToolSet := tool.NewMCPToolSet(tool.MCPConnectionConfig{
	//     Transport: "streamable_http",
	//     ServerURL: "http://localhost:3001/mcp",
	//     Timeout:   10 * time.Second,
	// })
	// defer otherToolSet.Close()

	// Collect tools from all toolsets.
	var allTools []tool.Tool

	// Get tools from HTTP MCP toolset.
	httpTools := httpToolSet.Tools(ctx)
	fmt.Printf("HTTP MCP toolset: found %d tools\n", len(httpTools))
	for _, t := range httpTools {
		fmt.Printf("   - %s (from HTTP MCP)\n", t.Declaration().Name)
		allTools = append(allTools, t)
	}

	// Get tools from STDIO MCP toolset.
	stdioTools := stdioToolSet.Tools(ctx)
	fmt.Printf("STDIO MCP toolset: found %d tools\n", len(stdioTools))
	for _, t := range stdioTools {
		fmt.Printf("   - %s (from STDIO MCP)\n", t.Declaration().Name)
		allTools = append(allTools, t)
	}

	fmt.Printf("\nTotal tool count: %d (from multiple independent MCP sessions)\n", len(allTools))

	// Use convenience function to convert tool mapping.
	toolsMap := toolsToMapDemo(allTools)

	// Read configuration from environment variables.
	baseURL := getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1")
	apiKey := getEnv("OPENAI_API_KEY", "")
	modelName := getEnv("OPENAI_MODEL", "gpt-4o-mini")

	// Validate required environment variables.
	if apiKey == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	fmt.Printf("Using configuration:\n")
	fmt.Printf("- Model Name: %s\n", modelName)
	fmt.Printf("- Base URL: %s\n", baseURL)
	fmt.Printf("- Channel Buffer Size: 50\n")
	fmt.Println()

	// Create LLM instance.
	modelInstance := openai.New(modelName, openai.Options{
		APIKey:            apiKey,
		BaseURL:           baseURL,
		ChannelBufferSize: 50,
	})

	// Test multi-session tool calls.
	testScenarios := []struct {
		name    string
		message string
	}{
		{
			name:    "Cross-session tool usage",
			message: "Please use tools from different sources: first use the greet tool from HTTP MCP to greet 'Alice', then use the calculator tool from STDIO MCP to calculate 25*4",
		},
		{
			name:    "Time function comparison",
			message: "Please call the time-related tools from HTTP MCP and STDIO MCP, and compare their functions",
		},
	}

	for i, scenario := range testScenarios {
		fmt.Printf("\nTest scenario %d: %s\n", i+1, scenario.name)
		fmt.Printf("User: %s\n", scenario.message)

		temperature := 0.7
		maxTokens := 2000

		request := &model.Request{
			Messages: []model.Message{
				model.NewSystemMessage(`You are an intelligent assistant, now you can use tools from multiple different MCP servers. The tool sources include:
- HTTP MCP server tools: echo, greet, current_time
- STDIO MCP server tools: echo, text_transform, calculator

Note: Different sources may have tools with the same name (e.g. echo), they are independent implementations.
Please intelligently select and use the appropriate tools based on user requests.`),
				model.NewUserMessage(scenario.message),
			},
			GenerationConfig: model.GenerationConfig{
				Temperature: &temperature,
				MaxTokens:   &maxTokens,
				Stream:      false,
			},
			Tools: toolsMap, // Tools from multiple MCP sessions.
		}

		// Call LLM.
		responseChan, err := modelInstance.GenerateContent(ctx, request)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		// Handle responses and tool calls.
		for response := range responseChan {
			if response.Error != nil {
				fmt.Printf("API Error: %s\n", response.Error.Message)
				break
			}

			if len(response.Choices) > 0 {
				choice := response.Choices[0]
				fmt.Printf("Assistant: %s\n", choice.Message.Content)

				// Handle tool calls.
				if len(response.ToolCalls) > 0 {
					fmt.Printf("\nExecuting %d tool calls:\n", len(response.ToolCalls))

					for _, toolCall := range response.ToolCalls {
						fmt.Printf("- %s: %s\n", toolCall.Function.Name, string(toolCall.Function.Arguments))

						if toolInstance, exists := toolsMap[toolCall.Function.Name]; exists {
							result, err := toolInstance.Call(ctx, toolCall.Function.Arguments)
							if err != nil {
								fmt.Printf("   Error: %v\n", err)
							} else {
								fmt.Printf("   Result: %v\n", result)
								request.Messages = append(request.Messages,
									model.NewToolCallMessage(fmt.Sprintf("%v", result), toolCall.ID))
							}
						}
					}

					// Get final response.
					fmt.Println("\n🔄 Getting final response...")
					responseChan2, err := modelInstance.GenerateContent(ctx, request)
					if err != nil {
						fmt.Printf("Final response error: %v\n", err)
						break
					}

					for response2 := range responseChan2 {
						if response2.Error != nil {
							fmt.Printf("Final API error: %s\n", response2.Error.Message)
							break
						}

						if len(response2.Choices) > 0 {
							fmt.Printf("Final response: %s\n", response2.Choices[0].Message.Content)
						}

						if response2.Done {
							break
						}
					}
				}
			}

			if response.Done {
				break
			}
		}

		fmt.Println("\n" + strings.Repeat("-", 80))
	}

	fmt.Println("\nMultiple MCPToolset demonstration completed")
	fmt.Println("Summary: By creating multiple MCPToolset instances, successfully managed multiple independent MCP sessions")
	fmt.Println("This approach is completely consistent with the design philosophy of ADK Python")
}

// toolsToMap converts tool array to map for easy use in model.Request.
func toolsToMapDemo(tools []tool.Tool) map[string]tool.Tool {
	toolsMap := make(map[string]tool.Tool, len(tools))
	for _, t := range tools {
		decl := t.Declaration()
		toolsMap[decl.Name] = t
	}
	return toolsMap
}

// If you run this file directly, set the log flags.
func init() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}
