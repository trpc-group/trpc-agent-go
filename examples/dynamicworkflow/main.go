//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates a normal LLMAgent that may create one temporary
// dynamic workflow. One neutral registered template can become multiple
// workflow-local roles through dynamic AgentSpecs while the root remains in
// control.
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
	modelName = flag.String("model", "gpt-5", "Model name for the OpenAI-compatible endpoint")
	prompt    = flag.String(
		"prompt",
		"",
		"Optional single-turn prompt. If empty, start interactive chat.",
	)
	showWorkflowCode = flag.Bool(
		"show-workflow-code",
		true,
		"Print the generated Python workflow code before executing it",
	)
)

func main() {
	flag.Parse()
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Fprintln(os.Stderr, "OPENAI_API_KEY is required (OPENAI_BASE_URL is optional).")
		os.Exit(2)
	}

	ctx := context.Background()
	modelInstance := openai.New(*modelName)
	workflowTool, err := buildWorkflowTool(modelInstance)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build workflow tool: %v\n", err)
		os.Exit(1)
	}
	if *showWorkflowCode {
		workflowTool = debugWorkflowCodeTool{inner: workflowTool}
	}

	root := llmagent.New(
		"workflow_assistant",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Creates temporary, role-based workflows for tasks that need collaboration or revision."),
		llmagent.WithInstruction(`Answer simple requests directly. For tasks that require role delegation, multi-step collaboration, concurrent analysis, or conditional iteration, call run_workflow exactly once and do not answer with Python source. This example registers one neutral general_agent template, so template is usually omitted. Use agent(...) to create workflow-local roles; the template fixes model, executor, tools, and permissions, while each dynamic instruction defines the temporary business role. For ordinary drafting, analysis, summaries, and non-policy pipeline stages, pass tools=[]. Only when a child is explicitly reviewing, approving, or rejecting against a team collaboration guideline should it use tools=["lookup_policy"] and be instructed to call lookup_policy before deciding. If a child must visibly demonstrate the policy lookup, prefer plain text output over structured_output for that child.`),
		llmagent.WithTools([]tool.Tool{workflowTool}),
	)
	r := runner.NewRunner("dynamic-workflow-example", root)
	defer r.Close()

	chat := &dynamicWorkflowChat{
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
	if err := chat.startChat(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "interactive chat failed: %v\n", err)
		os.Exit(1)
	}
}

type dynamicWorkflowChat struct {
	runner    runner.Runner
	userID    string
	sessionID string
	modelName string
}

func newSessionID() string {
	return fmt.Sprintf("dynamic-workflow-%d", time.Now().UnixNano())
}

func (c *dynamicWorkflowChat) printBanner() {
	fmt.Printf("Dynamic Workflow Example\n")
	fmt.Printf("Model: %s\n", c.modelName)
	fmt.Printf("Session: %s\n", c.sessionID)
	fmt.Println("Type '/new' to start a new session or '/exit' to quit.")
	fmt.Println("Sample prompts:")
	fmt.Println("  Use a temporary reviewer to check whether replacing daily status meetings with async updates and one weekly decision meeting follows the team collaboration guideline.")
	fmt.Println("  Build a temporary team: propose a collaboration plan for a remote team, have a reviewer check it against the team collaboration guideline, and revise the plan with the feedback.")
	fmt.Println("  Use run_workflow once. Use pipeline over two plans: plan A is daily status meetings, plan B is async written updates plus one weekly decision meeting. Stage 1 asks a child agent to analyze the plan. Stage 2 asks a child agent to make a final recommendation based on stage 1.")
	fmt.Println(strings.Repeat("=", 72))
}

func (c *dynamicWorkflowChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !scanner.Scan() {
			break
		}
		userInput := strings.TrimSpace(scanner.Text())
		if userInput == "" {
			continue
		}
		switch strings.ToLower(userInput) {
		case "/exit":
			fmt.Println("Goodbye.")
			return nil
		case "/new":
			c.sessionID = newSessionID()
			fmt.Printf("Started new session: %s\n\n", c.sessionID)
			continue
		}
		if err := c.processMessage(ctx, userInput); err != nil {
			fmt.Printf("Error: %v\n", err)
		}
		fmt.Println()
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("input scanner error: %w", err)
	}
	return nil
}

