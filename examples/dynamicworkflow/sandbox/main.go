//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates Dynamic Workflow with its generated Python glue
// running in the managed OS sandbox while child Agents and tools remain in Go.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/dynamicworkflow"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String(
		"model",
		"gpt-5",
		"Model name for the OpenAI-compatible endpoint",
	)
	prompt = flag.String(
		"prompt",
		"",
		"Optional single-turn prompt. If empty, start interactive chat.",
	)
	showWorkflowCode = flag.Bool(
		"show-workflow-code",
		false,
		"Print the generated Python workflow code before executing it",
	)
	workflowTimeout = flag.Duration(
		"workflow-timeout",
		10*time.Minute,
		"Maximum workflow duration; zero relies only on the caller context",
	)
	python = flag.String(
		"python",
		"",
		"Explicit Python interpreter; empty resolves python3 inside the sandbox; custom paths may need sandbox read grants",
	)
)

func main() {
	flag.Parse()
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Fprintln(
			os.Stderr,
			"OPENAI_API_KEY is required (OPENAI_BASE_URL is optional).",
		)
		os.Exit(2)
	}

	ctx := context.Background()
	modelInstance := openai.New(*modelName)
	workflowTool, err := buildWorkflowTool(
		modelInstance,
		*workflowTimeout,
		*python,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build workflow tool: %v\n", err)
		os.Exit(1)
	}
	if *showWorkflowCode {
		workflowTool = workflowCodePrintingTool{inner: workflowTool}
	}

	root := llmagent.New(
		"sandbox_workflow_assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription(
			"Builds temporary, sandboxed workflows for operational planning and review.",
		),
		llmagent.WithInstruction(`Answer simple requests directly. For a task that benefits from temporary roles, independent analysis, tool-backed verification, or review-and-revision, call run_workflow exactly once and do not return Python source. Call agent() for every requested business role; never simulate a role or hard-code its output in Python.

Important: run_workflow automatically wraps its code in an async function. Supply only that function body. A valid body starts directly with statements such as "result = await agent(...)" and ends with a top-level "return result". Code beginning with "async def run():" is invalid. Never define a wrapper, call asyncio.run(), or import anything.

This example has one operations_agent template, so template is usually omitted. Use tools=[] for planning, analysis, review, and revision roles. Use tools=["get_service_health"] only for a verifier explicitly instructed to inspect current service health before deciding, and preserve the exact catalog, checkout, or search service name from the request. Allow at most two revision attempts, followed by a final review if needed, and return a concise final result.`),
		llmagent.WithTools([]tool.Tool{workflowTool}),
	)
	r := runner.NewRunner("sandbox-dynamic-workflow-example", root)
	defer r.Close()

	chat := &sandboxWorkflowChat{
		runner:    r,
		userID:    "demo-user",
		sessionID: newSessionID(),
		modelName: *modelName,
	}
	if strings.TrimSpace(*prompt) != "" {
		if err := chat.processMessage(ctx, *prompt); err != nil {
			fmt.Fprintf(os.Stderr, "run agent: %v\n", err)
			os.Exit(1)
		}
		return
	}
	chat.printBanner()
	if err := chat.start(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "interactive chat failed: %v\n", err)
		os.Exit(1)
	}
}

func buildWorkflowTool(
	m model.Model,
	timeout time.Duration,
	pythonPath string,
) (tool.CallableTool, error) {
	healthTool := function.NewFunctionTool(
		getServiceHealth,
		function.WithName("get_service_health"),
		function.WithDescription(
			"Return a deterministic demo health snapshot for catalog, checkout, "+
				"or search. Use it only when a workflow role must verify "+
				"operational readiness.",
		),
	)
	operationsAgent := llmagent.New(
		"operations_agent",
		llmagent.WithModel(m),
		// Persist child events in the shared Session while keeping ancestor
		// tool transcripts out of each workflow-local model request.
		llmagent.WithMessageFilterMode(llmagent.IsolatedRequest),
		llmagent.WithDescription(
			"A neutral template for a workflow-local planner, verifier, reviewer, or reviser.",
		),
		llmagent.WithInstruction(`Follow the dynamic instance instruction as the complete definition of your current role. Treat input as JSON context. When explicitly asked for a current health check, call get_service_health exactly once with the requested catalog, checkout, or search service name before answering, and reproduce its status, error_rate, p95_latency_ms, and observations exactly. Otherwise do not call tools. Never call a tool not offered to your current role. Distinguish observed health data from recommendations. When a structured output contract is requested, return data conforming to it.`),
		llmagent.WithTools([]tool.Tool{healthTool}),
	)

	sandboxRunner := dynamicworkflow.NewSandboxRunner()
	sandboxRunner.Timeout = timeout
	sandboxRunner.Python = strings.TrimSpace(pythonPath)
	return dynamicworkflow.NewTool(
		sandboxRunner,
		[]agent.Agent{operationsAgent},
	)
}

