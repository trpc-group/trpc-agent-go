//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates model‑orchestrated external tools that run
// outside the graph process. The Large Language Model (LLM) first returns
// a tool call (for example: extract document content), the client executes
// that tool out‑of‑process and feeds the result back, then the model may
// request another tool (for example: summarize the extracted content) and
// continue. We implement this by intercepting tool calls in a callback,
// emitting a graph interrupt with the tool call details, and resuming with
// the user‑supplied tool result.
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
	nodeTools     = "external_tools"
	nodeFinish    = "finalize"

	// External + internal tool names for the demo.
	toolNameFetch     = "external_fetch"
	toolNameSummarize = "summarize_text"
	toolNameFormat    = "format_bullets"

	// Interrupt key for passing external tool results back to callback.
	interruptKeyTool = "external_tool_result"

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

	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer workflow.runner.Close()

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

	// coordinator intercepts tool calls and raises interrupts so the
	// client can execute tools externally and feed results back.
	coordinator *externalCoordinator

	currentLineage string
	pending        *pendingResume
}

// setup prepares the graph, agent, runner, and checkpoint services.
func (w *externalToolWorkflow) setup() error {
	w.coordinator = &externalCoordinator{}

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

	// Only external_fetch is external; others run as normal tools.
	tools := map[string]tool.Tool{
		toolNameFetch:     declaredTool(fetchDecl()),
		toolNameSummarize: summarizerTool{},
		toolNameFormat:    formatterTool{},
	}

	stateGraph := graph.NewStateGraph(schema)

	modelInstance := openai.New(w.modelName)
	// Lower temperature to reduce chatty clarifications and make
	// tool usage more consistent for this demo.
	var temp float64 = 0.0

	// Lower temperature to reduce chatty clarifications and keep tools
	// deterministic for this demo.
	stateGraph.
		AddNode(
			nodePrepare,
			w.prepareInput,
			graph.WithName("Capture Question"),
			graph.WithDescription(
				"Validates and stores the user question for later "+
					"nodes.",
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
				"Plans the answer and requests external tools in "+
					"order.",
			),
		).
		AddNode(
			nodeTools,
			w.wrapToolsNode(graph.NewToolsNodeFunc(tools)),
			graph.WithNodeType(graph.NodeTypeTool),
			graph.WithToolCallbacks(w.coordinator.callbacks()),
			graph.WithName("External Tools"),
			graph.WithDescription(
				"Intercepts tool calls and pauses for client‑executed "+
					"results.",
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
		"You coordinate external + internal tools.",
		"Workflow: first call external_fetch to obtain content.",
		"Then call summarize_text on that content.",
		"Optionally call format_bullets to format output.",
		"Do not fabricate tool results. Wait for the resume.",
		"Follow user instructions if they change the order.",
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
		w.coordinator.setState(state)
		defer w.coordinator.clearState()
		res, err := base(ctx, state)
		if err != nil {
			var interrupt *graph.InterruptError
			if errors.As(err, &interrupt) {
				return nil, interrupt
			}
		}
		return res, err
	}
}

// interactive provides the command-line interface.
func (w *externalToolWorkflow) interactive(ctx context.Context) error {
	fmt.Println("🔌 External Tools (Client‑Executed)")
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
			// Submit a simple placeholder content for external extract.
			content := strings.TrimSpace(
				strings.TrimPrefix(line, "/resume"),
			)
			if content == "" {
				fmt.Println("Usage: /resume <content>")
				continue
			}
			if err := w.resume(ctx, content); err != nil {
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
	fmt.Println("  /resume <content> Resume with extract result")
	fmt.Println("  /help            Show this help message")
	fmt.Println("  /exit            Quit the program")
	fmt.Println()
}

// ask launches a new run unless an interrupt is waiting for resume.
func (w *externalToolWorkflow) ask(ctx context.Context, input string) error {
	if w.pending != nil {
		fmt.Println("⚠️  Workflow is paused. Use /resume <content>.")
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
		fmt.Println("\n⏸️  Waiting for external tool result.")
	} else {
		w.currentLineage = w.newLineage()
	}
	return nil
}

// resume continues a paused run with the provided tool result.
func (w *externalToolWorkflow) resume(
	ctx context.Context,
	content string,
) error {
	if w.pending == nil {
		fmt.Println("Nothing to resume right now.")
		fmt.Println("Tip: wait for a tool call first.")
		return nil
	}
	// Provide a simple placeholder content to the external tool.
	cmd := &graph.Command{ResumeMap: map[string]any{
		interruptKeyTool: content,
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
		fmt.Println("\n⚠️  Waiting for next external tool result.")
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
		latest, err := w.manager.Latest(ctx, w.currentLineage, "")
		if err == nil && latest != nil && latest.Checkpoint != nil &&
			latest.Checkpoint.IsInterrupted() {
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
	fmt.Println("\n🛑 External tool requested:")
	fmt.Printf("   %s\n", w.pending.prompt)
	fmt.Println("   Reply: /resume <content>")
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

// ----- External tool interception layer -----

// externalCoordinator implements a ToolCallbacks.BeforeTool that pauses the
// graph and resumes with client‑provided tool results.
type externalCoordinator struct {
	mu    sync.Mutex
	state graph.State
}

func (c *externalCoordinator) setState(state graph.State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = state
}

func (c *externalCoordinator) clearState() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = nil
}

func (c *externalCoordinator) callbacks() *tool.Callbacks {
	cb := tool.NewCallbacks()
	cb.RegisterBeforeTool(c.before)
	return cb
}

// before intercepts tool execution, raises an interrupt and waits for
// the client to submit the tool result.
func (c *externalCoordinator) before(
	ctx context.Context,
	args *tool.BeforeToolArgs,
) (*tool.BeforeToolResult, error) {
	// Only intercept the external extract tool; others run normally.
	if args.ToolName != toolNameFetch {
		return nil, nil
	}
	_, _ = tool.ToolCallIDFromContext(ctx)
	state := c.getState()
	if state == nil {
		return nil, errors.New("externalCoordinator: state unavailable")
	}
	prompt := map[string]any{
		"message": "Run extract externally and return content.",
		"tool":    args.ToolName,
	}
	resume, err := graph.Interrupt(
		ctx, state, interruptKeyTool, prompt,
	)
	if err != nil {
		return nil, err
	}
	// Convert a simple string into {"content": string} object.
	parsed := parseExternalResult(resume)
	return &tool.BeforeToolResult{
		CustomResult: parsed,
	}, nil
}

func (c *externalCoordinator) getState() graph.State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// parseExternalResult converts the resume payload into a Go value.
// Accepts either a raw JSON string or a map with field "result".
func parseExternalResult(v any) any {
	// Preferred: plain string becomes content field.
	if s, ok := v.(string); ok {
		s = strings.TrimSpace(s)
		if s == "" {
			s = "EXTERNAL_CONTENT"
		}
		return map[string]any{"content": s}
	}
	// If client provided a map with content, pass it.
	if m, ok := v.(map[string]any); ok {
		if _, ok2 := m["content"]; ok2 {
			return m
		}
		if s, ok2 := m["result"].(string); ok2 {
			return map[string]any{"content": s}
		}
	}
	// Fallback: stringify.
	return map[string]any{"content": fmt.Sprint(v)}
}

// decodeJSONAny decodes a JSON string to an arbitrary Go value.
// (decodeJSONAny removed; not needed in simplified flow)

// declaredTool wraps a declaration as a non‑callable tool so that the
// BeforeTool callback can fully control execution.
type declaredToolWrapper struct{ d *tool.Declaration }

func (w declaredToolWrapper) Declaration() *tool.Declaration { return w.d }

func declaredTool(d *tool.Declaration) tool.Tool {
	return declaredToolWrapper{d: d}
}

// Declarations for demo tools.
func fetchDecl() *tool.Declaration {
	return &tool.Declaration{
		Name:        toolNameFetch,
		Description: "Fetch plain text content externally (URL or hint).",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"source": {Type: "string", Description: "URL or source"},
			},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"content": {
					Type:        "string",
					Description: "Fetched text content",
				},
			},
		},
	}
}

func summarizeDecl() *tool.Declaration {
	return &tool.Declaration{
		Name:        toolNameSummarize,
		Description: "Summarize given text into concise bullet points.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"text": {
					Type:        "string",
					Description: "Input text to summarize",
				},
			},
			Required: []string{"text"},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"summary": {Type: "string"},
			},
		},
	}
}

