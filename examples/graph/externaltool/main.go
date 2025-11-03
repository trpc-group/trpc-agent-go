//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main shows how to interrupt a graph-based workflow inside a tool
// node, wait for human input, and resume execution with the provided data.
// The example runs an interactive command-line application that streams LLM
// output, pauses when the tool asks for external facts, and resumes once the
// user supplies the missing information.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	ckptinmem "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultModelName = "deepseek-chat"
	userID           = "demo-user"
	sessionID        = "demo-session"

	nodePrepare   = "prepare_input"
	nodeAssistant = "assistant_plan"
	nodeTools     = "external_lookup"
	nodeFinish    = "finalize"

	toolNameLookup = "manual_lookup"
	interruptKey   = "lookup_result"

	stateKeyQuestion = "user_question"
)

var modelName = flag.String("model", defaultModelName,
	"Name of the model to use for the assistant")

func main() {
	flag.Parse()

	ctx := context.Background()
	workflow := &externalToolWorkflow{
		modelName: *modelName,
	}
	if err := workflow.setup(); err != nil {
		fmt.Printf("failed to set up workflow: %v\n", err)
		os.Exit(1)
	}
	if err := workflow.interactive(ctx); err != nil {
		fmt.Printf("workflow ended with error: %v\n", err)
		os.Exit(1)
	}
}

// externalToolWorkflow wires together the runner, graph, and CLI helpers.
type externalToolWorkflow struct {
	modelName string
	runner    runner.Runner
	saver     graph.CheckpointSaver
	manager   *graph.CheckpointManager

	lookupTool *manualLookupTool

	currentLineage string
	pending        *pendingResume
}

// setup prepares the graph, agent, runner, and checkpoint services.
func (w *externalToolWorkflow) setup() error {
	w.lookupTool = &manualLookupTool{}

	g, err := w.buildGraph()
	if err != nil {
		return fmt.Errorf("build graph: %w", err)
	}

	w.saver = ckptinmem.NewSaver()
	w.manager = graph.NewCheckpointManager(w.saver)

	ga, err := graphagent.New(
		"external-tool-graph",
		g,
		graphagent.WithDescription(
			"Graph that pauses inside a tool and resumes with human input",
		),
		graphagent.WithCheckpointSaver(w.saver),
	)
	if err != nil {
		return fmt.Errorf("create graph agent: %w", err)
	}

	w.runner = runner.NewRunner(
		"external_tool_demo",
		ga,
		runner.WithSessionService(sessioninmemory.NewSessionService()),
	)
	w.currentLineage = w.newLineage()
	return nil
}

// buildGraph constructs the minimal graph with LLM, tool, and finish nodes.
func (w *externalToolWorkflow) buildGraph() (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()

	tools := map[string]tool.Tool{
		toolNameLookup: w.lookupTool,
	}

	stateGraph := graph.NewStateGraph(schema)

	modelInstance := openai.New(w.modelName)
	// Lower temperature to reduce chatty clarifications and make
	// tool usage more consistent for this demo.
	var temp float64 = 0.0

	stateGraph.
		AddNode(
			nodePrepare,
			w.prepareInput,
			graph.WithName("Capture Question"),
			graph.WithDescription(
				"Validates and stores the user question for later nodes.",
			),
		).
		AddLLMNode(
			nodeAssistant,
			modelInstance,
			assistantPrompt(),
			tools,
			graph.WithName("Assistant Reasoning"),
			graph.WithGenerationConfig(model.GenerationConfig{
				Temperature: &temp,
				Stream:      true,
			}),
			graph.WithDescription(
				"Plans the answer and calls the manual lookup tool when needed.",
			),
		).
		AddNode(
			nodeTools,
			w.wrapToolsNode(graph.NewToolsNodeFunc(
				tools,
				graph.WithToolCallbacks(tool.NewCallbacksStructured()),
			)),
			graph.WithNodeType(graph.NodeTypeTool),
			graph.WithName("Manual Lookup Tool"),
			graph.WithDescription(
				"Calls graph.Interrupt to pause until human-provided data arrives.",
			),
		).
		AddNode(
			nodeFinish,
			w.finishConversation,
			graph.WithName("Finalize"),
			graph.WithDescription(
				"Checks that the assistant produced a final answer.",
			),
		).
		SetEntryPoint(nodePrepare).
		SetFinishPoint(nodeFinish)

	stateGraph.AddEdge(nodePrepare, nodeAssistant)
	stateGraph.AddToolsConditionalEdges(
		nodeAssistant,
		nodeTools,
		nodeFinish,
	)
	stateGraph.AddEdge(nodeTools, nodeAssistant)

	return stateGraph.Compile()
}

