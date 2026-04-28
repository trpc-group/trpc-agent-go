//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/a2aagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	appName         = "a2a-agent-tool-demo"
	parentAgent     = "parent_assistant"
	remoteAgent     = "remote_math_agent"
	defaultHost     = "127.0.0.1:18889"
	defaultQuestion = "请务必调用 remote_math_agent 工具计算 17*23+5，并用一句话给出最终结果。"
)

var (
	modelName = flag.String("model", getEnvOrDefault("MODEL_NAME", "deepseek-chat"), "model name")
	host      = flag.String("host", defaultHost, "A2A server listen host")
	question  = flag.String("question", defaultQuestion, "question sent to the parent agent")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if err := run(ctx); err != nil {
		panic(err)
	}
}

func run(ctx context.Context) error {
	fmt.Println("A2AAgent as AgentTool example")
	fmt.Printf("Model: %s\n", *modelName)
	fmt.Printf("A2A server: %s\n", *host)

	server, err := startA2AServer(ctx, *host, buildRemoteMathAgent())
	if err != nil {
		return err
	}
	defer server.Stop(context.Background())

	remote, err := a2aagent.New(
		a2aagent.WithAgentCardURL("http://"+*host),
		// AgentCard does not carry JSON Schema, so set the local
		// AgentTool input declaration explicitly.
		a2aagent.WithInputSchema(remoteMathInputSchema()),
		a2aagent.WithEnableStreaming(false),
		a2aagent.WithTransferStateKey("tenant_id"),
	)
	if err != nil {
		return fmt.Errorf("create a2a agent: %w", err)
	}
	if card := remote.GetAgentCard(); card != nil {
		fmt.Printf("Resolved remote agent: %s - %s\n", card.Name, card.Description)
	}

	remoteTool := agenttool.NewTool(
		remote,
		agenttool.WithStreamInner(false),
		agenttool.WithHistoryScope(agenttool.HistoryScopeParentBranch),
	)

	parent := llmagent.New(
		parentAgent,
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription("Parent agent that delegates math work to a remote A2A agent tool."),
		llmagent.WithInstruction(
			"You are a parent agent. For any calculation request, you must call the "+
				"remote_math_agent tool exactly once, then summarize the tool result for the user. "+
				"When calling remote_math_agent, pass the arithmetic expression in the request field. "+
				"Do not calculate the answer yourself before calling the tool.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      true,
			MaxTokens:   intPtr(1000),
			Temperature: floatPtr(0.1),
		}),
		llmagent.WithTools([]tool.Tool{remoteTool}),
	)

	r := runner.NewRunner(
		appName,
		parent,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()

	fmt.Printf("User: %s\n\n", *question)
	events, err := r.Run(
		ctx,
		"user-a2a-tool",
		fmt.Sprintf("session-%d", time.Now().UnixNano()),
		model.NewUserMessage(*question),
		agent.WithRuntimeState(map[string]any{"tenant_id": "demo_tenant"}),
	)
	if err != nil {
		return fmt.Errorf("run parent agent: %w", err)
	}

	toolCalled, finalText, err := consumeEvents(events)
	if err != nil {
		return err
	}
	if !toolCalled {
		return errors.New("validation failed: parent agent did not call remote_math_agent")
	}
	if strings.TrimSpace(finalText) == "" {
		return errors.New("validation failed: parent agent returned empty final text")
	}

	fmt.Println("\nValidation passed: remote A2A agent was injected as an AgentTool and called by the parent agent.")
	return nil
}

func startA2AServer(ctx context.Context, host string, remote agent.Agent) (*a2aserverHandle, error) {
	if err := ensureHostAvailable(host); err != nil {
		return nil, err
	}

	server, err := a2aserver.New(
		a2aserver.WithHost(host),
		a2aserver.WithAgent(remote, false),
	)
	if err != nil {
		return nil, fmt.Errorf("create a2a server: %w", err)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := server.Start(host); err != nil {
			errCh <- err
		}
	}()

	if err := waitForAgentCard(ctx, host, errCh); err != nil {
		_ = server.Stop(context.Background())
		return nil, err
	}
	return &a2aserverHandle{server: server}, nil
}

type a2aserverHandle struct {
	server interface {
		Stop(context.Context) error
	}
}

func (h *a2aserverHandle) Stop(ctx context.Context) error {
	if h == nil || h.server == nil {
		return nil
	}
	return h.server.Stop(ctx)
}

func ensureHostAvailable(host string) error {
	listener, err := net.Listen("tcp", host)
	if err != nil {
		return fmt.Errorf("host %s is not available: %w", host, err)
	}
	return listener.Close()
}

