//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates the dynamic AgentTool (agenttool.NewDynamicTool):
// a single, code-defined "dynamic_agent" entrypoint that spins up a
// short-lived sub-agent whose capability surface (tools and per-call
// instruction) is selected by the model within a safety boundary.
//
// Run with -mode=minimal (default) to register the workspace tools and
// dynamic_agent on the orchestrator, or -mode=bounded to register only
// dynamic_agent and keep the tools behind WithCapabilityTools + a template
// (progressive tool disclosure + least-privilege delegation).
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	// defaultModelName works with the OpenAI-compatible proxy. The proxy also
	// supports e.g. "gpt-5" and "deepseek-v4-flash"; override with -model.
	defaultModelName     = "claude-4-5-sonnet-20250929"
	defaultInnerTextMode = string(agenttool.InnerTextModeInclude)
	orchestratorName     = "orchestrator"
	subAgentName         = "subagent"

	// modeMinimal registers the workspace tools and dynamic_agent on the
	// orchestrator so the model narrows the tool subset per call (simplest
	// onboarding; the sub-agent reuses the parent's identity).
	modeMinimal = "minimal"
	// modeBounded registers only dynamic_agent on the orchestrator and keeps the
	// workspace tools behind WithCapabilityTools + a template, so the parent
	// model sees just the tool-name enum while full tool schemas surface only
	// inside the short-lived sub-agent (progressive disclosure + least
	// privilege).
	modeBounded = "bounded"
)

var (
	modelName = flag.String(
		"model",
		defaultModelName,
		"Name of the model to use",
	)
	debugAuthors = flag.Bool(
		"debug",
		false,
		"Print event author names with streamed text",
	)
	showTool = flag.Bool(
		"show-tool",
		false,
		"Show tool outputs (tool.response) in the transcript",
	)
	showInner = flag.Bool(
		"show-inner",
		true,
		"Show the sub-agent transcript forwarded by the dynamic tool",
	)
	innerTextMode = flag.String(
		"inner-text",
		defaultInnerTextMode,
		"Inner text mode: include or exclude",
	)
	demoModeFlag = flag.String(
		"mode",
		modeMinimal,
		"Demo mode: 'minimal' (orchestrator registers the workspace tools + "+
			"dynamic_agent) or 'bounded' (orchestrator registers only "+
			"dynamic_agent; tools live behind WithCapabilityTools + a "+
			"template, showing progressive tool disclosure)",
	)
)

func main() {
	flag.Parse()

	mode, err := parseInnerTextMode(*innerTextMode)
	if err != nil {
		log.Fatalf("invalid -inner-text: %v", err)
	}

	demoMode := strings.ToLower(strings.TrimSpace(*demoModeFlag))
	if demoMode == "" {
		demoMode = modeMinimal
	}
	if demoMode != modeMinimal && demoMode != modeBounded {
		log.Fatalf("invalid -mode %q (want %q or %q)",
			*demoModeFlag, modeMinimal, modeBounded)
	}

	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("⚠️  OPENAI_API_KEY is not set. Export it (and optionally " +
			"OPENAI_BASE_URL) before running, e.g.:")
		fmt.Println(`    export OPENAI_BASE_URL="https://api.openai.com/v1"`)
		fmt.Println(`    export OPENAI_API_KEY="<your-key>"`)
		fmt.Println()
	}

	fmt.Printf("🚀 Dynamic Sub-Agent Tool Example\n")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("Mode: %s\n", demoMode)
	fmt.Printf("Show inner: %t\n", *showInner)
	fmt.Printf("Inner text mode: %s\n", mode)
	fmt.Printf("Show tool: %t\n", *showTool)
	if demoMode == modeBounded {
		fmt.Printf("Workspace tools: calculator, current_time, word_count " +
			"(behind dynamic_agent's capability surface)\n")
	} else {
		fmt.Printf("Workspace tools: calculator, current_time, word_count " +
			"(+ dynamic_agent, all registered on the orchestrator)\n")
	}
	fmt.Printf("Dynamic tool: %s (runs a focused sub-agent)\n",
		agenttool.DefaultDynamicToolName)
	fmt.Println(strings.Repeat("=", 50))

	chat := &dynamicChat{
		modelName:     *modelName,
		mode:          demoMode,
		debugAuthors:  *debugAuthors,
		showTool:      *showTool,
		showInner:     *showInner,
		innerTextMode: mode,
	}
	if err := chat.run(); err != nil {
		log.Fatalf("Chat failed: %v", err)
	}
}