// assistantPrompt defines the system prompt passed to the LLM node.
func assistantPrompt() string {
	return strings.Join([]string{
		"You are a helpful assistant that can ask a human for missing facts.",
		"When you need outside data you MUST call the manual_lookup tool.",
		"The tool pauses execution, so use it only when the answer depends",
		"on information that is not in the conversation.",
		"If the user's question is ambiguous or lacks key specifics, do not",
		"ask clarifying questions in a normal reply. Instead, call",
		"manual_lookup with topic set to your exact clarification.",
		"After the tool returns, incorporate the provided data and craft a",
		"clear final answer for the user. Do not expose internal details.",
	}, "\n")
}

// prepareInput stores the cleaned user question in the shared state.
func (w *externalToolWorkflow) prepareInput(
	ctx context.Context,
	state graph.State,
) (any, error) {
	raw, _ := state[graph.StateKeyUserInput].(string)
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return nil, errors.New("please enter a question to get started")
	}
	return graph.State{
		stateKeyQuestion:           clean,
		graph.StateKeyUserInput:    clean,
		graph.StateKeyLastResponse: "",
	}, nil
}

// finishConversation ensures the assistant produced a response.
func (w *externalToolWorkflow) finishConversation(
	ctx context.Context,
	state graph.State,
) (any, error) {
	last, _ := state[graph.StateKeyLastResponse].(string)
	if strings.TrimSpace(last) == "" {
		return nil, errors.New("assistant reply is empty")
	}
	return nil, nil
}

// wrapToolsNode injects the current state before delegating to the tool node.
func (w *externalToolWorkflow) wrapToolsNode(
	base graph.NodeFunc,
) graph.NodeFunc {
	return func(ctx context.Context, state graph.State) (any, error) {
		w.lookupTool.setState(state)
		defer w.lookupTool.clearState()
		result, err := base(ctx, state)
		if err != nil {
			var interrupt *graph.InterruptError
			if errors.As(err, &interrupt) {
				return nil, interrupt
			}
		}
		return result, err
	}
}

