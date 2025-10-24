package graph

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
)

// TestEndsValidation ensures per-node ends' targets are validated at compile time.
func TestEndsValidation(t *testing.T) {
	schema := NewStateSchema().
		AddField("ok", StateField{Type: reflect.TypeOf(false), Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) { return nil, nil }, WithEndsMap(map[string]string{
		"goB":  "B",
		"stop": End,
	}))
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) { return State{"ok": true}, nil })
	sg.SetEntryPoint("A")

	// Should compile: ends refer to existing node B and End.
	_, err := sg.Compile()
	require.NoError(t, err)
}

// TestEndsValidation_InvalidTarget ensures compile fails if ends map refers to a non-existent node.
func TestEndsValidation_InvalidTarget(t *testing.T) {
	schema := NewStateSchema().
		AddField("ok", StateField{Type: reflect.TypeOf(false), Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) { return nil, nil }, WithEndsMap(map[string]string{
		"bad": "NOPE", // NOPE is not declared in graph
	}))
	sg.SetEntryPoint("A")

	_, err := sg.Compile()
	require.Error(t, err)
}

// TestCommandGoToWithEnds ensures Command.GoTo resolves via per-node ends.
func TestCommandGoToWithEnds(t *testing.T) {
	schema := NewStateSchema().
		AddField("visited", StateField{Type: reflect.TypeOf(""), Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	sg.AddNode("start", func(ctx context.Context, s State) (any, error) {
		return &Command{GoTo: "toB"}, nil // symbolic branch key
	}, WithEndsMap(map[string]string{"toB": "B"}))
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) { return State{"visited": "B"}, nil })
	sg.SetEntryPoint("start").SetFinishPoint("B")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	ch, err := exec.Execute(context.Background(), State{}, &agent.Invocation{InvocationID: "inv-ends-goto"})
	require.NoError(t, err)

	final := make(State)
	for ev := range ch {
		if ev.Done && ev.StateDelta != nil {
			for k, vb := range ev.StateDelta {
				if k == MetadataKeyNode || k == MetadataKeyPregel || k == MetadataKeyChannel || k == MetadataKeyState || k == MetadataKeyCompletion {
					continue
				}
				var v any
				if err := json.Unmarshal(vb, &v); err == nil {
					final[k] = v
				}
			}
		}
	}

	require.Equal(t, "B", final["visited"])
}

// TestConditionalEdgesWithEnds ensures conditional results are resolved via per-node ends when no PathMap is provided.
func TestConditionalEdgesWithEnds(t *testing.T) {
	schema := NewStateSchema().
		AddField("res", StateField{Type: reflect.TypeOf(""), Reducer: DefaultReducer})

	sg := NewStateGraph(schema)
	sg.AddNode("A", func(ctx context.Context, s State) (any, error) {
		// Do nothing; routing decided by conditional
		return nil, nil
	}, WithEndsMap(map[string]string{"go": "B"}))
	sg.AddNode("B", func(ctx context.Context, s State) (any, error) { return State{"res": "ok"}, nil })
	sg.SetEntryPoint("A")
	// Conditional returns symbolic key "go"; since no PathMap given, ends mapping should resolve it to B.
	sg.AddConditionalEdges("A", func(ctx context.Context, s State) (string, error) { return "go", nil }, nil)
	sg.SetFinishPoint("B")

	g, err := sg.Compile()
	require.NoError(t, err)
	exec, err := NewExecutor(g)
	require.NoError(t, err)

	ch, err := exec.Execute(context.Background(), State{}, &agent.Invocation{InvocationID: "inv-ends-cond"})
	require.NoError(t, err)

	final := make(State)
	for ev := range ch {
		if ev.Done && ev.StateDelta != nil {
			for k, vb := range ev.StateDelta {
				if k == MetadataKeyNode || k == MetadataKeyPregel || k == MetadataKeyChannel || k == MetadataKeyState || k == MetadataKeyCompletion {
					continue
				}
				var v any
				if err := json.Unmarshal(vb, &v); err == nil {
					final[k] = v
				}
			}
		}
	}
	require.Equal(t, "ok", final["res"])
}
