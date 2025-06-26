// Package main demonstrates how to use MCP tools with LLM integration.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run . <demo_type>")
		fmt.Println("Available demos:")
		fmt.Println("  streamable  - MCP tools via streamable HTTP server")
		fmt.Println("  hybrid      - Mixed function tools + MCP tools")
		fmt.Println("  stdio       - STDIO MCP server tools")
		fmt.Println("  filter      - Tool filtering showcase")
		fmt.Println("  diagnostics - Enhanced error diagnostics showcase")
		fmt.Println("  multiple    - Multiple MCPToolsets demo (ADK Python style)")
		return
	}

	demoType := os.Args[1]
	switch demoType {
	case "streamable":
		runStreamableHTTPMCPDemo()
	case "hybrid":
		runMixedToolsDemo()
	case "stdio":
		runStdioMCPDemo()
	case "filter":
		runFilterDemo()
	case "diagnostics":
		runDiagnosticsDemo()
	case "multiple":
		MultipleMCPToolsetsDemo()
	default:
		fmt.Printf("Unknown demo type: %s\n", demoType)
		fmt.Println("Use 'streamable', 'hybrid', 'stdio', 'filter', 'diagnostics', or 'multiple'")
	}
}

// Demo 1: Streamable HTTP MCP Toolset Demonstration.
func runStreamableHTTPMCPDemo() {
	fmt.Println("=== Demo 1: Streamable HTTP MCP Tools ===")
	fmt.Println()

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

	// Create OpenAI model instance.
	modelInstance := openai.New(modelName, openai.Options{
		APIKey:            apiKey,
		BaseURL:           baseURL,
		ChannelBufferSize: 50,
	})

	// Configure Streamable HTTP MCP connection.
	mcpConfig := tool.MCPConnectionConfig{
		Transport: "streamable_http",
		ServerURL: "http://localhost:3000/mcp", // Use ServerURL instead of URL
		Timeout:   10 * time.Second,
	}

	// Create MCP toolset with enterprise-level configuration.
	mcpToolSet := tool.NewMCPToolSet(mcpConfig,
		tool.WithRetry(tool.RetryConfig{
			Enabled:       true,
			MaxAttempts:   3,
			InitialDelay:  time.Second,
			BackoffFactor: 2.0,
			MaxDelay:      30 * time.Second,
		}),
		tool.WithToolFilter(tool.NewIncludeFilter("echo", "greet", "current_time", "calculate", "env_info")),
		tool.WithAutoRefresh(5*time.Minute), // Auto-refresh tool list every 5 minutes
	)
	defer mcpToolSet.Close()

	fmt.Println("MCP Toolset created successfully")
	fmt.Println("Connected to streamable HTTP MCP server at http://localhost:3000/mcp")

	// Discover available tools.
	ctx := context.Background()
	tools := mcpToolSet.Tools(ctx)
	fmt.Printf("Discovered %d MCP tools\n", len(tools))

	// 🎯 Use convenience function to auto-convert tool mapping.
	toolsMap := toolsToMap(tools)
	for name := range toolsMap {
		fmt.Printf("   - %s: %s\n", name, toolsMap[name].Declaration().Description)
	}
	fmt.Println()

	// Prepare tool context.
	toolCtx := &tool.ToolContext{
		SessionID: "mcp-http-demo-session",
		UserID:    "demo-user",
		Metadata: map[string]interface{}{
			"demo_type":   "streamable_http_mcp",
			"server_url":  "http://localhost:3000/mcp",
			"timestamp":   time.Now().Unix(),
			"environment": "development",
		},
	}
	ctx = tool.WithToolContext(ctx, toolCtx)

	// Test scenarios.
	scenarios := []struct {
		name    string
		message string
	}{
		{
			name:    "Multi-language greeting test",
			message: "Please greet 'Alice' in Chinese, then greet 'Marie' in French",
		},
		{
			name:    "Time and calculation combined test",
			message: "Please tell me the current time, then calculate 25 * 4 + 15",
		},
		{
			name:    "Environment info query",
			message: "Please get the current environment's hostname and user information",
		},
		{
			name:    "Echo and math operations",
			message: "Please echo 'Hello MCP!', then calculate 100 divided by 4",
		},
	}

	for i, scenario := range scenarios {
		fmt.Printf("Test scenario %d: %s\n", i+1, scenario.name)
		fmt.Printf("User: %s\n", scenario.message)

		// Prepare conversation messages and configuration.
		temperature := 0.7
		maxTokens := 2000

		request := &model.Request{
			Messages: []model.Message{
				model.NewSystemMessage(`You are an intelligent assistant that can use multiple tools to help users. Available tools include:
- echo: Echo messages, can add prefix
- greet: Generate multilingual greetings (supports en, zh, es, fr)  
- current_time: Get current time, supports different timezones and formats
- calculate: Execute mathematical operations (addition, subtraction, multiplication, division)
- env_info: Get environment information (hostname, user, working directory, etc.)

Please intelligently select and use appropriate tools based on user requests, and provide detailed answers.`),
				model.NewUserMessage(scenario.message),
			},
			GenerationConfig: model.GenerationConfig{
				Temperature: &temperature,
				MaxTokens:   &maxTokens,
				Stream:      false,
			},
			Tools: toolsMap, // Directly use the converted tool mapping
		}

		// Call LLM to handle tool calls.
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
					fmt.Printf("\n🔧 Executing %d tool calls:\n", len(response.ToolCalls))

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
					fmt.Println("\nGetting final response from LLM...")
					responseChan2, err := modelInstance.GenerateContent(ctx, request)
					if err != nil {
						fmt.Printf(" Final response error: %v\n", err)
						break
					}

					for response2 := range responseChan2 {
						if response2.Error != nil {
							fmt.Printf(" Final API error: %s\n", response2.Error.Message)
							break
						}

						if len(response2.Choices) > 0 {
							fmt.Printf("\n Final response: %s\n", response2.Choices[0].Message.Content)
						}

						if response2.Done {
							break
						}
					}
				}
			}

			if response.Usage != nil {
				fmt.Printf("Token usage: input=%d, output=%d, total=%d\n",
					response.Usage.PromptTokens,
					response.Usage.CompletionTokens,
					response.Usage.TotalTokens)
			}

			if response.Done {
				break
			}
		}
		fmt.Println(strings.Repeat("-", 80))
	}

	fmt.Println("Streamable HTTP MCP demonstration completed")
}

