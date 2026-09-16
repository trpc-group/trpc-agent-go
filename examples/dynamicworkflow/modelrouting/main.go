//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates host-authorized Dynamic Workflow child-Agent
// model profiles. One neutral template may select registered aliases per
// call, or omit model to keep the template default.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/dynamicworkflow"
)

var (
	modelName = flag.String(
		"model",
		"gpt-5",
		"Model name for the root Agent and the template default",
	)
	fastModelName = flag.String(
		"fast-model",
		"",
		"Provider model name for the fast profile; empty uses -model",
	)
	deepModelName = flag.String(
		"deep-model",
		"",
		"Provider model name for the deep profile; empty uses -model",
	)
	prompt = flag.String(
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

	fastName := resolveModelName(*fastModelName, *modelName)
	deepName := resolveModelName(*deepModelName, *modelName)

	rootModel := openai.New(*modelName)
	templateModel := openai.New(*modelName)
	fastModel := openai.New(fastName)
	deepModel := openai.New(deepName)

	ctx := context.Background()
	workflowTool, err := buildWorkflowTool(templateModel, fastModel, deepModel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build workflow tool: %v\n", err)
		os.Exit(1)
	}
	if *showWorkflowCode {
		workflowTool = workflowCodePrintingTool{inner: workflowTool}
	}

	root := llmagent.New(
		"workflow_assistant",
		llmagent.WithModel(rootModel),
		llmagent.WithDescription("Creates temporary collaboration workflows and may select host-authorized child-Agent model profiles."),
		llmagent.WithInstruction(`Answer simple requests directly. For tasks that need temporary collaboration, independent analysis, structured review, or revision loops, call run_workflow exactly once and do not answer with Python source. This example registers one neutral general_agent template, so template is usually omitted. Every agent(...) call should provide a concrete instruction and tools=[]. Host model routing: use model="fast" for independent low-latency analysis, extraction, and first drafts; use model="deep" for synthesis, strict review, and final decisions; omit model for a balanced ordinary summary that should use the template default. Prefer a short sequential chain of agent(...) calls. Use schema for values that control later branches or loops.`),
		llmagent.WithTools([]tool.Tool{workflowTool}),
	)
	r := runner.NewRunner("dynamic-workflow-modelrouting-example", root)
	defer r.Close()

	chat := &dynamicWorkflowChat{
		runner:           r,
		userID:           "demo-user",
		sessionID:        newSessionID(),
		rootModel:        *modelName,
		templateModel:    *modelName,
		fastProfileModel: fastName,
		deepProfileModel: deepName,
	}
	if strings.TrimSpace(*prompt) != "" {
		chat.printStartup()
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

func resolveModelName(override, fallback string) string {
	if strings.TrimSpace(override) == "" {
		return fallback
	}
	return strings.TrimSpace(override)
}

type dynamicWorkflowChat struct {
	runner           runner.Runner
	userID           string
	sessionID        string
	rootModel        string
	templateModel    string
	fastProfileModel string
	deepProfileModel string
}

func newSessionID() string {
	return "dynamic-workflow-" + uuid.NewString()
}

func (c *dynamicWorkflowChat) printStartup() {
	fmt.Printf("Root model: %s\n", c.rootModel)
	fmt.Printf("Template default model: %s\n", c.templateModel)
	fmt.Printf("Model profiles:\n")
	fmt.Printf("  fast -> %s\n", c.fastProfileModel)
	fmt.Printf("  deep -> %s\n", c.deepProfileModel)
}

func (c *dynamicWorkflowChat) printBanner() {
	fmt.Printf("Dynamic Workflow Model Routing Example\n")
	c.printStartup()
	fmt.Printf("Session: %s\n", c.sessionID)
	fmt.Println("Type '/new' to start a new session or '/exit' to quit.")
	fmt.Println("Sample prompts:")
	fmt.Println("  In one sentence, what is the difference between a mutex and a channel for mutual exclusion in Go?")
	fmt.Println("  Compare sharded LRU and W-TinyLFU for a Go local cache. Use run_workflow once: run two independent quick analyses in sequence, then a rigorous structured review, then a balanced concise summary.")
	fmt.Println("  Draft a short Go local-cache API proposal with an efficient drafter, have a rigorous reviewer return structured approval and issues, revise for at most two review rounds, and finish with a balanced concise summary.")
	fmt.Println("  Optional explicit routing check (troubleshooting): Use run_workflow once: analyze each cache option with model=\"fast\", then model=\"deep\" for a structured review, then omit model for the summary.")
	fmt.Println(strings.Repeat("=", 72))
}

func (c *dynamicWorkflowChat) startChat(ctx context.Context) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
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

func buildWorkflowTool(
	templateModel, fastModel, deepModel model.Model,
) (tool.CallableTool, error) {
	generalAgent := llmagent.New(
		"general_agent",
		llmagent.WithModel(templateModel),
		llmagent.WithDescription("A neutral execution template for one workflow-local role defined by its dynamic instruction."),
		llmagent.WithInstruction(`Follow the dynamic instance instruction as the complete definition of your current role. Treat the input as JSON context. Do not assume a business domain from this template. Unless structured output is requested, return the requested content directly instead of wrapping it in a JSON object. When a structured output contract is requested, return data that conforms to it.`),
		llmagent.WithMessageFilterMode(llmagent.IsolatedRequest),
	)

	return dynamicworkflow.NewTool(
		dynamicworkflow.LocalRunner{},
		[]agent.Agent{generalAgent},
		dynamicworkflow.WithAgentModelProfile(
			"fast",
			"Low-latency model for independent analysis, extraction, and first drafts.",
			fastModel,
		),
		dynamicworkflow.WithAgentModelProfile(
			"deep",
			"Higher-capability model for synthesis, strict review, and final decisions.",
			deepModel,
		),
	)
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
		fmt.Fprintln(os.Stderr, "\n===== generated dynamic workflow code =====")
		fmt.Fprintln(os.Stderr, input.Code)
		fmt.Fprintln(os.Stderr, "===== end generated dynamic workflow code =====")
		fmt.Fprintln(os.Stderr)
	}
}

func printEvents(events <-chan *event.Event) {
	eventCount := 0
	seenModels := make(map[string]string)
	for evt := range events {
		if evt == nil {
			continue
		}
		eventCount++
		maybePrintResponseModel(evt, seenModels)
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

// maybePrintResponseModel emits one provider-model notice per child/root
// invocation when Response.Model first becomes non-empty. This is the
// provider-reported response value, not the workflow profile alias; event
// authors remain template Agent names.
func maybePrintResponseModel(evt *event.Event, seen map[string]string) {
	if evt == nil || evt.Response == nil || seen == nil {
		return
	}
	name := strings.TrimSpace(evt.Response.Model)
	if name == "" {
		return
	}
	key := strings.TrimSpace(evt.InvocationID)
	if key == "" {
		// Fallback when InvocationID is absent: author path plus provider model.
		key = eventLabel(evt) + "|" + name
	}
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = name
	fmt.Printf("[%s] provider model: %s\n", eventLabel(evt), name)
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
			runes := []rune(content)
			if len(runes) > 240 {
				content = string(runes[:240]) + "..."
			}
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
