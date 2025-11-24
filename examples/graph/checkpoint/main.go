//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates comprehensive checkpoint functionality
// using the graph package. This example shows how to save, restore,
// and manage execution checkpoints in a graph-based workflow, enabling
// workflow resumption, time-travel debugging, and fault tolerance.
package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"maps"
	"os"
	"reflect"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver (install with: go get github.com/mattn/go-sqlite3)

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	checkpointinmemory "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	checkpointredis "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/redis"
	checkpointsqlite "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/sqlite"
	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	// Default configuration values.
	defaultModelName = "deepseek-chat"
	defaultUserID    = "demo-user"
	defaultAppName   = "checkpoint-workflow"
	defaultDBPath    = "checkpoints.db"

	// State keys for the workflow.
	stateKeyCounter    = "counter"
	stateKeyMessages   = "messages"
	stateKeyStepCount  = "step_count"
	stateKeyLastAction = "last_action"

	// Node names.
	nodeIncrement1 = "increment1"
	nodeIncrement2 = "increment2"
	nodeIncrement3 = "increment3"
	nodeFinal      = "final"

	// Messages.
	msgNodeExecuted     = "Node %s executed at %s"
	msgWorkflowComplete = "Workflow completed with counter: %d"

	// Commands.
	cmdRun     = "run"
	cmdList    = "list"
	cmdResume  = "resume"
	cmdBranch  = "branch"
	cmdTree    = "tree"
	cmdDelete  = "delete"
	cmdHistory = "history"
	cmdLatest  = "latest"
	cmdDemo    = "demo"
	cmdHelp    = "help"
	cmdExit    = "exit"
	cmdQuit    = "quit"
)

var (
	modelName = flag.String("model", defaultModelName,
		"Name of the model to use")
	storage = flag.String("storage", "memory",
		"Storage type: 'memory' or 'sqlite' or 'redis'")
	dbPath = flag.String("db", defaultDBPath,
		"Path to SQLite database file (only used with -storage=sqlite)")
	redisClientURL = flag.String("redis-url", "redis://localhost:6379",
		"Redis client URL (only used with -storage=redis)")
	verbose = flag.Bool("verbose", false,
		"Enable verbose output")
)

func main() {
	// Parse command line flags.
	flag.Parse()

	fmt.Printf("🚀 Advanced Checkpoint Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Storage: %s", *storage)
	if *storage == "sqlite" {
		fmt.Printf(" (DB: %s)", *dbPath)
	}
	fmt.Println()
	fmt.Printf("Verbose Mode: %v\n", *verbose)
	fmt.Println(strings.Repeat("=", 50))

	// Create and run the workflow.
	workflow := &checkpointWorkflow{
		modelName:        *modelName,
		storageType:      *storage,
		dbPath:           *dbPath,
		verbose:          *verbose,
		redisClientURL:   *redisClientURL,
		currentNamespace: "checkpoint-demo",
	}
	if err := workflow.run(); err != nil {
		log.Fatalf("Workflow failed: %v", err)
	}
}

// checkpointWorkflow manages a workflow with comprehensive checkpoint support.
type checkpointWorkflow struct {
	modelName        string
	storageType      string
	dbPath           string
	redisClientURL   string
	verbose          bool
	logger           agentlog.Logger
	runner           runner.Runner
	saver            graph.CheckpointSaver
	manager          *graph.CheckpointManager
	graphAgent       *graphagent.GraphAgent
	currentLineageID string
	currentNamespace string
}

// run starts the checkpoint workflow.
func (w *checkpointWorkflow) run() error {
	ctx := context.Background()

	// Setup the workflow components.
	if err := w.setup(); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer w.runner.Close()

	// Start interactive mode.
	return w.startInteractiveMode(ctx)
}