// dynamicChat manages the conversation with the dynamic sub-agent tool.
type dynamicChat struct {
	modelName     string
	mode          string
	runner        runner.Runner
	userID        string
	sessionID     string
	agentName     string
	debugAuthors  bool
	showTool      bool
	showInner     bool
	innerTextMode agenttool.InnerTextMode
}

// run starts the interactive chat session.
func (c *dynamicChat) run() error {
	ctx := context.Background()
	if err := c.setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}
	defer c.runner.Close()
	return c.startChat(ctx)
}

// setup wires the orchestrator agent, its workspace tools, and the dynamic
// sub-agent tool, then builds the runner.
func (c *dynamicChat) setup(_ context.Context) error {
	modelInstance := openai.New(c.modelName)

	// Workspace tools the sub-agent may use. In minimal mode they are also
	// registered directly on the orchestrator; in bounded mode they live ONLY
	// behind the dynamic tool's capability surface.
	calculatorTool := function.NewFunctionTool(
		c.calculate,
		function.WithName("calculator"),
		function.WithDescription(
			"Perform one basic arithmetic operation (add, subtract, "+
				"multiply, divide) on two numbers.",
		),
	)
	timeTool := function.NewFunctionTool(
		c.getCurrentTime,
		function.WithName("current_time"),
		function.WithDescription(
			"Get the current time and date for a timezone "+
				"(UTC, EST, PST, CST, or local).",
		),
	)
	wordCountTool := function.NewFunctionTool(
		c.wordCount,
		function.WithName("word_count"),
		function.WithDescription(
			"Count the words and characters in a piece of text.",
		),
	)
	workspaceTools := []tool.Tool{calculatorTool, timeTool, wordCountTool}

	orchestratorTools, instruction := c.buildModeSetup(
		modelInstance, workspaceTools,
	)

	c.agentName = orchestratorName
	orchestrator := llmagent.New(
		c.agentName,
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(
			"An orchestrator that delegates focused subtasks to "+
				"short-lived sub-agents.",
		),
		llmagent.WithInstruction(instruction),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			MaxTokens:   intPtr(2000),
			Temperature: floatPtr(0.7),
			Stream:      true,
		}),
		llmagent.WithTools(orchestratorTools),
	)

	c.runner = runner.NewRunner("dynamic-agenttool-chat", orchestrator)
	c.userID = "user"
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().UnixNano())

	fmt.Printf("✅ Chat ready! Session: %s\n\n", c.sessionID)
	return nil
}

// buildModeSetup wires the dynamic tool, the orchestrator's tool set, and the
// orchestrator instruction for the selected demo mode.
//
//   - minimal: the orchestrator registers the workspace tools AND dynamic_agent.
//     The dynamic tool derives its max surface from the parent at call time (no
//     template, no capability tools) and the model narrows the subset per call.
//   - bounded: the orchestrator registers ONLY dynamic_agent. The workspace
//     tools live behind WithCapabilityTools + a template, so the parent model
//     sees just the tool-name enum while full tool schemas surface only inside
//     the short-lived sub-agent (progressive disclosure + least privilege).
func (c *dynamicChat) buildModeSetup(
	modelInstance model.Model,
	workspaceTools []tool.Tool,
) (orchestratorTools []tool.Tool, instruction string) {
	// Result-shaping options shared by both modes.
	common := []agenttool.Option{
		agenttool.WithStreamInner(c.showInner),
		agenttool.WithInnerTextMode(c.innerTextMode),
		agenttool.WithSkipSummarization(true),
		// The sub-agent does its own multi-step work; surface only its last
		// complete message as the tool result (instead of concatenating every
		// streamed chunk).
		agenttool.WithResponseMode(agenttool.ResponseModeFinalOnly),
	}

	if c.mode == modeBounded {
		// Template agent: a distinct, tool-less identity that fixes the
		// sub-agent's execution boundary (model, identity, default role). The
		// selected tools are injected per call via a runtime surface patch, so
		// the sub-agent's streamed output is easy to tell apart from the
		// orchestrator.
		subTemplate := llmagent.New(
			subAgentName,
			llmagent.WithModel(modelInstance),
			llmagent.WithDescription("A short-lived, focused worker sub-agent."),
			llmagent.WithInstruction(
				"You are a focused worker sub-agent. Complete the single task "+
					"in the request using ONLY the tools you were granted, then "+
					"report the result concisely. You cannot see the parent "+
					"conversation, so rely solely on the request.",
			),
			llmagent.WithGenerationConfig(model.GenerationConfig{
				MaxTokens:   intPtr(1000),
				Temperature: floatPtr(0.3),
				Stream:      true,
			}),
		)
		opts := append([]agenttool.Option{
			agenttool.WithTemplateAgent(subTemplate),
			// The tools live ONLY here: the parent model sees their names in the
			// dynamic_agent "tools" enum, but their full parameter schemas
			// surface only inside the sub-agent once selected.
			agenttool.WithCapabilityTools(workspaceTools),
		}, common...)
		dynamicTool := agenttool.NewDynamicTool(opts...)
		return []tool.Tool{dynamicTool}, boundedOrchestratorInstruction
	}

	// minimal: no template and no capability tools; the surface is derived from
	// the orchestrator (which also exposes the workspace tools directly).
	dynamicTool := agenttool.NewDynamicTool(common...)
	orchestratorTools = append(orchestratorTools, workspaceTools...)
	orchestratorTools = append(orchestratorTools, dynamicTool)
	return orchestratorTools, minimalOrchestratorInstruction
}

