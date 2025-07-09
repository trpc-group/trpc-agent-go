package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	mcp "trpc.group/trpc-go/trpc-mcp-go"
)

func main() {
	port := flag.Int("port", 8080, "Listen port")
	flag.Parse()

	// Create MCP SSE server.
	server := mcp.NewSSEServer("SSE Example Server", "1.0.0")

	// Register recipe tool.
	recipeTool := mcp.NewTool("sse_recipe",
		mcp.WithDescription("Chinese recipe query tool"),
		mcp.WithString("dish", mcp.Description("Dish name")),
	)
	server.RegisterTool(recipeTool, handleRecipe)

	// Register health tip tool.
	healthTipTool := mcp.NewTool("sse_health_tip",
		mcp.WithDescription("Health tip tool"),
		mcp.WithString("category", mcp.Description("Category")),
	)
	server.RegisterTool(healthTipTool, handleHealthTip)

	// Handle signals.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalChan
		cancel()
	}()

	// Start server.
	addr := fmt.Sprintf(":%d", *port)
	fmt.Printf("Start SSE server, port: %d\n", *port)
	fmt.Printf("Available tools: sse_recipe, sse_health_tip\n") // Available tools: sse_recipe, sse_health_tip.

	go server.Start(addr)
	<-ctx.Done()
	server.Shutdown(context.Background())
}

// Handle recipe tool.
func handleRecipe(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract dish parameter.
	dish := "Red braised pork"
	if dishArg, ok := req.Params.Arguments["dish"]; ok {
		if dishStr, ok := dishArg.(string); ok && dishStr != "" {
			dish = dishStr
		}
	}

	// Return a simplified but real recipe.
	result := "【Red braised pork】\n" +
		"Main ingredients: 500g pork belly\n" +
		"Seasoning: soy sauce, cooking wine, rock sugar, star anise\n" +
		"Steps:\n" +
		"1. Cut pork into pieces and boil\n" +
		"2. Cook sugar\n" +
		"3. Add pork and cook\n" +
		"4. Add seasoning and water\n" +
		"5. Simmer for 40 minutes\n" +
		"6. Stir-fry"
	log.Printf("Recipe request: dish=%s", dish)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(result),
		},
	}, nil
}

// Handle health tip tool.
func handleHealthTip(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract category parameter.
	category := "general"
	if catArg, ok := req.Params.Arguments["category"]; ok {
		if catStr, ok := catArg.(string); ok && catStr != "" {
			category = catStr
		}
	}

	// Return a simplified tip.
	result := "【Health tips】\n" +
		"1. Balanced diet\n" +
		"2. Regular exercise\n" +
		"3. Sufficient sleep\n" +
		"4. Maintain a good attitude\n" +
		"5. Regular physical examination"
	log.Printf("Health tip request: category=%s", category)

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(result),
		},
	}, nil
}