// Demo 2: Mixed Tools Environment Demonstration.
func runMixedToolsDemo() {
	fmt.Println("=== Demo 2: Mixed Tools Environment ===")
	fmt.Println()

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

	// Create OpenAI model instance.
	modelInstance := openai.New(modelName, openai.Options{
		APIKey:            apiKey,
		BaseURL:           baseURL,
		ChannelBufferSize: 50,
	})

	// 1. Create function tools (using functions defined in tool_functions.go).
	calcTool := tool.NewFunctionTool(calculate, tool.FunctionToolConfig{
		Name:        "calculator",
		Description: "Execute basic mathematical operations",
	})

	timeTool := tool.NewFunctionTool(getCurrentTime, tool.FunctionToolConfig{
		Name:        "get_time",
		Description: "Get current time",
	})

	// 2. Create MCP toolset.
	mcpConfig := tool.MCPConnectionConfig{
		Transport: "streamable_http",
		ServerURL: "http://localhost:3000/mcp",
		Timeout:   5 * time.Second,
	}

	mcpToolSet := tool.NewMCPToolSet(mcpConfig,
		tool.WithRetry(tool.RetryConfig{
			Enabled:      true,
			MaxAttempts:  2,
			InitialDelay: 500 * time.Millisecond,
		}),
	)

	var mcpTools []tool.Tool
	if mcpToolSet != nil {
		defer mcpToolSet.Close()
		fmt.Println("MCP Toolset connected to HTTP server")
		ctx := context.Background()
		mcpTools = mcpToolSet.Tools(ctx)
	} else {
		fmt.Println("MCP toolset creation failed, continuing with function tools only...")
	}

	// 3. Merge all tools.
	allTools := []tool.Tool{calcTool, timeTool}
	allTools = append(allTools, mcpTools...)

	fmt.Printf("Total tools count: %d (2 function tools + %d MCP tools)\n", len(allTools), len(mcpTools))

	// Use convenience function to auto-create tool mapping.
	toolsMap := toolsToMap(allTools)

	fmt.Println("Available tools list:")
	for name, t := range toolsMap {
		fmt.Printf("   - %s: %s\n", name, t.Declaration().Description)
	}
	fmt.Println()

	// Test comprehensive scenarios.
	fmt.Println("Comprehensive test scenarios:")
	fmt.Println("User: Please tell me the current time, then calculate 15*23+7, and finally greet 'Alice' in English")

	ctx := context.Background()
	temperature := 0.5
	maxTokens := 1000

	request := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(`You are an intelligent assistant with multiple tools to help users. Please select the appropriate tools based on user needs and provide detailed answers.

Available tools description:
- calculator: Execute mathematical expression calculation
- get_time: Get current time
- greet: Generate multilingual greetings (if available)
- echo: Echo message (if available)
- current_time: MCP time tool (if available)
- calculate: MCP calculation tool (if available)

Please intelligently select the most appropriate tools to complete the task.`),
			model.NewUserMessage("Please tell me the current time, then calculate 15*23+7, and finally greet 'Alice' in English"),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature: &temperature,
			MaxTokens:   &maxTokens,
			Stream:      false,
		},
		Tools: toolsMap, // Directly use the converted tool mapping.
	}

	responseChan, err := modelInstance.GenerateContent(ctx, request)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	for response := range responseChan {
		if response.Error != nil {
			fmt.Printf("API error: %s\n", response.Error.Message)
			break
		}

		if len(response.Choices) > 0 {
			choice := response.Choices[0]
			fmt.Printf("Assistant: %s\n", choice.Message.Content)

			if len(response.ToolCalls) > 0 {
				fmt.Printf("\n Executing %d tool calls:\n", len(response.ToolCalls))

				for _, toolCall := range response.ToolCalls {
					fmt.Printf("- %s: %s\n", toolCall.Function.Name, string(toolCall.Function.Arguments))

					if toolInstance, exists := toolsMap[toolCall.Function.Name]; exists {
						result, err := toolInstance.Call(ctx, toolCall.Function.Arguments)
						if err != nil {
							fmt.Printf("Error: %v\n", err)
						} else {
							fmt.Printf("Result: %v\n", result)
							request.Messages = append(request.Messages,
								model.NewToolCallMessage(fmt.Sprintf("%v", result), toolCall.ID))
						}
					}
				}

				// Get final response.
				responseChan2, err := modelInstance.GenerateContent(ctx, request)
				if err != nil {
					fmt.Printf("Final response error: %v\n", err)
					break
				}

				for response2 := range responseChan2 {
					if response2.Error != nil {
						fmt.Printf("Final response error: %s\n", response2.Error.Message)
						break
					}

					if len(response2.Choices) > 0 {
						fmt.Printf("\nFinal response: %s\n", response2.Choices[0].Message.Content)
					}

					if response2.Done {
						break
					}
				}
			}
		}

		if response.Usage != nil {
			fmt.Printf("Token usage: input=%d, output=%d, total=%d\n",
				response.Usage.PromptTokens,
				response.Usage.CompletionTokens,
				response.Usage.TotalTokens)
		}

		if response.Done {
			break
		}
	}

	fmt.Println("Mixed tools environment demonstration completed")
}

