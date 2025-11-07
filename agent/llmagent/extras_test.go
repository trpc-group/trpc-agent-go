//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace/noop"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockTool implements tool.CallableTool for testing.
type mockTool struct{ name string }

func (m *mockTool) Declaration() *tool.Declaration { return &tool.Declaration{Name: m.name} }
func (m *mockTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return "ok", nil
}

// mockToolSet returns a static slice of tools.
type mockToolSet struct {
	tools []tool.Tool
	name  string
}

func (s *mockToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (s *mockToolSet) Close() error                      { return nil }
func (s *mockToolSet) Name() string                      { return s.name }

// fakeKnowledge implements a minimal Knowledge interface.
// It is only used to verify that the knowledge search tool is appended.
type fakeKnowledge struct{}

func (f *fakeKnowledge) Search(ctx context.Context, req *knowledge.SearchRequest) (*knowledge.SearchResult, error) {
	return &knowledge.SearchResult{Document: &document.Document{Content: "none"}}, nil
}

func TestRegisterTools_AddsToolSet(t *testing.T) {
	// Prepare inputs.
	direct := []tool.Tool{&mockTool{name: "direct"}}

	setTool := &mockTool{name: "set-tool"}
	ts := &mockToolSet{tools: []tool.Tool{setTool}, name: "mock"}

	kb := &fakeKnowledge{}

	all, userToolNames := registerTools(&Options{Tools: direct, ToolSets: []tool.ToolSet{ts}, Knowledge: kb})

	// Expect 1 direct + 1 from set + 1 knowledge search tool.
	require.Equal(t, 3, len(all))

	// Verify user tool tracking
	require.True(t, userToolNames["direct"], "direct tool should be tracked as user tool")

	names := []string{}
	for _, t := range all {
		names = append(names, t.Declaration().Name)
	}
	require.Contains(t, names, "direct")
	require.Contains(t, names, "mock_set-tool") // Tool name is now namespaced with toolset name
	// Knowledge search tool name is "knowledge_search" per implementation.
	require.Contains(t, names, "knowledge_search")
}

// mockAgent minimal implementation.
type mockAgent struct{ name string }

func (m *mockAgent) Info() agent.Info                { return agent.Info{Name: m.name} }
func (m *mockAgent) SubAgents() []agent.Agent        { return nil }
func (m *mockAgent) FindSubAgent(string) agent.Agent { return nil }
func (m *mockAgent) Tools() []tool.Tool              { return nil }
func (m *mockAgent) Run(ctx context.Context, inv *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	close(ch)
	return ch, nil
}

func TestLLMAgent_ToolsAddsTransfer(t *testing.T) {
	coreTool := &mockTool{name: "core"}
	sub1 := &mockAgent{name: "sub1"}
	sub2 := &mockAgent{name: "sub2"}

	a := New("parent",
		WithTools([]tool.Tool{coreTool}),
		WithSubAgents([]agent.Agent{sub1, sub2}),
	)

	got := a.Tools()
	// Should contain core tool + transfer tool.
	require.Equal(t, 2, len(got))
	names := []string{got[0].Declaration().Name, got[1].Declaration().Name}
	require.Contains(t, names, "core")
	require.Contains(t, names, "transfer_to_agent")

	// FindSubAgent should work.
	found := a.FindSubAgent("sub2")
	require.NotNil(t, found)
	require.Equal(t, "sub2", found.Info().Name)
}

func TestLLMAgent_InfoAndTools(t *testing.T) {
	t1 := &mockTool{name: "t1"}
	agent := New("my-agent", WithDescription("desc"), WithTools([]tool.Tool{t1}))

	info := agent.Info()
	require.Equal(t, "my-agent", info.Name)
	require.Equal(t, "desc", info.Description)

	ts := agent.Tools()
	require.Equal(t, 1, len(ts))
	require.Equal(t, "t1", ts[0].Declaration().Name)
}

func TestLLMAgent_AfterCb(t *testing.T) {
	// Prepare original event channel.
	orig := make(chan *event.Event, 1)
	orig <- event.New("id", "agent")
	close(orig)

	cb := agent.NewCallbacks()
	cb.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, err error) (*model.Response, error) {
		return &model.Response{Object: "after", Done: true}, nil
	})

	inv := &agent.Invocation{InvocationID: "id", AgentName: "agent"}

	llm := &LLMAgent{agentCallbacks: cb}
	wrapped := llm.wrapEventChannel(context.Background(), inv, orig, noop.Span{})

	var objs []string
	for e := range wrapped {
		objs = append(objs, e.Object)
	}
	require.Equal(t, 2, len(objs))
	require.Equal(t, "after", objs[1])
}

func TestLLMAgent_AfterCbNoResp(t *testing.T) {
	orig := make(chan *event.Event, 1)
	orig <- event.New("id2", "agent2")
	close(orig)

	cb := agent.NewCallbacks()
	cb.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, err error) (*model.Response, error) {
		// Return nil response and nil error to exercise no-op branch.
		return nil, nil
	})

	inv := &agent.Invocation{InvocationID: "id2", AgentName: "agent2"}

	llm := &LLMAgent{}
	wrapped := llm.wrapEventChannel(context.Background(), inv, orig, noop.Span{})

	// Expect exactly one event propagated from original channel and no extras.
	count := 0
	for range wrapped {
		count++
	}
	if count != 1 {
		t.Fatalf("expected 1 event, got %d", count)
	}
}