// interactive provides the command-line interface.
func (w *externalToolWorkflow) interactive(ctx context.Context) error {
	fmt.Println("🔌 External Tool Interrupt Demo")
	fmt.Printf("Model: %s\n", w.modelName)
	fmt.Println(strings.Repeat("=", 50))
	w.printHelp()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You> ")
		if !scanner.Scan() {
			fmt.Println()
			break
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch {
		case line == "/exit":
			fmt.Println("👋 Bye!")
			return nil
		case line == "/help":
			w.printHelp()
		case strings.HasPrefix(line, "/resume"):
			arg := strings.TrimSpace(strings.TrimPrefix(line, "/resume"))
			if arg == "" {
				fmt.Println("Usage: /resume <value>")
				continue
			}
			if err := w.resume(ctx, arg); err != nil {
				fmt.Printf("resume error: %v\n", err)
			}
		default:
			if err := w.ask(ctx, line); err != nil {
				fmt.Printf("run error: %v\n", err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read input: %w", err)
	}
	return nil
}

// printHelp displays available commands.
func (w *externalToolWorkflow) printHelp() {
	fmt.Println("Type a question to start the workflow.")
	fmt.Println("Commands:")
	fmt.Println("  /resume <value>  Resume the paused run with tool output")
	fmt.Println("  /help            Show this help message")
	fmt.Println("  /exit            Quit the program")
	fmt.Println()
}

// ask launches a new run unless an interrupt is waiting for resume.
func (w *externalToolWorkflow) ask(ctx context.Context, input string) error {
	if w.pending != nil {
		fmt.Println("⚠️  Workflow is paused. Use /resume <value> to continue.")
		return nil
	}
	runtimeState := map[string]any{
		graph.CfgKeyLineageID: w.currentLineage,
	}
	interrupted, err := w.runAndStream(
		ctx,
		model.NewUserMessage(input),
		runtimeState,
	)
	if err != nil {
		return err
	}
	if interrupted {
		fmt.Println("\n⏸️  Workflow paused for manual data.")
	} else {
		w.currentLineage = w.newLineage()
	}
	return nil
}

// resume continues a paused run with the provided tool result.
func (w *externalToolWorkflow) resume(
	ctx context.Context,
	value string,
) error {
	if w.pending == nil {
		fmt.Println("Nothing to resume right now.")
		fmt.Println(
			"Tip: ask a question and wait for '🛑 Manual data required:'",
		)
		fmt.Println("before using /resume <value>.")
		return nil
	}
	// Use graph.Command to carry resume values.
	// Executor applies resume only for *graph.Command, not ResumeCommand.
	cmd := &graph.Command{ResumeMap: map[string]any{
		interruptKey: value,
	}}
	runtimeState := map[string]any{
		graph.StateKeyCommand:    cmd,
		graph.CfgKeyLineageID:    w.currentLineage,
		graph.CfgKeyCheckpointID: w.pending.checkpointID,
	}
	interrupted, err := w.runAndStream(
		ctx,
		model.NewUserMessage("resume"),
		runtimeState,
	)
	if err != nil {
		return err
	}
	if interrupted {
		fmt.Println("\n⚠️  Workflow paused again.")
	} else {
		w.pending = nil
		w.currentLineage = w.newLineage()
	}
	return nil
}

// runAndStream executes the graph and streams events to the terminal.
func (w *externalToolWorkflow) runAndStream(
	ctx context.Context,
	msg model.Message,
	runtimeState map[string]any,
) (bool, error) {
	events, err := w.runner.Run(
		ctx,
		userID,
		sessionID,
		msg,
		agent.WithRuntimeState(runtimeState),
	)
	if err != nil {
		return false, err
	}

	var (
		interrupted     bool
		interruptSource any
		startedOutput   bool
	)

	for evt := range events {
		if evt.Error != nil {
			fmt.Printf("\n❌ Error: %s\n", evt.Error.Message)
			continue
		}

		if meta := extractInterrupt(evt); meta != nil {
			interrupted = true
			if meta.InterruptValue != nil {
				interruptSource = meta.InterruptValue
			}
			continue
		}

		w.printToolCalls(evt)
		w.printToolResult(evt)
		startedOutput = w.printStreaming(evt, startedOutput)
	}

	if !interrupted && w.manager != nil {
		if latest, err := w.manager.Latest(ctx, w.currentLineage, ""); err == nil && latest != nil && latest.Checkpoint != nil && latest.Checkpoint.IsInterrupted() {
			interrupted = true
			interruptSource = latest.Checkpoint.GetInterruptValue()
		}
	}

	if interrupted {
		if err := w.capturePending(ctx, interruptSource); err != nil {
			return true, err
		}
		w.printPending()
	}
	return interrupted, nil
}

// capturePending stores checkpoint information needed for resume.
func (w *externalToolWorkflow) capturePending(
	ctx context.Context,
	raw any,
) error {
	tuple, err := w.manager.Latest(ctx, w.currentLineage, "")
	if err != nil {
		return fmt.Errorf("fetch latest checkpoint: %w", err)
	}
	if tuple == nil ||
		tuple.Checkpoint == nil ||
		!tuple.Checkpoint.IsInterrupted() {
		return errors.New("no interrupt checkpoint found")
	}
	if raw == nil {
		raw = tuple.Checkpoint.GetInterruptValue()
	}
	w.pending = &pendingResume{
		checkpointID: tuple.Checkpoint.ID,
		prompt:       formatInterruptPrompt(raw),
		raw:          raw,
	}
	return nil
}

// printPending informs the user about the paused state.
func (w *externalToolWorkflow) printPending() {
	if w.pending == nil {
		return
	}
	fmt.Println("\n🛑 Manual data required:")
	fmt.Printf("   %s\n", w.pending.prompt)
	fmt.Println("   Resume with: /resume <value>")
}

// printToolCalls displays tool call requests issued by the LLM.
func (w *externalToolWorkflow) printToolCalls(evt *event.Event) {
	if len(evt.Choices) == 0 {
		return
	}
	tc := evt.Choices[0].Message.ToolCalls
	if len(tc) == 0 {
		return
	}
	fmt.Println("🔧 Tool call requested:")
	for _, call := range tc {
		fmt.Printf("   • %s (ID: %s)\n", call.Function.Name, call.ID)
		if len(call.Function.Arguments) > 0 {
			fmt.Printf("     args: %s\n", string(call.Function.Arguments))
		}
	}
}

// printToolResult shows tool response events after resume.
func (w *externalToolWorkflow) printToolResult(evt *event.Event) {
	if evt.Response == nil {
		return
	}
	if evt.Response.Object != model.ObjectTypeToolResponse {
		return
	}
	if len(evt.Choices) == 0 {
		return
	}
	content := strings.TrimSpace(evt.Choices[0].Message.Content)
	if content == "" {
		return
	}
	fmt.Printf("\n🧰 Tool result: %s\n", content)
}

// printStreaming streams assistant output as it arrives.
func (w *externalToolWorkflow) printStreaming(
	evt *event.Event,
	started bool,
) bool {
	if len(evt.Choices) == 0 {
		return started
	}
	choice := evt.Choices[0]

	if choice.Delta.Content != "" {
		if !started {
			fmt.Print("🤖 Assistant: ")
			started = true
		}
		fmt.Print(choice.Delta.Content)
	}
	if !started && choice.Message.Content != "" {
		fmt.Printf("🤖 Assistant: %s\n", choice.Message.Content)
		started = true
	}
	if evt.Response != nil && evt.Response.Done && started {
		fmt.Println()
	}
	return started
}

// extractInterrupt decodes interrupt metadata from an event, if any.
func extractInterrupt(evt *event.Event) *graph.PregelStepMetadata {
	if evt == nil || evt.StateDelta == nil {
		return nil
	}
	raw, ok := evt.StateDelta[graph.MetadataKeyPregel]
	if !ok {
		return nil
	}
	var meta graph.PregelStepMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil
	}
	if meta.InterruptValue == nil {
		return nil
	}
	return &meta
}

// newLineage generates a unique lineage id for checkpointing.
func (w *externalToolWorkflow) newLineage() string {
	return fmt.Sprintf("external-tool-%d", time.Now().UnixNano())
}

// pendingResume holds data required to resume an interrupted run.
type pendingResume struct {
	checkpointID string
	prompt       string
	raw          any
}

// manualLookupInput describes the tool arguments sent by the LLM node.
type manualLookupInput struct {
	Topic string `json:"topic"`
}

// manualLookupResult is the structured output returned to the LLM node.
type manualLookupResult struct {
	Data string `json:"data"`
}

// manualLookupTool calls graph.Interrupt and waits for manual input.
type manualLookupTool struct {
	mu    sync.Mutex
	state graph.State
}

func (t *manualLookupTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        toolNameLookup,
		Description: "Pauses execution so a human can supply external data.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"topic": {
					Type:        "string",
					Description: "Information the assistant needs.",
				},
			},
			Required: []string{"topic"},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"data": {
					Type:        "string",
					Description: "Human-provided answer.",
				},
			},
			Required: []string{"data"},
		},
	}
}

