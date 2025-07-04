package llmagent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge"
	"trpc.group/trpc-go/trpc-agent-go/core/knowledge/document"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

func newDummyModel() model.Model {
	return openai.New("dummy-model", openai.Options{})
}

func TestLLMAgent_InfoAndTools(t *testing.T) {
	agt := New("test-agent",
		WithDescription("desc"),
		WithModel(newDummyModel()),
		WithTools([]tool.Tool{}),
	)
	info := agt.Info()
	if info.Name != "test-agent" || info.Description != "desc" {
		t.Errorf("unexpected agent info: %+v", info)
	}
	if len(agt.Tools()) != 0 {
		t.Errorf("expected no tools")
	}
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

func TestLLMAgent_Run_BeforeAgentShortCircuit(t *testing.T) {
	// BeforeAgentCallback returns a custom response, should short-circuit.
	agentCallbacks := agent.NewAgentCallbacks()
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
	agentCallbacks := agent.NewAgentCallbacks()
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
	agentCallbacks := agent.NewAgentCallbacks()
	agentCallbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
		return &model.Response{
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleAssistant, Content: "after-cb"},
			}},
		}, nil
	})
	agt := New("test", WithModel(newDummyModel()), WithAgentCallbacks(agentCallbacks))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
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
	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
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

func TestLLMAgent_Run_AfterAgentCallbackError(t *testing.T) {
	agentCallbacks := agent.NewAgentCallbacks()
	agentCallbacks.RegisterAfterAgent(func(ctx context.Context, inv *agent.Invocation, runErr error) (*model.Response, error) {
		return nil, errors.New("after error")
	})
	agt := New("test", WithModel(newDummyModel()), WithAgentCallbacks(agentCallbacks))
	inv := &agent.Invocation{Message: model.NewUserMessage("hi")}
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

func (m *mockKnowledgeBase) AddDocument(ctx context.Context, doc *document.Document) error {
	m.documents[doc.ID] = doc
	return nil
}

func (m *mockKnowledgeBase) Search(ctx context.Context, query string) (*knowledge.SearchResult, error) {
	// Simple keyword-based search for testing.
	query = strings.ToLower(query)

	var bestMatch *document.Document
	var bestScore float64

	for _, doc := range m.documents {
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

	return &knowledge.SearchResult{
		Document: bestMatch,
		Score:    bestScore,
		Text:     bestMatch.Content,
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