// minimalOrchestratorInstruction is used in minimal mode, where the
// orchestrator also has the workspace tools registered directly.
const minimalOrchestratorInstruction = "You are an orchestrator. You have " +
	"three direct tools (calculator, current_time, word_count) and a special " +
	"'dynamic_agent' tool that runs a short-lived sub-agent.\n\n" +
	"Use direct tools for simple one-step answers. Use 'dynamic_agent' according " +
	"to its tool description when a task should be delegated to a focused child " +
	"run.\n\n" +
	"When you call 'dynamic_agent':\n" +
	"- Put EVERYTHING the sub-agent needs into 'request'; by default it cannot " +
	"see this conversation.\n" +
	"- Use 'tools' to grant only the minimal tools the subtask needs " +
	"(by exact name).\n" +
	"- Use 'instruction' to give the sub-agent a clear role for that task.\n\n" +
	"If the user asks for two independent subtasks, you may run two separate " +
	"sub-agents. After a sub-agent returns, summarize its result for the user."

// boundedOrchestratorInstruction is used in bounded mode, where the
// orchestrator's ONLY tool is dynamic_agent; the workspace tools live behind
// its capability surface and can only be used by spawning a sub-agent.
const boundedOrchestratorInstruction = "You are an orchestrator. Your ONLY " +
	"tool is 'dynamic_agent', which runs a short-lived sub-agent. You cannot " +
	"call calculator, current_time, or word_count directly; delegate every " +
	"subtask by spawning a sub-agent.\n\n" +
	"When you call 'dynamic_agent':\n" +
	"- Put EVERYTHING the sub-agent needs into 'request'; by default it cannot " +
	"see this conversation.\n" +
	"- Use 'tools' to grant only the minimal tools the subtask needs, choosing " +
	"from the names offered by the 'tools' field (by exact name).\n" +
	"- Use 'instruction' to give the sub-agent a clear role for that task.\n\n" +
	"If the user asks for two independent subtasks, you may run two separate " +
	"sub-agents. After a sub-agent returns, summarize its result for the user."

