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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
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

// mockModelWithResponse is a mock model that returns a predefined response.
type mockModelWithResponse struct {
	response *model.Response
}

func (m *mockModelWithResponse) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, 1)
	if m.response != nil {
		ch <- m.response
	}
	close(ch)
	return ch, nil
}

func (m *mockModelWithResponse) Info() model.Info {
	return model.Info{Name: "mock-model"}
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

// Test that buildRequestProcessors wires PreserveSameBranch into
// ContentRequestProcessor correctly.
func TestBuildRequestProcessors_PreserveSameBranchWiring(t *testing.T) {
	// true case - ensure option is propagated to content processor.
	optsTrue := &Options{}
	WithPreserveSameBranch(true)(optsTrue)
	procs := buildRequestProcessors("tester", optsTrue)
	var crp *processor.ContentRequestProcessor
	for _, p := range procs {
		if v, ok := p.(*processor.ContentRequestProcessor); ok {
			crp = v
		}
	}
	require.NotNil(t, crp)
	require.True(t, crp.PreserveSameBranch)

	// false case - ensure disabled option is propagated.
	optsFalse := &Options{}
	WithPreserveSameBranch(false)(optsFalse)
	procs = buildRequestProcessors("tester", optsFalse)
	crp = nil
	for _, p := range procs {
		if v, ok := p.(*processor.ContentRequestProcessor); ok {
			crp = v
		}
	}
	require.NotNil(t, crp)
	require.False(t, crp.PreserveSameBranch)
}

// Test that WithPreserveSameBranch option sets the corresponding
// field in Options correctly.
func TestWithPreserveSameBranch_Option(t *testing.T) {
	opts := &Options{}
	WithPreserveSameBranch(true)(opts)
	require.True(t, opts.PreserveSameBranch)

	opts = &Options{}
	WithPreserveSameBranch(false)(opts)
	require.False(t, opts.PreserveSameBranch)
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
		Documents: []*knowledge.Result{
			{
				Document: bestMatch,
				Score:    bestScore,
			},
		},
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
				// Use a simple tool that implements tool.Tool interface.
				simpleTool := dummyTool{decl: &tool.Declaration{Name: "test"}}
				_ = New("test",
					WithOutputSchema(schema),
					WithTools([]tool.Tool{simpleTool}),
				)
			},
		)
	})

	t.Run("with toolsets", func(t *testing.T) {
		require.PanicsWithValue(t,
			"Invalid LLMAgent configuration: if output_schema is set, tools and toolSets must be empty",
			func() {
				toolset := dummyToolSet{name: "test-toolset"}
				_ = New("test",
					WithOutputSchema(schema),
					WithToolSets([]tool.ToolSet{toolset}),
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

// fakeInvocation creates an invocation carrying a runtime state include_contents flag.
func fakeInvocation(include string) *agent.Invocation {
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("u")),
	)
	st := map[string]any{graph.CfgKeyIncludeContents: include}
	inv.RunOptions = agent.RunOptions{RuntimeState: st}
	return inv
}

// Test that buildRequestProcessors honors include_contents from runtime state
// by constructing the content processor with the expected mode.
func TestBuildRequestProcessors_IncludeContentsHonored(t *testing.T) {
	opts := &Options{}

	// Case 1: none
	{
		inv := fakeInvocation("none")
		ctx := agent.NewInvocationContext(context.Background(), inv)
		if ctx == nil {
			t.Fatalf("context should not be nil")
		}

		procs := buildRequestProcessors("tester", opts)
		// Last processor is content processor created with include mode.
		// We cannot access internals directly; instead, simulate request processing:
		// When include=none and session is nil, invocation message should be appended.
		req := &model.Request{}
		for _, p := range procs {
			p.ProcessRequest(ctx, inv, req, nil)
		}
		// Expect at least the invocation message present (role=user, content="u").
		if len(req.Messages) == 0 {
			t.Fatalf("expected invocation message appended")
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != model.RoleUser || last.Content != "u" {
			t.Fatalf("unexpected last message: %+v", last)
		}
	}

	// Case 2: filtered
	{
		inv := fakeInvocation("filtered")
		ctx := agent.NewInvocationContext(context.Background(), inv)
		procs := buildRequestProcessors("tester", opts)
		req := &model.Request{}
		for _, p := range procs {
			p.ProcessRequest(ctx, inv, req, nil)
		}
		// With no session, behavior equals none: invocation message appended.
		if len(req.Messages) == 0 {
			t.Fatalf("expected invocation message appended (filtered)")
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != model.RoleUser || last.Content != "u" {
			t.Fatalf("unexpected last message (filtered): %+v", last)
		}
	}

	// Case 3: all (same expectation with empty session)
	{
		inv := fakeInvocation("all")
		ctx := agent.NewInvocationContext(context.Background(), inv)
		procs := buildRequestProcessors("tester", opts)
		req := &model.Request{}
		for _, p := range procs {
			p.ProcessRequest(ctx, inv, req, nil)
		}
		if len(req.Messages) == 0 {
			t.Fatalf("expected invocation message appended (all)")
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != model.RoleUser || last.Content != "u" {
			t.Fatalf("unexpected last message (all): %+v", last)
		}
	}

	// Case 4: invalid -> defaults to filtered (still works with empty session)
	{
		inv := fakeInvocation("invalid")
		ctx := agent.NewInvocationContext(context.Background(), inv)
		procs := buildRequestProcessors("tester", opts)
		req := &model.Request{}
		for _, p := range procs {
			p.ProcessRequest(ctx, inv, req, nil)
		}
		if len(req.Messages) == 0 {
			t.Fatalf("expected invocation message appended (invalid->filtered)")
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role != model.RoleUser || last.Content != "u" {
			t.Fatalf("unexpected last message (invalid->filtered): %+v", last)
		}
	}
}

// TestLLMAgent_OptionsWithCodeExecutor tests WithCodeExecutor option.
func TestLLMAgent_OptionsWithCodeExecutor(t *testing.T) {
	mockCE := &mockCodeExecutor{}
	agt := New("test", WithCodeExecutor(mockCE))
	require.Equal(t, mockCE, agt.CodeExecutor())
}

// TestLLMAgent_OptionsWithOutputKey tests WithOutputKey option.
func TestLLMAgent_OptionsWithOutputKey(t *testing.T) {
	agt := New("test", WithOutputKey("my_output"))
	require.Equal(t, "my_output", agt.outputKey)
}

// TestLLMAgent_OptionsWithInputSchema tests WithInputSchema option.
func TestLLMAgent_OptionsWithInputSchema(t *testing.T) {
	schema := map[string]any{"type": "object"}
	agt := New("test", WithInputSchema(schema))
	info := agt.Info()
	require.Equal(t, schema, info.InputSchema)
}

// TestLLMAgent_OptionsWithAddNameToInstruction tests WithAddNameToInstruction option.
func TestLLMAgent_OptionsWithAddNameToInstruction(t *testing.T) {
	opts := &Options{}
	WithAddNameToInstruction(true)(opts)
	require.True(t, opts.AddNameToInstruction)
}

// TestLLMAgent_OptionsWithDefaultTransferMessage tests WithDefaultTransferMessage option.
func TestLLMAgent_OptionsWithDefaultTransferMessage(t *testing.T) {
	opts := &Options{}
	WithDefaultTransferMessage("custom message")(opts)
	require.NotNil(t, opts.DefaultTransferMessage)
	require.Equal(t, "custom message", *opts.DefaultTransferMessage)
}

// TestLLMAgent_OptionsWithStructuredOutputJSON tests WithStructuredOutputJSON option.
func TestLLMAgent_OptionsWithStructuredOutputJSON(t *testing.T) {
	type MyStruct struct {
		Field1 string `json:"field1"`
		Field2 int    `json:"field2"`
	}
	opts := &Options{}
	WithStructuredOutputJSON(new(MyStruct), true, "test description")(opts)
	require.NotNil(t, opts.StructuredOutput)
	require.Equal(t, model.StructuredOutputJSONSchema, opts.StructuredOutput.Type)
	require.NotNil(t, opts.StructuredOutput.JSONSchema)
	require.Equal(t, "MyStruct", opts.StructuredOutput.JSONSchema.Name)
	require.True(t, opts.StructuredOutput.JSONSchema.Strict)
	require.Equal(t, "test description", opts.StructuredOutput.JSONSchema.Description)
}

// TestLLMAgent_OptionsWithAddCurrentTime tests WithAddCurrentTime option.
func TestLLMAgent_OptionsWithAddCurrentTime(t *testing.T) {
	opts := &Options{}
	WithAddCurrentTime(true)(opts)
	require.True(t, opts.AddCurrentTime)
}

// TestLLMAgent_OptionsWithTimezone tests WithTimezone option.
func TestLLMAgent_OptionsWithTimezone(t *testing.T) {
	opts := &Options{}
	WithTimezone("Asia/Shanghai")(opts)
	require.Equal(t, "Asia/Shanghai", opts.Timezone)
}

// TestLLMAgent_OptionsWithTimeFormat tests WithTimeFormat option.
func TestLLMAgent_OptionsWithTimeFormat(t *testing.T) {
	opts := &Options{}
	WithTimeFormat("2006-01-02 15:04:05")(opts)
	require.Equal(t, "2006-01-02 15:04:05", opts.TimeFormat)
}

// TestLLMAgent_OptionsWithAddContextPrefix tests WithAddContextPrefix option.
func TestLLMAgent_OptionsWithAddContextPrefix(t *testing.T) {
	opts := &Options{}
	WithAddContextPrefix(true)(opts)
	require.True(t, opts.AddContextPrefix)
}

// TestLLMAgent_OptionsWithKnowledgeFilter tests WithKnowledgeFilter option.
func TestLLMAgent_OptionsWithKnowledgeFilter(t *testing.T) {
	filter := map[string]any{"category": "tech"}
	opts := &Options{}
	WithKnowledgeFilter(filter)(opts)
	require.Equal(t, filter, opts.KnowledgeFilter)
}

// TestLLMAgent_OptionsWithKnowledgeAgenticFilterInfo tests WithKnowledgeAgenticFilterInfo option.
func TestLLMAgent_OptionsWithKnowledgeAgenticFilterInfo(t *testing.T) {
	filterInfo := map[string][]any{"tags": {"ai", "ml"}}
	opts := &Options{}
	WithKnowledgeAgenticFilterInfo(filterInfo)(opts)
	require.Equal(t, filterInfo, opts.AgenticFilterInfo)
}

// TestLLMAgent_OptionsWithEnableKnowledgeAgenticFilter tests WithEnableKnowledgeAgenticFilter option.
func TestLLMAgent_OptionsWithEnableKnowledgeAgenticFilter(t *testing.T) {
	opts := &Options{}
	WithEnableKnowledgeAgenticFilter(true)(opts)
	require.True(t, opts.EnableKnowledgeAgenticFilter)
}

// TestLLMAgent_OptionsWithEndInvocationAfterTransfer tests WithEndInvocationAfterTransfer option.
func TestLLMAgent_OptionsWithEndInvocationAfterTransfer(t *testing.T) {
	opts := &Options{}
	WithEndInvocationAfterTransfer(false)(opts)
	require.False(t, opts.EndInvocationAfterTransfer)
}

// TestLLMAgent_SetInstruction tests SetInstruction method.
func TestLLMAgent_SetInstruction(t *testing.T) {
	agt := New("test", WithInstruction("initial instruction"))
	require.Equal(t, "initial instruction", agt.getInstruction())

	agt.SetInstruction("updated instruction")
	require.Equal(t, "updated instruction", agt.getInstruction())
}

// TestLLMAgent_SetGlobalInstruction tests SetGlobalInstruction method.
func TestLLMAgent_SetGlobalInstruction(t *testing.T) {
	agt := New("test", WithGlobalInstruction("initial global"))
	require.Equal(t, "initial global", agt.getSystemPrompt())

	agt.SetGlobalInstruction("updated global")
	require.Equal(t, "updated global", agt.getSystemPrompt())
}

// TestHaveCustomResponseError tests the Error method of haveCustomResponseError.
func TestHaveCustomResponseError(t *testing.T) {
	err := &haveCustomResponseError{EventChan: make(<-chan *event.Event)}
	require.Equal(t, "custom response provided, returning early", err.Error())
}

// mockCodeExecutor is a mock implementation of CodeExecutor for testing.
type mockCodeExecutor struct{}

func (m *mockCodeExecutor) ExecuteCode(ctx context.Context, input codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{
		Output: "executed",
	}, nil
}

func (m *mockCodeExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{
		Start: "```",
		End:   "```",
	}
}

// TestWithModels tests the WithModels option.
func TestWithModels(t *testing.T) {
	model1 := newDummyModel()
	model2 := newDummyModel()
	model3 := newDummyModel()

	models := map[string]model.Model{
		"model1": model1,
		"model2": model2,
		"model3": model3,
	}

	agent := New("test-agent",
		WithModels(models),
		WithModel(model1),
	)

	require.NotNil(t, agent)
	require.NotNil(t, agent.models)
	require.Equal(t, 3, len(agent.models))
}

// TestSetModelByName tests switching models by name.
func TestSetModelByName(t *testing.T) {
	model1 := newDummyModel()
	model2 := newDummyModel()

	models := map[string]model.Model{
		"fast":  model1,
		"smart": model2,
	}

	agent := New("test-agent",
		WithModels(models),
		WithModel(model1),
	)

	// Initial model should be model1.
	require.Equal(t, model1, agent.model)

	// Switch to model2.
	err := agent.SetModelByName("smart")
	require.NoError(t, err)
	require.Equal(t, model2, agent.model)

	// Switch back to model1.
	err = agent.SetModelByName("fast")
	require.NoError(t, err)
	require.Equal(t, model1, agent.model)

	// Try to switch to non-existent model.
	err = agent.SetModelByName("non-existent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found in registered models")
}

// TestSetModelByName_NotRegistered tests error when model name not found.
func TestSetModelByName_NotRegistered(t *testing.T) {
	model1 := newDummyModel()

	agent := New("test-agent", WithModel(model1))

	// Try to switch to a model that was not registered.
	err := agent.SetModelByName("some-model")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found in registered models")
}

// TestWithModelsOnly tests using only WithModels without WithModel.
func TestWithModelsOnly(t *testing.T) {
	model1 := newDummyModel()
	model2 := newDummyModel()

	models := map[string]model.Model{
		"model1": model1,
		"model2": model2,
	}

	agent := New("test-agent", WithModels(models))

	require.NotNil(t, agent)
	require.NotNil(t, agent.model)
	require.NotNil(t, agent.models)
	require.Equal(t, 2, len(agent.models))
}

// TestWithModelAndModels_ModelNotInMap tests when WithModel is set but not
// in WithModels map.
func TestWithModelAndModels_ModelNotInMap(t *testing.T) {
	model1 := newDummyModel()
	model2 := newDummyModel()
	model3 := newDummyModel()

	models := map[string]model.Model{
		"model2": model2,
		"model3": model3,
	}

	agent := New("test-agent",
		WithModels(models),
		WithModel(model1),
	)

	require.NotNil(t, agent)
	require.Equal(t, model1, agent.model)
	require.Equal(t, 3, len(agent.models))

	// The model1 should be added with defaultModelName.
	defaultModel, ok := agent.models[defaultModelName]
	require.True(t, ok)
	require.Equal(t, model1, defaultModel)
}

// TestWithModelOnly tests using only WithModel without WithModels.
func TestWithModelOnly(t *testing.T) {
	model1 := newDummyModel()

	agent := New("test-agent", WithModel(model1))

	require.NotNil(t, agent)
	require.Equal(t, model1, agent.model)
	require.Equal(t, 1, len(agent.models))

	// The model should be registered with defaultModelName.
	defaultModel, ok := agent.models[defaultModelName]
	require.True(t, ok)
	require.Equal(t, model1, defaultModel)
}

// TestSetModelByName_Concurrent tests concurrent model switching.
func TestSetModelByName_Concurrent(t *testing.T) {
	model1 := newDummyModel()
	model2 := newDummyModel()

	models := map[string]model.Model{
		"model1": model1,
		"model2": model2,
	}

	agent := New("test-agent",
		WithModels(models),
		WithModel(model1),
	)

	const numGoroutines = 10
	const numIterations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numIterations; j++ {
				if j%2 == 0 {
					err := agent.SetModelByName("model1")
					require.NoError(t, err)
				} else {
					err := agent.SetModelByName("model2")
					require.NoError(t, err)
				}
			}
		}(i)
	}

	wg.Wait()

	// After all concurrent operations, the model should be one of the two.
	require.True(t, agent.model == model1 || agent.model == model2)
}

// TestInitializeModels_EmptyOptions tests initializeModels with no models.
func TestInitializeModels_EmptyOptions(t *testing.T) {
	opts := &Options{}
	initialModel, models := initializeModels(opts)

	require.Nil(t, initialModel)
	require.NotNil(t, models)
	require.Equal(t, 0, len(models))
}

// TestInitializeModels_OnlyWithModel tests initializeModels with only
// WithModel.
func TestInitializeModels_OnlyWithModel(t *testing.T) {
	model1 := newDummyModel()
	opts := &Options{Model: model1}

	initialModel, models := initializeModels(opts)

	require.Equal(t, model1, initialModel)
	require.Equal(t, 1, len(models))
	require.Equal(t, model1, models[defaultModelName])
}

// TestInitializeModels_OnlyWithModels tests initializeModels with only
// WithModels.
func TestInitializeModels_OnlyWithModels(t *testing.T) {
	model1 := newDummyModel()
	model2 := newDummyModel()

	opts := &Options{
		Models: map[string]model.Model{
			"model1": model1,
			"model2": model2,
		},
	}

	initialModel, models := initializeModels(opts)

	require.NotNil(t, initialModel)
	require.True(t, initialModel == model1 || initialModel == model2)
	require.Equal(t, 2, len(models))
}

// TestInitializeModels_BothWithModelAndModels tests initializeModels with
// both WithModel and WithModels.
func TestInitializeModels_BothWithModelAndModels(t *testing.T) {
	model1 := newDummyModel()
	model2 := newDummyModel()
	model3 := newDummyModel()

	opts := &Options{
		Model: model1,
		Models: map[string]model.Model{
			"model2": model2,
			"model3": model3,
		},
	}

	initialModel, models := initializeModels(opts)

	require.Equal(t, model1, initialModel)
	require.Equal(t, 3, len(models))
	require.Equal(t, model1, models[defaultModelName])
}

// TestInitializeModels_WithModelInModelsMap tests when WithModel is already
// in the WithModels map.
func TestInitializeModels_WithModelInModelsMap(t *testing.T) {
	model1 := newDummyModel()
	model2 := newDummyModel()

	opts := &Options{
		Model: model1,
		Models: map[string]model.Model{
			"model1": model1,
			"model2": model2,
		},
	}

	initialModel, models := initializeModels(opts)

	require.Equal(t, model1, initialModel)
	require.Equal(t, 2, len(models))
	_, hasDefault := models[defaultModelName]
	require.False(t, hasDefault)
}

// TestLLMAgent_RunWithModel tests per-request model switching using WithModel.
func TestLLMAgent_RunWithModel(t *testing.T) {
	// Create two mock models with different responses.
	defaultModel := &mockModelWithResponse{
		response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Response from default model",
				},
			}},
			Usage: &model.Usage{TotalTokens: 100},
		},
	}

	customModel := &mockModelWithResponse{
		response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Response from custom model",
				},
			}},
			Usage: &model.Usage{TotalTokens: 200},
		},
	}

	// Create agent with default model.
	llmAgent := New(
		"test-agent",
		WithModel(defaultModel),
	)

	// Test 1: Verify setupInvocation uses default model when no RunOptions.Model is set.
	inv1 := &agent.Invocation{
		InvocationID: "test-1",
		AgentName:    "test-agent",
		Message:      model.NewUserMessage("Test message 1"),
		RunOptions:   agent.RunOptions{},
	}

	llmAgent.setupInvocation(inv1)
	require.Equal(t, defaultModel, inv1.Model)

	// Test 2: Verify setupInvocation uses custom model when RunOptions.Model is set.
	inv2 := &agent.Invocation{
		InvocationID: "test-2",
		AgentName:    "test-agent",
		Message:      model.NewUserMessage("Test message 2"),
		RunOptions: agent.RunOptions{
			Model: customModel,
		},
	}

	llmAgent.setupInvocation(inv2)
	require.Equal(t, customModel, inv2.Model)

	// Verify that the agent's default model is unchanged.
	require.Equal(t, defaultModel, llmAgent.model)
}

