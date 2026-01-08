//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main demonstrates how an Agent node passes data to later nodes by
// writing to graph state.
//
// Key idea:
//   - The Agent node runs a child agent (a sub-agent).
//   - The child agent writes values into its own (child) graph state.
//   - WithSubgraphOutputMapper copies selected values from the child
//     final state back into the parent graph state.
//   - Subsequent parent nodes can read those values from state.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	emptyString = ""

	parentAgentName = "parent"
	childAgentName  = "child"

	childNodeCompute = "compute"
	parentNodeUse    = "use_child_value"

	keyChildValue     = "child_value"
	keyValueFromChild = "value_from_child"

	childValuePrefix  = "computed: "
	parentValuePrefix = "parent received: "

	flagInputName  = "input"
	flagInputUsage = "Text sent into the parent graph as user input"
	defaultInput   = "hello"

	childAgentDesc  = "Child agent that computes a value into state"
	parentAgentDesc = "Parent agent that calls the child and uses its state"

	exitCodeError = 1

	errNoCompletion          = "no graph completion event received"
	fmtMissingStateKey       = "missing state key: %s"
	fmtUnmarshalStateKey     = "unmarshal state key %s: %w"
	fmtInputLine             = "Input: %s\n"
	fmtValueFromChildLine    = "Value from child (via state): %s\n"
	fmtFinalResponseLine     = "Final response: %s\n"
	fmtErrorLine             = "error: %v\n"
	fmtNoFinalResponseChoice = "(no final response choice)"
)

var input = flag.String(flagInputName, defaultInput, flagInputUsage)

func main() {
	flag.Parse()

	childAgent, err := buildChildAgent()
	if err != nil {
		fmt.Fprintf(os.Stderr, fmtErrorLine, err)
		os.Exit(exitCodeError)
	}

	parentAgent, err := buildParentAgent(childAgent)
	if err != nil {
		fmt.Fprintf(os.Stderr, fmtErrorLine, err)
		os.Exit(exitCodeError)
	}

	completionEvent, err := runOnce(context.Background(), parentAgent, *input)
	if err != nil {
		fmt.Fprintf(os.Stderr, fmtErrorLine, err)
		os.Exit(exitCodeError)
	}

	valueFromChild, err := decodeJSONString(
		completionEvent.StateDelta,
		keyValueFromChild,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, fmtErrorLine, err)
		os.Exit(exitCodeError)
	}

	fmt.Printf(fmtInputLine, *input)
	fmt.Printf(fmtValueFromChildLine, valueFromChild)
	fmt.Printf(fmtFinalResponseLine, finalResponseText(completionEvent))
}

func buildChildAgent() (agent.Agent, error) {
	schema := graph.NewStateSchema()
	schema.AddField(
		graph.StateKeyUserInput,
		graph.StateField{Type: reflect.TypeOf(emptyString)},
	)
	schema.AddField(
		graph.StateKeyLastResponse,
		graph.StateField{Type: reflect.TypeOf(emptyString)},
	)
	schema.AddField(
		keyChildValue,
		graph.StateField{Type: reflect.TypeOf(emptyString)},
	)

	childGraph, err := graph.NewStateGraph(schema).
		AddNode(childNodeCompute, childComputeNode).
		SetEntryPoint(childNodeCompute).
		SetFinishPoint(childNodeCompute).
		Compile()
	if err != nil {
		return nil, err
	}

	return graphagent.New(
		childAgentName,
		childGraph,
		graphagent.WithDescription(childAgentDesc),
		graphagent.WithInitialState(graph.State{}),
	)
}

func childComputeNode(ctx context.Context, state graph.State) (any, error) {
	userInput, _ := graph.GetStateValue[string](state, graph.StateKeyUserInput)
	computed := childValuePrefix + userInput
	return graph.State{
		keyChildValue:              computed,
		graph.StateKeyLastResponse: computed,
	}, nil
}

func buildParentAgent(childAgent agent.Agent) (agent.Agent, error) {
	schema := graph.NewStateSchema()
	schema.AddField(
		graph.StateKeyLastResponse,
		graph.StateField{Type: reflect.TypeOf(emptyString)},
	)
	schema.AddField(
		keyValueFromChild,
		graph.StateField{Type: reflect.TypeOf(emptyString)},
	)

	parentGraph, err := graph.NewStateGraph(schema).
		AddAgentNode(
			childAgentName,
			graph.WithSubgraphOutputMapper(subgraphOutputMapper),
		).
		AddNode(parentNodeUse, parentUseChildValueNode).
		AddEdge(childAgentName, parentNodeUse).
		SetEntryPoint(childAgentName).
		SetFinishPoint(parentNodeUse).
		Compile()
	if err != nil {
		return nil, err
	}

	return graphagent.New(
		parentAgentName,
		parentGraph,
		graphagent.WithDescription(parentAgentDesc),
		graphagent.WithInitialState(graph.State{}),
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
}

func subgraphOutputMapper(
	parent graph.State,
	result graph.SubgraphResult,
) graph.State {
	value, ok := graph.GetStateValue[string](result.FinalState, keyChildValue)
	if !ok {
		return nil
	}
	return graph.State{keyValueFromChild: value}
}

func parentUseChildValueNode(
	ctx context.Context,
	state graph.State,
) (any, error) {
	value, ok := graph.GetStateValue[string](state, keyValueFromChild)
	if !ok {
		return nil, fmt.Errorf(fmtMissingStateKey, keyValueFromChild)
	}
	final := parentValuePrefix + value
	return graph.State{graph.StateKeyLastResponse: final}, nil
}

func runOnce(
	ctx context.Context,
	a agent.Agent,
	userInput string,
) (*event.Event, error) {
	inv := agent.NewInvocation(
		agent.WithInvocationAgent(a),
		agent.WithInvocationMessage(model.NewUserMessage(userInput)),
	)
	eventChan, err := a.Run(ctx, inv)
	if err != nil {
		return nil, err
	}

	var completionEvent *event.Event
	for ev := range eventChan {
		if ev == nil {
			continue
		}
		if ev.Done && ev.Object == graph.ObjectTypeGraphExecution {
			completionEvent = ev
		}
	}

	if completionEvent == nil {
		return nil, fmt.Errorf(errNoCompletion)
	}
	return completionEvent, nil
}

func finalResponseText(ev *event.Event) string {
	if ev == nil || ev.Response == nil || len(ev.Choices) == 0 {
		return fmtNoFinalResponseChoice
	}
	return ev.Choices[0].Message.Content
}

func decodeJSONString(
	stateDelta map[string][]byte,
	key string,
) (string, error) {
	raw, ok := stateDelta[key]
	if !ok {
		return emptyString, fmt.Errorf(fmtMissingStateKey, key)
	}

	var out string
	if err := json.Unmarshal(raw, &out); err != nil {
		return emptyString, fmt.Errorf(fmtUnmarshalStateKey, key, err)
	}
	return out, nil
}
