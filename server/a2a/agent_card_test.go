//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2a

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	a2aprotocolserver "trpc.group/trpc-go/trpc-a2a-go/server"
	ia2a "trpc.group/trpc-go/trpc-agent-go/internal/a2a"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// mockCardTool is a minimal tool.Tool implementation for testing NewAgentCard.
type mockCardTool struct {
	name string
	desc string
}

func (m *mockCardTool) Declaration() *tool.Declaration {
	if m.name == "" {
		return nil
	}
	return &tool.Declaration{
		Name:        m.name,
		Description: m.desc,
	}
}

func TestNewAgentCard(t *testing.T) {
	card, err := NewAgentCard("tool-agent", "agent with tools", "localhost:9090", false)
	if err != nil {
		t.Fatalf("NewAgentCard() returned error: %v", err)
	}

	expected := a2aprotocolserver.AgentCard{
		Name:        "tool-agent",
		Description: "agent with tools",
		URL:         "http://localhost:9090",
		Capabilities: a2aprotocolserver.AgentCapabilities{
			Streaming: boolPtr(false),
			Extensions: []a2aprotocolserver.AgentExtension{
				{
					URI: ia2a.ExtensionTRPCA2AVersion,
					Params: map[string]any{
						"version": ia2a.InteractionVersion,
					},
				},
			},
		},
		Skills: []a2aprotocolserver.AgentSkill{
			{
				Name:        "tool-agent",
				Description: stringPtr("agent with tools"),
				InputModes:  []string{"text"},
				OutputModes: []string{"text"},
				Tags:        []string{"default"},
			},
		},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}

	if !compareAgentCards(card, expected) {
		t.Fatalf("NewAgentCard() = %+v, want %+v", card, expected)
	}
}

func TestNewAgentCard_Errors(t *testing.T) {
	_, err := NewAgentCard("", "desc", "localhost:8080", true)
	if err == nil || err.Error() != "agent name is required" {
		t.Fatalf("NewAgentCard(empty name) error = %v, want agent name is required", err)
	}

	_, err = NewAgentCard("test-agent", "desc", "", true)
	if err == nil || err.Error() != "host is required" {
		t.Fatalf("NewAgentCard(empty host) error = %v, want host is required", err)
	}
}

func TestNewAgentCard_WithCardTools(t *testing.T) {
	tools := []tool.Tool{
		&mockCardTool{name: "calculator", desc: "performs math operations"},
		&mockCardTool{name: "weather", desc: "fetches weather data"},
	}

	card, err := NewAgentCard(
		"smart-agent", "an agent with tools",
		"localhost:8080", true,
		WithCardTools(tools...),
	)
	if err != nil {
		t.Fatalf("NewAgentCard(WithCardTools) returned error: %v", err)
	}

	// Should have 3 skills: 1 default + 2 tools
	if len(card.Skills) != 3 {
		t.Fatalf("expected 3 skills, got %d: %+v", len(card.Skills), card.Skills)
	}

	// First skill is the default agent skill
	if card.Skills[0].Name != "smart-agent" {
		t.Errorf("first skill name = %q, want %q", card.Skills[0].Name, "smart-agent")
	}
	if card.Skills[0].Tags[0] != "default" {
		t.Errorf("first skill tag = %q, want %q", card.Skills[0].Tags[0], "default")
	}

	// Second skill is calculator tool
	if card.Skills[1].Name != "calculator" {
		t.Errorf("second skill name = %q, want %q", card.Skills[1].Name, "calculator")
	}
	if *card.Skills[1].Description != "performs math operations" {
		t.Errorf("second skill desc = %q, want %q", *card.Skills[1].Description, "performs math operations")
	}
	if card.Skills[1].Tags[0] != "tool" {
		t.Errorf("second skill tag = %q, want %q", card.Skills[1].Tags[0], "tool")
	}

	// Third skill is weather tool
	if card.Skills[2].Name != "weather" {
		t.Errorf("third skill name = %q, want %q", card.Skills[2].Name, "weather")
	}
}

func TestNewAgentCard_WithCardTools_NilAndEmptyDecl(t *testing.T) {
	tools := []tool.Tool{
		nil,                               // nil tool, should be skipped
		&mockCardTool{name: "", desc: ""}, // nil Declaration, should be skipped
		&mockCardTool{name: "valid", desc: "a valid tool"},
	}

	card, err := NewAgentCard(
		"agent", "desc", "localhost:8080", false,
		WithCardTools(tools...),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have 2 skills: 1 default + 1 valid tool
	if len(card.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d: %+v", len(card.Skills), card.Skills)
	}
	if card.Skills[1].Name != "valid" {
		t.Errorf("skill name = %q, want %q", card.Skills[1].Name, "valid")
	}
}

func TestNewAgentCard_WithCardTools_EmptySlice(t *testing.T) {
	card, err := NewAgentCard(
		"agent", "desc", "localhost:8080", false,
		WithCardTools(), // empty tools
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have only 1 default skill
	if len(card.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(card.Skills))
	}
	if card.Skills[0].Tags[0] != "default" {
		t.Errorf("skill tag = %q, want %q", card.Skills[0].Tags[0], "default")
	}
}

func TestNewAgentCardHandler_ReflectsUpdates(t *testing.T) {
	currentCard := a2aprotocolserver.AgentCard{
		Name:               "before",
		Description:        "before desc",
		URL:                "http://localhost:8080",
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}
	handler := NewAgentCardHandler(func() a2aprotocolserver.AgentCard {
		return currentCard
	})

	currentCard = a2aprotocolserver.AgentCard{
		Name:               "after",
		Description:        "after desc",
		URL:                "http://localhost:9090",
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
	}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusOK)
	}

	var gotCard a2aprotocolserver.AgentCard
	if err := json.Unmarshal(rec.Body.Bytes(), &gotCard); err != nil {
		t.Fatalf("failed to decode agent card: %v", err)
	}
	if gotCard.Name != "after" || gotCard.URL != "http://localhost:9090" {
		t.Fatalf("ServeHTTP() returned %+v, want updated card", gotCard)
	}
}

func TestNewAgentCardHandler_NilGetter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	rec := httptest.NewRecorder()

	NewAgentCardHandler(nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestNewAgentCardHandler_OptionsRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/.well-known/agent-card.json", nil)
	rec := httptest.NewRecorder()

	NewAgentCardHandler(func() a2aprotocolserver.AgentCard {
		return a2aprotocolserver.AgentCard{}
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != http.MethodGet {
		t.Fatalf("Access-Control-Allow-Methods = %q, want %q", got, http.MethodGet)
	}
}

func TestNewAgentCardHandler_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/.well-known/agent-card.json", nil)
	rec := httptest.NewRecorder()

	NewAgentCardHandler(func() a2aprotocolserver.AgentCard {
		return a2aprotocolserver.AgentCard{}
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if got := rec.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, want %q", got, http.MethodGet)
	}
}

func TestNewAgentCardHandler_MarshalError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json", nil)
	rec := httptest.NewRecorder()

	NewAgentCardHandler(func() a2aprotocolserver.AgentCard {
		return a2aprotocolserver.AgentCard{
			Name: "bad-card",
			Capabilities: a2aprotocolserver.AgentCapabilities{
				Extensions: []a2aprotocolserver.AgentExtension{
					{
						URI: "test://bad",
						Params: map[string]any{
							"bad": make(chan int),
						},
					},
				},
			},
		}
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("ServeHTTP() status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}
