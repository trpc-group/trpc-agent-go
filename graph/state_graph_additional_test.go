//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// captureModel captures the last request passed to GenerateContent.
type captureModel struct{ lastReq *model.Request }

func (c *captureModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	c.lastReq = req
	ch := make(chan *model.Response, 1)
	// Mark Done=true to avoid emitting streaming response events and keep focus on model start/complete events.
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Index: 0, Message: model.NewAssistantMessage("ok")}}}
	close(ch)
	return ch, nil
}

func (c *captureModel) Info() model.Info { return model.Info{Name: "capture"} }

// echoTool is a minimal CallableTool used for ToolSet injection tests.
type echoTool struct{ name string }

func (e *echoTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: e.name} }
func (e *echoTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return map[string]any{"ok": true}, nil
}

// simpleToolSet returns a fixed set of tools.
type simpleToolSet struct{}

func (s *simpleToolSet) Tools(ctx context.Context) []tool.CallableTool {
	return []tool.CallableTool{&echoTool{name: "echo"}}
}
func (s *simpleToolSet) Close() error { return nil }

func TestAddLLMNode_ToolSetInjection_And_ModelEventInput(t *testing.T) {
	schema := MessagesStateSchema()
	cm := &captureModel{}
	sg := NewStateGraph(schema)
	// Inject toolset via node options
	sg.AddLLMNode("llm", cm, "inst", nil, WithToolSets([]tool.ToolSet{&simpleToolSet{}}))
	// Ensure node type is LLM
	n, ok := sg.graph.nodes["llm"]
	require.True(t, ok)
	require.Equal(t, NodeTypeLLM, n.Type)

	// Build a minimal exec context to receive events
	ch := make(chan *event.Event, 8)
	exec := &ExecutionContext{InvocationID: "inv-llm", EventChan: ch}
	state := State{StateKeyExecContext: exec, StateKeyCurrentNodeID: "llm", StateKeyUserInput: "hi"}

	// Call the node function directly
	_, err := n.Function(context.Background(), state)
	require.NoError(t, err)

	// Verify model received tools injected from ToolSet
	require.NotNil(t, cm.lastReq)
	require.Contains(t, cm.lastReq.Tools, "echo")

	// Drain available events and verify model start/complete include input built from instruction+user_input
	var modelInputs []string
	for {
		select {
		case e := <-ch:
			if e != nil && e.StateDelta != nil {
				if b, ok := e.StateDelta[MetadataKeyModel]; ok {
					var meta ModelExecutionMetadata
					_ = json.Unmarshal(b, &meta)
					if meta.Input != "" {
						modelInputs = append(modelInputs, meta.Input)
					}
				}
			}
		default:
			goto DONE
		}
	}
DONE:
	// Expect at least one model event carrying the combined input string
	require.NotEmpty(t, modelInputs)
	found := false
	for _, in := range modelInputs {
		if in == "inst\n\nhi" || (len(in) >= 2 && in[0:4] == "inst") {
			found = true
			break
		}
	}
	require.True(t, found, "expected model event input to contain instruction and user input: %v", modelInputs)
}

func TestBuilderOptions_Destinations_And_Callbacks(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())

	before1 := func(ctx context.Context, cb *NodeCallbackContext, st State) (any, error) { return nil, nil }
	after1 := func(ctx context.Context, cb *NodeCallbackContext, st State, result any, nodeErr error) (any, error) {
		return nil, nil
	}
	onErr1 := func(ctx context.Context, cb *NodeCallbackContext, st State, err error) {}

	cbs := NewNodeCallbacks().
		RegisterBeforeNode(before1).
		RegisterAfterNode(after1).
		RegisterOnNodeError(onErr1)

	// Add node with destinations and per-node callbacks
	// Also add the declared destination node "A" so validation succeeds.
	sg.AddNode("A", func(ctx context.Context, st State) (any, error) { return st, nil })
	sg.AddNode("n", func(ctx context.Context, st State) (any, error) { return st, nil },
		WithDestinations(map[string]string{"A": "toA"}),
		WithNodeCallbacks(cbs),
		WithPreNodeCallback(func(ctx context.Context, cb *NodeCallbackContext, st State) (any, error) { return nil, nil }),
		WithPostNodeCallback(func(ctx context.Context, cb *NodeCallbackContext, st State, result any, err error) (any, error) {
			return nil, nil
		}),
		WithNodeErrorCallback(func(ctx context.Context, cb *NodeCallbackContext, st State, err error) {}),
		WithAgentNodeEventCallback(func(ctx context.Context, cb *NodeCallbackContext, st State, e *event.Event) {}),
	)

	// Compile to validate graph
	_, err := sg.SetEntryPoint("n").SetFinishPoint("n").Compile()
	require.NoError(t, err)

	node := sg.graph.nodes["n"]
	require.NotNil(t, node)
	require.Contains(t, node.destinations, "A")
	require.NotNil(t, node.callbacks)
	require.Len(t, node.callbacks.BeforeNode, 2)
	require.Len(t, node.callbacks.AfterNode, 2)
	require.Len(t, node.callbacks.OnNodeError, 2)
	require.Len(t, node.callbacks.AgentEvent, 1)
}

func TestAddEdge_PregelSetup(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	pass := func(ctx context.Context, st State) (any, error) { return st, nil }
	sg.AddNode("A", pass)
	sg.AddNode("B", pass)
	sg.AddEdge("A", "B")
	_, err := sg.SetEntryPoint("A").SetFinishPoint("B").Compile()
	require.NoError(t, err)

	// Channel mapping should include branch:to:B -> [B]
	triggers := sg.graph.getTriggerToNodes()
	require.Contains(t, triggers, "branch:to:B")
	require.Contains(t, triggers["branch:to:B"], "B")

	// Writers on A should include the branch channel
	nodeA := sg.graph.nodes["A"]
	found := false
	for _, w := range nodeA.writers {
		if w.Channel == "branch:to:B" {
			found = true
			break
		}
	}
	require.True(t, found, "expected writer to branch:to:B on node A")
}

func TestAddToolsAndAgentNode_Types(t *testing.T) {
	sg := NewStateGraph(NewStateSchema())
	sg.AddToolsNode("tools", map[string]tool.Tool{"echo": &echoTool{name: "echo"}})
	sg.AddAgentNode("agent")
	require.Equal(t, NodeTypeTool, sg.graph.nodes["tools"].Type)
	require.Equal(t, NodeTypeAgent, sg.graph.nodes["agent"].Type)
}