// Demo 3: STDIO MCP Server Tools Demonstration.
func runStdioMCPDemo() {
	fmt.Println("=== Demo 3: STDIO MCP Server Tools ===")
	fmt.Println()

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

	// Create OpenAI model instance.
	modelInstance := openai.New(modelName, openai.Options{
		APIKey:            apiKey,
		BaseURL:           baseURL,
		ChannelBufferSize: 50,
	})

	// Configure STDIO MCP to connect to our local server.
	mcpConfig := tool.MCPConnectionConfig{
		Transport: "stdio",
		Command:   "./stdio_server/stdio_server", // Point to our created STDIO server
		Timeout:   10 * time.Second,
	}

	// Create MCP toolset with enterprise-level configuration.
	mcpToolSet := tool.NewMCPToolSet(mcpConfig,
		tool.WithRetry(tool.RetryConfig{
			Enabled:       true,
			MaxAttempts:   3,
			InitialDelay:  time.Second,
			BackoffFactor: 2.0,
			MaxDelay:      30 * time.Second,
		}),
		tool.WithAutoRefresh(5*time.Minute), // Auto-refresh tool list every 5 minutes
	)
	defer mcpToolSet.Close()

	fmt.Println("STDIO MCP Toolset created successfully")
	fmt.Println("Connected to STDIO MCP server: ./stdio_server/stdio_server")

	// Discover available tools.
	ctx := context.Background()
	tools := mcpToolSet.Tools(ctx)
	fmt.Printf("Discovered %d STDIO MCP tools\n", len(tools))

	// Use convenience function to auto-convert tool mapping.
	toolsMap := toolsToMap(tools)
	fmt.Println("Available STDIO MCP tools:")
	for name := range toolsMap {
		fmt.Printf("   - %s: %s\n", name, toolsMap[name].Declaration().Description)
	}
	fmt.Println()

	// Prepare tool context.
	toolCtx := &tool.ToolContext{
		SessionID: "stdio-mcp-demo-session",
		UserID:    "demo-user",
		Metadata: map[string]interface{}{
			"demo_type":   "stdio_mcp",
			"server_path": "./stdio_server/stdio_server",
			"timestamp":   time.Now().Unix(),
			"environment": "development",
		},
	}
	ctx = tool.WithToolContext(ctx, toolCtx)

	// Test scenarios - show various features of the STDIO MCP server.
	scenarios := []struct {
		name    string
		message string
	}{
		{
			name:    "Echo and text transformation test",
			message: "Please echo 'Hello STDIO MCP!', then transform the text 'hello world' to uppercase and title case",
		},
		{
			name:    "Time query and calculation test",
			message: "Please get the current UTC time, then calculate the result of 123 multiplied by 456",
		},
		{
			name:    "System information and file query",
			message: "Please get the system environment information (user and hostname), then view the file list in the current directory",
		},
		{
			name:    "Random number generation test",
			message: "Please generate a random number between 1 and 100, a random string of 8 characters, and a UUID",
		},
		{
			name:    "Comprehensive function test",
			message: "Please echo 'Function test start', get the current time, calculate 25*4, generate a UUID, and finally get the system hostname",
		},
	}

	for i, scenario := range scenarios {
		fmt.Printf("Test scenario %d: %s\n", i+1, scenario.name)
		fmt.Printf("User: %s\n", scenario.message)

		// Prepare conversation messages and configuration.
		temperature := 0.7
		maxTokens := 2000

		request := &model.Request{
			Messages: []model.Message{
				model.NewSystemMessage(`You are an intelligent assistant with a variety of STDIO MCP tools to help users. Available tools include:
- echo: Echo message, supports custom prefix
- text_transform: Text transformation (uppercase, lowercase, title case, etc.)
- get_time: Get current time, supports multiple formats and timezones
- calculator: Basic mathematical calculator (four operations)
- file_info: File and directory information query
- env_info: System environment information (user, hostname, working directory, environment variables)
- random: Random number generator (numbers, strings, UUID)

Please intelligently select and use the appropriate tool combinations based on user requests, providing detailed and accurate answers.`),
				model.NewUserMessage(scenario.message),
			},
			GenerationConfig: model.GenerationConfig{
				Temperature: &temperature,
				MaxTokens:   &maxTokens,
				Stream:      false,
			},
			Tools: toolsMap, // Directly use the converted tool mapping.
		}

		// Call LLM to handle tool calls.
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
					fmt.Printf("\n Executing %d STDIO MCP tool calls:\n", len(response.ToolCalls))

					for _, toolCall := range response.ToolCalls {
						fmt.Printf("- %s: %s\n", toolCall.Function.Name, string(toolCall.Function.Arguments))

						if toolInstance, exists := toolsMap[toolCall.Function.Name]; exists {
							result, err := toolInstance.Call(ctx, toolCall.Function.Arguments)
							if err != nil {
								fmt.Printf("Error: %v\n", err)
							} else {
								fmt.Printf("Result: %v\n", result)
								request.Messages = append(request.Messages,
									model.NewToolCallMessage(fmt.Sprintf("%v", result), toolCall.ID))
							}
						}
					}

					// Get final response.
					fmt.Println("\n Getting final response from LLM...")
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
							fmt.Printf("\nFinal response: %s\n", response2.Choices[0].Message.Content)
						}

						if response2.Done {
							break
						}
					}
				}
			}

			if response.Usage != nil {
				fmt.Printf("Token usage: input=%d, output=%d, total=%d\n",
					response.Usage.PromptTokens,
					response.Usage.CompletionTokens,
					response.Usage.TotalTokens)
			}

			if response.Done {
				break
			}
		}
		fmt.Println(strings.Repeat("-", 80))
	}

	fmt.Println("STDIO MCP demonstration completed")
}

