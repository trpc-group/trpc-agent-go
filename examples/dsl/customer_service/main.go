package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	_ "trpc.group/trpc-go/trpc-agent-go/dsl/registry/builtin" // Import to register builtin components
	"trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// Simple interactive CLI to demonstrate builtin.user_approval with interrupt & resume.

func main() {
	if err := runInteractive(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func runInteractive() error {
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
	fmt.Printf("✅ Graph loaded: %s\n", graphDef.Name)

	// Compile
	compiler := dsl.NewCompiler(registry.DefaultRegistry).
		WithModelRegistry(modelRegistry)

	graphCompiled, err := compiler.Compile(&graphDef)
	if err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}
	fmt.Println("✅ Graph compiled successfully")

	// Checkpoint saver & agent with checkpoint support.
	saver := checkpointinmemory.NewSaver()
	ga, err := graphagent.New("openai-custom-service", graphCompiled,
		graphagent.WithDescription("Custom service graph with user approval (builtin.user_approval)"),
		graphagent.WithCheckpointSaver(saver),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	manager := ga.Executor().CheckpointManager()
	if manager == nil {
		return fmt.Errorf("checkpoint manager not configured")
	}

	sessSvc := inmemory.NewSessionService()
	r := runner.NewRunner("openai-custom-service-graph", ga, runner.WithSessionService(sessSvc))
	defer r.Close()

	fmt.Println()
	fmt.Println("🚀 Customer Service DSL Graph Example (with user approval)")
	fmt.Println("Commands:")
	fmt.Println("  run [text]    - run graph until first interrupt (optional custom customer message)")
	fmt.Println("  resume <ans>  - resume with approval decision (approve/reject)")
	fmt.Println("  quit          - exit")
	fmt.Println()
	fmt.Println("Example customer messages:")
	fmt.Println(`  run I want to return my phone, it stopped working yesterday.`)
	fmt.Println(`  run I'm thinking about cancelling my mobile plan, can you offer me a better deal?`)
	fmt.Println(`  run Can you explain how billing adjustments work if I'm overcharged?`)
	fmt.Println()

	lineageID := ""
	namespace := graph.DefaultCheckpointNamespace
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
		parts := strings.Fields(line)
		cmd := parts[0]

		switch cmd {
		case "quit", "exit":
			return nil

		case "run":
			// Generate a new lineage ID for each run.
			lineageID = fmt.Sprintf("cust-%d", time.Now().Unix())
			fmt.Printf("🧬 Lineage: %s\n", lineageID)
			// If user provided a custom query after 'run', join the rest of the line.
			query := defaultRetentionQuery()
			if len(parts) > 1 {
				query = strings.Join(parts[1:], " ")
			}
			if err := runOnce(ctx, r, manager, userID, sessionID, lineageID, namespace, query); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case "resume":
			if lineageID == "" {
				fmt.Println("⚠️  No lineage to resume. Run the graph first.")
				continue
			}
			if len(parts) < 2 {
				fmt.Println("Usage: resume <approve|reject>")
				continue
			}
			decision := parts[1]
			if err := resumeOnce(ctx, r, manager, userID, sessionID, lineageID, namespace, decision); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		default:
			fmt.Println("Unknown command. Available: run | resume <approve|reject> | quit")
		}
	}

	return reader.Err()
}

// runOnce executes the graph until completion or first interrupt.
func runOnce(
	ctx context.Context,
	r runner.Runner,
	manager *graph.CheckpointManager,
	userID, sessionID, lineageID, namespace, input string,
) error {
	msg := model.NewUserMessage(input)

	runtimeState := graph.State{
		graph.CfgKeyLineageID:    lineageID,
		graph.CfgKeyCheckpointNS: namespace,
	}

	events, err := r.Run(ctx, userID, sessionID, msg, agent.WithRuntimeState(runtimeState))
	if err != nil {
		return fmt.Errorf("failed to run graph: %w", err)
	}

	var lastResponse string
	var endOutput map[string]any
	for ev := range events {
		if ev.Error != nil {
			// Interrupts are handled via checkpoints, not error events, so we just print any errors.
			fmt.Printf("❌ Event error: %s\n", ev.Error.Message)
			continue
		}

		if len(ev.Choices) > 0 {
			choice := ev.Choices[0]
			if choice.Delta.Content != "" {
				fmt.Print(choice.Delta.Content)
			}
		}

		if ev.StateDelta != nil {
			// Log structured output from the classifier (per-node cache).
			if raw, ok := ev.StateDelta["node_structured"]; ok {
				var ns map[string]any
				if err := json.Unmarshal(raw, &ns); err == nil {
					if nodeOut, ok := ns["classifier"].(map[string]any); ok {
						if op, ok := nodeOut["output_parsed"].(map[string]any); ok {
							full, _ := json.Marshal(op)
							if cls, _ := op["classification"].(string); cls != "" {
								fmt.Printf("\n[CS][structured_output] classification=%q full=%s\n", cls, string(full))
							}
						}
					}
				}
			}
			if lastRespBytes, ok := ev.StateDelta[graph.StateKeyLastResponse]; ok {
				var resp string
				if err := json.Unmarshal(lastRespBytes, &resp); err == nil {
					lastResponse = resp
				}
			}
			if eoBytes, ok := ev.StateDelta["end_structured_output"]; ok {
				var eo map[string]any
				if err := json.Unmarshal(eoBytes, &eo); err == nil {
					endOutput = eo
				}
			}
		}
	}

	fmt.Println()

	// Check if there is an interrupt checkpoint.
	cfg := graph.NewCheckpointConfig(lineageID).WithNamespace(namespace)
	checkpoints, err := manager.ListCheckpoints(ctx, cfg.ToMap(), nil)
	if err != nil {
		return fmt.Errorf("failed to list checkpoints: %w", err)
	}

	interrupted := false
	for _, cp := range checkpoints {
		if cp.Metadata.Source == "interrupt" && cp.Checkpoint != nil && cp.Checkpoint.IsInterrupted() {
			interrupted = true
			break
		}
	}

	if interrupted {
		fmt.Println("💾 Execution interrupted at user approval.")
		// Show the agent's response that led to this approval step.
		if strings.TrimSpace(lastResponse) != "" {
			fmt.Printf("\n🤖 Agent response before approval:\n%s\n\n", lastResponse)
		}
		// Try to show the approval prompt message from the interrupt checkpoint.
		var promptMsg string
		for _, cp := range checkpoints {
			if cp.Metadata.Source == graph.CheckpointSourceInterrupt &&
				cp.Checkpoint != nil && cp.Checkpoint.InterruptState != nil &&
				cp.Checkpoint.InterruptState.InterruptValue != nil {
				if payload, ok := cp.Checkpoint.InterruptState.InterruptValue.(map[string]any); ok {
					if msg, ok := payload["message"].(string); ok && strings.TrimSpace(msg) != "" {
						promptMsg = msg
						break
					}
				}
			}
		}
		if promptMsg != "" {
			fmt.Printf("💬 Approval message: %s\n", promptMsg)
		}
		fmt.Println("   Use: resume <approve|reject> to continue.")
	} else if endOutput != nil && len(endOutput) > 0 {
		if b, err := json.MarshalIndent(endOutput, "", "  "); err == nil {
			fmt.Printf("\n🏁 End structured output:\n%s\n\n", string(b))
		} else {
			fmt.Printf("\n🏁 End structured output: %+v\n\n", endOutput)
		}
	} else if strings.TrimSpace(lastResponse) != "" {
		fmt.Printf("\n🤖 Response:\n%s\n\n", lastResponse)
	} else {
		fmt.Println("\n🤖 No final response captured.\n")
	}

	return nil
}

// resumeOnce resumes execution from the latest interrupt for the given lineage.
func resumeOnce(
	ctx context.Context,
	r runner.Runner,
	manager *graph.CheckpointManager,
	userID, sessionID, lineageID, namespace, decision string,
) error {
	// Find latest interrupted checkpoint to extract TaskID.
	latest, err := manager.Latest(ctx, lineageID, namespace)
	if err != nil {
		return fmt.Errorf("failed to get latest checkpoint: %w", err)
	}
	if latest == nil || latest.Checkpoint == nil || latest.Checkpoint.InterruptState == nil {
		return fmt.Errorf("no interrupted checkpoint found for lineage: %s", lineageID)
	}

	taskID := latest.Checkpoint.InterruptState.TaskID
	if taskID == "" {
		return fmt.Errorf("interrupt checkpoint has empty TaskID")
	}

	// Build resume command with decision.
	cmd := &graph.Command{
		ResumeMap: map[string]any{
			taskID: decision,
		},
	}

	runtimeState := graph.State{
		graph.StateKeyCommand:    cmd,
		graph.CfgKeyLineageID:    lineageID,
		graph.CfgKeyCheckpointNS: namespace,
	}

	// Use a synthetic "resume" message, matching the graph interrupt example.
	msg := model.NewUserMessage("resume")

	events, err := r.Run(ctx, userID, sessionID, msg, agent.WithRuntimeState(runtimeState))
	if err != nil {
		return fmt.Errorf("resume failed: %w", err)
	}

	var lastResponse string
	var endOutput map[string]any
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

		if ev.StateDelta != nil {
			if lastRespBytes, ok := ev.StateDelta[graph.StateKeyLastResponse]; ok {
				var resp string
				if err := json.Unmarshal(lastRespBytes, &resp); err == nil {
					lastResponse = resp
				}
			}
			if eoBytes, ok := ev.StateDelta["end_structured_output"]; ok {
				var eo map[string]any
				if err := json.Unmarshal(eoBytes, &eo); err == nil {
					endOutput = eo
				}
			}
		}
	}

	fmt.Println()
	if endOutput != nil && len(endOutput) > 0 {
		if b, err := json.MarshalIndent(endOutput, "", "  "); err == nil {
			fmt.Printf("\n🏁 End structured output after approval (%q):\n%s\n\n", decision, string(b))
		} else {
			fmt.Printf("\n🏁 End structured output after approval (%q): %+v\n\n", decision, endOutput)
		}
	} else if strings.TrimSpace(lastResponse) != "" {
		fmt.Printf("\n🤖 Response after approval:\n%s\n\n", lastResponse)
	} else {
		fmt.Printf("\n✅ Resume completed with decision %q.\n\n", decision)
	}

	return nil
}

// defaultRetentionQuery returns a sample query that typically routes to the
// retention path (cancel_subscription) so that user_approval is exercised.
func defaultRetentionQuery() string {
	return "I'm thinking about cancelling my mobile plan, can you offer me a better deal?"
}
