//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates a React-style graph agent.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/planner/react"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	nodePlanner      = "planner"
	nodeReasoning    = "reasoning"
	nodeTool         = "tool"
	nodeFinalAnswer  = "finalanswer"
	nodeFormatOutput = "formatoutput"
)

const (
	plannerInstruction = `You are the planning stage of a React-style agent.
Create a concise numbered plan that explains how to solve the user's question.
Do not give the answer inside the plan.`

	reasoningInstruction = `You are the reasoning stage of a React-style agent.
Follow the planner output and think step by step.
- Clearly describe your next thought in natural language.
- If a tool call is required, explain why and then output exactly one JSON tool call.
- If no tool is needed, write two short sentences: first describe the reasoning, then state the conclusion.
Do not address the user directly.`

	finalInstruction = `You provide the final answer to the user.
Use the conversation context to craft a helpful response in English.`
)

func main() {
	modelName := flag.String("model", "deepseek-chat", "LLM model name")
	question := flag.String("question", "", "User question; leave empty to type interactively")
	flag.Parse()

	prompt := strings.TrimSpace(*question)
	if prompt == "" {
		prompt = readQuestionFromStdin()
	}

	ctx := context.Background()
	graphDef, err := buildGraph(*modelName)
	if err != nil {
		log.Fatalf("failed to build graph: %v", err)
	}

	agent, err := graphagent.New(
		"react-graph",
		graphDef,
		graphagent.WithDescription("Planner → Reasoning → Tool → FinalAnswer → FormatOutput example"),
	)
	if err != nil {
		log.Fatalf("failed to create graph agent: %v", err)
	}

	sessionSvc := inmemory.NewSessionService()
	r := runner.NewRunner("graph-react-example", agent, runner.WithSessionService(sessionSvc))
	defer r.Close()

	userID := "demo-user"
	sessionID := fmt.Sprintf("react-example-%d", time.Now().Unix())

	message := model.NewUserMessage(prompt)
	eventChan, err := r.Run(ctx, userID, sessionID, message)
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}
	if err := streamEvents(eventChan); err != nil {
		log.Fatalf("stream failed: %v", err)
	}
}

func readQuestionFromStdin() string {
	fmt.Println("Enter a question and press Enter (examples: 'How much is 12 + 7?'):")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("Question> ")
		if !scanner.Scan() {
			log.Fatal("no input provided")
		}
		text := strings.TrimSpace(scanner.Text())
		if text != "" {
			return text
		}
	}
}

func buildGraph(modelName string) (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	mdl := openai.New(modelName)
	streamOpt := graph.WithGenerationConfig(model.GenerationConfig{Stream: true})

	calculatorTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription("Perform basic arithmetic operations (add, subtract, multiply, divide). "+
			"Fields: operation, a, b."),
	)
	tools := map[string]tool.Tool{"calculator": calculatorTool}

	sg := graph.NewStateGraph(schema)
	sg.
		AddLLMNode(nodePlanner, mdl, plannerInstruction, nil, streamOpt).
		AddLLMNode(nodeReasoning, mdl, reasoningInstruction, tools, streamOpt).
		AddToolsNode(nodeTool, tools).
		AddLLMNode(nodeFinalAnswer, mdl, finalInstruction, nil, streamOpt).
		AddNode(nodeFormatOutput, formatOutput)

	sg.AddEdge(nodePlanner, nodeReasoning)
	sg.AddToolsConditionalEdges(nodeReasoning, nodeTool, nodeFinalAnswer)
	sg.AddEdge(nodeTool, nodeReasoning)
	sg.AddEdge(nodeFinalAnswer, nodeFormatOutput)
	sg.SetEntryPoint(nodePlanner)
	sg.SetFinishPoint(nodeFormatOutput)

	return sg.Compile()
}

func streamEvents(eventChan <-chan *event.Event) error {
	var stage string
	var streaming bool
	var lineOpen bool
	var pendingToolCalls []string
	var formatPayload string

	flushToolCalls := func() {
		if len(pendingToolCalls) == 0 {
			return
		}
		for _, payload := range pendingToolCalls {
			fmt.Printf("🔧 %s\n", payload)
			lineOpen = false
		}
		pendingToolCalls = pendingToolCalls[:0]
	}
	flushStream := func() {
		if streaming {
			fmt.Println()
			streaming = false
			lineOpen = false
		}
	}

	for evt := range eventChan {
		if evt == nil {
			continue
		}
		if label := maybeStartNode(evt); label != "" {
			flushStream()
			if lineOpen {
				fmt.Println()
				lineOpen = false
			}
			fmt.Printf("---------- %s ----------\n", label)
			lineOpen = false
			stage = label
			if label == react.ActionTag {
				flushToolCalls()
			}
		}

		if payload, ok := extractFormatPayload(evt); ok {
			formatPayload = payload
			continue
		}

		if evt.IsToolResultResponse() {
			if text := toolResultText(evt); text != "" {
				fmt.Printf("✅ Tool result: %s\n", text)
				lineOpen = false
			}
			continue
		}

		if payloads := collectToolCalls(evt); len(payloads) > 0 {
			pendingToolCalls = append(pendingToolCalls, payloads...)
			if stage == react.ActionTag {
				flushToolCalls()
			}
			continue
		}

		if text, isDelta := extractDisplayText(evt); text != "" {
			if stage == "" {
				continue
			}
			if isDelta {
				if !streaming {
					fmt.Print("🤖 ")
					streaming = true
					lineOpen = true
				}
				fmt.Print(text)
				lineOpen = true
				continue
			}
			flushStream()
			clean := strings.TrimSpace(text)
			if clean == "" {
				continue
			}
			fmt.Println("🤖 " + strings.ReplaceAll(clean, "\n", "\n🤖 "))
			lineOpen = false
		}

		if evt.Response != nil && evt.Response.Done {
			flushStream()
		}

		if evt.Error != nil {
			fmt.Printf("error: %s\n", evt.Error.Message)
			lineOpen = false
			continue
		}
	}

	flushToolCalls()

	if strings.TrimSpace(formatPayload) != "" {
		if lineOpen {
			fmt.Println()
			lineOpen = false
		}
		fmt.Println("[FormatOutput]")
		lineOpen = false
		fmt.Println(formatPayload)
		lineOpen = false
	}

	return nil
}

