//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how to apply graph NodeCallbacks from a
// runner-scoped plugin.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName     = "runner-plugin-node-callbacks"
	agentName   = "demo-graph"
	userID      = "demo-user"
	sessionPref = "demo-session"

	nodeUpper  = "upper"
	nodeAnswer = "answer"

	stateKeyUpper = "upper_text"

	answerPrefix = "Uppercase: "
	emptyInput   = "(empty)"

	pluginName = "inject_node_callbacks"
)

var input = flag.String(
	"input",
	"hello plugin hooks",
	"User input for the graph",
)

func main() {
	flag.Parse()

	g, err := buildGraph()
	if err != nil {
		log.Fatalf("build graph: %v", err)
	}

	ga, err := graphagent.New(agentName, g)
	if err != nil {
		log.Fatalf("create graph agent: %v", err)
	}

	cb := newNodeLoggerCallbacks()
	p := &nodeCallbacksPlugin{callbacks: cb}

	r := runner.NewRunner(
		appName,
		ga,
		runner.WithPlugins(p),
	)
	defer r.Close()

	ctx := context.Background()
	sessionID := fmt.Sprintf("%s-%d", sessionPref, time.Now().Unix())

	events, err := r.Run(
		ctx,
		userID,
		sessionID,
		model.NewUserMessage(*input),
		agent.WithStreamMode(agent.StreamModeTasks),
	)
	if err != nil {
		log.Fatalf("run: %v", err)
	}

	completion := waitRunnerCompletion(events)
	if err := printCompletion(completion); err != nil {
		log.Fatalf("print result: %v", err)
	}
}

func buildGraph() (*graph.Graph, error) {
	schema := graph.NewStateSchema().
		AddField(graph.StateKeyUserInput, graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField(stateKeyUpper, graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		}).
		AddField(graph.StateKeyLastResponse, graph.StateField{
			Type:    reflect.TypeOf(""),
			Reducer: graph.DefaultReducer,
		})

	return graph.NewStateGraph(schema).
		AddNode(nodeUpper, upperNode).
		AddNode(nodeAnswer, answerNode).
		SetEntryPoint(nodeUpper).
		AddEdge(nodeUpper, nodeAnswer).
		SetFinishPoint(nodeAnswer).
		Compile()
}

func upperNode(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	input, _ := state[graph.StateKeyUserInput].(string)
	return graph.State{
		stateKeyUpper: strings.ToUpper(input),
	}, nil
}

func answerNode(ctx context.Context, state graph.State) (any, error) {
	_ = ctx
	upper, _ := state[stateKeyUpper].(string)
	if upper == "" {
		upper = emptyInput
	}
	answer := answerPrefix + upper
	return graph.State{
		graph.StateKeyLastResponse: answer,
	}, nil
}

func newNodeLoggerCallbacks() *graph.NodeCallbacks {
	return graph.NewNodeCallbacks().
		RegisterBeforeNode(beforeNodeLog).
		RegisterAfterNode(afterNodeLog).
		RegisterOnNodeError(onNodeErrorLog)
}

func beforeNodeLog(
	ctx context.Context,
	cbCtx *graph.NodeCallbackContext,
	state graph.State,
) (any, error) {
	_ = ctx
	_ = state

	if cbCtx == nil || cbCtx.NodeType != graph.NodeTypeFunction {
		return nil, nil
	}

	fmt.Printf(
		"[node before] step=%d id=%s\n",
		cbCtx.StepNumber,
		cbCtx.NodeID,
	)
	return nil, nil
}

func afterNodeLog(
	ctx context.Context,
	cbCtx *graph.NodeCallbackContext,
	state graph.State,
	result any,
	nodeErr error,
) (any, error) {
	_ = ctx
	_ = state
	_ = result
	_ = nodeErr

	if cbCtx == nil || cbCtx.NodeType != graph.NodeTypeFunction {
		return nil, nil
	}

	fmt.Printf(
		"[node after]  step=%d id=%s\n",
		cbCtx.StepNumber,
		cbCtx.NodeID,
	)
	return nil, nil
}

func onNodeErrorLog(
	ctx context.Context,
	cbCtx *graph.NodeCallbackContext,
	state graph.State,
	err error,
) {
	_ = ctx
	_ = state

	if cbCtx == nil || err == nil {
		return
	}

	fmt.Printf(
		"[node error] id=%s err=%v\n",
		cbCtx.NodeID,
		err,
	)
}

type nodeCallbacksPlugin struct {
	callbacks *graph.NodeCallbacks
}

func (p *nodeCallbacksPlugin) Name() string { return pluginName }

func (p *nodeCallbacksPlugin) Register(reg *plugin.Registry) {
	reg.BeforeAgent(p.beforeAgent)
}

func (p *nodeCallbacksPlugin) beforeAgent(
	ctx context.Context,
	args *agent.BeforeAgentArgs,
) (*agent.BeforeAgentResult, error) {
	_ = ctx
	if p == nil || p.callbacks == nil {
		return nil, nil
	}
	if args == nil || args.Invocation == nil {
		return nil, nil
	}

	inv := args.Invocation
	if inv.RunOptions.RuntimeState == nil {
		inv.RunOptions.RuntimeState = make(map[string]any)
	}
	if inv.RunOptions.RuntimeState[graph.StateKeyNodeCallbacks] != nil {
		return nil, nil
	}
	inv.RunOptions.RuntimeState[graph.StateKeyNodeCallbacks] = p.callbacks
	return nil, nil
}

func waitRunnerCompletion(events <-chan *event.Event) *event.Event {
	var completion *event.Event
	for e := range events {
		if e == nil || e.Response == nil {
			continue
		}
		if e.Response.Object == model.ObjectTypeRunnerCompletion {
			completion = e
		}
	}
	return completion
}

func printCompletion(e *event.Event) error {
	if e == nil || e.Response == nil {
		return errors.New("missing runner completion event")
	}

	fmt.Println()
	fmt.Println("Final answer:")
	if text, ok := assistantText(e); ok {
		fmt.Println(text)
	}

	fmt.Println()
	fmt.Println("Selected final state values:")
	if upper, ok := stateString(e.StateDelta, stateKeyUpper); ok {
		fmt.Printf("- %s: %s\n", stateKeyUpper, upper)
	}
	if last, ok := stateString(e.StateDelta, graph.StateKeyLastResponse); ok {
		fmt.Printf("- %s: %s\n", graph.StateKeyLastResponse, last)
	}
	return nil
}

func assistantText(e *event.Event) (string, bool) {
	if e == nil || e.Response == nil {
		return "", false
	}
	if len(e.Response.Choices) == 0 {
		return "", false
	}
	msg := e.Response.Choices[0].Message
	if msg.Role != model.RoleAssistant || msg.Content == "" {
		return "", false
	}
	return msg.Content, true
}

func stateString(delta map[string][]byte, key string) (string, bool) {
	if delta == nil || key == "" {
		return "", false
	}
	raw, ok := delta[key]
	if !ok || len(raw) == 0 {
		return "", false
	}
	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", false
	}
	return out, true
}
