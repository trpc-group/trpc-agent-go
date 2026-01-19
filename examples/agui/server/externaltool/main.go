//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main is an AG-UI server example that demonstrates external tool execution with GraphAgent.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"strings"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	graphcheckpoint "trpc.group/trpc-go/trpc-agent-go/graph/checkpoint/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	aguiadapter "trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	appName          = "agui-externaltool-demo"
	nodeCallToolLLM  = "call_tool_llm"
	nodeExternalTool = "external_tool"
	nodeAnswerLLM    = "answer_llm"
	externalToolName = "external_search"
)

var (
	modelName = flag.String("model", "deepseek-chat", "OpenAI-compatible model name.")
	isStream  = flag.Bool("stream", true, "Whether to stream the response.")
	address   = flag.String("address", "127.0.0.1:8080", "Listen address.")
	path      = flag.String("path", "/agui", "HTTP path.")
)

func main() {
	flag.Parse()

	modelInstance := openai.New(*modelName)
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.2),
		Stream:      *isStream,
	}

	g, err := buildGraph(modelInstance, generationConfig)
	if err != nil {
		log.Fatalf("build graph failed: %v", err)
	}

	sessionService := sessioninmemory.NewSessionService()
	checkpointSaver := graphcheckpoint.NewSaver()

	ga, err := graphagent.New(
		"agui-externaltool",
		g,
		graphagent.WithDescription("AG-UI server demo for external tool execution."),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithCheckpointSaver(checkpointSaver),
	)
	if err != nil {
		log.Fatalf("create graph agent failed: %v", err)
	}

	r := runner.NewRunner(appName, ga, runner.WithSessionService(sessionService))
	defer r.Close()

	server, err := agui.New(
		r,
		agui.WithAppName(appName),
		agui.WithSessionService(sessionService),
		agui.WithPath(*path),
		agui.WithAGUIRunnerOptions(
			aguirunner.WithRunOptionResolver(resolveRunOptions),
		),
		agui.WithGraphNodeInterruptActivityEnabled(true),
		agui.WithMessagesSnapshotEnabled(true),
	)
	if err != nil {
		log.Fatalf("create AG-UI server failed: %v", err)
	}

	log.Infof("AG-UI: serving agent %q on http://%s%s", ga.Info().Name, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

func buildGraph(modelInstance model.Model, generationConfig model.GenerationConfig) (*graph.Graph, error) {
	schema := graph.MessagesStateSchema()
	sg := graph.NewStateGraph(schema)

	tools := map[string]tool.Tool{
		externalToolName: function.NewFunctionTool(
			externalSearchNotImplemented,
			function.WithName(externalToolName),
			function.WithDescription("Search an external system for information."),
		),
	}

	sg.AddLLMNode(
		nodeCallToolLLM,
		modelInstance,
		`You are a helpful assistant.`,
		tools,
		graph.WithGenerationConfig(generationConfig),
	)

	sg.AddNode(nodeExternalTool, externalToolNode, graph.WithNodeType(graph.NodeTypeTool))

	sg.AddLLMNode(
		nodeAnswerLLM,
		modelInstance,
		`You are a helpful assistant.`,
		nil,
		graph.WithGenerationConfig(generationConfig),
	)

	sg.SetEntryPoint(nodeCallToolLLM)
	sg.AddToolsConditionalEdges(nodeCallToolLLM, nodeExternalTool, graph.End)
	sg.AddEdge(nodeExternalTool, nodeAnswerLLM)
	sg.SetFinishPoint(nodeAnswerLLM)

	return sg.Compile()
}

func externalToolNode(ctx context.Context, state graph.State) (any, error) {
	msgs, _ := graph.GetStateValue[[]model.Message](state, graph.StateKeyMessages)
	if len(msgs) == 0 {
		return nil, errors.New("no messages in state")
	}

	pendingToolCallID, ok := findPendingToolCallID(msgs, externalToolName)
	if !ok {
		return nil, errors.New("no pending tool call found")
	}

	if _, err := graph.Interrupt(ctx, state, nodeExternalTool, pendingToolCallID); err != nil {
		return nil, err
	}

	invocation, ok := agent.InvocationFromContext(ctx)
	if !ok || invocation == nil {
		return nil, errors.New("invocation not found in context")
	}

	toolMessage := invocation.Message
	if toolMessage.Role != model.RoleTool {
		return nil, fmt.Errorf("expected invocation message role tool, got %s", toolMessage.Role)
	}
	if toolMessage.ToolID == "" {
		return nil, errors.New("tool message missing tool id")
	}
	if toolMessage.Content == "" {
		return nil, errors.New("tool message missing content")
	}
	if toolMessage.ToolID != pendingToolCallID {
		return nil, fmt.Errorf(
			"tool result id does not match pending tool call: %s != %s",
			toolMessage.ToolID,
			pendingToolCallID,
		)
	}

	if len(msgs) > 0 &&
		msgs[len(msgs)-1].Role == model.RoleTool &&
		msgs[len(msgs)-1].ToolID == toolMessage.ToolID {
		return nil, nil
	}

	return graph.State{
		graph.StateKeyMessages: graph.AppendMessages{Items: []model.Message{toolMessage}},
	}, nil
}

func findPendingToolCallID(messages []model.Message, toolName string) (string, bool) {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		switch msg.Role {
		case model.RoleAssistant:
			if len(msg.ToolCalls) == 0 {
				continue
			}
			for _, tc := range msg.ToolCalls {
				if tc.ID == "" {
					continue
				}
				if toolName == "" || tc.Function.Name == toolName {
					return tc.ID, true
				}
			}
			if msg.ToolCalls[0].ID != "" {
				return msg.ToolCalls[0].ID, true
			}
			continue
		case model.RoleUser:
			return "", false
		default:
			continue
		}
	}
	return "", false
}