func TestLLMAgent_WithToolSet(t *testing.T) {
	ct := &mockTool{name: "foo"}
	ts := &mockToolSet{tools: []tool.Tool{ct}, name: "mock"}

	agt := New("toolset-agent",
		WithModel(newDummyModel()),
		WithToolSets([]tool.ToolSet{ts}),
	)

	tools := agt.Tools()
	var found bool
	for _, tl := range tools {
		if tl.Declaration().Name == "mock_foo" { // Tool name is now namespaced with toolset name
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected tool from toolset to be registered")
	}
}

func TestLLMAgent_Tools_WithSubAgents(t *testing.T) {
	sub := New("child")
	parent := New("parent", WithSubAgents([]agent.Agent{sub}))

	tools := parent.Tools()
	// A transfer tool should be automatically added when SubAgents() is non-empty.
	var foundTransfer bool
	for _, tl := range tools {
		if tl.Declaration().Name == "transfer_to_agent" {
			foundTransfer = true
			break
		}
	}
	if !foundTransfer {
		t.Fatalf("expected transfer_to_agent tool when subagents present")
	}
}

// dummyPlanner provides minimal planner implementation for option coverage.
type dummyPlanner struct{}

func (d *dummyPlanner) BuildPlanningInstruction(ctx context.Context, inv *agent.Invocation, req *model.Request) string {
	return ""
}

func (d *dummyPlanner) ProcessPlanningResponse(ctx context.Context, inv *agent.Invocation, rsp *model.Response) *model.Response {
	return rsp
}

func TestLLMAgent_WithVariousOptions(t *testing.T) {
	max := 42
	genConfig := model.GenerationConfig{MaxTokens: &max}
	mc := model.NewCallbacks()
	tc := tool.NewCallbacks()

	a := New("opts",
		WithInstruction("instr"),
		WithGlobalInstruction("global"),
		WithGenerationConfig(genConfig),
		WithChannelBufferSize(12),
		WithPlanner(&dummyPlanner{}),
		WithModelCallbacks(mc),
		WithToolCallbacks(tc),
		WithModel(newDummyModel()),
	)

	if a == nil {
		t.Fatalf("expected non-nil agent")
	}

	// Ensure fields set via reflection or exported methods.
	if info := a.Info(); info.Name != "opts" {
		t.Fatalf("unexpected name %s", info.Name)
	}
}

func TestLLMAgent_WithEnableParallelTools_Option(t *testing.T) {
	// Test that WithEnableParallelTools option sets the correct value
	tests := []struct {
		name           string
		enableParallel bool
		expected       bool
	}{
		{
			name:           "parallel disabled (default)",
			enableParallel: false,
			expected:       false,
		},
		{
			name:           "parallel enabled",
			enableParallel: true,
			expected:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal options structure to test
			opts := &Options{}

			// Apply the WithEnableParallelTools option
			option := WithEnableParallelTools(tt.enableParallel)
			option(opts)

			// Verify the option was set correctly
			assert.Equal(t, tt.expected, opts.EnableParallelTools,
				"EnableParallelTools should be set to %v", tt.expected)

			t.Logf("✅ Verified EnableParallelTools = %v", opts.EnableParallelTools)
		})
	}
}

func TestLLMAgent_WithEnableParallelTools_DefaultBehavior(t *testing.T) {
	// Test that the default behavior (without WithEnableParallelTools) is serial execution
	opts := &Options{}

	// Don't apply WithEnableParallelTools option, should default to false
	assert.False(t, opts.EnableParallelTools, "Default behavior should be serial execution")

	t.Log("✅ Verified default behavior is serial execution")
}

func TestLLMAgent_WithEnableParallelTools_Integration(t *testing.T) {
	// Test basic integration without complex model mocking
	tool1 := &simpleTestTool{name: "tool1"}
	tool2 := &simpleTestTool{name: "tool2"}

	// Test with parallel disabled (default)
	agentDisabled := New(
		"test-agent-disabled",
		WithDescription("Test agent with parallel disabled"),
		WithTools([]tool.Tool{tool1, tool2}),
		WithEnableParallelTools(false),
	)

	// Test with parallel enabled
	agentEnabled := New(
		"test-agent-enabled",
		WithDescription("Test agent with parallel enabled"),
		WithTools([]tool.Tool{tool1, tool2}),
		WithEnableParallelTools(true),
	)

	// Verify agents were created successfully
	require.NotNil(t, agentDisabled, "Agent with disabled parallel should be created")
	require.NotNil(t, agentEnabled, "Agent with enabled parallel should be created")

	// Basic checks - agents should have the same tools but different parallel settings
	assert.Equal(t, len(agentDisabled.tools), len(agentEnabled.tools),
		"Both agents should have the same number of tools")

	t.Log("✅ Successfully created agents with different parallel settings")
}

// Simple tool for testing
type simpleTestTool struct {
	name string
}

func (s *simpleTestTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        s.name,
		Description: "Simple test tool",
	}
}

func (s *simpleTestTool) Call(_ []byte) (any, error) {
	return map[string]string{"result": s.name}, nil
}