type serviceHealthRequest struct {
	Service string `json:"service" jsonschema:"description=Service name: catalog, checkout, or search."`
}

type serviceHealthResult struct {
	Service      string   `json:"service"`
	Status       string   `json:"status"`
	ErrorRate    float64  `json:"error_rate"`
	P95LatencyMS int      `json:"p95_latency_ms"`
	Observations []string `json:"observations"`
}

func getServiceHealth(
	_ context.Context,
	req serviceHealthRequest,
) (serviceHealthResult, error) {
	switch strings.ToLower(strings.TrimSpace(req.Service)) {
	case "catalog":
		return serviceHealthResult{
			Service:      "catalog",
			Status:       "healthy",
			ErrorRate:    0.002,
			P95LatencyMS: 84,
			Observations: []string{
				"Read traffic is stable.",
				"Database utilization is below the alert threshold.",
				"No active incidents are associated with the service.",
			},
		}, nil
	case "checkout":
		return serviceHealthResult{
			Service:      "checkout",
			Status:       "degraded",
			ErrorRate:    0.018,
			P95LatencyMS: 420,
			Observations: []string{
				"Latency increased during the latest traffic peak.",
				"One downstream payment dependency is recovering.",
			},
		}, nil
	case "search":
		return serviceHealthResult{
			Service:      "search",
			Status:       "healthy",
			ErrorRate:    0.004,
			P95LatencyMS: 132,
			Observations: []string{
				"Index freshness is within the target window.",
				"Capacity has headroom for the expected rollout traffic.",
			},
		}, nil
	default:
		return serviceHealthResult{}, fmt.Errorf(
			"unknown service %q; expected catalog, checkout, or search",
			req.Service,
		)
	}
}

type sandboxWorkflowChat struct {
	runner    runner.Runner
	userID    string
	sessionID string
	modelName string
}

func newSessionID() string {
	return fmt.Sprintf("sandbox-dynamic-workflow-%d", time.Now().UnixNano())
}

func (c *sandboxWorkflowChat) printBanner() {
	fmt.Println("Sandbox Dynamic Workflow Example")
	fmt.Printf("Model: %s\n", c.modelName)
	fmt.Printf("Session: %s\n", c.sessionID)
	fmt.Printf("Workflow timeout: %s\n", *workflowTimeout)
	if strings.TrimSpace(*python) == "" {
		fmt.Println("Python: sandbox-resolved python3")
	} else {
		fmt.Printf("Python: %s\n", *python)
	}
	fmt.Println("Type '/new' to start a new session or '/exit' to quit.")
	fmt.Println("Sample prompts:")
	fmt.Println("  Build a temporary team to assess rolling out an in-memory cache for the catalog service. Have a planner propose the rollout, a verifier call get_service_health, and a reviewer allow at most two revisions before making a final decision.")
	fmt.Println("  Compare two rollout plans in parallel for the search service, verify current health, and recommend the safer plan.")
	fmt.Println(strings.Repeat("=", 72))
}

func (c *sandboxWorkflowChat) start(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		switch strings.ToLower(input) {
		case "/exit":
			fmt.Println("Goodbye.")
			return nil
		case "/new":
			c.sessionID = newSessionID()
			fmt.Printf("Started new session: %s\n\n", c.sessionID)
			continue
		}
		if err := c.processMessage(ctx, input); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (c *sandboxWorkflowChat) processMessage(
	ctx context.Context,
	userMessage string,
) error {
	events, err := c.runner.Run(
		ctx,
		c.userID,
		c.sessionID,
		model.NewUserMessage(userMessage),
	)
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}
	printEvents(events)
	return nil
}

type workflowCodePrintingTool struct {
	inner tool.CallableTool
}

