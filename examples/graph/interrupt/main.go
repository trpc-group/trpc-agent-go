//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates comprehensive interrupt and resume functionality
// using the graph package. It shows how to create a graph-based agent that
// can be interrupted at specific points and resumed with user input, using
// Runner for orchestration and GraphAgent for execution.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"reflect"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver (install with: go get github.com/mattn/go-sqlite3)

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	checkpointsqlite "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/sqlite"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	// Default configuration values.
	defaultModelName     = "deepseek-chat"
	defaultUserID        = "interrupt-user"
	defaultAppName       = "interrupt-workflow"
	defaultDBPath        = "interrupt-checkpoints.db"
	defaultLineagePrefix = "interrupt-demo"
	defaultNamespace     = ""

	// State keys for the workflow.
	stateKeyCounter   = "counter"
	stateKeyMessages  = "messages"
	stateKeyUserInput = "user_input"
	stateKeyApproved  = "approved"
	stateKeyStepCount = "step_count"
	stateKeyLastNode  = "last_node"

	// Node names.
	nodeIncrement        = "increment"
	nodeRequestApproval  = "request_approval"
	nodeSecondApproval   = "second_approval"
	nodeProcessApproval  = "process_approval"
	nodeFinalize         = "finalize"

	// Messages.
	msgApprovalRequest    = "Please approve the current state (yes/no):"
	msgSecondApproval     = "This requires a second approval (yes/no):"
	msgUserApproved       = "user approved: %t"
	msgUserRejected       = "user rejected - stopping execution"
	msgUserApprovedCont   = "user approved - continuing execution"
	msgExecutionComplete  = "execution completed successfully"
	msgNodeExecuted       = "Node %s executed at %s"
	msgWorkflowComplete   = "Workflow completed with counter: %d"

	// Commands.
	cmdRun       = "run"
	cmdInterrupt = "interrupt"
	cmdResume    = "resume"
	cmdList      = "list"
	cmdTree      = "tree"
	cmdHistory   = "history"
	cmdLatest    = "latest"
	cmdDelete    = "delete"
	cmdStatus    = "status"
	cmdDemo      = "demo"
	cmdHelp      = "help"
	cmdExit      = "exit"
	cmdQuit      = "quit"
)

var (
	modelName = flag.String("model", defaultModelName,
		"Name of the model to use")
	storage = flag.String("storage", "memory",
		"Storage type: 'memory' or 'sqlite'")
	dbPath = flag.String("db", defaultDBPath,
		"Path to SQLite database file (only used with -storage=sqlite)")
	verbose = flag.Bool("verbose", false,
		"Enable verbose output")
	interactiveMode = flag.Bool("interactive", true,
		"Enable interactive command-line mode")
	lineageFlag = flag.String("lineage", "",
		"Lineage ID for checkpointing (default: auto-generated)")
)

// interruptWorkflow manages a workflow with comprehensive interrupt and resume support.
type interruptWorkflow struct {
	modelName        string
	storageType      string
	dbPath           string
	verbose          bool
	logger           agentlog.Logger
	runner           runner.Runner
	graphAgent       *graphagent.GraphAgent
	saver            graph.CheckpointSaver
	manager          *graph.CheckpointManager
	currentLineageID string
	currentNamespace string
	userID           string
	sessionID        string
}

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🔄 Advanced Interrupt & Resume Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Storage: %s", *storage)
	if *storage == "sqlite" {
		fmt.Printf(" (DB: %s)", *dbPath)
	}
	fmt.Println()
	fmt.Printf("Verbose Mode: %v\n", *verbose)
	fmt.Printf("Interactive Mode: %v\n", *interactiveMode)
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the workflow.
	workflow := &interruptWorkflow{
		modelName:   *modelName,
		storageType: *storage,
		dbPath:      *dbPath,
		verbose:     *verbose,
		userID:      defaultUserID,
	}

	// Setup lineage ID.
	if *lineageFlag != "" {
		workflow.currentLineageID = *lineageFlag
	} else {
		workflow.currentLineageID = fmt.Sprintf("%s-%d", defaultLineagePrefix,
			time.Now().Unix())
	}
	workflow.sessionID = fmt.Sprintf("session-%d", time.Now().Unix())
	workflow.currentNamespace = defaultNamespace

	if err := workflow.run(); err != nil {
		fmt.Printf("❌ Workflow failed: %v\n", err)
		os.Exit(1)
	}
}

// run starts the interrupt workflow.
func (w *interruptWorkflow) run() error {
	ctx := context.Background()

	// Setup the workflow components.
	if err := w.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Start interactive mode.
	if *interactiveMode {
		return w.startInteractiveMode(ctx)
	}

	// Single command execution.
	fmt.Println("Non-interactive mode - use -interactive=true for command-line interface")
	return nil
}