func runFilterDemo() {
	fmt.Println("=== Demo 4: Flexible Tool Filtering Showcase ===")

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Demo different filter types using STDIO MCP server.
	demos := []struct {
		name        string
		description string
		filter      tool.ToolFilter
	}{
		{
			name:        "No Filter",
			description: "Show all available tools",
			filter:      tool.NoFilter,
		},
		{
			name:        "Include Specific Tools",
			description: "Only show echo and calculator tools",
			filter:      tool.NewIncludeFilter("echo", "calculator"),
		},
		{
			name:        "Exclude System Tools",
			description: "Exclude potentially sensitive system tools",
			filter:      tool.NewExcludeFilter("file_info", "env_info"),
		},
		{
			name:        "Pattern Include Filter",
			description: "Only tools starting with 'text' or 'get'",
			filter:      tool.NewPatternIncludeFilter("^(text|get).*"),
		},
		{
			name:        "Description Filter",
			description: "Tools related to 'time' or 'random'",
			filter:      tool.NewDescriptionFilter(".*(time|random).*"),
		},
		{
			name:        "Composite Filter",
			description: "Combine multiple filters: include math tools but exclude calculator",
			filter: tool.NewCompositeFilter(
				tool.NewDescriptionFilter(".*(math|calc|random).*"),
				tool.NewExcludeFilter("calculator"),
			),
		},
		{
			name:        "Custom Function Filter",
			description: "Custom filter: only tools with names shorter than 7 characters",
			filter: tool.NewFuncFilter(func(ctx context.Context, tools []tool.MCPToolInfo) []tool.MCPToolInfo {
				var filtered []tool.MCPToolInfo
				for _, tool := range tools {
					if len(tool.Name) < 7 {
						filtered = append(filtered, tool)
					}
				}
				return filtered
			}),
		},
	}

	for i, demo := range demos {
		fmt.Printf("Filter demonstration %d: %s\n", i+1, demo.name)
		fmt.Printf("Description: %s\n", demo.description)

		// Create MCP toolset with the specific filter.
		config := tool.MCPConnectionConfig{
			Transport: "stdio",
			Command:   "./stdio_server/stdio_server",
			Timeout:   30 * time.Second,
		}

		mcpToolset := tool.NewMCPToolSet(config,
			tool.WithToolFilter(demo.filter),
			tool.WithRetry(tool.RetryConfig{
				Enabled:      true,
				MaxAttempts:  2,
				InitialDelay: 100 * time.Millisecond,
			}),
		)
		defer mcpToolset.Close()

		// Get filtered tools.
		tools := mcpToolset.Tools(ctx)
		fmt.Printf("Found %d tools\n", len(tools))

		if len(tools) > 0 {
			fmt.Printf("Tool list:\n")
			for _, t := range tools {
				decl := t.Declaration()
				fmt.Printf("   - %s: %s\n", decl.Name, decl.Description)
			}
		} else {
			fmt.Printf("No tools found that match the filter\n")
		}

		fmt.Println(strings.Repeat("-", 80))
	}

	fmt.Printf("Tool filtering demonstration completed\n")
}