func formatDecl() *tool.Declaration {
	return &tool.Declaration{
		Name:        toolNameFormat,
		Description: "Format text into bullet points.",
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"text": {Type: "string"},
			},
			Required: []string{"text"},
		},
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"formatted": {Type: "string"},
			},
		},
	}
}

// ----- Internal logic tools (callable) -----

// summarizerTool implements a simple in-process summarization.
type summarizerTool struct{}

func (summarizerTool) Declaration() *tool.Declaration {
	return summarizeDecl()
}

func (summarizerTool) Call(
	ctx context.Context, jsonArgs []byte,
) (any, error) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(jsonArgs, &in); err != nil {
		return nil, fmt.Errorf("summarize: bad args: %w", err)
	}
	txt := strings.TrimSpace(in.Text)
	if txt == "" {
		return nil, errors.New("summarize: text is empty")
	}
	// Create a tiny, readable summary for demo purposes.
	sum := clip(txt, 120)
	if len(sum) < len(txt) {
		sum += " ..."
	}
	return map[string]any{"summary": sum}, nil
}

// formatterTool formats text into simple bullets.
type formatterTool struct{}

func (formatterTool) Declaration() *tool.Declaration { return formatDecl() }

func (formatterTool) Call(
	ctx context.Context, jsonArgs []byte,
) (any, error) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(jsonArgs, &in); err != nil {
		return nil, fmt.Errorf("format: bad args: %w", err)
	}
	t := strings.TrimSpace(in.Text)
	if t == "" {
		return nil, errors.New("format: text is empty")
	}
	lines := toBullets(t)
	return map[string]any{"formatted": lines}, nil
}

// clip returns a string trimmed to n runes.
func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// toBullets turns text into a bullet list.
func toBullets(s string) string {
	// Split by sentences; keep it simple for demo.
	parts := strings.Split(s, ".")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, "• "+p)
	}
	if len(out) == 0 {
		return "• " + s
	}
	return strings.Join(out, "\n")
}