func (t workflowCodePrintingTool) Declaration() *tool.Declaration {
	return t.inner.Declaration()
}

func (t workflowCodePrintingTool) Call(
	ctx context.Context,
	raw []byte,
) (any, error) {
	t.printWorkflowCode(raw)
	return t.inner.Call(ctx, raw)
}

func (t workflowCodePrintingTool) StreamableCall(
	ctx context.Context,
	raw []byte,
) (*tool.StreamReader, error) {
	t.printWorkflowCode(raw)
	streamable, ok := t.inner.(tool.StreamableTool)
	if !ok {
		return nil, fmt.Errorf(
			"workflow code printer: inner tool is not streamable",
		)
	}
	return streamable.StreamableCall(ctx, raw)
}

func (t workflowCodePrintingTool) TRPCAgentGoStructuredStreamErrorsOptIn() bool {
	structured, ok := t.inner.(interface {
		TRPCAgentGoStructuredStreamErrorsOptIn() bool
	})
	return ok && structured.TRPCAgentGoStructuredStreamErrorsOptIn()
}

func (t workflowCodePrintingTool) printWorkflowCode(raw []byte) {
	var input struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &input); err == nil &&
		strings.TrimSpace(input.Code) != "" {
		fmt.Fprintln(
			os.Stderr,
			"\n===== generated sandbox workflow code =====",
		)
		fmt.Fprintln(os.Stderr, input.Code)
		fmt.Fprintln(
			os.Stderr,
			"===== end generated sandbox workflow code =====",
		)
		fmt.Fprintln(os.Stderr)
	}
}

func printEvents(events <-chan *event.Event) {
	eventCount := 0
	for evt := range events {
		if evt == nil {
			continue
		}
		eventCount++
		if evt.Response != nil && evt.Response.Error != nil {
			fmt.Fprintf(
				os.Stderr,
				"[%s] error: %s\n",
				eventLabel(evt),
				evt.Response.Error.Message,
			)
			continue
		}
		if printToolEvent(evt) {
			continue
		}
		if evt.StructuredOutput != nil {
			if raw, err := json.Marshal(evt.StructuredOutput); err == nil {
				fmt.Printf("[%s] structured: %s\n", eventLabel(evt), raw)
			}
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		content := choice.Delta.Content
		if content == "" {
			content = choice.Message.Content
		}
		if strings.TrimSpace(content) != "" {
			fmt.Printf("[%s] %s\n", eventLabel(evt), content)
		}
	}
	if eventCount == 0 {
		fmt.Fprintln(os.Stderr, "no events were emitted")
	}
}

func printToolEvent(evt *event.Event) bool {
	if evt == nil || evt.Response == nil {
		return false
	}
	if evt.Response.IsToolCallResponse() {
		for _, choice := range evt.Response.Choices {
			for _, call := range append(
				choice.Message.ToolCalls,
				choice.Delta.ToolCalls...,
			) {
				fmt.Printf(
					"[%s] tool call: %s",
					eventLabel(evt),
					call.Function.Name,
				)
				if call.ID != "" {
					fmt.Printf(" (id: %s)", call.ID)
				}
				if len(call.Function.Arguments) > 0 {
					fmt.Printf(" args: %s", string(call.Function.Arguments))
				}
				fmt.Println()
			}
		}
		return true
	}
	if evt.Response.IsToolResultResponse() {
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			if msg.ToolID == "" && choice.Delta.ToolID != "" {
				msg = choice.Delta
			}
			if msg.ToolID == "" {
				continue
			}
			name := msg.ToolName
			if name == "" {
				name = "tool"
			}
			content := strings.TrimSpace(msg.Content)
			runes := []rune(content)
			if len(runes) > 240 {
				content = string(runes[:240]) + "..."
			}
			fmt.Printf(
				"[%s] tool result: %s (id: %s) %s\n",
				eventLabel(evt),
				name,
				msg.ToolID,
				content,
			)
		}
		return true
	}
	return false
}

func eventLabel(evt *event.Event) string {
	if evt == nil {
		return "unknown"
	}
	label := evt.Author
	if label == "" {
		label = "unknown"
	}
	if evt.ParentMetadata != nil && evt.ParentMetadata.TriggerType != "" {
		label += " via " + evt.ParentMetadata.TriggerType
	}
	return label
}