func runDiagnosticsDemo() {
	fmt.Println("=== Demo 5: Enhanced Error Diagnostics Showcase ===")

	// Create context with timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// Scenario 1: Connection error (server not running).
	fmt.Println("\n1. Testing Connection Error Diagnostics:")
	testConnectionError(ctx)

	// Scenario 2: Tool not found error
	fmt.Println("\n2. Testing Tool Not Found Error Diagnostics:")
	testToolNotFoundError(ctx)

	// Scenario 3: Parameter validation errors.
	fmt.Println("\n3. Testing Parameter Validation Error Diagnostics:")
	testParameterErrors(ctx)

	// Scenario 4: Server error simulation.
	fmt.Println("\n4. Testing Server Error Diagnostics:")
	testServerError(ctx)

	fmt.Println("\n=== Error Diagnostics Demo Complete ===")
}

func testConnectionError(ctx context.Context) {
	// Try to connect to a non-existent server.
	config := tool.MCPConnectionConfig{
		Transport: "http",
		ServerURL: "http://localhost:9999/mcp", // Non-existent server
		Timeout:   5 * time.Second,
	}

	toolset := tool.NewMCPToolSet(config,
		tool.WithRetry(tool.RetryConfig{
			Enabled:     true,
			MaxAttempts: 2, // Reduce attempts for demo
		}),
	)
	defer toolset.Close()

	// This should fail with connection error.
	tools := toolset.Tools(ctx)
	fmt.Printf("Got %d tools (expected: connection failure with enhanced diagnostics)\n", len(tools))
}

