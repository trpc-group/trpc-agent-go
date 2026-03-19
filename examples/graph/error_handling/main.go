//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName = "graph-error-handling"

	stateKeyNote = "note"

	recoverableScenario = "recoverable_local_node"
	fatalScenario       = "fatal_local_node"
	subgraphScenario    = "fatal_child_subgraph"

	recoverableAgentName = "recoverable-graph"
	fatalAgentName       = "fatal-graph"
	parentAgentName      = "parent-error-graph"
	childAgentName       = "child-error-graph"

	softErrorCode       = "LOOKUP_SOFT_TIMEOUT"
	fatalErrorCode      = "WRITE_FATAL"
	childFatalErrorCode = "CHILD_AGENT_FATAL"
)

type codedError struct {
	code        string
	message     string
	recoverable bool
}

func (e codedError) Error() string {
	return e.message
}

func (e codedError) Code() string {
	return e.code
}

func (e codedError) Recoverable() bool {
	return e.recoverable
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	recoverableAgent, err := buildRecoverableAgent()
	if err != nil {
		return err
	}
	if err := runScenario(
		recoverableScenario,
		recoverableAgent,
	); err != nil {
		return err
	}

	fatalAgent, err := buildFatalAgent()
	if err != nil {
		return err
	}
	if err := runScenario(
		fatalScenario,
		fatalAgent,
	); err != nil {
		return err
	}

	parentAgent, err := buildParentSubgraphAgent()
	if err != nil {
		return err
	}
	if err := runScenario(
		subgraphScenario,
		parentAgent,
	); err != nil {
		return err
	}
	return nil
}

func buildRecoverableAgent() (agent.Agent, error) {
	collector := graph.NewExecutionErrorCollector()

	schema := graph.MessagesStateSchema()
	collector.AddField(schema)
	schema.AddField(stateKeyNote, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
		Default: func() any { return "" },
	})

	sg := graph.NewStateGraph(schema).
		WithNodeCallbacks(collector.NodeCallbacks())

	sg.AddNode("lookup", func(
		ctx context.Context,
		state graph.State,
	) (any, error) {
		return nil, codedError{
			code:        softErrorCode,
			message:     "catalog lookup timed out",
			recoverable: true,
		}
	})

	sg.AddNode("finalize", func(
		ctx context.Context,
		state graph.State,
	) (any, error) {
		executionErrors := readExecutionErrors(state)
		note := "completed without fallback"
		if len(executionErrors) > 0 &&
			executionErrors[0].Error != nil {
			note = fmt.Sprintf(
				"completed with fallback after %s",
				executionErrors[0].Error.Message,
			)
		}
		return graph.State{stateKeyNote: note}, nil
	})

	compiled, err := sg.
		AddEdge("lookup", "finalize").
		SetEntryPoint("lookup").
		SetFinishPoint("finalize").
		Compile()
	if err != nil {
		return nil, err
	}
	return graphagent.New(recoverableAgentName, compiled)
}

func buildFatalAgent() (agent.Agent, error) {
	collector := graph.NewExecutionErrorCollector()

	schema := graph.MessagesStateSchema()
	collector.AddField(schema)
	schema.AddField(stateKeyNote, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
		Default: func() any { return "" },
	})

	sg := graph.NewStateGraph(schema).
		WithNodeCallbacks(collector.NodeCallbacks())

	sg.AddNode("write", func(
		ctx context.Context,
		state graph.State,
	) (any, error) {
		return nil, codedError{
			code:    fatalErrorCode,
			message: "database write failed",
		}
	})

	compiled, err := sg.
		SetEntryPoint("write").
		SetFinishPoint("write").
		Compile()
	if err != nil {
		return nil, err
	}
	return graphagent.New(fatalAgentName, compiled)
}