// setup creates the graph agent with interrupt support and runner.
func (w *interruptWorkflow) setup() error {
	// Initialize logger.
	w.logger = agentlog.Default
	if w.verbose {
		w.logger.Infof("Initializing interrupt workflow: storage=%s, model=%s, verbose=%v",
			w.storageType, w.modelName, w.verbose)
	}

	// Create checkpoint saver based on storage type.
	switch w.storageType {
	case "sqlite":
		db, err := sql.Open("sqlite3", w.dbPath)
		if err != nil {
			return fmt.Errorf("failed to open SQLite database: %w", err)
		}
		saver, err := checkpointsqlite.NewSaver(db)
		if err != nil {
			db.Close()
			return fmt.Errorf("failed to create SQLite saver: %w", err)
		}
		w.saver = saver
	case "memory":
		w.saver = checkpointinmemory.NewSaver()
	default:
		return fmt.Errorf("unsupported storage type: %s", w.storageType)
	}
	// Create the workflow graph.
	if w.verbose {
		w.logger.Debugf("Creating interrupt workflow graph")
	}
	interruptGraph, err := w.createInterruptGraph()
	if err != nil {
		return fmt.Errorf("failed to create graph: %w", err)
	}

	// Create GraphAgent with checkpoint support.
	w.graphAgent, err = graphagent.New("interrupt-demo", interruptGraph,
		graphagent.WithDescription("Demonstration of interrupt and resume features"),
		graphagent.WithCheckpointSaver(w.saver),
	)
	if err != nil {
		return fmt.Errorf("failed to create graph agent: %w", err)
	}

	// Get the checkpoint manager from the executor.
	w.manager = w.graphAgent.Executor().CheckpointManager()
	if w.manager == nil {
		return fmt.Errorf("checkpoint manager not configured")
	}

	if w.verbose {
		w.logger.Debugf("Created checkpoint manager successfully")
	}

	// Create session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create Runner.
	w.runner = runner.NewRunner(
		defaultAppName,
		w.graphAgent,
		runner.WithSessionService(sessionService),
	)

	fmt.Printf("✅ Interrupt workflow ready! Type 'help' for commands.\n\n")
	return nil
}

// createInterruptGraph creates the interrupt-capable workflow graph.
func (w *interruptWorkflow) createInterruptGraph() (*graph.Graph, error) {
	// Define schema with comprehensive state tracking.
	schema := graph.NewStateSchema()
	schema.AddField(stateKeyCounter, graph.StateField{
		Type:    reflect.TypeOf(0),
		Reducer: graph.DefaultReducer,
		Default: func() any { return 0 },
	})
	schema.AddField(stateKeyMessages, graph.StateField{
		Type:    reflect.TypeOf([]string{}),
		Reducer: graph.StringSliceReducer,
		Default: func() any { return []string{} },
	})
	schema.AddField(stateKeyUserInput, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
		Default: func() any { return "" },
	})
	schema.AddField(stateKeyApproved, graph.StateField{
		Type:    reflect.TypeOf(false),
		Reducer: graph.DefaultReducer,
		Default: func() any { return false },
	})
	schema.AddField(stateKeyStepCount, graph.StateField{
		Type:    reflect.TypeOf(0),
		Reducer: graph.DefaultReducer,
		Default: func() any { return 0 },
	})
	schema.AddField(stateKeyLastNode, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
		Default: func() any { return "" },
	})

	// Build graph.
	b := graph.NewStateGraph(schema)

	// Node 1: Increment counter and track execution.
	b.AddNode(nodeIncrement, func(ctx context.Context, s graph.State) (any, error) {
		v := getInt(s, stateKeyCounter)
		stepCount := getInt(s, stateKeyStepCount)
		newValue := v + 1
		executionTime := time.Now().Format("15:04:05")
		return graph.State{
			stateKeyCounter:   newValue,
			stateKeyStepCount: stepCount + 1,
			stateKeyLastNode:  nodeIncrement,
			stateKeyMessages: append(getStrs(s, stateKeyMessages),
				fmt.Sprintf(msgNodeExecuted, nodeIncrement, executionTime)),
		}, nil
	})

	// Node 2: Request user approval (first interrupt point).
	b.AddNode(nodeRequestApproval, func(ctx context.Context, s graph.State) (any, error) {
		stepCount := getInt(s, stateKeyStepCount)
		// Create interrupt payload with rich context.
		interruptValue := map[string]any{
			"message":    msgApprovalRequest,
			"counter":    getInt(s, stateKeyCounter),
			"messages":   getStrs(s, stateKeyMessages),
			"step_count": stepCount,
			"node_id":    nodeRequestApproval,
		}

		// Interrupt and wait for user input.
		resumeValue, err := graph.Interrupt(ctx, s, "approval", interruptValue)
		if err != nil {
			return nil, err
		}

		// Process resume value.
		approved := false
		if resumeStr, ok := resumeValue.(string); ok {
			resumeStr = strings.ToLower(strings.TrimSpace(resumeStr))
			approved = resumeStr == "yes" || resumeStr == "y"
		}

		executionTime := time.Now().Format("15:04:05")
		return graph.State{
			stateKeyApproved:  approved,
			stateKeyStepCount: stepCount + 1,
			stateKeyLastNode:  nodeRequestApproval,
			stateKeyMessages: append(getStrs(s, stateKeyMessages),
				fmt.Sprintf(msgNodeExecuted, nodeRequestApproval, executionTime),
				fmt.Sprintf(msgUserApproved, approved)),
		}, nil
	})

	// Node 3: Second approval for complex workflow (optional interrupt).
	b.AddNode(nodeSecondApproval, func(ctx context.Context, s graph.State) (any, error) {
		approved := getBool(s, stateKeyApproved)
		stepCount := getInt(s, stateKeyStepCount)
		
		// Skip second approval if first was rejected.
		if !approved {
			return graph.State{
				stateKeyStepCount: stepCount + 1,
				stateKeyLastNode:  nodeSecondApproval,
				stateKeyMessages: append(getStrs(s, stateKeyMessages),
					"Skipping second approval due to rejection"),
			}, nil
		}

		// Create second interrupt for approved workflows.
		interruptValue := map[string]any{
			"message":    msgSecondApproval,
			"counter":    getInt(s, stateKeyCounter),
			"messages":   getStrs(s, stateKeyMessages),
			"step_count": stepCount,
			"node_id":    nodeSecondApproval,
		}

		// Second interrupt point.
		resumeValue, err := graph.Interrupt(ctx, s, "second_approval", interruptValue)
		if err != nil {
			return nil, err
		}

		// Process second approval.
		secondApproved := false
		if resumeStr, ok := resumeValue.(string); ok {
			resumeStr = strings.ToLower(strings.TrimSpace(resumeStr))
			secondApproved = resumeStr == "yes" || resumeStr == "y"
		}

		executionTime := time.Now().Format("15:04:05")
		return graph.State{
			stateKeyApproved:  secondApproved,
			stateKeyStepCount: stepCount + 1,
			stateKeyLastNode:  nodeSecondApproval,
			stateKeyMessages: append(getStrs(s, stateKeyMessages),
				fmt.Sprintf(msgNodeExecuted, nodeSecondApproval, executionTime),
				fmt.Sprintf("Second approval: %t", secondApproved)),
		}, nil
	})

	// Node 4: Process final approval decision.
	b.AddNode(nodeProcessApproval, func(ctx context.Context, s graph.State) (any, error) {
		approved := getBool(s, stateKeyApproved)
		stepCount := getInt(s, stateKeyStepCount)
		msg := msgUserRejected
		if approved {
			msg = msgUserApprovedCont
		}

		executionTime := time.Now().Format("15:04:05")
		return graph.State{
			stateKeyStepCount: stepCount + 1,
			stateKeyLastNode:  nodeProcessApproval,
			stateKeyMessages: append(getStrs(s, stateKeyMessages),
				fmt.Sprintf(msgNodeExecuted, nodeProcessApproval, executionTime),
				msg),
		}, nil
	})

	// Node 5: Finalize workflow.
	b.AddNode(nodeFinalize, func(ctx context.Context, s graph.State) (any, error) {
		stepCount := getInt(s, stateKeyStepCount)
		counter := getInt(s, stateKeyCounter)
		executionTime := time.Now().Format("15:04:05")
		return graph.State{
			stateKeyStepCount: stepCount + 1,
			stateKeyLastNode:  nodeFinalize,
			stateKeyMessages: append(getStrs(s, stateKeyMessages),
				fmt.Sprintf(msgNodeExecuted, nodeFinalize, executionTime),
				fmt.Sprintf(msgWorkflowComplete, counter)),
		}, nil
	})

	// Setup workflow edges.
	b.SetEntryPoint(nodeIncrement)
	b.SetFinishPoint(nodeFinalize)
	b.AddEdge(nodeIncrement, nodeRequestApproval)
	b.AddEdge(nodeRequestApproval, nodeSecondApproval)
	b.AddEdge(nodeSecondApproval, nodeProcessApproval)
	b.AddEdge(nodeProcessApproval, nodeFinalize)

	return b.Compile()
}

