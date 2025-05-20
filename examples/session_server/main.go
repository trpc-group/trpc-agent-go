// Package main provides a simple example of a session-aware agent server.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"net/http"

	"trpc.group/trpc-go/trpc-agent-go/agent/agents/react"
	"trpc.group/trpc-go/trpc-agent-go/api"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model/models"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

var (
	// Command line flags
	port          = flag.Int("port", 8080, "HTTP server port")
	modelName     = flag.String("model-name", "gpt-3.5-turbo", "OpenAI model name")
	openaiBaseURL = flag.String("openai-url", "https://api.openai.com/v1", "OpenAI API base URL")
	logLevel      = flag.String("level", "debug", "Log level (debug, info, warn, error, fatal)")
)

func main() {
	// Parse command line flags
	flag.Parse()

	// Set up logging
	log.SetLevel(*logLevel)
	log.Info("Starting session server")

	// Create context that listens for interrupt signals
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Get OpenAI API key
	openAIKey := os.Getenv("OPENAI_API_KEY")
	if openAIKey == "" {
		log.Fatal("OpenAI API key is required. Set it with -api_key flag or OPENAI_API_KEY env var")
	}

	// Create the OpenAI streaming model
	llmModel := models.NewOpenAIStreamingModel(
		*modelName,
		models.WithOpenAIAPIKey(openAIKey),
		models.WithOpenAIBaseURL(*openaiBaseURL),
	)
	log.Infof("Using streaming model. model: %s, base_url: %s", *modelName, *openaiBaseURL)

	// Create a simple calculator tool
	calculatorTool := NewSimpleCalculator()

	// Create the agent
	agentConfig := react.AgentConfig{
		Model:           llmModel,
		Tools:           []tool.Tool{calculatorTool},
		EnableStreaming: true,
	}

	myAgent, err := react.NewAgent(agentConfig)
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}

	// Create in-memory session manager
	sessionManager := session.NewMemoryManager(
		session.WithExpiration(24 * time.Hour),
	)
	log.Info("Using in-memory session storage")

	// Create runner configuration
	runnerConfig := runner.DefaultConfig().
		WithTimeout(2 * time.Minute).
		WithSessionExpiration(24 * time.Hour)

	// Create session-aware runner
	sessionRunner := runner.NewSessionRunner("chat-runner", myAgent, runnerConfig, sessionManager)

	// Start the runner
	if err := sessionRunner.Start(ctx); err != nil {
		log.Fatalf("Failed to start runner: %v", err)
	}
	defer sessionRunner.Stop(context.Background())

	// Create API server
	server := api.NewServer(sessionRunner)

	// Create HTTP server
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: server.Handler(),
	}

	// Start HTTP server in a goroutine
	go func() {
		log.Infof("Starting server. port: %d", *port)
		log.Info("Example usage: curl -X POST http://localhost:8080/sessions")
		log.Info("Then: curl -X POST http://localhost:8080/sessions/YOUR_SESSION_ID/run -H \"Content-Type: application/json\" -d '{\"message\":\"Hello, AI!\"}'")
		log.Info("For streaming: curl -X POST http://localhost:8080/sessions/YOUR_SESSION_ID/run_stream -H \"Content-Type: application/json\" -d '{\"message\":\"Hello, AI!\"}'")

		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("Server error: %v", err)
		}
	}()

	// Wait for context cancellation (SIGINT or SIGTERM)
	<-ctx.Done()
	log.Info("Shutting down server...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Shutdown server gracefully
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Errorf("Server shutdown error: %v", err)
	}

	log.Info("Server stopped")
}

// SimpleCalculator is a basic calculator tool for demonstration purposes.
type SimpleCalculator struct {
	tool.BaseTool
}

// NewSimpleCalculator creates a new calculator tool.
func NewSimpleCalculator() *SimpleCalculator {
	parameters := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"operation": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"add", "subtract", "multiply", "divide"},
				"description": "The arithmetic operation to perform",
			},
			"a": map[string]interface{}{
				"type":        "number",
				"description": "The first operand",
			},
			"b": map[string]interface{}{
				"type":        "number",
				"description": "The second operand",
			},
		},
		"required": []string{"operation", "a", "b"},
	}

	return &SimpleCalculator{
		BaseTool: *tool.NewBaseTool(
			"calculator",
			"Performs basic arithmetic operations like add, subtract, multiply, and divide",
			parameters,
		),
	}
}

// Execute performs the calculator operation.
func (t *SimpleCalculator) Execute(ctx context.Context, args map[string]interface{}) (*tool.Result, error) {
	// Extract operation and operands
	operation, ok := args["operation"].(string)
	if !ok {
		return nil, fmt.Errorf("operation must be a string")
	}

	a, okA := args["a"].(float64)
	if !okA {
		return nil, fmt.Errorf("a must be a number")
	}

	b, okB := args["b"].(float64)
	if !okB {
		return nil, fmt.Errorf("b must be a number")
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
			return nil, fmt.Errorf("division by zero")
		}
		result = a / b
	default:
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}

	return tool.NewResult(result), nil
}