// TestLLMAgent_RunWithModelName tests per-request model switching using WithModelName.
func TestLLMAgent_RunWithModelName(t *testing.T) {
	// Create multiple mock models.
	gpt4Model := &mockModelWithResponse{
		response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Response from GPT-4",
				},
			}},
			Usage: &model.Usage{TotalTokens: 150},
		},
	}

	gpt35Model := &mockModelWithResponse{
		response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Response from GPT-3.5",
				},
			}},
			Usage: &model.Usage{TotalTokens: 80},
		},
	}

	// Create agent with multiple models registered.
	llmAgent := New(
		"test-agent",
		WithModels(map[string]model.Model{
			"gpt-4":   gpt4Model,
			"gpt-3.5": gpt35Model,
		}),
	)

	// Test 1: Verify setupInvocation uses gpt-4 model when ModelName is "gpt-4".
	inv1 := &agent.Invocation{
		InvocationID: "test-1",
		AgentName:    "test-agent",
		Message:      model.NewUserMessage("Test message 1"),
		RunOptions: agent.RunOptions{
			ModelName: "gpt-4",
		},
	}

	llmAgent.setupInvocation(inv1)
	require.Equal(t, gpt4Model, inv1.Model)

	// Test 2: Verify setupInvocation uses gpt-3.5 model when ModelName is "gpt-3.5".
	inv2 := &agent.Invocation{
		InvocationID: "test-2",
		AgentName:    "test-agent",
		Message:      model.NewUserMessage("Test message 2"),
		RunOptions: agent.RunOptions{
			ModelName: "gpt-3.5",
		},
	}

	llmAgent.setupInvocation(inv2)
	require.Equal(t, gpt35Model, inv2.Model)
}

