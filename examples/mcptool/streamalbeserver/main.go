//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides a simple MCP server example for testing MCP tool integration.
package main

import (
	"context"
	"fmt"
	"log"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

// WeatherRequest represents the input structure for weather tool
type WeatherRequest struct {
	Location string `json:"location" jsonschema:"required,description=City name or location"`
	Units    string `json:"units,omitempty" jsonschema:"description=Temperature units,enum=celsius,enum=fahrenheit,default=celsius"`
}

// WeatherResponse represents the output structure for weather tool
type WeatherResponse struct {
	Location    string `json:"location" jsonschema:"required,description=Requested location"`
	Temperature int    `json:"temperature" jsonschema:"required,description=Current temperature in degrees"`
	Condition   string `json:"condition" jsonschema:"required,description=Weather condition"`
	Humidity    int    `json:"humidity" jsonschema:"required,description=Humidity percentage"`
	WindSpeed   int    `json:"windSpeed" jsonschema:"required,description=Wind speed in km/h"`
	Units       string `json:"units" jsonschema:"required,description=Temperature units"`
}

func main() {
	// Create MCP server.
	server := mcp.NewServer("mcp-example-server", "1.0.0", mcp.WithServerAddress(":3000"))

	// Register a weather tool with struct-first API and OutputSchema
	weatherTool := mcp.NewTool(
		"get_weather",
		mcp.WithDescription("Get current weather for a location with structured output"),
		mcp.WithInputStruct[WeatherRequest](),
		mcp.WithOutputStruct[WeatherResponse](),
	)

	server.RegisterTool(weatherTool, mcp.NewTypedToolHandler(
		func(ctx context.Context, req *mcp.CallToolRequest, input WeatherRequest) (WeatherResponse, error) {
			// Default units if not specified
			units := input.Units
			if units == "" {
				units = "celsius"
			}

			// Simple fake weather response with structured data
			response := WeatherResponse{
				Location:    input.Location,
				Temperature: 22,
				Condition:   "Sunny",
				Humidity:    45,
				WindSpeed:   10,
				Units:       units,
			}

			return response, nil
		},
	))

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
	fmt.Println("- get_weather: Get current weather with structured output (struct-first API + OutputSchema)")
	fmt.Println("- get_news: Get latest news headlines (fake data)")
}