func testToolNotFoundError(ctx context.Context) {
	// Connect to STDIO server.
	config := tool.MCPConnectionConfig{
		Transport: "stdio",
		Command:   "./stdio_server/stdio_server",
		Timeout:   10 * time.Second,
	}

	toolset := tool.NewMCPToolSet(config)
	defer toolset.Close()

	// Get available tools first.
	tools := toolset.Tools(ctx)
	if len(tools) == 0 {
		fmt.Println("No tools available for tool not found test")
		return
	}

	fmt.Printf("Available tools: ")
	for i, t := range tools {
		if i > 0 {
			fmt.Print(", ")
		}
		fmt.Print(t.Declaration().Name)
	}
	fmt.Println()

	// Try to call a non-existent tool.
	nonExistentTool := toolset.GetToolByName(ctx, "nonexistent_tool")
	if nonExistentTool != nil {
		fmt.Println("ERROR: Found tool that shouldn't exist!")
		return
	}

	// Simulate tool not found by trying to get it directly.
	fmt.Println("Attempting to call non-existent tool 'nonexistent_tool'...")
	fmt.Println("Expected: Tool not found error with available tools list in suggestions")
}

func testParameterErrors(ctx context.Context) {
	// Connect to STDIO server.
	config := tool.MCPConnectionConfig{
		Transport: "stdio",
		Command:   "./stdio_server/stdio_server",
		Timeout:   10 * time.Second,
	}

	toolset := tool.NewMCPToolSet(config)
	defer toolset.Close()

	tools := toolset.Tools(ctx)
	if len(tools) == 0 {
		fmt.Println("No tools available for parameter error test")
		return
	}

	// Find calculator tool.
	var calcTool tool.Tool
	for _, t := range tools {
		if t.Declaration().Name == "calculator" {
			calcTool = t
			break
		}
	}

	if calcTool == nil {
		fmt.Println("Calculator tool not found, using first available tool")
		calcTool = tools[0]
	}

	fmt.Printf("Testing parameter errors with tool: %s\n", calcTool.Declaration().Name)

	// Test 1: Missing required parameters.
	fmt.Println("  Testing missing parameters...")
	emptyArgs, _ := json.Marshal(map[string]interface{}{})
	_, err := calcTool.Call(ctx, emptyArgs)
	if err != nil {
		if mcpErr, ok := err.(*tool.MCPError); ok {
			fmt.Printf("  Got enhanced error: %s\n", mcpErr.Code)
			fmt.Printf("  Suggestions: %v\n", mcpErr.Suggestions)
		} else {
			fmt.Printf("  Got regular error: %v\n", err)
		}
	}

	// Test 2: Invalid parameter types
	fmt.Println("  Testing invalid parameter types...")
	invalidArgs, _ := json.Marshal(map[string]interface{}{
		"expression": 12345, // Should be string
	})
	_, err = calcTool.Call(ctx, invalidArgs)
	if err != nil {
		if mcpErr, ok := err.(*tool.MCPError); ok {
			fmt.Printf("  Got enhanced error: %s\n", mcpErr.Code)
			fmt.Printf("  User-friendly message: %s\n", mcpErr.Error())
		} else {
			fmt.Printf("  Got regular error: %v\n", err)
		}
	}
}

