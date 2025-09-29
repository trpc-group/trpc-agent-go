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
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func newDummyModel() model.Model {
	return openai.New("dummy-model")
}

func TestLLMAgent_SubAgents(t *testing.T) {
	sub := New("sub", WithDescription("subdesc"))
	agt := New("main", WithSubAgents([]agent.Agent{sub}))
	if len(agt.SubAgents()) != 1 {
		t.Errorf("expected 1 subagent")
	}
	if agt.FindSubAgent("sub") == nil {
		t.Errorf("FindSubAgent failed")
	}
	if agt.FindSubAgent("notfound") != nil {
		t.Errorf("FindSubAgent should return nil for missing")
	}
}

// Test that buildRequestProcessors wires AddSessionSummary into
// ContentRequestProcessor correctly.
func TestBuildRequestProcessors_AddSessionSummaryWiring(t *testing.T) {
	// true case - test that WithAddSessionSummary(true) is properly wired.
	optsTrue := &Options{}
	WithAddSessionSummary(true)(optsTrue)
	procs := buildRequestProcessors("test-agent", optsTrue)
	var crp *processor.ContentRequestProcessor
	for _, p := range procs {
		if v, ok := p.(*processor.ContentRequestProcessor); ok {
			crp = v
		}
	}
	require.NotNil(t, crp)
	require.True(t, crp.AddSessionSummary)

	// false case - test that WithAddSessionSummary(false) is properly wired.
	optsFalse := &Options{}
	WithAddSessionSummary(false)(optsFalse)
	procs = buildRequestProcessors("test-agent", optsFalse)
	crp = nil
	for _, p := range procs {
		if v, ok := p.(*processor.ContentRequestProcessor); ok {
			crp = v
		}
	}
	require.NotNil(t, crp)
	require.False(t, crp.AddSessionSummary)
}

// Test that buildRequestProcessors wires MaxHistoryRuns into
// ContentRequestProcessor correctly.
func TestBuildRequestProcessors_MaxHistoryRunsWiring(t *testing.T) {
	// Test with MaxHistoryRuns set - test that WithMaxHistoryRuns(10) is properly wired.
	optsWithMax := &Options{}
	WithMaxHistoryRuns(10)(optsWithMax)
	procs := buildRequestProcessors("test-agent", optsWithMax)
	var crp *processor.ContentRequestProcessor
	for _, p := range procs {
		if v, ok := p.(*processor.ContentRequestProcessor); ok {
			crp = v
		}
	}
	require.NotNil(t, crp)
	require.Equal(t, 10, crp.MaxHistoryRuns)

	// Test with default value (0) - test that WithMaxHistoryRuns(0) is properly wired.
	optsDefault := &Options{}
	WithMaxHistoryRuns(0)(optsDefault)
	procs = buildRequestProcessors("test-agent", optsDefault)
	crp = nil
	for _, p := range procs {
		if v, ok := p.(*processor.ContentRequestProcessor); ok {
			crp = v
		}
	}
	require.NotNil(t, crp)
	require.Equal(t, 0, crp.MaxHistoryRuns)
}

// Test that WithAddSessionSummary option sets the AddSessionSummary field correctly.
func TestWithAddSessionSummary_Option(t *testing.T) {
	opts := &Options{}
	WithAddSessionSummary(true)(opts)
	require.True(t, opts.AddSessionSummary)

	// Test with false value
	opts = &Options{}
	WithAddSessionSummary(false)(opts)
	require.False(t, opts.AddSessionSummary)
}

// Test that WithMaxHistoryRuns option sets the MaxHistoryRuns field correctly.
func TestWithMaxHistoryRuns_Option(t *testing.T) {
	opts := &Options{}
	WithMaxHistoryRuns(5)(opts)
	require.Equal(t, 5, opts.MaxHistoryRuns)

	// Test with zero value
	opts = &Options{}
	WithMaxHistoryRuns(0)(opts)
	require.Equal(t, 0, opts.MaxHistoryRuns)
}

func TestLLMAgent_Run_BeforeAgentShort(t *testing.T) {
	// BeforeAgentCallback returns a custom response, should short-circuit.
	agentCallbacks := agent.NewCallbacks()
	agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "short-circuit"},
			}},
		}, nil
	})
	agt := New("test", WithModel(newDummyModel()), WithAgentCallbacks(agentCallbacks))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
	events, err := agt.Run(context.Background(), inv)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	ev := <-events
	if ev.Response == nil || len(ev.Response.Choices) == 0 || !strings.Contains(ev.Response.Choices[0].Message.Content, "short-circuit") {
		t.Errorf("expected short-circuit response, got %+v", ev.Response)
	}
}

