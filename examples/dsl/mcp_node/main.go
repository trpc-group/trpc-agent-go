package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/compiler"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Register builtin components
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// This example demonstrates a standalone MCP node wired with Transform
// before and after:
//   Start -> Transform (build MCP args) -> MCP node -> Transform (format summary) -> End.
//
// To run end-to-end, start the example MCP HTTP server first:
//   cd examples/mcptool/streamalbeserver && go run .
//
// Then run this example from examples/dsl:
//   cd examples/dsl && go run ./mcp_node

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	fmt.Println("🚀 MCP Node DSL Example")
	fmt.Println("Chain: Start → Agent → Transform → MCP → Transform → End")
	fmt.Println("==================================================")
	fmt.Println()

	data, err := os.ReadFile("mcp_node/workflow.json")
	if err != nil {
		return fmt.Errorf("failed to read workflow.json: %w", err)
	}

	parser := dsl.NewParser()
	graphDef, err := parser.Parse(data)
	if err != nil {
		return fmt.Errorf("failed to parse workflow.json: %w", err)
	}

	fmt.Printf("✅ Loaded graph: %s\n", graphDef.Name)
	fmt.Printf("   Description: %s\n", graphDef.Description)
	fmt.Printf("   Nodes: %d\n", len(graphDef.Nodes))
	fmt.Println()

	// Compile graph.
	comp := compiler.New(
		compiler.WithAllowEnvSecrets(true),
	)
	compiledGraph, err := comp.Compile(graphDef)
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}
	fmt.Println("✅ Graph compiled successfully")
	fmt.Println()

	// Create GraphAgent.
	graphAgent, err := graphagent.New("mcp-node-example", compiledGraph,
		graphagent.WithDescription("Standalone MCP node with pre/post transforms"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create Runner.
	appRunner := runner.NewRunner("mcp-node-example", graphAgent)
	defer appRunner.Close()

	// Run a single example input.
	userID := "demo-user"
	sessionID := "demo-session"
	input := "Beijing"

	fmt.Printf("🔄 Running graph with input: %q\n\n", input)
	if err := executeGraph(ctx, appRunner, userID, sessionID, input); err != nil {
		return err
	}

	return nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func executeGraph(ctx context.Context, appRunner runner.Runner, userID, sessionID, userInput string) error {
	msg := model.NewUserMessage(userInput)
	events, err := appRunner.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return fmt.Errorf("failed to run graph: %w", err)
	}

	var (
		mcpResult   any
		transformIn any
		endOutput   map[string]any
		lastText    string
	)

	eventIndex := 0
	for ev := range events {
		eventIndex++

		if ev.Error != nil {
			return fmt.Errorf("workflow error: %s", ev.Error.Message)
		}

		if ev.StateDelta != nil {
			// Debug: print state delta keys for each event.
			keys := make([]string, 0, len(ev.StateDelta))
			for k := range ev.StateDelta {
				keys = append(keys, k)
			}
			fmt.Printf("[DEBUG] Event %d: StateDelta keys = %v\n", eventIndex, keys)

			// Inspect transform result from node_structured.prepare_request.output_parsed.
			if raw, ok := ev.StateDelta["node_structured"]; ok {
				fmt.Printf("[DEBUG] Event %d: raw node_structured delta = %s\n", eventIndex, string(raw))

				var ns map[string]any
				if err := json.Unmarshal(raw, &ns); err == nil {
					fmt.Printf("[DEBUG] Event %d: decoded node_structured = %#v\n", eventIndex, ns)

					if nodeRaw, ok := ns["prepare_request"]; ok {
						if nodeMap, ok := nodeRaw.(map[string]any); ok {
							if parsed, ok := nodeMap["output_parsed"]; ok {
								transformIn = parsed
							}
						}
					}

					if nodeRaw, ok := ns["mcp_weather"]; ok {
						if nodeMap, ok := nodeRaw.(map[string]any); ok {
							if parsed, ok := nodeMap["output_parsed"]; ok {
								mcpResult = parsed
							}
						}
					}
				}
			}

			// Inspect final structured output from End node.
			if raw, ok := ev.StateDelta["end_structured_output"]; ok {
				var v map[string]any
				if err := json.Unmarshal(raw, &v); err == nil {
					endOutput = v
					fmt.Printf("[DEBUG] Event %d: end_structured_output delta = %s\n", eventIndex, string(raw))
				}
			}

			// Fallback textual view via last_response.
			if raw, ok := ev.StateDelta[graph.StateKeyLastResponse]; ok {
				var s string
				if err := json.Unmarshal(raw, &s); err == nil {
					lastText = s
				}
			}
		}
	}

	fmt.Println("📥 Transform Output (nodes.prepare_request.output_parsed)")
	fmt.Println("==================================================")
	if transformIn != nil {
		b, _ := json.MarshalIndent(transformIn, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Println("<nil>")
	}
	fmt.Println()

	fmt.Println("🔧 MCP Result (nodes.mcp_weather.output_parsed)")
	fmt.Println("==================================================")
	if mcpResult != nil {
		b, _ := json.MarshalIndent(mcpResult, "", "  ")
		fmt.Println(string(b))
	} else {
		fmt.Println("<nil>")
	}
	fmt.Println()

	fmt.Println("📋 End Structured Output")
	fmt.Println("==================================================")
	if endOutput != nil {
		b, _ := json.MarshalIndent(endOutput, "", "  ")
		fmt.Println(string(b))
	} else if lastText != "" {
		fmt.Println(lastText)
	} else {
		fmt.Println("<none>")
	}
	fmt.Println()

	return nil
}
