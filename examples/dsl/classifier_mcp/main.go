package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/compiler"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Import to register builtin components
	dslvalidator "trpc.group/trpc-go/trpc-agent-go/dsl/validator"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func main() {
	if err := runInteractive(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runInteractive() error {
	ctx := context.Background()

	// Load graph definition
	data, err := os.ReadFile("workflow.json")
	if err != nil {
		return fmt.Errorf("failed to read workflow.json: %w", err)
	}
	parser := dsl.NewParser()
	graphDef, err := parser.Parse(data)
	if err != nil {
		return fmt.Errorf("failed to parse workflow.json: %w", err)
	}
	fmt.Printf("✅ Graph loaded: %s\n", graphDef.Name)

	// Validate graph definition
	validator := dslvalidator.New()
	if err := validator.Validate(graphDef); err != nil {
		return fmt.Errorf("graph validation failed: %w", err)
	}
	fmt.Println("✅ Graph validated successfully")

	// Compile
	comp := compiler.New(
		compiler.WithAllowEnvSecrets(true),
	)

	graphCompiled, err := comp.Compile(graphDef)
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}
	fmt.Println("✅ Graph compiled successfully")

	// Create graph agent
	ga, err := graphagent.New("classifier-mcp-demo", graphCompiled,
		graphagent.WithDescription("Classifier agent with MCP calculator tools"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	r := runner.NewRunner("classifier-mcp-graph", ga)
	defer r.Close()

	fmt.Println()
	fmt.Println("🚀 Classifier MCP Example")
	fmt.Println("This example demonstrates a classifier agent that routes to two worker agents,")
	fmt.Println("each equipped with MCP calculator tools (SSE transport).")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  <query>  - Enter a math query to process")
	fmt.Println("  quit     - Exit the program")
	fmt.Println()
	fmt.Println("Example queries:")
	fmt.Println("  Simple math (add/subtract):  1 + 1, 5 - 3, add 10 and 20")
	fmt.Println("  Complex math (multiply/divide): 5 * 6, 10 / 2, calculate 3 times 4")
	fmt.Println()

	userID := "user"
	sessionID := "session-1"

	reader := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !reader.Scan() {
			break
		}
		line := strings.TrimSpace(reader.Text())
		if line == "" {
			continue
		}

		if line == "quit" || line == "exit" {
			fmt.Println("👋 Goodbye!")
			return nil
		}

		if err := runQuery(ctx, r, userID, sessionID, line); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}

	return reader.Err()
}

func runQuery(ctx context.Context, r runner.Runner, userID, sessionID, input string) error {
	msg := model.NewUserMessage(input)

	events, err := r.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return fmt.Errorf("failed to run graph: %w", err)
	}

	var lastResponse string
	for ev := range events {
		if ev.Error != nil {
			fmt.Printf("❌ Event error: %s\n", ev.Error.Message)
			continue
		}

		if len(ev.Choices) > 0 {
			choice := ev.Choices[0]
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
			}
		}

		// Log structured output from the classifier
		if ev.StateDelta != nil {
			if raw, ok := ev.StateDelta["node_structured"]; ok {
				var ns map[string]any
				if err := json.Unmarshal(raw, &ns); err == nil {
					if nodeOut, ok := ns["classifier"].(map[string]any); ok {
						if op, ok := nodeOut["output_parsed"].(map[string]any); ok {
							if cls, _ := op["classification"].(string); cls != "" {
								reason, _ := op["reason"].(string)
								fmt.Printf("\n📋 [Classifier] classification=%q reason=%q\n", cls, reason)
							}
						}
					}
				}
			}
			if lastRespBytes, ok := ev.StateDelta["last_response"]; ok {
				var resp string
				if err := json.Unmarshal(lastRespBytes, &resp); err == nil {
					lastResponse = resp
				}
			}
		}
	}

	fmt.Println()
	if strings.TrimSpace(lastResponse) != "" {
		fmt.Printf("🤖 Final Response:\n%s\n", lastResponse)
	}

	return nil
}
