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
)

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