// startInteractiveMode starts the interactive command-line interface.
func (w *interruptWorkflow) startInteractiveMode(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	w.showHelp()

	for {
		fmt.Print("\n🔐 interrupt> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		// Parse command and arguments.
		parts := strings.Fields(input)
		command := strings.ToLower(parts[0])

		switch command {
		case cmdExit, cmdQuit:
			fmt.Println("👋 Goodbye!")
			return nil

		case cmdHelp:
			w.showHelp()

		case cmdRun:
			lineageID := w.currentLineageID
			if len(parts) > 1 {
				lineageID = parts[1]
			} else {
				lineageID = generateLineageID()
			}
			if err := w.runWorkflow(ctx, lineageID, false); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case cmdInterrupt:
			lineageID := w.currentLineageID
			if len(parts) > 1 {
				lineageID = parts[1]
			} else {
				lineageID = generateLineageID()
			}
			if err := w.runWorkflow(ctx, lineageID, true); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case cmdResume:
			if len(parts) < 2 {
				fmt.Println("❌ Usage: resume <lineage-id> [checkpoint-id] [\"user-input\"]")
				continue
			}
			lineageID := parts[1]
			var checkpointID string
			var userInput string
			if len(parts) > 2 {
				if len(parts) == 3 {
					// Could be checkpoint-id or user input
					if parts[2] == "yes" || parts[2] == "no" || parts[2] == "y" || parts[2] == "n" {
						userInput = parts[2]
					} else {
						checkpointID = parts[2]
					}
				} else {
					checkpointID = parts[2]
					userInput = parts[3]
				}
			}
			if err := w.resumeWorkflow(ctx, lineageID, checkpointID, userInput); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case cmdList:
			lineageID := w.currentLineageID
			if len(parts) > 1 {
				lineageID = parts[1]
			}
			if err := w.listCheckpoints(ctx, lineageID); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case cmdLatest:
			lineageID := w.currentLineageID
			if len(parts) > 1 {
				lineageID = parts[1]
			}
			if err := w.showLatestCheckpoint(ctx, lineageID); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case cmdTree:
			lineageID := w.currentLineageID
			if len(parts) > 1 {
				lineageID = parts[1]
			}
			if err := w.showTree(ctx, lineageID); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case cmdHistory:
			lineageID := w.currentLineageID
			if len(parts) > 1 {
				lineageID = parts[1]
			}
			if err := w.showHistory(ctx, lineageID); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case cmdStatus:
			lineageID := w.currentLineageID
			if len(parts) > 1 {
				lineageID = parts[1]
			}
			if err := w.showInterruptStatus(ctx, lineageID); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case cmdDelete:
			if len(parts) < 2 {
				fmt.Println("❌ Usage: delete <lineage-id>")
				continue
			}
			if err := w.deleteLineage(ctx, parts[1]); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case cmdDemo:
			if err := w.runDemo(ctx); err != nil {
				fmt.Printf("❌ Demo failed: %v\n", err)
			}

		default:
			fmt.Printf("❌ Unknown command: %s (type 'help' for commands)\n", command)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scanner error: %w", err)
	}
	return nil
}

// runWorkflow executes the workflow with the given lineage ID.
func (w *interruptWorkflow) runWorkflow(ctx context.Context, lineageID string, waitForInterrupt bool) error {
	startTime := time.Now()
	w.currentLineageID = lineageID
	w.currentNamespace = defaultNamespace

	w.logger.Infof("Starting workflow execution: lineage_id=%s, namespace=%s, wait_for_interrupt=%v", lineageID, w.currentNamespace, waitForInterrupt)

	if waitForInterrupt {
		fmt.Printf("\n🔄 Running workflow until interrupt (lineage: %s)...\n", lineageID)
	} else {
		fmt.Printf("\n🚀 Running workflow normally (lineage: %s)...\n", lineageID)
	}

	// Create initial message.
	message := model.NewUserMessage("start")

	// Create checkpoint config for this lineage.
	config := graph.NewCheckpointConfig(lineageID).
		WithNamespace(w.currentNamespace)

	if w.verbose {
		w.logger.Debugf("Created checkpoint configuration: %+v", config.ToMap())
	}

	// Run the workflow through the runner.
	// Pass lineage_id and checkpoint namespace to enable checkpoint saving.
	events, err := w.runner.Run(
		ctx,
		w.userID,
		w.sessionID,
		message,
		agent.WithRuntimeState(graph.State{
			"lineage_id": lineageID,
			"namespace":  w.currentNamespace,
		}),
	)
	if err != nil {
		return fmt.Errorf("execution failed: %w", err)
	}

	// Process events and track execution.
	count := 0
	interrupted := false
	var lastNode string
	for event := range events {
		if event.Error != nil {
			fmt.Printf("❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Track node execution.
		if event.Author == graph.AuthorGraphNode {
			if event.StateDelta != nil {
				// Check for interrupt metadata.
				if metadata, ok := event.StateDelta[graph.MetadataKeyNode]; ok {
					var nodeMetadata graph.NodeExecutionMetadata
					if err := json.Unmarshal(metadata, &nodeMetadata); err == nil {
						if nodeMetadata.NodeID != "" {
							lastNode = nodeMetadata.NodeID
							if waitForInterrupt {
								fmt.Printf("⚡ Executing: %s\n", nodeMetadata.NodeID)
							}
						}
					}
				}

				// Check for interrupt state.
				if pregelData, ok := event.StateDelta["_pregel_metadata"]; ok {
					if strings.Contains(string(pregelData), "interrupt") {
						if waitForInterrupt {
							fmt.Printf("⚠️  Interrupt detected\n")
						}
						interrupted = true
					}
				}
			}
		}
		count++
	}

	duration := time.Since(startTime)
	if interrupted && waitForInterrupt {
		fmt.Printf("💾 Execution interrupted, checkpoint saved\n")
		fmt.Printf("   Use 'resume %s <yes/no>' to continue\n", lineageID)
	} else {
		fmt.Printf("✅ Workflow execution finished\n")
		if lastNode != "" {
			fmt.Printf("   Last node: %s", lastNode)
		}
		fmt.Printf(", events: %d, duration: %v\n", count, duration)
	}

	return nil
}

// resumeWorkflow resumes execution from a checkpoint.
func (w *interruptWorkflow) resumeWorkflow(ctx context.Context, lineageID, checkpointID, userInput string) error {
	w.currentLineageID = lineageID
	w.currentNamespace = defaultNamespace

	if userInput == "" {
		// Prompt for user input if not provided.
		fmt.Print("Enter approval decision (yes/no): ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			userInput = strings.TrimSpace(scanner.Text())
		}
	}

	if userInput == "" {
		return fmt.Errorf("user input required for resume")
	}

	if checkpointID != "" && checkpointID != "auto" {
		fmt.Printf("\n⏪ Resuming workflow from lineage: %s, checkpoint: %s with input: %s\n", lineageID, checkpointID, userInput)
	} else {
		fmt.Printf("\n⏪ Resuming workflow from lineage: %s with input: %s\n", lineageID, userInput)
	}

	// Create resume command with multiple interrupt keys.
	cmd := &graph.Command{
		ResumeMap: map[string]any{
			"approval":        userInput,
			"second_approval": userInput,
		},
	}

	message := model.NewUserMessage("resume")

	runtimeState := graph.State{
		graph.StateKeyCommand: cmd,
		"lineage_id":          lineageID,
		"namespace":           w.currentNamespace,
	}

	// Add checkpoint ID if specified.
	if checkpointID != "" && checkpointID != "auto" {
		runtimeState["checkpoint_id"] = checkpointID
	}

	events, err := w.runner.Run(
		ctx,
		w.userID,
		w.sessionID,
		message,
		agent.WithRuntimeState(runtimeState),
	)
	if err != nil {
		return fmt.Errorf("resume failed: %w", err)
	}

	// Process events.
	count := 0
	var lastNode string
	for event := range events {
		if event.Error != nil {
			fmt.Printf("❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Track execution.
		if event.Author == graph.AuthorGraphNode {
			if event.StateDelta != nil {
				if metadata, ok := event.StateDelta[graph.MetadataKeyNode]; ok {
					var nodeMetadata graph.NodeExecutionMetadata
					if err := json.Unmarshal(metadata, &nodeMetadata); err == nil {
						if nodeMetadata.NodeID != "" {
							lastNode = nodeMetadata.NodeID
						}
					}
				}
			}
		}
		count++
	}

	fmt.Printf("✅ Resume completed (%d events)\n", count)
	if lastNode != "" {
		fmt.Printf("   Last node: %s\n", lastNode)
	}
	return nil
}

// listCheckpoints lists available checkpoints with interrupt-specific details.
func (w *interruptWorkflow) listCheckpoints(ctx context.Context, lineageID string) error {
	if lineageID == "" {
		return fmt.Errorf("lineage ID required")
	}

	fmt.Printf("\n📋 Checkpoints for lineage: %s\n", lineageID)

	cfg := graph.CreateCheckpointConfig(lineageID, "", w.currentNamespace)
	filter := &graph.CheckpointFilter{Limit: 20}
	items, err := w.manager.ListCheckpoints(ctx, cfg, filter)
	if err != nil {
		return fmt.Errorf("list failed: %w", err)
	}

	if len(items) == 0 {
		fmt.Printf("   No checkpoints found for lineage: %s\n", lineageID)
		return nil
	}

	fmt.Println(strings.Repeat("-", 80))
	for i, item := range items {
		fmt.Printf("%2d. ID: %s\n", i+1, item.Checkpoint.ID)
		fmt.Printf("    Namespace: %s\n", item.Config["namespace"])
		fmt.Printf("    Created: %s | Source: %s | Step: %d\n",
			time.Now().Format("15:04:05"), // Use current time as placeholder since CreatedAt may not be available
			item.Metadata.Source, item.Metadata.Step)

		// Display state summary.
		if state := w.extractRootState(item.Checkpoint); state != nil {
			counter, _ := state[stateKeyCounter].(int)
			stepCount, _ := state[stateKeyStepCount].(int)
			lastNode, _ := state[stateKeyLastNode].(string)
			fmt.Printf("    State: counter=%d, steps=%d, last_node=%s\n",
				counter, stepCount, lastNode)
		}

		// Highlight interrupts with detailed information.
		if item.Checkpoint.IsInterrupted() {
			interruptState := item.Checkpoint.InterruptState
			fmt.Printf("    🔴 INTERRUPTED at node: %s\n", interruptState.NodeID)
			if interruptState.InterruptValue != nil {
				if valueMap, ok := interruptState.InterruptValue.(map[string]any); ok {
					if msg, ok := valueMap["message"]; ok {
						fmt.Printf("    💬 Message: %s\n", msg)
					}
					if nodeID, ok := valueMap["node_id"]; ok {
						fmt.Printf("    🔗 Node ID: %s\n", nodeID)
					}
				}
			}
		} else {
			fmt.Printf("    ✅ Completed checkpoint\n")
		}
	}
	fmt.Println(strings.Repeat("-", 80))

	return nil
}

// showLatestCheckpoint displays detailed information about the latest checkpoint.
func (w *interruptWorkflow) showLatestCheckpoint(ctx context.Context, lineageID string) error {
	if lineageID == "" {
		return fmt.Errorf("lineage ID required")
	}

	fmt.Printf("\n🔍 Latest Checkpoint for lineage: %s\n", lineageID)

	latest, err := w.manager.Latest(ctx, lineageID, w.currentNamespace)
	if err != nil {
		return fmt.Errorf("failed to get latest checkpoint: %w", err)
	}

	if latest == nil {
		fmt.Printf("   No checkpoints found for lineage: %s\n", lineageID)
		return nil
	}

	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("ID: %s\n", latest.Checkpoint.ID)
	fmt.Printf("Namespace: %s\n", latest.Config["namespace"])
	fmt.Printf("Created: %s\n", time.Now().Format("2006-01-02 15:04:05")) // Use current time as placeholder
	fmt.Printf("Source: %s | Step: %d\n", latest.Metadata.Source, latest.Metadata.Step)

	// Display comprehensive state.
	if state := w.extractRootState(latest.Checkpoint); state != nil {
		fmt.Printf("\nState Details:\n")
		for key, value := range state {
			switch v := value.(type) {
			case []string:
				if len(v) > 0 {
					start := len(v) - 3
					if start < 0 {
						start = 0
					}
					fmt.Printf("  %s: %v\n", key, v[start:]) // Show last 3 messages
				} else {
					fmt.Printf("  %s: []\n", key)
				}
			default:
				fmt.Printf("  %s: %v\n", key, v)
			}
		}
	}

	// Display interrupt details if present.
	if latest.Checkpoint.IsInterrupted() {
		fmt.Printf("\n🔴 INTERRUPT DETAILS:\n")
		interruptState := latest.Checkpoint.InterruptState
		fmt.Printf("  Node ID: %s\n", interruptState.NodeID)
		fmt.Printf("  Task ID: %s\n", interruptState.TaskID)
		
		if interruptState.InterruptValue != nil {
			fmt.Printf("  Context:\n")
			if valueMap, ok := interruptState.InterruptValue.(map[string]any); ok {
				for key, value := range valueMap {
					fmt.Printf("    %s: %v\n", key, value)
				}
			} else {
				fmt.Printf("    value: %v\n", interruptState.InterruptValue)
			}
		}
		
		fmt.Printf("\n💡 Resume with: resume %s <yes/no>\n", lineageID)
	}

	fmt.Println(strings.Repeat("=", 80))
	return nil
}

// showTree displays checkpoint tree structure with interrupt indicators.
func (w *interruptWorkflow) showTree(ctx context.Context, lineageID string) error {
	if lineageID == "" {
		return fmt.Errorf("lineage ID required")
	}

	fmt.Printf("\n🌳 Interrupt Checkpoint Tree for lineage: %s\n", lineageID)

	cfg := graph.CreateCheckpointConfig(lineageID, "", w.currentNamespace)
	filter := &graph.CheckpointFilter{Limit: 50}
	items, err := w.manager.ListCheckpoints(ctx, cfg, filter)
	if err != nil {
		return fmt.Errorf("failed to list checkpoints: %w", err)
	}

	if len(items) == 0 {
		fmt.Printf("   No checkpoints found for lineage: %s\n", lineageID)
		return nil
	}

	// Build parent-child relationships.
	checkpointMap := make(map[string]*graph.CheckpointTuple)
	childMap := make(map[string][]*graph.CheckpointTuple)
	var rootCheckpoints []*graph.CheckpointTuple

	for _, item := range items {
		checkpointMap[item.Checkpoint.ID] = item
		if item.Checkpoint.ParentCheckpointID == "" {
			rootCheckpoints = append(rootCheckpoints, item)
		} else {
			childMap[item.Checkpoint.ParentCheckpointID] = append(childMap[item.Checkpoint.ParentCheckpointID], item)
		}
	}

	interruptCount := 0
	for _, item := range items {
		if item.Checkpoint.IsInterrupted() {
			interruptCount++
		}
	}

	fmt.Println(strings.Repeat("-", 80))
	fmt.Printf("Total checkpoints: %d\n", len(items))
	fmt.Printf("Interrupted checkpoints: %d\n", interruptCount)
	fmt.Printf("Branch points: %d\n", len(childMap))
	fmt.Println()

	// Display tree structure.
	for _, root := range rootCheckpoints {
		w.printCheckpointTree(root, "", checkpointMap, childMap, true)
	}

	fmt.Println(strings.Repeat("-", 80))
	return nil
}

// printCheckpointTree recursively prints the checkpoint tree structure.
func (w *interruptWorkflow) printCheckpointTree(checkpoint *graph.CheckpointTuple, prefix string, checkpointMap map[string]*graph.CheckpointTuple, childMap map[string][]*graph.CheckpointTuple, isRoot bool) {
	// Extract state information.
	var counter int
	var lastNode string
	if state := w.extractRootState(checkpoint.Checkpoint); state != nil {
		counter, _ = state[stateKeyCounter].(int)
		lastNode, _ = state[stateKeyLastNode].(string)
	}

	// Create display info.
	shortID := checkpoint.Checkpoint.ID[:8]
	timeStr := time.Now().Format("15:04:05") // Use current time as placeholder
	
	// Choose icon based on interrupt status.
	icon := "📍"
	status := ""
	if checkpoint.Checkpoint.IsInterrupted() {
		icon = "🔴"
		status = " [INTERRUPTED]"
	}

	fmt.Printf("%s%s %s (counter=%d, %s=%s, %s)%s\n", 
		prefix, icon, shortID, counter, "node", lastNode, timeStr, status)

	// Print children.
	children := childMap[checkpoint.Checkpoint.ID]
	for i, child := range children {
		isLast := i == len(children)-1
		childPrefix := prefix
		if isLast {
			childPrefix += "    └── "
		} else {
			childPrefix += "    ├── "
		}
		
		nextPrefix := prefix
		if isLast {
			nextPrefix += "        "
		} else {
			nextPrefix += "    │   "
		}
		
		w.printCheckpointTree(child, childPrefix[:len(childPrefix)-4], checkpointMap, childMap, false)
		
		// Add connection lines for non-last children.
		if !isLast {
			for _, grandChild := range childMap[child.Checkpoint.ID] {
				w.printCheckpointTree(grandChild, nextPrefix, checkpointMap, childMap, false)
			}
		}
	}
}

// showHistory displays execution history with interrupt markers.
func (w *interruptWorkflow) showHistory(ctx context.Context, lineageID string) error {
	if lineageID == "" {
		return fmt.Errorf("lineage ID required")
	}

	fmt.Printf("\n📚 Interrupt History for lineage: %s\n", lineageID)

	cfg := graph.CreateCheckpointConfig(lineageID, "", w.currentNamespace)
	filter := &graph.CheckpointFilter{Limit: 50}
	items, err := w.manager.ListCheckpoints(ctx, cfg, filter)
	if err != nil {
		return fmt.Errorf("failed to list checkpoints: %w", err)
	}

	if len(items) == 0 {
		fmt.Printf("   No checkpoints found for lineage: %s\n", lineageID)
		return nil
	}

	// Sort by step.
	sortedItems := make([]*graph.CheckpointTuple, len(items))
	copy(sortedItems, items)
	
	fmt.Println(strings.Repeat("=", 80))

	for _, item := range sortedItems {
		timeStr := time.Now().Format("15:04:05") // Use current time as placeholder
		fmt.Printf("\n⏰ %s (Step %d)\n", timeStr, item.Metadata.Step)
		
		// Extract state.
		if state := w.extractRootState(item.Checkpoint); state != nil {
			counter, _ := state[stateKeyCounter].(int)
			stepCount, _ := state[stateKeyStepCount].(int)
			lastNode, _ := state[stateKeyLastNode].(string)
			
			fmt.Printf("   State: counter=%d, total_steps=%d\n", counter, stepCount)
			if lastNode != "" {
				fmt.Printf("   Action: %s executed\n", lastNode)
			}
			
			// Show recent messages.
			if messages, ok := state[stateKeyMessages].([]string); ok && len(messages) > 0 {
				fmt.Printf("   Message: %s\n", messages[len(messages)-1])
			}
		}

		// Highlight interrupts.
		if item.Checkpoint.IsInterrupted() {
			interruptState := item.Checkpoint.InterruptState
			fmt.Printf("   🔴 INTERRUPT: %s\n", interruptState.NodeID)
			if interruptState.InterruptValue != nil {
				if valueMap, ok := interruptState.InterruptValue.(map[string]any); ok {
					if msg, ok := valueMap["message"]; ok {
						fmt.Printf("   📝 Prompt: %s\n", msg)
					}
				}
			}
		}
	}

	fmt.Println(strings.Repeat("=", 80))
	return nil
}

// showInterruptStatus shows current interrupt status (interrupt-specific command).
func (w *interruptWorkflow) showInterruptStatus(ctx context.Context, lineageID string) error {
	if lineageID == "" {
		return fmt.Errorf("lineage ID required")
	}

	fmt.Printf("\n🔍 Interrupt Status for lineage: %s\n", lineageID)

	latest, err := w.manager.Latest(ctx, lineageID, w.currentNamespace)
	if err != nil {
		return fmt.Errorf("failed to get latest checkpoint: %w", err)
	}

	if latest == nil {
		fmt.Printf("   No checkpoints found for lineage: %s\n", lineageID)
		return nil
	}

	fmt.Println(strings.Repeat("-", 60))

	if latest.Checkpoint.IsInterrupted() {
		interruptState := latest.Checkpoint.InterruptState
		fmt.Printf("🔴 STATUS: INTERRUPTED\n")
		fmt.Printf("   Node: %s\n", interruptState.NodeID)
		fmt.Printf("   Task ID: %s\n", interruptState.TaskID)
		fmt.Printf("   Created: %s\n", time.Now().Format("15:04:05")) // Use current time as placeholder
		
		if interruptState.InterruptValue != nil {
			fmt.Printf("\n📋 Context:\n")
			if valueMap, ok := interruptState.InterruptValue.(map[string]any); ok {
				for key, value := range valueMap {
					fmt.Printf("   %s: %v\n", key, value)
				}
			} else {
				fmt.Printf("   value: %v\n", interruptState.InterruptValue)
			}
		}
		
		fmt.Printf("\n💡 Actions:\n")
		fmt.Printf("   resume %s yes   - Approve and continue\n", lineageID)
		fmt.Printf("   resume %s no    - Reject and stop\n", lineageID)
		
	} else {
		fmt.Printf("✅ STATUS: COMPLETED\n")
		fmt.Printf("   Final Step: %d\n", latest.Metadata.Step)
		fmt.Printf("   Completed: %s\n", time.Now().Format("15:04:05")) // Use current time as placeholder
		
		// Show final state.
		if state := w.extractRootState(latest.Checkpoint); state != nil {
			if counter, ok := state[stateKeyCounter].(int); ok {
				fmt.Printf("   Final Counter: %d\n", counter)
			}
			if lastNode, ok := state[stateKeyLastNode].(string); ok {
				fmt.Printf("   Last Node: %s\n", lastNode)
			}
		}
	}

	fmt.Println(strings.Repeat("-", 60))
	return nil
}

// deleteLineage deletes all checkpoints for a lineage.
func (w *interruptWorkflow) deleteLineage(ctx context.Context, lineageID string) error {
	fmt.Printf("\n🗑️  Deleting all checkpoints for lineage: %s\n", lineageID)
	
	// Confirm deletion.
	fmt.Printf("Are you sure? This action cannot be undone. (yes/no): ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return fmt.Errorf("failed to read confirmation")
	}
	
	response := strings.ToLower(strings.TrimSpace(scanner.Text()))
	if response != "yes" && response != "y" {
		fmt.Println("❌ Deletion cancelled")
		return nil
	}

	err := w.manager.DeleteLineage(ctx, lineageID)
	if err != nil {
		return fmt.Errorf("failed to delete lineage: %w", err)
	}

	fmt.Printf("✅ Successfully deleted all checkpoints for lineage: %s\n", lineageID)
	return nil
}

// runDemo runs a comprehensive demonstration of interrupt features.
func (w *interruptWorkflow) runDemo(ctx context.Context) error {
	demoLineage := fmt.Sprintf("demo-%d", time.Now().Unix())
	
	fmt.Printf("\n🎬 Running Advanced Interrupt Demo\n")
	fmt.Printf("Demo Lineage: %s\n", demoLineage)
	fmt.Println(strings.Repeat("=", 60))

	// Step 1: Run until first interrupt.
	fmt.Println("\n1️⃣  Running until first interrupt...")
	if err := w.runWorkflow(ctx, demoLineage, true); err != nil {
		return fmt.Errorf("demo step 1 failed: %w", err)
	}

	// Step 2: Show interrupt status.
	fmt.Println("\n2️⃣  Showing interrupt status...")
	if err := w.showInterruptStatus(ctx, demoLineage); err != nil {
		return fmt.Errorf("demo step 2 failed: %w", err)
	}

	// Step 3: List checkpoints with details.
	fmt.Println("\n3️⃣  Listing checkpoints with interrupt details...")
	if err := w.listCheckpoints(ctx, demoLineage); err != nil {
		return fmt.Errorf("demo step 3 failed: %w", err)
	}

	// Step 4: Resume with approval to hit second interrupt.
	fmt.Println("\n4️⃣  Resuming with approval (will hit second interrupt)...")
	if err := w.resumeWorkflow(ctx, demoLineage, "", "yes"); err != nil {
		return fmt.Errorf("demo step 4 failed: %w", err)
	}

	// Step 5: Show updated status.
	fmt.Println("\n5️⃣  Showing status after second interrupt...")
	if err := w.showInterruptStatus(ctx, demoLineage); err != nil {
		return fmt.Errorf("demo step 5 failed: %w", err)
	}

	// Step 6: Resume and complete.
	fmt.Println("\n6️⃣  Resuming second interrupt to complete workflow...")
	if err := w.resumeWorkflow(ctx, demoLineage, "", "yes"); err != nil {
		return fmt.Errorf("demo step 6 failed: %w", err)
	}

	// Step 7: Show final tree structure.
	fmt.Println("\n7️⃣  Displaying final checkpoint tree...")
	if err := w.showTree(ctx, demoLineage); err != nil {
		return fmt.Errorf("demo step 7 failed: %w", err)
	}

	// Step 8: Show execution history.
	fmt.Println("\n8️⃣  Displaying execution history with interrupt markers...")
	if err := w.showHistory(ctx, demoLineage); err != nil {
		return fmt.Errorf("demo step 8 failed: %w", err)
	}

	fmt.Printf("\n✅ Advanced Interrupt Demo Completed!\n")
	fmt.Printf("Demo lineage '%s' showcased:\n", demoLineage)
	fmt.Println("  • Multiple interrupt points in workflow")
	fmt.Println("  • Rich interrupt context and metadata")
	fmt.Println("  • Checkpoint tree visualization")
	fmt.Println("  • Execution history tracking")
	fmt.Println("  • Status monitoring capabilities")
	fmt.Println(strings.Repeat("=", 60))

	return nil
}

// showHelp displays available commands with comprehensive interrupt documentation.
func (w *interruptWorkflow) showHelp() {
	fmt.Println(`
💡 Advanced Interrupt & Resume Commands:
════════════════════════════════════════════════════════════════════════════════

🚀 Workflow Execution:
  run [lineage-id]           - Execute workflow normally (auto-generates ID if not provided)
  interrupt [lineage-id]     - Run workflow until interrupt point (auto-generates ID if not provided)

⏪ Resume Operations:
  resume <lineage-id> [checkpoint-id] ["input"]  - Resume from latest or specific checkpoint
  resume <lineage-id> yes                        - Resume with approval
  resume <lineage-id> no                         - Resume with rejection

📋 Checkpoint Management:
  list [lineage-id]          - List all checkpoints for lineage (uses current if not specified)
  latest [lineage-id]        - Show detailed information about latest checkpoint
  tree [lineage-id]          - Display checkpoint tree with interrupt indicators
  history [lineage-id]       - Show execution history with interrupt markers
  delete <lineage-id>        - Delete all checkpoints for a lineage

🔍 Interrupt-Specific Features:
  status [lineage-id]        - Show current interrupt status and available actions

🎬 Demonstration:
  demo                       - Run comprehensive demo showcasing all interrupt features

📚 Usage Examples:
────────────────────────────────────────────────────────────────────────────────
  interrupt my-workflow      (runs workflow until first interrupt)
  status my-workflow         (shows if workflow is interrupted and why)  
  resume my-workflow yes     (approves and continues to next interrupt/completion)
  tree my-workflow           (visualizes checkpoint structure with interrupt markers)
  history my-workflow        (shows timeline of execution with interrupt details)
  demo                       (runs full demonstration with multiple interrupts)

🔧 Current Configuration:
  Lineage: ` + w.currentLineageID + `
  Session: ` + w.sessionID + `
  Storage: ` + w.storageType + `
  Model: ` + w.modelName + `
`)
}

// Helper functions.

func getInt(s graph.State, key string) int {
	if v, ok := s[key].(int); ok {
		return v
	}
	return 0
}

func getBool(s graph.State, key string) bool {
	if v, ok := s[key].(bool); ok {
		return v
	}
	return false
}

func getStrs(s graph.State, key string) []string {
	if v, ok := s[key].([]string); ok {
		return v
	}
	return []string{}
}

func generateLineageID() string {
	return fmt.Sprintf("%s-%d", defaultLineagePrefix, time.Now().Unix())
}

// extractRootState extracts the root state from a checkpoint.
func (w *interruptWorkflow) extractRootState(checkpoint *graph.Checkpoint) map[string]any {
	if checkpoint.ChannelValues == nil {
		return nil
	}

	state := make(map[string]any)
	maps.Copy(state, checkpoint.ChannelValues)

	return state
}
