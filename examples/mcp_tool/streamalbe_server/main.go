// Package main provides a simple MCP server example for testing MCP tool integration.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	// Create MCP server.
	server := mcp.NewServer("mcp-example-server", "1.0.0")

	// Register a simple echo tool
	echoTool := mcp.NewTool(
		"echo",
		mcp.WithDescription("Echoes the input message with optional prefix"),
		mcp.WithString("message", mcp.Description("The message to echo"), mcp.Required()),
		mcp.WithString("prefix", mcp.Description("Optional prefix to add"), mcp.Default("Echo: ")),
	)

	server.RegisterTool(echoTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		message, ok := req.Params.Arguments["message"].(string)
		if !ok {
			return mcp.NewErrorResult("message parameter is required and must be a string"), nil
		}

		prefix, _ := req.Params.Arguments["prefix"].(string)
		if prefix == "" {
			prefix = "Echo: "
		}

		result := prefix + message
		return mcp.NewTextResult(result), nil
	})

	// Register a greeting tool.
	greetTool := mcp.NewTool(
		"greet",
		mcp.WithDescription("Generates a greeting message"),
		mcp.WithString("name", mcp.Description("Name of the person to greet"), mcp.Required()),
		mcp.WithString("language", mcp.Description("Language for greeting"), mcp.Default("en"), mcp.Enum("en", "zh", "es", "fr")),
	)

	server.RegisterTool(greetTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, ok := req.Params.Arguments["name"].(string)
		if !ok {
			return mcp.NewErrorResult("name parameter is required and must be a string"), nil
		}

		language, _ := req.Params.Arguments["language"].(string)
		if language == "" {
			language = "en"
		}

		var greeting string
		switch language {
		case "en":
			greeting = fmt.Sprintf("Hello, %s!", name)
		case "zh":
			greeting = fmt.Sprintf("你好，%s！", name)
		case "es":
			greeting = fmt.Sprintf("¡Hola, %s!", name)
		case "fr":
			greeting = fmt.Sprintf("Bonjour, %s!", name)
		default:
			greeting = fmt.Sprintf("Hello, %s!", name)
		}

		return mcp.NewTextResult(greeting), nil
	})

	// Register a time tool.
	timeTool := mcp.NewTool(
		"current_time",
		mcp.WithDescription("Returns the current time"),
		mcp.WithString("timezone", mcp.Description("Timezone (e.g., UTC, Local)"), mcp.Default("Local")),
		mcp.WithString("format", mcp.Description("Time format"), mcp.Default("2006-01-02 15:04:05")),
	)

	server.RegisterTool(timeTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		timezone, _ := req.Params.Arguments["timezone"].(string)
		format, _ := req.Params.Arguments["format"].(string)

		if timezone == "" {
			timezone = "Local"
		}
		if format == "" {
			format = "2006-01-02 15:04:05"
		}

		var t time.Time
		if strings.ToLower(timezone) == "utc" {
			t = time.Now().UTC()
		} else {
			t = time.Now()
		}

		result := t.Format(format)
		return mcp.NewTextResult(result), nil
	})

	// Register a math tool.
	mathTool := mcp.NewTool(
		"calculate",
		mcp.WithDescription("Performs basic mathematical operations"),
		mcp.WithNumber("a", mcp.Description("First number"), mcp.Required()),
		mcp.WithNumber("b", mcp.Description("Second number"), mcp.Required()),
		mcp.WithString("operation", mcp.Description("Mathematical operation"), mcp.Required(), mcp.Enum("add", "subtract", "multiply", "divide")),
	)

	server.RegisterTool(mathTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, aOk := req.Params.Arguments["a"].(float64)
		b, bOk := req.Params.Arguments["b"].(float64)
		operation, opOk := req.Params.Arguments["operation"].(string)

		if !aOk || !bOk || !opOk {
			return mcp.NewErrorResult("Invalid parameters: a and b must be numbers, operation must be a string"), nil
		}

		var result float64

		switch operation {
		case "add":
			result = a + b
		case "subtract":
			result = a - b
		case "multiply":
			result = a * b
		case "divide":
			if b == 0 {
				return mcp.NewErrorResult("Cannot divide by zero"), nil
			}
			result = a / b
		default:
			return mcp.NewErrorResult("Invalid operation. Supported: add, subtract, multiply, divide"), nil
		}

		return mcp.NewTextResult(fmt.Sprintf("%.2f", result)), nil
	})

	// Register environment info tool.
	envTool := mcp.NewTool(
		"env_info",
		mcp.WithDescription("Returns information about the environment"),
		mcp.WithString("type", mcp.Description("Type of info to return"), mcp.Default("all"), mcp.Enum("all", "hostname", "user", "pwd")),
	)

	server.RegisterTool(envTool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		infoType, _ := req.Params.Arguments["type"].(string)
		if infoType == "" {
			infoType = "all"
		}

		info := make(map[string]string)

		switch infoType {
		case "hostname":
			if hostname, err := os.Hostname(); err == nil {
				info["hostname"] = hostname
			}
		case "user":
			info["user"] = os.Getenv("USER")
		case "pwd":
			if pwd, err := os.Getwd(); err == nil {
				info["pwd"] = pwd
			}
		case "all":
			if hostname, err := os.Hostname(); err == nil {
				info["hostname"] = hostname
			}
			info["user"] = os.Getenv("USER")
			if pwd, err := os.Getwd(); err == nil {
				info["pwd"] = pwd
			}
		}

		jsonData, _ := json.MarshalIndent(info, "", "  ")
		return mcp.NewTextResult(string(jsonData)), nil
	})

	// Start HTTP server.
	port := ":3000"
	if p := os.Getenv("PORT"); p != "" {
		port = ":" + p
	}

	fmt.Printf("Starting MCP server on http://localhost%s\n", port)
	fmt.Println("Available tools:")
	fmt.Println("- echo: Echoes messages with optional prefix")
	fmt.Println("- greet: Generates greetings in different languages")
	fmt.Println("- current_time: Returns current time")
	fmt.Println("- calculate: Performs basic math operations")
	fmt.Println("- env_info: Returns environment information")
	fmt.Println()
	fmt.Println("You can test the server with curl:")
	fmt.Printf("curl -X POST http://localhost%s/mcp -H 'Content-Type: application/json' -d '{\"method\":\"tools/list\",\"jsonrpc\":\"2.0\",\"id\":1}'\n", port)

	log.Fatal(http.ListenAndServe(port, server.Handler()))
}
