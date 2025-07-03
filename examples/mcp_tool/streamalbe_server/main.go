// Package main provides a simple MCP server example for testing MCP tool integration.
package main

import (
	"context"
	"fmt"
	"log"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	// Create MCP server.
	server := mcp.NewServer("mcp-example-server", "1.0.0", mcp.WithServerAddress(":3000"))

	// Register a weather tool (returns fake data)
	weatherTool := mcp.NewTool(
		"get_weather",
		mcp.WithDescription("Get current weather for a location"),
		mcp.WithString("location", mcp.Description("City name or location"), mcp.Required()),
	)

	server.RegisterTool(weatherTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		location, ok := req.Params.Arguments["location"].(string)
		if !ok || location == "" {
			return mcp.NewErrorResult("location parameter is required"), nil
		}

		// Simple fake weather response
		result := fmt.Sprintf("Weather for %s: 22°C, Sunny, Humidity: 45%%, Wind: 10 km/h", location)
		return mcp.NewTextResult(result), nil
	})

	// Register a news tool (returns fake data)
	newsTool := mcp.NewTool(
		"get_news",
		mcp.WithDescription("Get latest news headlines"),
		mcp.WithString("category", mcp.Description("News category"), mcp.Default("general")),
	)

	server.RegisterTool(newsTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		category, _ := req.Params.Arguments["category"].(string)
		if category == "" {
			category = "general"
		}

		// Simple fake news response
		result := fmt.Sprintf("Latest %s news headlines:\n", category)
		result += "1. Global Summit on Climate Change Concludes with New Agreements\n"
		result += "2. International Space Station Celebrates 25 Years in Orbit\n"
		result += "3. New Study Reveals Benefits of Mediterranean Diet\n"

		return mcp.NewTextResult(result), nil
	})

	// Start HTTP server.

	if err := server.Start(); err != nil {
		log.Fatalf("Server failed to start: %v", err)
	}

	fmt.Printf("Starting MCP server on http://localhost:%d\n", 3000)
	fmt.Println("Available tools:")
	fmt.Println("- get_weather: Get current weather for a location (fake data)")
	fmt.Println("- get_news: Get latest news headlines (fake data)")
}