func waitForAgentCard(ctx context.Context, host string, errCh <-chan error) error {
	client := &http.Client{Timeout: time.Second}
	url := "http://" + host + protocol.AgentCardPath
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for agent card at %s: %w", url, ctx.Err())
		case err := <-errCh:
			return fmt.Errorf("a2a server exited before ready: %w", err)
		case <-ticker.C:
			resp, err := client.Get(url)
			if err != nil {
				continue
			}
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
}

func consumeEvents(events <-chan *event.Event) (bool, string, error) {
	var (
		toolCalled bool
		finalText  strings.Builder
	)

	for ev := range events {
		if ev == nil {
			continue
		}
		if ev.Error != nil {
			return toolCalled, finalText.String(), fmt.Errorf("event error: %s", ev.Error.Message)
		}
		if ev.Response != nil && ev.Response.Error != nil {
			return toolCalled, finalText.String(), fmt.Errorf("response error: %s", ev.Response.Error.Message)
		}
		if ev.Response == nil || len(ev.Response.Choices) == 0 {
			continue
		}

		choice := ev.Response.Choices[0]
		for _, tc := range choice.Message.ToolCalls {
			fmt.Printf("Tool call: %s args=%s\n", tc.Function.Name, string(tc.Function.Arguments))
			if tc.Function.Name == remoteAgent {
				toolCalled = true
			}
		}
		if choice.Message.Role == model.RoleTool && choice.Message.ToolName == remoteAgent {
			fmt.Printf("Remote tool response: %s\n", strings.TrimSpace(choice.Message.Content))
			continue
		}
		if ev.Author != parentAgent {
			if choice.Delta.Content != "" {
				fmt.Printf("Remote stream: %s\n", strings.TrimSpace(choice.Delta.Content))
			}
			continue
		}
		if choice.Delta.Content != "" {
			finalText.WriteString(choice.Delta.Content)
			fmt.Print(choice.Delta.Content)
		}
		if choice.Message.Content != "" {
			if strings.Contains(finalText.String(), choice.Message.Content) {
				continue
			}
			finalText.WriteString(choice.Message.Content)
			fmt.Print(choice.Message.Content)
		}
	}

	return toolCalled, finalText.String(), nil
}

func buildRemoteMathAgent() agent.Agent {
	calculatorTool := function.NewFunctionTool(
		calculate,
		function.WithName("calculator"),
		function.WithDescription("Calculate an arithmetic expression."),
	)

	return llmagent.New(
		remoteAgent,
		llmagent.WithModel(openai.New(*modelName)),
		llmagent.WithDescription("A remote A2A math agent. Use it for arithmetic questions."),
		llmagent.WithInstruction(
			"You are a remote math agent exposed over A2A. "+
				"For every arithmetic request, call the calculator tool first. "+
				"Then answer with the calculation result in one short sentence.",
		),
		llmagent.WithGenerationConfig(model.GenerationConfig{
			Stream:      false,
			MaxTokens:   intPtr(500),
			Temperature: floatPtr(0),
		}),
		llmagent.WithTools([]tool.Tool{calculatorTool}),
	)
}

func remoteMathInputSchema() map[string]any {
	return map[string]any{
		"type":        "object",
		"description": "Local AgentTool input for the remote A2A math agent.",
		"properties": map[string]any{
			"request": map[string]any{
				"type":        "string",
				"description": "Arithmetic expression to calculate, for example 17*23+5.",
			},
		},
		"required": []string{"request"},
	}
}

type calculatorInput struct {
	Expression string `json:"expression,omitempty" jsonschema_description:"Arithmetic expression, for example 17*23+5."`
}

type calculatorOutput struct {
	Expression string `json:"expression"`
	Result     int    `json:"result"`
}

func calculate(_ context.Context, input calculatorInput) (calculatorOutput, error) {
	expression := strings.TrimSpace(input.Expression)
	if expression == "" {
		expression = "17*23+5"
	}
	normalized := strings.NewReplacer(
		" ", "",
		"×", "*",
		"乘以", "*",
		"加", "+",
	).Replace(expression)
	if normalized != "17*23+5" {
		return calculatorOutput{}, fmt.Errorf("unsupported demo expression %q", expression)
	}

	fmt.Printf("Remote internal tool called: calculator expression=%s\n", expression)
	return calculatorOutput{
		Expression: expression,
		Result:     17*23 + 5,
	}, nil
}

func intPtr(v int) *int {
	return &v
}

func floatPtr(v float64) *float64 {
	return &v
}

func getEnvOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