// setup creates the graph agent with checkpoint support and runner.
func (w *checkpointWorkflow) setup() error {
	// Initialize logger.
	w.logger = agentlog.Default
	if w.verbose {
		w.logger.Infof("Initializing checkpoint workflow: storage=%s, model=%s, verbose=%v",
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
	case "redis":
		saver, err := checkpointredis.NewSaver(checkpointredis.WithRedisClientURL(w.redisClientURL))
		if err != nil {
			return fmt.Errorf("failed to create Redis saver: %w", err)
		}
		w.saver = saver
	default:
		return fmt.Errorf("unsupported storage type: %s", w.storageType)
	}

	// Create the workflow graph.
	fmt.Printf("🔧 DEBUG: About to create workflow graph\n")
	workflowGraph, err := w.createWorkflowGraph()
	if err != nil {
		fmt.Printf("🔧 DEBUG: Failed to create workflow graph: %v\n", err)
		return fmt.Errorf("failed to create graph: %w", err)
	}
	fmt.Printf("🔧 DEBUG: Workflow graph created successfully\n")

	// Create GraphAgent with checkpoint support.
	w.graphAgent, err = graphagent.New("checkpoint-demo", workflowGraph,
		graphagent.WithDescription("Demonstration of checkpoint features"),
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
		fmt.Printf("🔧 DEBUG: Checkpoint manager configured successfully\n")
		fmt.Printf("🔧 DEBUG: Checkpoint saver type: %T\n", w.saver)
	}

	// Create session service.
	sessionService := sessioninmemory.NewSessionService()

	// Create runner with the graph agent.
	w.runner = runner.NewRunner(
		defaultAppName,
		w.graphAgent,
		runner.WithSessionService(sessionService),
	)

	fmt.Printf("✅ Checkpoint workflow ready!\n\n")
	return nil
}

// createWorkflowGraph creates a workflow graph with multiple nodes.
func (w *checkpointWorkflow) createWorkflowGraph() (*graph.Graph, error) {
	// Create state schema with custom fields.
	schema := graph.NewStateSchema()

	// Add custom state fields with proper type definitions.
	schema.AddField(stateKeyCounter, graph.StateField{
		Type:    reflect.TypeOf(0), // Explicitly define as int
		Reducer: graph.DefaultReducer,
		Default: func() any { return 0 },
	})

	schema.AddField(stateKeyMessages, graph.StateField{
		Type:    reflect.TypeOf([]string{}), // Explicitly define as []string
		Reducer: graph.AppendReducer,
		Default: func() any { return []string{} },
	})

	schema.AddField(stateKeyStepCount, graph.StateField{
		Type:    reflect.TypeOf(0), // Explicitly define as int
		Reducer: graph.DefaultReducer,
		Default: func() any { return 0 },
	})

	schema.AddField(stateKeyLastAction, graph.StateField{
		Type:    reflect.TypeOf(""), // Explicitly define as string
		Reducer: graph.DefaultReducer,
		Default: func() any { return "" },
	})

	// Create the state graph.
	stateGraph := graph.NewStateGraph(schema)

	// Add workflow nodes.
	stateGraph.
		AddNode(nodeIncrement1, w.incrementNode1).
		AddNode(nodeIncrement2, w.incrementNode2).
		AddNode(nodeIncrement3, w.incrementNode3).
		AddNode(nodeFinal, w.finalNode).
		SetEntryPoint(nodeIncrement1).
		SetFinishPoint(nodeFinal)

	// Add workflow edges.
	stateGraph.AddEdge(nodeIncrement1, nodeIncrement2)
	stateGraph.AddEdge(nodeIncrement2, nodeIncrement3)
	stateGraph.AddEdge(nodeIncrement3, nodeFinal)

	fmt.Printf("🔧 DEBUG: Graph edges configured: %s->%s, %s->%s, %s->%s\n",
		nodeIncrement1, nodeIncrement2, nodeIncrement2, nodeIncrement3, nodeIncrement3, nodeFinal)

	// Compile the graph.
	graph, err := stateGraph.Compile()
	if err != nil {
		fmt.Printf("🔧 DEBUG: Graph compilation failed: %v\n", err)
		return nil, err
	}

	fmt.Printf("🔧 DEBUG: Graph compiled successfully\n")
	return graph, nil
}

// Node implementations.

func (w *checkpointWorkflow) incrementNode1(ctx context.Context, state graph.State) (any, error) {
	fmt.Printf("🔧 Executing node: %s\n", nodeIncrement1)

	counter := w.getCounter(state)
	stepCount := w.getStepCount(state)
	messages := w.getMessages(state)

	counter++
	stepCount++
	msg := fmt.Sprintf(msgNodeExecuted, nodeIncrement1, time.Now().Format("15:04:05"))

	fmt.Printf("⚙️  %s: counter=%d, step=%d\n", nodeIncrement1, counter, stepCount)

	return graph.State{
		stateKeyCounter:    counter,
		stateKeyStepCount:  stepCount,
		stateKeyMessages:   append(messages, msg),
		stateKeyLastAction: nodeIncrement1,
	}, nil
}

func (w *checkpointWorkflow) incrementNode2(ctx context.Context, state graph.State) (any, error) {
	fmt.Printf("🔧 Executing node: %s\n", nodeIncrement2)

	counter := w.getCounter(state)
	stepCount := w.getStepCount(state)
	messages := w.getMessages(state)

	counter++
	stepCount++
	msg := fmt.Sprintf(msgNodeExecuted, nodeIncrement2, time.Now().Format("15:04:05"))

	fmt.Printf("⚙️  %s: counter=%d, step=%d\n", nodeIncrement2, counter, stepCount)

	return graph.State{
		stateKeyCounter:    counter,
		stateKeyStepCount:  stepCount,
		stateKeyMessages:   append(messages, msg),
		stateKeyLastAction: nodeIncrement2,
	}, nil
}

func (w *checkpointWorkflow) incrementNode3(ctx context.Context, state graph.State) (any, error) {
	fmt.Printf("🔧 Executing node: %s\n", nodeIncrement3)

	counter := w.getCounter(state)
	stepCount := w.getStepCount(state)
	messages := w.getMessages(state)

	counter++
	stepCount++
	msg := fmt.Sprintf(msgNodeExecuted, nodeIncrement3, time.Now().Format("15:04:05"))

	fmt.Printf("⚙️  %s: counter=%d, step=%d\n", nodeIncrement3, counter, stepCount)

	return graph.State{
		stateKeyCounter:    counter,
		stateKeyStepCount:  stepCount,
		stateKeyMessages:   append(messages, msg),
		stateKeyLastAction: nodeIncrement3,
	}, nil
}

func (w *checkpointWorkflow) finalNode(ctx context.Context, state graph.State) (any, error) {
	fmt.Printf("🔧 Executing node: %s\n", nodeFinal)

	// Create a safe copy of keys to avoid concurrent map iteration
	var stateKeys []string
	for k := range state {
		stateKeys = append(stateKeys, k)
	}

	w.logger.Debugf("🔧 finalNode: received state with %d keys", len(state))
	w.logger.Debugf("🔧 finalNode: state keys: %v", stateKeys)

	// Log key state values before helper calls
	if rawCounter, exists := state[stateKeyCounter]; exists {
		w.logger.Debugf("🔧 finalNode: raw counter value in state: %v (type: %T)", rawCounter, rawCounter)
	} else {
		w.logger.Debugf("🔧 finalNode: counter key '%s' not present in state", stateKeyCounter)
	}

	if rawStepCount, exists := state[stateKeyStepCount]; exists {
		w.logger.Debugf("🔧 finalNode: raw step_count value in state: %v (type: %T)", rawStepCount, rawStepCount)
	}

	counter := w.getCounter(state)
	stepCount := w.getStepCount(state)
	messages := w.getMessages(state)

	w.logger.Debugf("🔧 finalNode: extracted values - counter=%d, stepCount=%d, messages=%d",
		counter, stepCount, len(messages))

	stepCount++
	msg := fmt.Sprintf(msgWorkflowComplete, counter)

	fmt.Printf("✅ %s: Workflow complete, counter=%d, total steps=%d\n",
		nodeFinal, counter, stepCount)

	return graph.State{
		stateKeyCounter:    counter,
		stateKeyStepCount:  stepCount,
		stateKeyMessages:   append(messages, msg),
		stateKeyLastAction: nodeFinal,
	}, nil
}

// Helper functions for state access.

func (w *checkpointWorkflow) getCounter(state graph.State) int {
	if v, ok := state[stateKeyCounter].(int); ok {
		return v
	}
	return 0
}

func (w *checkpointWorkflow) getStepCount(state graph.State) int {
	if v, ok := state[stateKeyStepCount].(int); ok {
		return v
	}
	return 0
}

func (w *checkpointWorkflow) getMessages(state graph.State) []string {
	if v, ok := state[stateKeyMessages].([]string); ok {
		return v
	}
	// Try to handle []any case from JSON deserialization.
	if v, ok := state[stateKeyMessages].([]any); ok {
		messages := make([]string, len(v))
		for i, item := range v {
			if s, ok := item.(string); ok {
				messages[i] = s
			}
		}
		return messages
	}
	return []string{}
}

// startInteractiveMode starts the interactive command-line interface.
func (w *checkpointWorkflow) startInteractiveMode(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	w.showHelp()

	for {
		fmt.Print("\n🔐 checkpoint> ")
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
			lineageID := w.generateLineageID()
			if len(parts) > 1 {
				lineageID = parts[1]
			}
			if err := w.runWorkflow(ctx, lineageID); err != nil {
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

		case cmdResume:
			if len(parts) < 2 {
				fmt.Println("❌ Usage: resume <lineage-id> [checkpoint-id] [\"additional input\"]")
				continue
			}
			lineageID := parts[1]
			var checkpointID string
			var additionalInput string
			if len(parts) > 2 {
				checkpointID = parts[2]
			}
			if len(parts) > 3 {
				// Support additional input when resuming (advanced feature)
				// Join remaining parts to preserve spaces, trim simple quotes.
				additionalInput = strings.Join(parts[3:], " ")
				additionalInput = strings.Trim(additionalInput, "\"'")
			}
			if err := w.resumeWorkflow(ctx, lineageID, checkpointID, additionalInput); err != nil {
				fmt.Printf("❌ Error: %v\n", err)
			}

		case cmdBranch:
			if len(parts) < 3 {
				fmt.Println("❌ Usage: branch <lineage-id> <checkpoint-id>")
				continue
			}
			if err := w.branchCheckpoint(ctx, parts[1], parts[2]); err != nil {
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
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

// runWorkflow executes the workflow with the given lineage ID.
func (w *checkpointWorkflow) runWorkflow(ctx context.Context, lineageID string) error {
	startTime := time.Now()
	w.currentLineageID = lineageID

	w.logger.Infof("Starting workflow execution: lineage_id=%s, namespace=%s", lineageID, w.currentNamespace)

	fmt.Printf("\n🚀 Starting workflow with lineage: %s\n", lineageID)

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
	sessionID := fmt.Sprintf("session-%s-%d", lineageID, time.Now().Unix())
	runtimeState := config.ToMap()
	runtimeState[graph.CfgKeyLineageID] = lineageID             // Ensure lineage_id is set
	runtimeState[graph.CfgKeyCheckpointNS] = w.currentNamespace // Set checkpoint namespace

	w.logger.Infof("Executing workflow through runner: session_id=%s, user_id=%s, message=%s",
		sessionID, defaultUserID, message)

	// Create safe copy of keys to avoid concurrent map access
	runtimeStateKeys := make([]string, 0, len(runtimeState))
	for k := range runtimeState {
		runtimeStateKeys = append(runtimeStateKeys, k)
	}
	fmt.Printf("🔧 DEBUG: Initial runtime state keys: %v\n", runtimeStateKeys)
	fmt.Printf("🔧 DEBUG: Runtime state values: lineage_id=%s, checkpoint_ns=%s\n",
		runtimeState[graph.CfgKeyLineageID], runtimeState[graph.CfgKeyCheckpointNS])
	fmt.Printf("🔧 DEBUG: Message content: %v\n", message)

	eventChan, err := w.runner.Run(
		ctx,
		defaultUserID,
		sessionID,
		message,
		agent.WithRuntimeState(runtimeState),
	)
	if err != nil {
		w.logger.Errorf("Failed to start workflow execution for lineage %s: %v", lineageID, err)
		return fmt.Errorf("failed to run workflow: %w", err)
	}

	// Process streaming response.
	fmt.Printf("🔧 DEBUG: About to process streaming response, eventChan: %v\n", eventChan != nil)
	if err := w.processStreamingResponse(eventChan); err != nil {
		w.logger.Errorf("Failed during workflow processing for lineage %s: %v", lineageID, err)
		return err
	}
	fmt.Printf("🔧 DEBUG: Finished processing streaming response\n")

	duration := time.Since(startTime)
	w.logger.Infof("Workflow execution completed for lineage %s in %v", lineageID, duration)

	// Check if checkpoints were created after execution
	if w.manager != nil {
		config := graph.NewCheckpointConfig(lineageID).WithNamespace(w.currentNamespace)
		checkpoints, err := w.manager.ListCheckpoints(ctx, config.ToMap(), nil)
		if err != nil {
			w.logger.Errorf("Failed to check created checkpoints: %v", err)
		} else {
			w.logger.Infof("Found %d checkpoints after workflow execution", len(checkpoints))
			if len(checkpoints) > 0 && w.verbose {
				fmt.Printf("🔧 DEBUG: Created %d checkpoints during execution\n", len(checkpoints))
			}
		}
	}
	if w.verbose {
		fmt.Printf("⏱️  Execution time: %v\n", duration.Round(time.Millisecond))
	}
	return nil
}

// resumeWorkflow resumes a workflow from a checkpoint.
func (w *checkpointWorkflow) resumeWorkflow(
	ctx context.Context, lineageID, checkpointID, additionalInput string,
) error {
	startTime := time.Now()

	w.logger.Infof("Starting workflow resume: lineage_id=%s, checkpoint_id=%s", lineageID, checkpointID)

	fmt.Printf("\n🔄 Resuming workflow from lineage: %s", lineageID)
	if checkpointID != "" {
		fmt.Printf(", checkpoint: %s", checkpointID)
	}
	fmt.Println()

	// Create checkpoint config.
	config := graph.NewCheckpointConfig(lineageID).
		WithNamespace(w.currentNamespace) // Ensure namespace matches storage
	if checkpointID != "" {
		config = config.WithCheckpointID(checkpointID)
	}

	if w.verbose {
		w.logger.Debugf("Created resume checkpoint configuration: %+v", config.ToMap())
	}

	// Get the checkpoint to see its state.
	checkpoint, err := w.manager.Get(ctx, config.ToMap())
	if err != nil {
		w.logger.Errorf("Failed to retrieve checkpoint for lineage %s: %v", lineageID, err)
		return fmt.Errorf("failed to get checkpoint: %w", err)
	}

	// If no checkpoint found, just run normally.
	if checkpoint == nil {
		w.logger.Warnf("No checkpoint found for lineage %s, starting fresh workflow", lineageID)
		fmt.Println("📦 No checkpoint found, starting fresh workflow")
		return w.runWorkflow(ctx, lineageID)
	}

	// Removed unused source variable
	w.logger.Infof("Retrieved checkpoint for resume: id=%s, timestamp=%v", checkpoint.ID, checkpoint.Timestamp)

	if w.verbose {
		fmt.Printf("📍 Found checkpoint: %s (created: %v)\n",
			checkpoint.ID, checkpoint.Timestamp.Format(time.RFC3339))
	}

	// Update current lineage tracking.
	w.currentLineageID = lineageID

	// Create resume message based on input mode.
	var message model.Message
	if additionalInput != "" {
		// Resume with additional input (advanced mode)
		message = model.NewUserMessage(additionalInput)
		fmt.Printf("🔄 Resuming workflow with additional input: %s\n", additionalInput)
	} else {
		// Pure resume with no input (standard mode, following LangGraph pattern)
		message = model.NewUserMessage("resume")
		fmt.Printf("🔄 Resuming workflow from checkpoint\n")
	}

	// Run with the checkpoint config.
	sessionID := fmt.Sprintf("session-%s-%d", lineageID, time.Now().Unix())
	runtimeState := config.ToMap()
	runtimeState[graph.CfgKeyLineageID] = lineageID             // Ensure lineage_id is set
	runtimeState[graph.CfgKeyCheckpointNS] = w.currentNamespace // Set checkpoint namespace

	// Add checkpoint_id directly to runtime state for the executor to find it
	if checkpointID != "" {
		runtimeState[graph.CfgKeyCheckpointID] = checkpointID
	} else {
		// Use the actual checkpoint ID from the retrieved checkpoint (checkpoint is guaranteed to be non-nil here)
		runtimeState[graph.CfgKeyCheckpointID] = checkpoint.ID
	}

	// The framework now handles type restoration automatically using schema information.
	// No manual pre-population is needed.

	w.logger.Infof("Executing workflow resume through runner: session_id=%s, user_id=%s", sessionID, defaultUserID)
	eventChan, err := w.runner.Run(
		ctx,
		defaultUserID,
		sessionID,
		message,
		agent.WithRuntimeState(runtimeState),
	)
	if err != nil {
		w.logger.Errorf("Failed to start workflow resume for lineage %s: %v", lineageID, err)
		return fmt.Errorf("failed to resume workflow: %w", err)
	}

	// Process streaming response.
	if err := w.processStreamingResponse(eventChan); err != nil {
		w.logger.Errorf("Failed during workflow resume processing for lineage %s: %v", lineageID, err)
		return err
	}

	duration := time.Since(startTime)
	w.logger.Infof("Workflow resume completed for lineage %s in %v", lineageID, duration)

	if w.verbose {
		fmt.Printf("⏱️  Resume time: %v\n", duration.Round(time.Millisecond))
	}

	return nil
}

// branchCheckpoint creates a branch within the same lineage (fork).
func (w *checkpointWorkflow) branchCheckpoint(ctx context.Context, lineageID, checkpointID string) error {
	w.logger.Infof("Creating branch: lineage=%s, checkpoint_id=%s", lineageID, checkpointID)

	fmt.Printf("\n🌿 Creating branch in lineage %s from checkpoint %s\n", lineageID, checkpointID)
	fmt.Printf("📍 DEBUG: Attempting to fork from checkpoint ID: %s\n", checkpointID)

	// Get the executor.
	executor := w.graphAgent.Executor()
	if executor == nil {
		return fmt.Errorf("executor not available")
	}

	// Create config for the source checkpoint.
	config := graph.NewCheckpointConfig(lineageID).WithCheckpointID(checkpointID)

	// Fork the checkpoint (keeps same lineage_id).
	branchedConfig, err := executor.Fork(ctx, config.ToMap())
	if err != nil {
		w.logger.Errorf("Failed to create branch: %v", err)
		return fmt.Errorf("failed to create branch: %w", err)
	}

	// Get the branched checkpoint ID.
	branchedCheckpointID := graph.GetCheckpointID(branchedConfig)
	fmt.Printf("✅ Branch created successfully\n")
	fmt.Printf("   Branched checkpoint ID: %s\n", branchedCheckpointID)
	fmt.Printf("   Parent checkpoint ID: %s\n", checkpointID)
	fmt.Printf("   Lineage ID (unchanged): %s\n", lineageID)

	return nil
}

// showTree displays the checkpoint tree for a lineage.
func (w *checkpointWorkflow) showTree(ctx context.Context, lineageID string) error {
	if lineageID == "" {
		fmt.Println("❌ No lineage ID specified")
		return nil
	}

	fmt.Printf("\n🌳 Checkpoint Tree for lineage: %s\n", lineageID)
	fmt.Println(strings.Repeat("-", 80))

	// Get the checkpoint manager.
	manager := w.graphAgent.Executor().CheckpointManager()
	if manager == nil {
		return fmt.Errorf("checkpoint manager not configured")
	}

	// Get the checkpoint tree.
	tree, err := manager.GetCheckpointTree(ctx, lineageID)
	if err != nil {
		return fmt.Errorf("failed to get checkpoint tree: %w", err)
	}

	if tree.Root == nil {
		fmt.Println("📭 No checkpoints found")
		return nil
	}

	// Display the tree recursively.
	w.printTreeNode(tree.Root, "", true)

	// Display summary.
	fmt.Printf("\n📊 Total checkpoints: %d\n", len(tree.Branches))

	// Count branches (nodes with children).
	branchCount := 0
	for _, node := range tree.Branches {
		if len(node.Children) > 0 {
			branchCount++
		}
	}
	fmt.Printf("   Branch points: %d\n", branchCount)

	return nil
}

// printTreeNode recursively prints a checkpoint tree node.
func (w *checkpointWorkflow) printTreeNode(node *graph.CheckpointNode, prefix string, isLast bool) {
	if node == nil || node.Checkpoint == nil {
		return
	}

	// Determine the branch character.
	branch := "├── "
	if isLast {
		branch = "└── "
	}
	if prefix == "" {
		branch = ""
	}

	// Get checkpoint info.
	checkpoint := node.Checkpoint.Checkpoint
	metadata := node.Checkpoint.Metadata

	// Format the checkpoint display.
	source := "unknown"
	if metadata != nil {
		source = metadata.Source
	}

	// Get counter value if available using extractRootState for proper type handling.
	counterVal := 0
	if state := w.extractRootState(checkpoint); state != nil {
		if counter, ok := state[stateKeyCounter]; ok {
			// Handle different number types from JSON deserialization.
			switch v := counter.(type) {
			case int:
				counterVal = v
			case float64:
				counterVal = int(v)
			case json.Number:
				if val, err := v.Int64(); err == nil {
					counterVal = int(val)
				}
			}
		}
	}

	// Print the node.
	fmt.Printf("%s%s📍 %s (counter=%d, source=%s, %s)\n",
		prefix, branch, checkpoint.ID[:8], counterVal, source,
		checkpoint.Timestamp.Format("15:04:05"))

	// Update prefix for children.
	childPrefix := prefix
	if prefix != "" {
		if isLast {
			childPrefix = prefix + "    "
		} else {
			childPrefix = prefix + "│   "
		}
	} else {
		childPrefix = "    "
	}

	// Print children.
	for i, child := range node.Children {
		isLastChild := (i == len(node.Children)-1)
		w.printTreeNode(child, childPrefix, isLastChild)
	}
}

// listCheckpoints lists all checkpoints for a lineage.
func (w *checkpointWorkflow) listCheckpoints(ctx context.Context, lineageID string) error {
	if lineageID == "" {
		fmt.Println("❌ No lineage ID specified")
		return nil
	}

	fmt.Printf("\n📜 Checkpoints for lineage: %s\n", lineageID)
	fmt.Println(strings.Repeat("-", 80))

	// Create config for the lineage.
	config := graph.NewCheckpointConfig(lineageID)
	config.Namespace = "checkpoint-demo"

	// List checkpoints with a filter.
	manager := w.graphAgent.Executor().CheckpointManager()
	if manager == nil {
		return fmt.Errorf("checkpoint manager not configured")
	}
	filter := graph.NewCheckpointFilter().WithLimit(20)
	checkpoints, err := manager.ListCheckpoints(ctx, config.ToMap(), filter)
	if err != nil {
		return fmt.Errorf("failed to list checkpoints: %w", err)
	}

	if len(checkpoints) == 0 {
		fmt.Println("📭 No checkpoints found")
		return nil
	}

	// Display checkpoints.
	for i, tuple := range checkpoints {
		checkpointID := tuple.Checkpoint.ID
		if checkpointID == "" {
			checkpointID = "<none>"
		}
		namespace := graph.GetNamespace(tuple.Config)
		if namespace == "" {
			namespace = "<empty>"
		}
		fmt.Printf("%d. ID: %s\n", i+1, checkpointID)
		fmt.Printf("   Namespace: %s\n", namespace)
		fmt.Printf("   Created: %s | Source: %s | Step: %d\n",
			tuple.Checkpoint.Timestamp.Format("15:04:05"),
			tuple.Metadata.Source,
			tuple.Metadata.Step)

		// Show state summary.
		if state := w.extractRootState(tuple.Checkpoint); state != nil {
			if counter, ok := state[stateKeyCounter]; ok {
				fmt.Printf("   State: counter=%v", counter)
			}
			if stepCount, ok := state[stateKeyStepCount]; ok {
				fmt.Printf(", steps=%v", stepCount)
			}
			if lastAction, ok := state[stateKeyLastAction]; ok {
				fmt.Printf(", last_action=%v", lastAction)
			}
			fmt.Println()
		}
	}

	fmt.Println(strings.Repeat("-", 80))
	return nil
}

// showLatestCheckpoint displays the latest checkpoint for a lineage.
func (w *checkpointWorkflow) showLatestCheckpoint(ctx context.Context, lineageID string) error {
	if lineageID == "" {
		fmt.Println("❌ No lineage ID specified")
		return nil
	}

	fmt.Printf("\n📍 Latest checkpoint for lineage: %s\n", lineageID)

	// Get the latest checkpoint.
	manager := w.graphAgent.Executor().CheckpointManager()
	if manager == nil {
		return fmt.Errorf("checkpoint manager not configured")
	}
	config := graph.NewCheckpointConfig(lineageID)
	tuple, err := manager.GetTuple(ctx, config.ToMap())
	if err != nil {
		return fmt.Errorf("failed to get latest checkpoint: %w", err)
	}

	if tuple == nil {
		fmt.Println("📭 No checkpoints found")
		return nil
	}

	// Display checkpoint details.
	checkpointID := tuple.Checkpoint.ID
	if checkpointID == "" {
		checkpointID = "<none>"
	}
	namespace := graph.GetNamespace(tuple.Config)
	if namespace == "" {
		namespace = "<empty>"
	}
	fmt.Printf("ID: %s\n", checkpointID)
	fmt.Printf("Namespace: %s\n", namespace)
	fmt.Printf("Created: %s\n", tuple.Checkpoint.Timestamp.Format(time.RFC3339))
	fmt.Printf("Source: %s | Step: %d\n", tuple.Metadata.Source, tuple.Metadata.Step)

	// Show full state.
	if state := w.extractRootState(tuple.Checkpoint); state != nil {
		fmt.Println("\nState:")
		stateJSON, _ := json.MarshalIndent(state, "  ", "  ")
		fmt.Println(string(stateJSON))
	}

	// Show messages if any.
	if state := w.extractRootState(tuple.Checkpoint); state != nil {
		if messages, ok := state[stateKeyMessages].([]any); ok && len(messages) > 0 {
			fmt.Println("\nMessages:")
			for i, msg := range messages {
				fmt.Printf("  %d. %v\n", i+1, msg)
			}
		}
	}

	return nil
}

// showHistory shows the execution history for a lineage.
func (w *checkpointWorkflow) showHistory(ctx context.Context, lineageID string) error {
	if lineageID == "" {
		fmt.Println("❌ No lineage ID specified")
		return nil
	}

	fmt.Printf("\n📚 Execution history for lineage: %s\n", lineageID)
	fmt.Println(strings.Repeat("=", 80))

	// List all checkpoints.
	config := graph.NewCheckpointConfig(lineageID)
	checkpoints, err := w.manager.ListCheckpoints(ctx, config.ToMap(), nil)
	if err != nil {
		return fmt.Errorf("failed to list checkpoints: %w", err)
	}

	if len(checkpoints) == 0 {
		fmt.Println("📭 No history found")
		return nil
	}

	// Display in chronological order (oldest first).
	for i := len(checkpoints) - 1; i >= 0; i-- {
		tuple := checkpoints[i]

		// Format header based on source.
		timestamp := tuple.Checkpoint.Timestamp.Format("15:04:05")
		source := tuple.Metadata.Source
		step := tuple.Metadata.Step

		fmt.Printf("\n⏰ %s | Source: %s | Step: %d\n", timestamp, source, step)
		fmt.Println(strings.Repeat("-", 40))

		if state := w.extractRootState(tuple.Checkpoint); state != nil {
			// Show what happened.
			if lastAction, ok := state[stateKeyLastAction]; ok && lastAction != "" {
				fmt.Printf("   🎯 Action: %v executed\n", lastAction)
			} else if source == "input" {
				fmt.Printf("   🎯 Action: Workflow started\n")
			}

			// Show state values.
			counter := state[stateKeyCounter]
			steps := state[stateKeyStepCount]
			fmt.Printf("   📊 State: counter=%v, steps=%v\n", counter, steps)

			// Show messages if any.
			if messages, ok := state[stateKeyMessages].([]any); ok && len(messages) > 0 {
				fmt.Printf("   💬 Recent messages:\n")
				// Show last 2 messages for context.
				start := max(0, len(messages)-2)
				for j := start; j < len(messages); j++ {
					fmt.Printf("      - %v\n", messages[j])
				}
			}

			// Show checkpoint metadata.
			checkpointID := tuple.Checkpoint.ID
			if checkpointID == "" {
				checkpointID = "<none>"
			}
			fmt.Printf("   🔖 Checkpoint ID: %s\n", checkpointID)
		}
	}

	fmt.Println(strings.Repeat("=", 80))
	return nil
}

// deleteLineage deletes all checkpoints for a lineage.
func (w *checkpointWorkflow) deleteLineage(ctx context.Context, lineageID string) error {
	fmt.Printf("\n🗑️  Deleting all checkpoints for lineage: %s\n", lineageID)

	// Confirm deletion.
	fmt.Print("Are you sure? (yes/no): ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		response := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if response != "yes" && response != "y" {
			fmt.Println("❌ Deletion cancelled")
			return nil
		}
	}

	// Delete the lineage.
	manager := w.graphAgent.Executor().CheckpointManager()
	if manager == nil {
		return fmt.Errorf("checkpoint manager not configured")
	}
	if err := manager.DeleteLineage(ctx, lineageID); err != nil {
		return fmt.Errorf("failed to delete lineage: %w", err)
	}

	fmt.Println("✅ Lineage deleted successfully")

	// Clear current lineage if it was deleted.
	if w.currentLineageID == lineageID {
		w.currentLineageID = ""
	}

	return nil
}

// runDemo runs a demonstration sequence.
func (w *checkpointWorkflow) runDemo(ctx context.Context) error {
	fmt.Println("\n🎬 Running Checkpoint Demo...")
	fmt.Println(strings.Repeat("=", 50))

	demoLineage := fmt.Sprintf("demo-%d", time.Now().Unix())

	// Step 1: Run workflow.
	fmt.Println("\n1️⃣ Running workflow...")
	if err := w.runWorkflow(ctx, demoLineage); err != nil {
		return err
	}

	// Step 2: List checkpoints.
	fmt.Println("\n2️⃣ Listing checkpoints...")
	if err := w.listCheckpoints(ctx, demoLineage); err != nil {
		return err
	}

	// Step 3: Show latest checkpoint.
	fmt.Println("\n3️⃣ Showing latest checkpoint...")
	if err := w.showLatestCheckpoint(ctx, demoLineage); err != nil {
		return err
	}

	// Step 4: Create a new workflow run.
	fmt.Println("\n4️⃣ Creating a new workflow run...")
	if err := w.runWorkflow(ctx, demoLineage); err != nil {
		return err
	}

	// Step 5: Show execution history.
	fmt.Println("\n5️⃣ Showing execution history...")
	if err := w.showHistory(ctx, demoLineage); err != nil {
		return err
	}

	// Step 6: Create a branch within same lineage.
	fmt.Println("\n6️⃣ Creating a branch from checkpoint...")

	// Get the latest checkpoint ID for branching.
	manager := w.graphAgent.Executor().CheckpointManager()
	if manager == nil {
		return fmt.Errorf("checkpoint manager not configured")
	}
	config := graph.NewCheckpointConfig(demoLineage)
	tuple, err := manager.GetTuple(ctx, config.ToMap())
	if err != nil {
		return fmt.Errorf("failed to get checkpoint for branch: %w", err)
	}

	if tuple != nil && tuple.Checkpoint != nil {
		// Use the checkpoint's actual ID.
		checkpointID := tuple.Checkpoint.ID
		if checkpointID == "" {
			checkpointID = "auto"
		}

		fmt.Printf("   Using checkpoint: %s\n", checkpointID)
		if err := w.branchCheckpoint(ctx, demoLineage, checkpointID); err != nil {
			return err
		}

		// Resume from the branched checkpoint.
		fmt.Println("\n7️⃣ Resuming from branched checkpoint...")
		// Get the latest checkpoint (which should be the branch).
		branchTuple, err := manager.GetTuple(ctx, config.ToMap())
		if err != nil {
			return fmt.Errorf("failed to get branched checkpoint: %w", err)
		}
		if branchTuple != nil && branchTuple.Checkpoint != nil {
			if err := w.resumeWorkflow(ctx, demoLineage, branchTuple.Checkpoint.ID, ""); err != nil {
				return err
			}
		}
	} else {
		fmt.Println("   No checkpoint found for branching")
	}

	fmt.Println("\n✅ Demo completed successfully!")
	fmt.Println(strings.Repeat("=", 50))
	return nil
}

// processStreamingResponse handles the streaming workflow response.
func (w *checkpointWorkflow) processStreamingResponse(eventChan <-chan *event.Event) error {
	var lastNodeExecuted string
	var interrupted bool
	var nodeExecutionCount int
	var currentState map[string]any

	fmt.Printf("🔧 DEBUG: Starting to process streaming response events\n")
	w.logger.Debug("Starting to process streaming response events")

	for event := range eventChan {
		// Handle errors.
		if event.Error != nil {
			w.logger.Errorf("Received error event from %s: %s", event.Author, event.Error.Message)
			fmt.Printf("❌ Error: %s\n", event.Error.Message)
			continue
		}

		// Log all events for debugging.
		if w.verbose && event.Author != "" {
			fmt.Printf("🔧 DEBUG: Event - author=%s, object=%s, done=%v\n", event.Author, event.Object, event.Done)
			if len(event.StateDelta) > 0 {
				keys := make([]string, 0, len(event.StateDelta))
				for k := range event.StateDelta {
					keys = append(keys, k)
				}
				fmt.Printf("🔧 DEBUG: StateDelta keys: %v\n", keys)
			}
		}

		// Try multiple approaches to detect node execution.

		// Approach 1: Look for node execution events directly by checking the object field.
		if event.Object == "graph.node.complete" && event.Author != "" {
			// This is a node completion event - the author is the node ID
			lastNodeExecuted = event.Author
			nodeExecutionCount++
			w.logger.Infof("Node execution completed: %s (count: %d)", event.Author, nodeExecutionCount)
			if w.verbose {
				fmt.Printf("✓ Completed node: %s\n", event.Author)
			}
		}

		// Approach 2: Parse node execution metadata regardless of author.
		if event.StateDelta != nil {
			if nodeData, exists := event.StateDelta[graph.MetadataKeyNode]; exists {
				var nodeMetadata graph.NodeExecutionMetadata
				if err := json.Unmarshal(nodeData, &nodeMetadata); err == nil {
					switch nodeMetadata.Phase {
					case graph.ExecutionPhaseStart:
						w.logger.Infof("Node execution started: %s (%s)", nodeMetadata.NodeID, nodeMetadata.NodeType)
						if w.verbose {
							fmt.Printf("⚡ Starting node: %s (type: %s)\n", nodeMetadata.NodeID, nodeMetadata.NodeType)
						}
					case graph.ExecutionPhaseComplete:
						// Avoid double-counting; rely on explicit graph.node.complete events for counts.
						if lastNodeExecuted == "" {
							lastNodeExecuted = nodeMetadata.NodeID
						}
						duration := nodeMetadata.EndTime.Sub(nodeMetadata.StartTime)
						w.logger.Infof("Node execution completed: %s (%s) in %v, total nodes: %d",
							nodeMetadata.NodeID, nodeMetadata.NodeType, duration, nodeExecutionCount)
						if w.verbose {
							fmt.Printf("✓ Completed node: %s (duration: %v)\n", nodeMetadata.NodeID, duration.Round(time.Microsecond))
						}
					}
				}
			}
		}

		// Track final state updates.
		if event.Author == graph.AuthorGraphExecutor {
			if event.StateDelta != nil {
				// Store current state for final display.
				if currentState == nil {
					currentState = make(map[string]any)
				}
				for k, v := range event.StateDelta {
					if !strings.HasPrefix(k, "_") { // Skip metadata keys
						// Unmarshal the byte data to get the actual value
						var value any
						if err := json.Unmarshal(v, &value); err == nil {
							currentState[k] = value
						}
					}
				}

				if w.verbose && len(event.StateDelta) > 0 {
					keys := make([]string, 0, len(event.StateDelta))
					for k := range event.StateDelta {
						keys = append(keys, k)
					}
					w.logger.Debugf("State updated with keys: %v", keys)
				}
			}
		}

		// Handle completion.
		if event.Done {
			w.logger.Infof("Workflow execution finished: last_node=%s, nodes_executed=%d, interrupted=%v",
				lastNodeExecuted, nodeExecutionCount, interrupted)

			if interrupted {
				fmt.Println("⚠️  Workflow interrupted - checkpoint saved")
				fmt.Printf("   Last node: %s\n", lastNodeExecuted)
			} else if lastNodeExecuted != "" {
				fmt.Println("✅ Workflow execution finished")
				fmt.Printf("   Last node: %s\n", lastNodeExecuted)
				if w.verbose && currentState != nil {
					if counter, ok := currentState[stateKeyCounter]; ok {
						fmt.Printf("   Final counter: %v\n", counter)
					}
					if stepCount, ok := currentState[stateKeyStepCount]; ok {
						fmt.Printf("   Total steps: %v\n", stepCount)
					}
				}
			} else {
				fmt.Println("✅ Workflow execution finished")
				fmt.Println("   (No nodes executed)")
			}
			break
		}
	}
	return nil
}

// extractRootState extracts the root state from a checkpoint.
func (w *checkpointWorkflow) extractRootState(checkpoint *graph.Checkpoint) map[string]any {
	// State is stored directly in ChannelValues
	if checkpoint.ChannelValues == nil {
		return nil
	}

	// Convert to map[string]any
	state := make(map[string]any)
	maps.Copy(state, checkpoint.ChannelValues)

	return state
}

// generateLineageID generates a new lineage ID.
func (w *checkpointWorkflow) generateLineageID() string {
	return fmt.Sprintf("workflow-%d", time.Now().Unix())
}

// showHelp displays available commands.
func (w *checkpointWorkflow) showHelp() {
	const helpText = `
💡 Checkpoint Management Commands:
════════════════════════════════════════════════════

Workflow Execution:
  run [lineage-id]           - Run a new workflow (auto-generates lineage ID if not provided)
  resume <lineage-id> [checkpoint-id] ["input"] - Resume from latest or specific checkpoint
                               (optional input provides additional context when resuming)

Checkpoint Operations:
  list [lineage-id]          - List all checkpoints for a lineage
  latest [lineage-id]        - Show details of the latest checkpoint
  history [lineage-id]       - Show execution history for a lineage
  tree [lineage-id]          - Display checkpoint tree showing branches
  branch <lineage-id> <checkpoint-id> - Create branch within same lineage

Management:
  delete <lineage-id>        - Delete all checkpoints for a lineage
  demo                       - Run a comprehensive demonstration
  help                       - Show this help message
  exit                       - Exit the application

📚 Key Concepts:
  - Lineage ID: Unique identifier for a workflow conversation/thread
  - Checkpoint: Saved state at a specific point in execution
  - Branch: Create alternative execution paths from checkpoints

🔍 Examples:
  run workflow-1             - Start a new workflow with ID "workflow-1"
  resume workflow-1          - Resume "workflow-1" from its latest checkpoint
  resume workflow-1 ckpt-123 - Resume from specific checkpoint (pure resume)
  resume workflow-1 ckpt-123 "new context" - Resume with additional input
  branch workflow-1 ckpt-123 - Create a branch within the same lineage
  tree workflow-1            - View branching structure
  history workflow-1         - View complete execution history
`
	fmt.Print(helpText)
}