func maybeStartNode(evt *event.Event) string {
	meta, ok := extractNodeMetadata(evt)
	if !ok || meta.Phase != graph.ExecutionPhaseStart {
		return ""
	}
	label, ok := nodeLabel(meta.NodeID)
	if !ok {
		return ""
	}
	return label
}

func extractNodeMetadata(evt *event.Event) (graph.NodeExecutionMetadata, bool) {
	var meta graph.NodeExecutionMetadata
	if evt == nil || evt.StateDelta == nil {
		return meta, false
	}
	raw, ok := evt.StateDelta[graph.MetadataKeyNode]
	if !ok || len(raw) == 0 {
		return meta, false
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, false
	}
	return meta, true
}

func extractDisplayText(evt *event.Event) (string, bool) {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return "", false
	}
	choice := evt.Response.Choices[0]
	if len(choice.Message.ToolCalls) > 0 || choice.Message.ToolID != "" {
		return "", false
	}
	if delta := choice.Delta.Content; delta != "" {
		return delta, true
	}
	return strings.TrimSpace(choice.Message.Content), false
}

func collectToolCalls(evt *event.Event) []string {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return nil
	}
	choice := evt.Response.Choices[0]
	if len(choice.Message.ToolCalls) == 0 {
		return nil
	}
	payloads := make([]string, 0, len(choice.Message.ToolCalls))
	for _, call := range choice.Message.ToolCalls {
		if call.Function.Arguments != nil {
			payloads = append(payloads, string(call.Function.Arguments))
		}
	}
	return payloads
}

func toolResultText(evt *event.Event) string {
	if evt.Response == nil || len(evt.Response.Choices) == 0 {
		return ""
	}
	msg := evt.Response.Choices[0].Message
	text := strings.TrimSpace(msg.Content)
	if text == "" && msg.ToolID != "" {
		return fmt.Sprintf("tool call %s completed", msg.ToolID)
	}
	if text == "" {
		return "tool call completed"
	}
	return text
}

func formatOutput(_ context.Context, state graph.State) (any, error) {
	finalText := readNodeResponse(state, nodeFinalAnswer)
	if finalText == "" {
		finalText = readNodeResponse(state, nodeReasoning)
	}
	payload := map[string]any{
		"final_answer":   strings.TrimSpace(finalText),
		"node_responses": state[graph.StateKeyNodeResponses],
	}
	return graph.State{nodeFormatOutput: payload}, nil
}

func readNodeResponse(state graph.State, nodeID string) string {
	responses, _ := state[graph.StateKeyNodeResponses].(map[string]any)
	if responses == nil {
		return ""
	}
	if val, ok := responses[nodeID]; ok {
		switch v := val.(type) {
		case string:
			return v
		default:
			if b, err := json.Marshal(v); err == nil {
				return string(b)
			}
		}
	}
	return ""
}

func nodeLabel(nodeID string) (string, bool) {
	switch nodeID {
	case nodePlanner:
		return react.PlanningTag, true
	case nodeReasoning:
		return react.ReasoningTag, true
	case nodeTool:
		return react.ActionTag, true
	case nodeFinalAnswer:
		return react.FinalAnswerTag, true
	default:
		return "", false
	}
}

func calculator(_ context.Context, args calculatorArgs) (calculatorResult, error) {
	switch strings.ToLower(args.Operation) {
	case "add", "+":
		return calculatorResult{Operation: args.Operation, A: args.A, B: args.B, Result: args.A + args.B}, nil
	case "subtract", "-":
		return calculatorResult{Operation: args.Operation, A: args.A, B: args.B, Result: args.A - args.B}, nil
	case "multiply", "*":
		return calculatorResult{Operation: args.Operation, A: args.A, B: args.B, Result: args.A * args.B}, nil
	case "divide", "/":
		if args.B == 0 {
			return calculatorResult{}, fmt.Errorf("cannot divide by zero")
		}
		return calculatorResult{Operation: args.Operation, A: args.A, B: args.B, Result: args.A / args.B}, nil
	default:
		return calculatorResult{}, fmt.Errorf("unsupported operation %s", args.Operation)
	}
}

type calculatorArgs struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
}

type calculatorResult struct {
	Operation string  `json:"operation"`
	A         float64 `json:"a"`
	B         float64 `json:"b"`
	Result    float64 `json:"result"`
}

func extractFormatPayload(evt *event.Event) (string, bool) {
	if evt.StateDelta == nil {
		return "", false
	}
	raw, ok := evt.StateDelta[nodeFormatOutput]
	if !ok || len(raw) == 0 {
		return "", false
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", false
	}
	if data, err := json.MarshalIndent(payload, "", "  "); err == nil {
		return string(data), true
	}
	return "", false
}
