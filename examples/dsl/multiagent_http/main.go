package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Register builtin components
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("OPENAI_API_KEY environment variable not set")
	}

	modelRegistry := registry.NewModelRegistry()
	modelName := os.Getenv("MODEL_NAME")
	if modelName == "" {
		modelName = "deepseek-chat"
	}

	modelClient := openai.New(
		modelName,
		openai.WithAPIKey(apiKey),
	)
	modelRegistry.MustRegister(modelName, modelClient)
	fmt.Printf("✅ Model registered: %s\n", modelName)

	// Load graph definition
	data, err := os.ReadFile("workflow.json")
	if err != nil {
		return fmt.Errorf("failed to read workflow.json: %w", err)
	}

	var graphDef dsl.Graph
	if err := json.Unmarshal(data, &graphDef); err != nil {
		return fmt.Errorf("failed to parse workflow.json: %w", err)
	}

	fmt.Println("🚀 Multi-Agent HTTP DSL Graph Example")
	fmt.Println("==================================================")
	fmt.Printf("✅ Loaded graph: %s\n", graphDef.Name)
	fmt.Printf("   Description: %s\n", graphDef.Description)
	fmt.Printf("   Nodes: %d\n", len(graphDef.Nodes))
	fmt.Println()

	// Compile graph
	compiler := dsl.NewCompiler(registry.DefaultRegistry).
		WithModelRegistry(modelRegistry)

	compiledGraph, err := compiler.Compile(&graphDef)
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}
	fmt.Println("✅ Graph compiled successfully")
	fmt.Println()

	// Create agent & runner
	graphAgent, err := graphagent.New("multiagent-http", compiledGraph,
		graphagent.WithDescription("Classifier routing HTTP vs chat"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	sessionService := inmemory.NewSessionService()
	appRunner := runner.NewRunner("multiagent-http-graph", graphAgent, runner.WithSessionService(sessionService))
	defer appRunner.Close()

	queries := []string{
		"Please call an HTTP endpoint to echo the text 'hello-http' and then stop.",
		"Just chat with me: give me a short greeting.",
	}

	userID := "user"

	for i, q := range queries {
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("Test %d: %s\n", i+1, q)
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		if err := executeGraph(ctx, appRunner, userID, fmt.Sprintf("session-%d", i+1), q); err != nil {
			return err
		}
	}

	return nil
}

func executeGraph(ctx context.Context, appRunner runner.Runner, userID, sessionID, userInput string) error {
	msg := model.NewUserMessage(userInput)
	events, err := appRunner.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return fmt.Errorf("failed to run graph: %w", err)
	}

	var (
		lastResponse string
		httpStatus   int
		httpBody     string
		streaming    bool
	)

	for ev := range events {
		if ev.Error != nil {
			return fmt.Errorf("graph error: %s", ev.Error.Message)
		}

		if len(ev.Choices) > 0 {
			choice := ev.Choices[0]
			if choice.Delta.Content != "" {
				if !streaming {
					fmt.Print("🤖 ")
					streaming = true
				}
				fmt.Print(choice.Delta.Content)
			}
			if choice.Delta.Content == "" && streaming {
				fmt.Println()
				streaming = false
			}
		}

		if ev.StateDelta != nil {
			// Log structured output classification if present.
			if raw, ok := ev.StateDelta["node_structured"]; ok {
				var ns map[string]map[string]any
				if err := json.Unmarshal(raw, &ns); err == nil {
					// This example uses a classifier node with id "classifier".
					if nodeOut, ok := ns["classifier"]; ok {
						if op, ok := nodeOut["output_parsed"].(map[string]any); ok {
							if cls, _ := op["classification"].(string); cls != "" {
								fmt.Printf("\n[MA][structured_output] classification=%q full=%v\n", cls, op)
							}
						}
					}
				}
			}

			if b, ok := ev.StateDelta[graph.StateKeyLastResponse]; ok {
				var s string
				if err := json.Unmarshal(b, &s); err == nil {
					lastResponse = s
				}
			}
			if scBytes, ok := ev.StateDelta["status_code"]; ok {
				var sc int
				if err := json.Unmarshal(scBytes, &sc); err == nil {
					httpStatus = sc
				}
			}
			if rbBytes, ok := ev.StateDelta["response_body"]; ok {
				var rb string
				if err := json.Unmarshal(rbBytes, &rb); err == nil {
					httpBody = rb
				}
			}
		}
	}

	// Small separator after streaming
	if streaming {
		fmt.Println()
	}

	if httpBody != "" {
		fmt.Println()
		fmt.Println("📊 HTTP Request Result")
		fmt.Println("==================================================")
		fmt.Printf("Status Code: %d\n", httpStatus)
		if httpBody != "" {
			fmt.Println("Response Body (truncated to 400 chars):")
			body := strings.TrimSpace(httpBody)
			if len(body) > 400 {
				fmt.Println(body[:400] + "...")
			} else {
				fmt.Println(body)
			}
		}
	} else if lastResponse != "" {
		fmt.Println()
		fmt.Println("🤖 Chat Response")
		fmt.Println("==================================================")
		fmt.Println(lastResponse)
	}

	fmt.Println()
	return nil
}