type externalSearchArgs struct {
	Query string `json:"query" description:"The search query."`
}

type externalSearchResult struct {
	Result string `json:"result" description:"The tool result content."`
}

func externalSearchNotImplemented(ctx context.Context, args externalSearchArgs) (externalSearchResult, error) {
	return externalSearchResult{}, errors.New("external_search is executed by the caller")
}

func resolveRunOptions(ctx context.Context, input *aguiadapter.RunAgentInput) ([]agent.RunOption, error) {
	if input == nil {
		return nil, errors.New("run input is nil")
	}
	if input.ThreadID == "" {
		return nil, errors.New("threadId is required")
	}
	if len(input.Messages) == 0 {
		return nil, errors.New("no messages provided")
	}
	last := input.Messages[len(input.Messages)-1]
	if last.Role != types.RoleUser && last.Role != types.RoleTool {
		return nil, errors.New("last message role must be user or tool")
	}

	var forwardedProps map[string]any
	if input.ForwardedProps != nil {
		props, ok := input.ForwardedProps.(map[string]any)
		if !ok || props == nil {
			return nil, errors.New("forwardedProps must be an object")
		}
		forwardedProps = props
	}

	var lineageID string
	if forwardedProps != nil {
		rawLineageID, exists := forwardedProps[graph.CfgKeyLineageID]
		if exists {
			id, ok := rawLineageID.(string)
			if !ok {
				return nil, fmt.Errorf("forwardedProps.%s must be a string", graph.CfgKeyLineageID)
			}
			lineageID = strings.TrimSpace(id)
			if lineageID == "" {
				return nil, fmt.Errorf("forwardedProps.%s cannot be empty", graph.CfgKeyLineageID)
			}
		}
	}

	runtimeState := make(map[string]any)
	if lineageID != "" {
		runtimeState[graph.CfgKeyLineageID] = lineageID
	}
	if last.Role == types.RoleTool {
		if lineageID == "" {
			return nil, fmt.Errorf("missing forwardedProps.%s", graph.CfgKeyLineageID)
		}
		runtimeState[graph.StateKeyCommand] = &graph.Command{
			ResumeMap: map[string]any{
				nodeExternalTool: true,
			},
		}
	}

	if len(runtimeState) == 0 {
		return nil, nil
	}
	return []agent.RunOption{
		agent.WithRuntimeState(runtimeState),
	}, nil
}

func intPtr(i int) *int { return &i }

func floatPtr(f float64) *float64 { return &f }
