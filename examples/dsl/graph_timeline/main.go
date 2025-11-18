package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Register builtin components
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Example: demonstrate how to build a per-node run timeline
// by consuming graph events (graph.node.start / complete / error)
// and inspecting StateDelta + NodeExecutionMetadata.

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	fmt.Println("🚀 Graph Timeline Example")
	fmt.Println("==================================================")
	fmt.Println()

	// Configure model registry for builtin.llmagent nodes.
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
	fmt.Println()

	// Load graph definition from JSON.
	data, err := os.ReadFile("workflow.json")
	if err != nil {
		return fmt.Errorf("failed to read workflow.json: %w", err)
	}

	var graphDef dsl.Graph
	if err := json.Unmarshal(data, &graphDef); err != nil {
		return fmt.Errorf("failed to parse workflow.json: %w", err)
	}

	fmt.Printf("✅ Loaded graph: %s\n", graphDef.Name)
	fmt.Printf("   Description: %s\n", graphDef.Description)
	fmt.Printf("   Nodes: %d\n", len(graphDef.Nodes))
	fmt.Println()

	// Compile graph with ModelRegistry so builtin.llmagent can resolve model_name.
	compiler := dsl.NewCompiler(registry.DefaultRegistry).
		WithModelRegistry(modelRegistry)

	compiledGraph, err := compiler.Compile(&graphDef)
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}
	fmt.Println("✅ Graph compiled successfully")
	fmt.Println()

	// Create GraphAgent.
	graphAgent, err := graphagent.New("graph-timeline", compiledGraph,
		graphagent.WithDescription("Demonstrates building a run timeline from graph events"),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Create Runner.
	appRunner := runner.NewRunner("graph-timeline", graphAgent)
	defer appRunner.Close()

	// Run a single example input.
	userID := "demo-user"
	sessionID := "demo-session"
	input := "Hello from graph_timeline example"

	fmt.Printf("🔄 Running graph with input: %q\n\n", input)
	if err := executeWithTimeline(ctx, appRunner, userID, sessionID, input); err != nil {
		return err
	}

	return nil
}

func executeWithTimeline(ctx context.Context, appRunner runner.Runner, userID, sessionID, userInput string) error {
	msg := model.NewUserMessage(userInput)
	events, err := appRunner.Run(ctx, userID, sessionID, msg)
	if err != nil {
		return fmt.Errorf("failed to run graph: %w", err)
	}

	var (
		endOutput    map[string]any
		lastResponse string
	)

	fmt.Println("📜 Node run timeline")
	fmt.Println("==================================================")

	for ev := range events {
		if ev.Error != nil {
			return fmt.Errorf("graph error: %s", ev.Error.Message)
		}

		// Print timeline entry when a node starts/completes/errors.
		printNodeTimelineEvent(ev)

		// Also capture final outputs for convenience.
		if ev.StateDelta != nil {
			if raw, ok := ev.StateDelta["end_structured_output"]; ok {
				var v map[string]any
				if err := json.Unmarshal(raw, &v); err == nil {
					endOutput = v
				}
			}
			if raw, ok := ev.StateDelta[graph.StateKeyLastResponse]; ok {
				var s string
				if err := json.Unmarshal(raw, &s); err == nil {
					lastResponse = s
				}
			}
		}
	}

	fmt.Println()
	fmt.Println("📋 Final output")
	fmt.Println("==================================================")
	if endOutput != nil {
		b, _ := json.MarshalIndent(endOutput, "", "  ")
		fmt.Println(string(b))
	} else if lastResponse != "" {
		fmt.Println(lastResponse)
	} else {
		fmt.Println("<none>")
	}

	return nil
}

// printNodeTimelineEvent renders a single event into a human-readable
// timeline entry, using NodeExecutionMetadata and StateDelta.
func printNodeTimelineEvent(ev *event.Event) {
	switch ev.Object {
	case graph.ObjectTypeGraphNodeStart,
		graph.ObjectTypeGraphNodeComplete,
		graph.ObjectTypeGraphNodeError:
	default:
		return
	}

	if ev.StateDelta == nil {
		return
	}

	raw, ok := ev.StateDelta[graph.MetadataKeyNode]
	if !ok {
		return
	}

	var meta graph.NodeExecutionMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		fmt.Printf("  [warn] failed to decode node metadata: %v\n", err)
		return
	}

	ts := ev.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}

	header := fmt.Sprintf("[%s] node=%s type=%s phase=%s",
		ts.Format(time.RFC3339),
		meta.NodeID,
		meta.NodeType,
		meta.Phase,
	)

	switch meta.Phase {
	case graph.ExecutionPhaseStart:
		fmt.Println(header)

	case graph.ExecutionPhaseComplete:
		fmt.Printf("%s duration=%s\n", header, meta.Duration)
		// Print outputs associated with this event.
		for _, key := range meta.OutputKeys {
			if data, ok := ev.StateDelta[key]; ok {
				preview := string(data)
				if len(preview) > 200 {
					preview = preview[:200] + "..."
				}
				fmt.Printf("  ↳ output %s: %s\n", key, preview)
			}
		}

	case graph.ExecutionPhaseError:
		fmt.Printf("%s ERROR=%s\n", header, meta.Error)
	}
}