func testServerError(ctx context.Context) {
	// Connect to STDIO server.
	config := tool.MCPConnectionConfig{
		Transport: "stdio",
		Command:   "./stdio_server/stdio_server",
		Timeout:   10 * time.Second,
	}

	toolset := tool.NewMCPToolSet(config)
	defer toolset.Close()

	tools := toolset.Tools(ctx)
	if len(tools) == 0 {
		fmt.Println("No tools available for server error test")
		return
	}

	// Try to cause a server error by providing malformed input
	var testTool tool.Tool
	for _, t := range tools {
		testTool = t
		break
	}

	fmt.Printf("Testing with tool: %s\n", testTool.Declaration().Name)

	// This might cause various types of errors depending on the tool.
	malformedArgs, _ := json.Marshal(map[string]interface{}{
		"invalid_param": map[string]interface{}{
			"nested": "deeply_nested_invalid_structure",
		},
	})

	_, err := testTool.Call(ctx, malformedArgs)
	if err != nil {
		if mcpErr, ok := err.(*tool.MCPError); ok {
			fmt.Printf("✓ Got enhanced error code: %s\n", mcpErr.Code)
			fmt.Printf("  User-friendly: %s\n", mcpErr.Error())
			fmt.Printf("  Suggestions: %v\n", mcpErr.Suggestions[:min(3, len(mcpErr.Suggestions))])
		} else {
			fmt.Printf("Got regular error: %v\n", err)
		}
	} else {
		fmt.Println("No error occurred (tool was more flexible than expected)")
	}
}

// getEnv gets an environment variable with a default value.
func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// toolsToMap converts tool array to map for easy use in model.Request.
func toolsToMap(tools []tool.Tool) map[string]tool.Tool {
	toolsMap := make(map[string]tool.Tool, len(tools))
	for _, t := range tools {
		decl := t.Declaration()
		toolsMap[decl.Name] = t
	}
	return toolsMap
}