// TestLLMAgent_RunWithModelName_NotFound tests fallback when model name is not found.
func TestLLMAgent_RunWithModelName_NotFound(t *testing.T) {
	defaultModel := &mockModelWithResponse{
		response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Response from default model",
				},
			}},
			Usage: &model.Usage{TotalTokens: 100},
		},
	}

	// Create agent with a default model.
	llmAgent := New(
		"test-agent",
		WithModel(defaultModel),
	)

	// Verify setupInvocation falls back to default model when model name is not found.
	inv := &agent.Invocation{
		InvocationID: "test-1",
		AgentName:    "test-agent",
		Message:      model.NewUserMessage("Test message"),
		RunOptions: agent.RunOptions{
			ModelName: "non-existent-model",
		},
	}

	llmAgent.setupInvocation(inv)
	require.Equal(t, defaultModel, inv.Model)
}

// TestLLMAgent_RunWithModel_Priority tests that WithModel takes priority over WithModelName.
func TestLLMAgent_RunWithModel_Priority(t *testing.T) {
	modelFromWithModel := &mockModelWithResponse{
		response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Response from WithModel",
				},
			}},
			Usage: &model.Usage{TotalTokens: 300},
		},
	}

	namedModel := &mockModelWithResponse{
		response: &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Response from named model",
				},
			}},
			Usage: &model.Usage{TotalTokens: 250},
		},
	}

	// Create agent with named models.
	llmAgent := New(
		"test-agent",
		WithModels(map[string]model.Model{
			"named-model": namedModel,
		}),
	)

	// Verify setupInvocation prioritizes Model over ModelName.
	inv := &agent.Invocation{
		InvocationID: "test-1",
		AgentName:    "test-agent",
		Message:      model.NewUserMessage("Test message"),
		RunOptions: agent.RunOptions{
			Model:     modelFromWithModel,
			ModelName: "named-model",
		},
	}

	llmAgent.setupInvocation(inv)
	require.Equal(t, modelFromWithModel, inv.Model)
}

func TestLLMAgent_MessageFilterMode(t *testing.T) {
	options := &Options{
		messageBranchFilterMode: "prefix",
	}
	WithMessageTimelineFilterMode("all")(options)

	require.Equal(t, options.messageTimelineFilterMode, "all")
	require.Equal(t, options.messageBranchFilterMode, "prefix")

	options = &Options{
		messageBranchFilterMode:   "prefix",
		messageTimelineFilterMode: "all",
	}
	WithMessageTimelineFilterMode("request")(options)
	WithMessageBranchFilterMode("exact")(options)

	require.Equal(t, options.messageTimelineFilterMode, "request")
	require.Equal(t, options.messageBranchFilterMode, "exact")
}