// startChat runs the interactive conversation loop.
func (c *dynamicChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("💡 Try:")
	fmt.Println("   • Use a sub-agent to compute (123 * 456) + 789.")
	fmt.Println("   • Use a sub-agent to tell me the current time in UTC.")
	fmt.Println("   • Count the words in: \"the quick brown fox\" using a sub-agent.")
	fmt.Println("   Commands: /new (new session), /exit (quit)")
	fmt.Println()

	for {
		fmt.Print("👤 You: ")
		if !scanner.Scan() {
			break
		}
		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}
		switch strings.ToLower(userInput) {
		case "/exit":
			fmt.Println("👋 Goodbye!")
			return nil
		case "/new":
			c.startNewSession()
			continue
		}
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

// processMessage handles a single message exchange.
func (c *dynamicChat) processMessage(
	ctx context.Context,
	userMessage string,
) error {
	message := model.NewUserMessage(userMessage)
	eventChan, err := c.runner.Run(ctx, c.userID, c.sessionID, message)
	if err != nil {
		return fmt.Errorf("failed to run agent: %w", err)
	}
	return c.processStreamingResponse(eventChan)
}

// processStreamingResponse handles streaming with tool call visualization.
func (c *dynamicChat) processStreamingResponse(
	eventChan <-chan *event.Event,
) error {
	fmt.Print("🤖 Assistant: ")
	var (
		assistantStarted bool
		fullContent      strings.Builder
	)
	for ev := range eventChan {
		c.handleEvent(ev, &assistantStarted, &fullContent)
	}
	fmt.Println()
	return nil
}

// handleEvent processes one event.
func (c *dynamicChat) handleEvent(
	ev *event.Event,
	assistantStarted *bool,
	fullContent *strings.Builder,
) {
	if ev.Error != nil {
		fmt.Printf("\n❌ Error: %s\n", ev.Error.Message)
		return
	}
	if c.handleToolCalls(ev, assistantStarted) {
		return
	}
	if c.handleInnerAgentStreaming(ev) {
		return
	}
	if c.handleAssistantStreaming(ev, assistantStarted, fullContent) {
		return
	}
	c.handleToolResponses(ev)
}

// handleToolCalls prints tool call markers (including dynamic_agent args).
func (c *dynamicChat) handleToolCalls(
	ev *event.Event,
	assistantStarted *bool,
) bool {
	if ev.Response == nil || len(ev.Response.Choices) == 0 {
		return false
	}
	ch := ev.Response.Choices[0]
	if len(ch.Message.ToolCalls) == 0 {
		return false
	}
	if *assistantStarted {
		fmt.Printf("\n")
	}
	fmt.Printf("🔧 Tool calls initiated:\n")
	for _, tc := range ch.Message.ToolCalls {
		fmt.Printf("   • %s (ID: %s)\n", tc.Function.Name, tc.ID)
		if len(tc.Function.Arguments) > 0 {
			fmt.Printf("     Args: %s\n", string(tc.Function.Arguments))
		}
	}
	fmt.Printf("\n🔄 Executing tools...\n")
	return true
}

// handleInnerAgentStreaming prints forwarded prose from a distinct child agent.
// In minimal mode the dynamic child reuses the orchestrator identity, so its
// text is handled like normal assistant streaming instead of being separated
// here.
func (c *dynamicChat) handleInnerAgentStreaming(ev *event.Event) bool {
	if !c.showInner ||
		ev.Author == c.agentName ||
		ev.Response == nil ||
		len(ev.Response.Choices) == 0 {
		return false
	}
	ch := ev.Response.Choices[0]
	if ch.Delta.Content == "" {
		return false
	}
	if c.debugAuthors {
		fmt.Printf("[%s] ", ev.Author)
	}
	fmt.Print(ch.Delta.Content)
	return true
}

// handleAssistantStreaming prints the orchestrator's own streamed text.
func (c *dynamicChat) handleAssistantStreaming(
	ev *event.Event,
	assistantStarted *bool,
	fullContent *strings.Builder,
) bool {
	if ev.Author != c.agentName ||
		ev.Response == nil ||
		len(ev.Response.Choices) == 0 {
		return false
	}
	ch := ev.Response.Choices[0]
	if ch.Delta.Content == "" {
		return false
	}
	if c.debugAuthors && !*assistantStarted {
		fmt.Printf("[%s] ", ev.Author)
	}
	*assistantStarted = true
	fmt.Print(ch.Delta.Content)
	fullContent.WriteString(ch.Delta.Content)
	return true
}

// handleToolResponses optionally prints the aggregated tool.response.
func (c *dynamicChat) handleToolResponses(ev *event.Event) bool {
	if ev.Response == nil ||
		ev.Object != model.ObjectTypeToolResponse ||
		len(ev.Response.Choices) == 0 {
		return false
	}
	ch := ev.Response.Choices[0]
	if ch.Delta.Content != "" {
		if c.showTool && !c.showInner {
			fmt.Printf("\n🛠️  tool> %s", ch.Delta.Content)
		}
		return true
	}
	if ch.Message.Content != "" {
		if c.showTool {
			fmt.Printf(
				"\n✅ Tool response (ID: %s): %s\n",
				ch.Message.ToolID,
				strings.TrimSpace(ch.Message.Content),
			)
		} else {
			fmt.Printf("\n✅ Tool execution completed.\n")
		}
		return true
	}
	fmt.Printf("\n✅ Tool execution completed.\n")
	return true
}

// startNewSession creates a new session.
func (c *dynamicChat) startNewSession() {
	c.sessionID = fmt.Sprintf("chat-session-%d", time.Now().UnixNano())
	fmt.Printf("🔄 New session started: %s\n\n", c.sessionID)
}

func parseInnerTextMode(mode string) (agenttool.InnerTextMode, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", string(agenttool.InnerTextModeInclude):
		return agenttool.InnerTextModeInclude, nil
	case string(agenttool.InnerTextModeExclude):
		return agenttool.InnerTextModeExclude, nil
	default:
		return "", fmt.Errorf("unsupported mode %q", mode)
	}
}