func (c *dynamicWorkflowChat) processMessage(
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

func buildWorkflowTool(m model.Model) (tool.CallableTool, error) {
	policyTool := function.NewFunctionTool(
		lookupPolicy,
		function.WithName("lookup_policy"),
		function.WithDescription("Look up the team collaboration guideline for an explicit review, approval, or rejection decision. Use topic \"remote_collaboration\"."),
	)
	generalAgent := llmagent.New(
		"general_agent",
		llmagent.WithModel(m),
		llmagent.WithDescription("A neutral execution template for one workflow-local role defined by its dynamic instruction."),
		llmagent.WithInstruction(`Follow the dynamic instance instruction as the complete definition of your current role. Treat the input as JSON context. Do not assume a business domain from this template. Use lookup_policy only when your dynamic instruction explicitly asks you to review, approve, or reject something against a team collaboration guideline, or explicitly tells you to call lookup_policy. Do not use lookup_policy for ordinary summaries, generic analysis, operational-risk review, or pipeline stages that are not policy reviews. When a structured output contract is requested, return data that conforms to it.`),
		llmagent.WithTools([]tool.Tool{policyTool}),
	)

	return dynamicworkflow.NewTool(
		dynamicworkflow.LocalRunner{},
		[]agent.Agent{generalAgent},
	)
}

type policyLookupRequest struct {
	Topic string `json:"topic" jsonschema:"description=Policy topic to look up, for example remote_collaboration."`
}

type policyLookupResult struct {
	Topic      string   `json:"topic"`
	Guidelines []string `json:"guidelines"`
}

func lookupPolicy(_ context.Context, req policyLookupRequest) (policyLookupResult, error) {
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		topic = "remote_collaboration"
	}
	switch topic {
	case "remote_collaboration", "meeting_load", "async_updates":
		return policyLookupResult{
			Topic: "remote_collaboration",
			Guidelines: []string{
				"Prefer asynchronous written updates for routine status sharing.",
				"Keep recurring meetings below three hours per person per week unless explicitly justified.",
				"Every recurring meeting should have an owner, agenda, decision log, and cancellation rule.",
				"Escalate blockers with a clear owner and deadline instead of adding broad status meetings.",
			},
		}, nil
	default:
		return policyLookupResult{
			Topic: topic,
			Guidelines: []string{
				"No specific policy exists for this topic; ask for explicit approval before making a high-impact change.",
			},
		}, nil
	}
}

type debugWorkflowCodeTool struct {
	inner tool.CallableTool
}

func (t debugWorkflowCodeTool) Declaration() *tool.Declaration {
	return t.inner.Declaration()
}

func (t debugWorkflowCodeTool) Call(ctx context.Context, raw []byte) (any, error) {
	var input struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &input); err == nil &&
		strings.TrimSpace(input.Code) != "" {
		fmt.Fprintln(os.Stderr, "\n===== generated dynamic workflow code =====")
		fmt.Fprintln(os.Stderr, input.Code)
		fmt.Fprintln(os.Stderr, "===== end generated dynamic workflow code =====")
		fmt.Fprintln(os.Stderr)
	}
	return t.inner.Call(ctx, raw)
}

func printEvents(events <-chan *event.Event) {
	eventCount := 0
	for evt := range events {
		if evt == nil {
			continue
		}
		eventCount++
		if evt.Response != nil && evt.Response.Error != nil {
			fmt.Fprintf(os.Stderr, "[%s] error: %s\n", evt.Author, evt.Response.Error.Message)
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
		if evt.Response == nil {
			continue
		}
		if len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		content := choice.Delta.Content
		if content == "" {
			content = choice.Message.Content
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		fmt.Printf("[%s] %s\n", eventLabel(evt), content)
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
		printToolCalls(evt)
		return true
	}
	if evt.Response.IsToolResultResponse() {
		printToolResults(evt)
		return true
	}
	return false
}

func printToolCalls(evt *event.Event) {
	for _, choice := range evt.Response.Choices {
		for _, call := range append(choice.Message.ToolCalls, choice.Delta.ToolCalls...) {
			fmt.Printf("[%s] tool call: %s", eventLabel(evt), call.Function.Name)
			if call.ID != "" {
				fmt.Printf(" (id: %s)", call.ID)
			}
			if len(call.Function.Arguments) > 0 {
				fmt.Printf(" args: %s", string(call.Function.Arguments))
			}
			fmt.Println()
		}
	}
}

func printToolResults(evt *event.Event) {
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
		if len(content) > 240 {
			content = content[:240] + "..."
		}
		fmt.Printf("[%s] tool result: %s (id: %s) %s\n", eventLabel(evt), name, msg.ToolID, content)
	}
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