func TestLLMAgent_Run_BeforeAgentError(t *testing.T) {
	agentCallbacks := agent.NewCallbacks()
	agentCallbacks.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		return nil, errors.New("fail")
	})
	agt := New("test", WithModel(newDummyModel()), WithAgentCallbacks(agentCallbacks))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
	_, err := agt.Run(context.Background(), inv)
	if err == nil || !strings.Contains(err.Error(), "before agent callback failed") {
		t.Errorf("expected before agent callback error, got %v", err)
	}
}

func TestLLMAgent_Run_AfterAgentCallback(t *testing.T) {
	// AfterAgentCallback should append a custom event after normal flow.
	agentCallbacks := agent.NewCallbacks()
	agentCallbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
		return &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "after-cb"},
			}},
		}, nil
	})
	agt := New("test", WithModel(newDummyModel()), WithAgentCallbacks(agentCallbacks))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi"), InvocationID: "test-invocation", Session: &session.Session{ID: "test-session"}}
	events, err := agt.Run(context.Background(), inv)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	var found bool
	for ev := range events {
		if ev.Response != nil && len(ev.Response.Choices) > 0 && strings.Contains(ev.Response.Choices[0].Message.Content, "after-cb") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected after agent callback event")
	}
}

func TestLLMAgent_Run_NormalFlow(t *testing.T) {
	agt := New("test", WithModel(newDummyModel()))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi"), InvocationID: "test-invocation", Session: &session.Session{ID: "test-session"}}
	events, err := agt.Run(context.Background(), inv)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	// Should get at least one event (may be empty response)
	_, ok := <-events
	if !ok {
		t.Errorf("expected at least one event")
	}
}

func TestLLMAgent_Run_AfterAgentCbErr(t *testing.T) {
	agentCallbacks := agent.NewCallbacks()
	agentCallbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
		return nil, errors.New("after error")
	})
	agt := New("test", WithModel(newDummyModel()), WithAgentCallbacks(agentCallbacks))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi"), InvocationID: "test-invocation", Session: &session.Session{ID: "test-session"}}
	events, err := agt.Run(context.Background(), inv)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	var foundErr bool
	for ev := range events {
		if ev.Error != nil && strings.Contains(ev.Error.Message, "after error") {
			foundErr = true
			break
		}
	}
	if !foundErr {
		t.Errorf("expected error event from after agent callback")
	}
}

// mockKnowledgeBase is a simple in-memory knowledge base for testing.
type mockKnowledgeBase struct {
	documents map[string]*document.Document
}

func (m *mockKnowledgeBase) Load(ctx context.Context, opts ...knowledge.LoadOption) error {
	return nil
}

func (m *mockKnowledgeBase) AddDocument(ctx context.Context, doc *document.Document) error {
	m.documents[doc.ID] = doc
	return nil
}

func (m *mockKnowledgeBase) Search(ctx context.Context, req *knowledge.SearchRequest) (*knowledge.SearchResult, error) {
	// Simple keyword-based search for testing.
	query := strings.ToLower(req.Query)

	var bestMatch *document.Document
	var bestScore float64

	for _, doc := range m.documents {
		// Get content directly as string.
		content := strings.ToLower(doc.Content)
		name := strings.ToLower(doc.Name)

		// Calculate a simple relevance score.
		score := 0.0
		if strings.Contains(content, query) {
			score += 0.5
		}
		if strings.Contains(name, query) {
			score += 0.3
		}

		if score > bestScore {
			bestScore = score
			bestMatch = doc
		}
	}

	if bestMatch == nil {
		return nil, nil
	}

	// Get content for the result.
	content := bestMatch.Content

	return &knowledge.SearchResult{
		Document: bestMatch,
		Score:    bestScore,
		Text:     content,
	}, nil
}

func (m *mockKnowledgeBase) Close() error {
	return nil
}

func TestLLMAgent_WithKnowledge(t *testing.T) {
	// Create a mock knowledge base.
	kb := &mockKnowledgeBase{
		documents: map[string]*document.Document{
			"test-doc": {
				ID:      "test-doc",
				Name:    "Test Document",
				Content: "This is a test document about testing.",
			},
		},
	}

	// Create agent with knowledge base.
	agt := New("test-agent",
		WithModel(newDummyModel()),
		WithKnowledge(kb),
	)

	// Check that the knowledge search tool was automatically added.
	tools := agt.Tools()
	foundKnowledgeTool := false
	for _, toolItem := range tools {
		decl := toolItem.Declaration()
		if decl.Name == "knowledge_search" {
			foundKnowledgeTool = true
			break
		}
	}

	if !foundKnowledgeTool {
		t.Errorf("expected knowledge_search tool to be automatically added")
	}

	// Verify that the tool can be called.
	for _, toolItem := range tools {
		decl := toolItem.Declaration()
		if decl.Name == "knowledge_search" {
			// Check if it's a callable tool.
			if callableTool, ok := toolItem.(tool.CallableTool); ok {
				// Test the tool with a simple query.
				result, err := callableTool.Call(context.Background(), []byte(`{"query": "test"}`))
				if err != nil {
					t.Errorf("knowledge search tool call failed: %v", err)
				}
				if result == nil {
					t.Errorf("expected non-nil result from knowledge search")
				}
			}
			break
		}
	}
}