func buildParentSubgraphAgent() (agent.Agent, error) {
	childAgent, err := buildChildFatalAgent()
	if err != nil {
		return nil, err
	}

	collector := graph.NewExecutionErrorCollector()
	schema := graph.MessagesStateSchema()
	collector.AddField(schema)
	schema.AddField(stateKeyNote, graph.StateField{
		Type:    reflect.TypeOf(""),
		Reducer: graph.DefaultReducer,
		Default: func() any { return "" },
	})

	sg := graph.NewStateGraph(schema).
		WithNodeCallbacks(collector.NodeCallbacks())

	sg.AddAgentNode(
		childAgentName,
		graph.WithSubgraphOutputMapper(
			collector.SubgraphOutputMapper(),
		),
	)

	sg.AddNode("parent_finalize", func(
		ctx context.Context,
		state graph.State,
	) (any, error) {
		executionErrors := readExecutionErrors(state)
		note := "parent did not receive child fallback state"
		if len(executionErrors) > 0 &&
			executionErrors[0].Error != nil {
			note = fmt.Sprintf(
				"parent received child error %s",
				executionErrors[0].Error.Message,
			)
		}
		return graph.State{stateKeyNote: note}, nil
	})

	compiled, err := sg.
		AddEdge(childAgentName, "parent_finalize").
		SetEntryPoint(childAgentName).
		SetFinishPoint("parent_finalize").
		Compile()
	if err != nil {
		return nil, err
	}

	return graphagent.New(
		parentAgentName,
		compiled,
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
}

func buildChildFatalAgent() (agent.Agent, error) {
	collector := graph.NewExecutionErrorCollector()
	schema := graph.MessagesStateSchema()
	collector.AddField(schema)

	sg := graph.NewStateGraph(schema).
		WithNodeCallbacks(collector.NodeCallbacks())

	sg.AddNode("child_write", func(
		ctx context.Context,
		state graph.State,
	) (any, error) {
		return nil, codedError{
			code:    childFatalErrorCode,
			message: "child agent database write failed",
		}
	})

	compiled, err := sg.
		SetEntryPoint("child_write").
		SetFinishPoint("child_write").
		Compile()
	if err != nil {
		return nil, err
	}

	return graphagent.New(childAgentName, compiled)
}

func runScenario(name string, ag agent.Agent) error {
	sessionService := inmemory.NewSessionService()
	r := runner.NewRunner(
		appName+"-"+name,
		ag,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	fmt.Printf("== %s ==\n", name)
	eventCh, err := r.Run(
		context.Background(),
		"demo-user",
		"session-"+name,
		model.NewUserMessage("run"),
	)
	if err != nil {
		return err
	}

	for evt := range eventCh {
		if evt.IsTerminalError() &&
			evt.Response != nil {
			fmt.Printf(
				"stream error: code=%s message=%s\n",
				ptrValue(evt.Response.Error.Code),
				evt.Response.Error.Message,
			)
		}
		if !evt.IsRunnerCompletion() {
			continue
		}
		fmt.Println("runner completion received")
		executionErrors, err := graph.ExecutionErrorsFromStateDelta(
			evt.StateDelta,
			graph.StateKeyExecutionErrors,
		)
		if err != nil {
			return err
		}
		fmt.Printf(
			"runner completion collected %d error(s)\n",
			len(executionErrors),
		)
		for _, executionError := range executionErrors {
			fmt.Printf(
				"  - %s code=%s message=%s\n",
				executionError.Severity,
				ptrValue(executionError.Error.Code),
				executionError.Error.Message,
			)
		}
		if note, ok, err := decodeStringState(
			evt.StateDelta,
			stateKeyNote,
		); err != nil {
			return err
		} else if ok {
			fmt.Printf("runner completion note: %s\n", note)
		}
	}
	fmt.Println()
	return nil
}

func decodeStringState(
	stateDelta map[string][]byte,
	key string,
) (string, bool, error) {
	raw, ok := stateDelta[key]
	if !ok || len(raw) == 0 {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, err
	}
	return value, true, nil
}

func readExecutionErrors(
	state graph.State,
) []graph.ExecutionError {
	value, _ := state[graph.StateKeyExecutionErrors]
	executionErrors, _ := value.([]graph.ExecutionError)
	return executionErrors
}

func ptrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
