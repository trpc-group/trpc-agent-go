package main

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// This example demonstrates how to implement a simple "while" style loop
// using the existing StateGraph primitives: nodes, edges, and conditional
// edges. No engine changes are required – the loop is expressed as a
// conditional back-edge in the graph.
//
// Pseudocode semantics:
//
//   counter := 0
//   while counter < 3 {
//       counter++
//   }
//
// The final value of counter should be 3.

func main() {
	// 1. Define state schema with a single integer field "counter".
	schema := graph.NewStateSchema().
		AddField("counter", graph.StateField{
			Type:    reflect.TypeOf(0),
			Reducer: graph.DefaultReducer,
			// Provide a default so that the field exists even if initial
			// state does not set it explicitly.
			Default: func() any { return 0 },
		})

	// 2. Build StateGraph.
	sg := graph.NewStateGraph(schema)

	// loop_body increments the counter and returns the updated value.
	sg.AddNode("loop_body", func(ctx context.Context, s graph.State) (any, error) {
		currentAny, ok := s["counter"]
		var current int
		if ok {
			if v, ok := currentAny.(int); ok {
				current = v
			}
		}
		next := current + 1
		fmt.Printf("[loop_body] counter: %d -> %d\n", current, next)
		return graph.State{"counter": next}, nil
	})

	// finish just prints the final counter value.
	sg.AddNode("finish", func(ctx context.Context, s graph.State) (any, error) {
		v, _ := s["counter"].(int)
		fmt.Printf("[finish] final counter = %d\n", v)
		return nil, nil
	})

	// Entry point is loop_body; finish is the virtual end.
	sg.SetEntryPoint("loop_body").SetFinishPoint("finish")

	// 3. Add conditional edge that implements the while semantics.
	//
	// The conditional returns either "continue" or "break". PathMap then
	// maps those symbolic results to actual node IDs, creating:
	//   loop_body --(continue)--> loop_body
	//   loop_body --(break)-----> finish
	sg.AddConditionalEdges("loop_body",
		func(ctx context.Context, s graph.State) (string, error) {
			vAny, ok := s["counter"]
			if !ok {
				return "continue", nil
			}
			v, ok := vAny.(int)
			if !ok {
				return "continue", nil
			}
			if v < 3 {
				return "continue", nil
			}
			return "break", nil
		},
		map[string]string{
			"continue": "loop_body",
			"break":    "finish",
		},
	)

	// 4. Compile and execute.
	g, err := sg.Compile()
	if err != nil {
		panic(fmt.Errorf("compile graph: %w", err))
	}

	exec, err := graph.NewExecutor(g)
	if err != nil {
		panic(fmt.Errorf("new executor: %w", err))
	}

	initialState := graph.State{} // "counter" will start from default 0
	invocation := &agent.Invocation{InvocationID: "while-basic"}

	events, err := exec.Execute(context.Background(), initialState, invocation)
	if err != nil {
		panic(fmt.Errorf("execute: %w", err))
	}

	final := make(graph.State)
	for ev := range events {
		if ev.Error != nil {
			fmt.Printf("error: %s\n", ev.Error.Message)
		}
		if ev.Done && ev.StateDelta != nil {
			for k, b := range ev.StateDelta {
				var v any
				if err := json.Unmarshal(b, &v); err == nil {
					final[k] = v
				}
			}
		}
	}

	if v, ok := final["counter"].(float64); ok { // JSON numbers decode as float64
		fmt.Printf("[main] observed final counter in StateDelta = %.0f\n", v)
	}
}