func (t *manualLookupTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	var input manualLookupInput
	if err := json.Unmarshal(jsonArgs, &input); err != nil {
		return nil, fmt.Errorf("manual_lookup: bad arguments: %w", err)
	}

	t.mu.Lock()
	state := t.state
	t.mu.Unlock()
	if state == nil {
		return nil, errors.New("manual_lookup: state unavailable")
	}

	topic := strings.TrimSpace(input.Topic)
	if topic == "" {
		return nil, errors.New("manual_lookup: topic is empty")
	}

	prompt := map[string]any{
		"message": fmt.Sprintf(
			"Manual lookup required for %q. Provide the missing data.",
			topic,
		),
		"topic": topic,
		"node":  nodeTools,
	}

	resumeValue, err := graph.Interrupt(ctx, state, interruptKey, prompt)
	if err != nil {
		return nil, err
	}

	answer, ok := resumeValue.(string)
	if !ok {
		return nil, errors.New("manual_lookup: resume value must be a string")
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return nil, errors.New("manual_lookup: resume value is empty")
	}
	return manualLookupResult{Data: answer}, nil
}

func (t *manualLookupTool) setState(state graph.State) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = state
}

func (t *manualLookupTool) clearState() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = nil
}

// formatInterruptPrompt converts the interrupt payload into a
// human-friendly text block.
func formatInterruptPrompt(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case map[string]any:
		if msg, ok := v["message"].(string); ok {
			return msg
		}
	}
	bytes, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(bytes)
}
