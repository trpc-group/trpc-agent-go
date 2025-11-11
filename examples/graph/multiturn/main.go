//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates an interactive, multi‑turn chat using Graph + GraphAgent + Runner.
// It highlights that conversation history persists via the session service across runs,
// and shows tool use with streaming outputs.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model name to use")
)

func main() {
	flag.Parse()
	fmt.Printf("🤖 Graph Multi‑turn Chat (tools + streaming)\n")
	fmt.Printf("Model: %s\n\n", *modelName)
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Println("💡 Hint: OPENAI_API_KEY is not set. Configure your provider API key/base URL if required.")
	}

	if err := run(); err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Build graph: simple chat with optional tools.
	schema := graph.MessagesStateSchema()
	llm := openai.New(*modelName)

	// Define a simple calculator tool: only two numbers and an operator.
	calcTool := function.NewFunctionTool(calc,
		function.WithName("calculator"),
		function.WithDescription(
			`Performs a calculation on two numbers using one of the following operators: +, -, *, /, ^.
Input fields:
- a (required): The first number (float)
- b (required): The second number (float)
- op (required): The operator, one of "+", "-", "*", "/", "^"

Few-shot examples:
Q: What is 2 + 2?
A: { "a": 2, "b": 2, "op": "+" }
Result: { "result": 4 }

Q: What is 3.5 ^ 2?
A: { "a": 3.5, "b": 2, "op": "^" }
Result: { "result": 12.25 }
`),
	)
	tools := map[string]tool.Tool{"calculator": calcTool}

	// Instruction encourages tool use when appropriate.
	instruction := `You are a helpful assistant. For math, ALWAYS use the calculator tool with two numbers and an operator (+, -, *, /, ^).
After getting the tool result, explain the result succinctly in natural language.`

	sg := graph.NewStateGraph(schema).
		AddLLMNode("chat", llm, instruction, tools).
		AddToolsNode("tools", tools).
		AddNode("end", end).
		// If chat produces tool calls → tools; otherwise → End (finish this round)
		AddToolsConditionalEdges("chat", "tools", "end").
		AddEdge("tools", "chat").
		SetEntryPoint("chat").
		SetFinishPoint("end")

	g, err := sg.Compile()
	if err != nil {
		return err
	}

	ga, err := graphagent.New("chat-graph", g, graphagent.WithInitialState(graph.State{}))
	if err != nil {
		return err
	}

	r := runner.NewRunner("graph-multiturn", ga)
	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer r.Close()

	// Interactive loop using same session for history.
	userID := "user"
	sessionID := fmt.Sprintf("chat-%d", time.Now().Unix())
	fmt.Printf("Session: %s\n\n", sessionID)

	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You: ")
		if !in.Scan() {
			break
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if strings.EqualFold(line, "exit") || strings.EqualFold(line, "quit") {
			fmt.Println("bye")
			break
		}

		ch, err := r.Run(context.Background(), userID, sessionID, model.NewUserMessage(line))
		if err != nil {
			return err
		}
		if err := streamPrint(ch); err != nil {
			return err
		}
		fmt.Println()
	}
	return in.Err()
}

func streamPrint(ch <-chan *event.Event) error {
	prefixPrinted := false
	streaming := false
	var streamed strings.Builder
	lastStreamed := ""
	finalMessage := ""
	for e := range ch {
		if e.Error != nil {
			fmt.Printf("[error] %s\n", e.Error.Message)
			continue
		}

		if printed := renderToolCalls(e); printed {
			continue
		}

		if rendered := renderToolResponses(e); rendered {
			continue
		}
		if len(e.Choices) > 0 {
			c := e.Choices[0]
			if text := c.Delta.Content; text != "" {
				if !prefixPrinted {
					fmt.Print("Assistant: ")
					prefixPrinted = true
				}
				fmt.Print(text)
				streamed.WriteString(text)
				streaming = true
			}
			if c.Message.Content != "" {
				finalMessage = c.Message.Content
			}
			if c.Delta.Content == "" && streaming {
				fmt.Println()
				prefixPrinted = false
				streaming = false
				lastStreamed = streamed.String()
				streamed.Reset()
			}
		}
		if e.Done && e.Response != nil && e.Response.Object == model.ObjectTypeRunnerCompletion {
			break
		}
	}
	if streaming {
		fmt.Println()
		streaming = false
		prefixPrinted = false
		lastStreamed = streamed.String()
	}
	if finalMessage != "" {
		if finalMessage != lastStreamed {
			if !prefixPrinted {
				fmt.Print("Assistant: ")
			}
			fmt.Println(finalMessage)
		} else if lastStreamed == "" {
			// No streaming occurred; emit the message once.
			fmt.Print("Assistant: ")
			fmt.Println(finalMessage)
		}
	}
	return nil
}

func renderToolCalls(e *event.Event) bool {
	if len(e.Choices) == 0 || len(e.Choices[0].Message.ToolCalls) == 0 {
		return false
	}
	fmt.Println("🔧 Tool call requested:")
	for _, tc := range e.Choices[0].Message.ToolCalls {
		fmt.Printf("   • %s (id: %s)\n", tc.Function.Name, tc.ID)
		if len(tc.Function.Arguments) > 0 {
			fmt.Printf("     args: %s\n", string(tc.Function.Arguments))
		}
	}
	return true
}

func renderToolResponses(e *event.Event) bool {
	if e.Response == nil || e.Response.Object != model.ObjectTypeToolResponse {
		return false
	}
	if len(e.Response.Choices) == 0 {
		return false
	}
	fmt.Println("🛠️ Tool response:")
	for _, choice := range e.Response.Choices {
		if choice.Message.Role != model.RoleTool {
			continue
		}
		pretty := prettifyJSON(choice.Message.Content)
		fmt.Printf("   • %s → %s\n", displayToolName(choice.Message), pretty)
	}
	if e.Response.Error != nil {
		fmt.Printf("   ⚠️ tool error: %s\n", e.Response.Error.Message)
	}
	return true
}

func displayToolName(msg model.Message) string {
	if msg.ToolName != "" {
		return fmt.Sprintf("%s (id: %s)", msg.ToolName, msg.ToolID)
	}
	if msg.ToolID != "" {
		return msg.ToolID
	}
	return "tool"
}

func prettifyJSON(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "<empty>"
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return trimmed
	}
	switch parsed.(type) {
	case string, float64, int, int64, bool, nil:
		return fmt.Sprintf("%v", parsed)
	default:
		var buf bytes.Buffer
		if err := json.Indent(&buf, []byte(trimmed), "", "  "); err != nil {
			return trimmed
		}
		return "\n" + buf.String()
	}
}

func end(_ context.Context, _ graph.State) (any, error) {
	return nil, nil
}

type calcArgs struct {
	A  float64 `json:"a" description:"First number"`
	B  float64 `json:"b" description:"Second number"`
	Op string  `json:"op" description:"Operator: one of +, -, *, /, ^"`
}

type calcResult struct {
	Result float64 `json:"result"`
}

func calc(_ context.Context, in calcArgs) (calcResult, error) {
	switch in.Op {
	case "+":
		return calcResult{Result: in.A + in.B}, nil
	case "-":
		return calcResult{Result: in.A - in.B}, nil
	case "*":
		return calcResult{Result: in.A * in.B}, nil
	case "/":
		if in.B == 0 {
			return calcResult{}, errors.New("division by zero")
		}
		return calcResult{Result: in.A / in.B}, nil
	case "^":
		return calcResult{Result: math.Pow(in.A, in.B)}, nil
	default:
		return calcResult{}, errors.New("unsupported operator: must be one of +, -, *, /, ^")
	}
}