// staticModel is a lightweight test model that exposes a fixed name.
type staticModel struct{ name string }

func (m *staticModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	// Not used in this test since we short-circuit via callbacks.
	return nil, nil
}

func (m *staticModel) Info() model.Info { return model.Info{Name: m.name} }

func TestLLMAgent_SetModel_UpdatesInvocationModel(t *testing.T) {
	mA := &staticModel{name: "model-A"}
	mB := &staticModel{name: "model-B"}

	// Capture model name seen inside the agent before run.
	var seen string
	cbs := agent.NewCallbacks()
	cbs.RegisterBeforeAgent(func(ctx context.Context, inv *agent.Invocation) (*model.Response, error) {
		if inv.Model != nil {
			seen = inv.Model.Info().Name
		}
		// Short-circuit to avoid invoking underlying flow.
		return &model.Response{Choices: []model.Choice{{
			Message: model.Message{Role: model.RoleAssistant, Content: "ok"},
		}}}, nil
	})

	agt := New("test-agent", WithModel(mA), WithAgentCallbacks(cbs))

	// First run should use model-A.
	inv1 := &agent.Invocation{Message: model.NewUserMessage("hi")}
	ch1, err := agt.Run(context.Background(), inv1)
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	<-ch1 // drain one event
	if seen != "model-A" {
		t.Fatalf("expected model-A, got %s", seen)
	}

	// Switch to model-B and verify it is applied.
	agt.SetModel(mB)
	inv2 := &agent.Invocation{Message: model.NewUserMessage("hi again")}
	ch2, err := agt.Run(context.Background(), inv2)
	if err != nil {
		t.Fatalf("Run error after SetModel: %v", err)
	}
	<-ch2
	if seen != "model-B" {
		t.Fatalf("expected model-B after SetModel, got %s", seen)
	}
}

func TestLLMAgent_New_WithOutputSchema_InvalidCombos(t *testing.T) {
	// Output schema to trigger validation.
	schema := map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}

	t.Run("with tools", func(t *testing.T) {
		require.PanicsWithValue(t,
			"Invalid LLMAgent configuration: if output_schema is set, tools and toolSets must be empty",
			func() {
				_ = New("test",
					WithOutputSchema(schema),
					WithTools([]tool.Tool{dummyTool{}}),
				)
			},
		)
	})

	t.Run("with toolsets", func(t *testing.T) {
		require.PanicsWithValue(t,
			"Invalid LLMAgent configuration: if output_schema is set, tools and toolSets must be empty",
			func() {
				_ = New("test",
					WithOutputSchema(schema),
					WithToolSets([]tool.ToolSet{dummyToolSet{}}),
				)
			},
		)
	})

	t.Run("with knowledge", func(t *testing.T) {
		kb := &mockKnowledgeBase{documents: map[string]*document.Document{}}
		require.PanicsWithValue(t,
			"Invalid LLMAgent configuration: if output_schema is set, knowledge must be empty",
			func() {
				_ = New("test",
					WithOutputSchema(schema),
					WithKnowledge(kb),
				)
			},
		)
	})

	t.Run("with subagents", func(t *testing.T) {
		sub := New("sub")
		require.PanicsWithValue(t,
			"Invalid LLMAgent configuration: if output_schema is set, sub_agents must be empty to disable agent transfer",
			func() {
				_ = New("test",
					WithOutputSchema(schema),
					WithSubAgents([]agent.Agent{sub}),
				)
			},
		)
	})
}

// TestLLMAgent_InvocationContextAccess verifies that LLMAgent can access invocation
// from context when called through runner (after removing duplicate injection).
func TestLLMAgent_InvocationContextAccess(t *testing.T) {
	// Create a simple LLM agent.
	llmAgent := New("test-llm-agent")

	// Create invocation with context that contains invocation.
	invocation := &agent.Invocation{
		InvocationID: "test-invocation-123",
		AgentName:    "test-llm-agent",
		Message:      model.NewUserMessage("Test invocation context access"),
	}

	// Create context with invocation (simulating what runner does).
	ctx := agent.NewInvocationContext(context.Background(), invocation)

	// Run the agent.
	eventCh, err := llmAgent.Run(ctx, invocation)
	require.NoError(t, err)
	require.NotNil(t, eventCh)

	// Collect events.
	var events []*event.Event
	for evt := range eventCh {
		events = append(events, evt)
	}

	// Verify that the agent can access invocation from context.
	// This test ensures that even after removing the duplicate injection from LLMAgent,
	// it can still access invocation when called through runner.
	require.Greater(t, len(events), 0)

	// The agent should have been able to run successfully, which means
	// it could access the invocation from context for any internal operations.
	t.Logf("LLMAgent successfully executed with %d events, confirming invocation context access", len(events))
}
