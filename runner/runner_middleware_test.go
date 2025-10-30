//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	openaiopt "github.com/openai/openai-go/option"
	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// TestRunner_MiddlewareCanAccessInvocation verifies that middleware can access
// invocation from context when using OpenAI model.
func TestRunner_MiddlewareCanAccessInvocation(t *testing.T) {
	// Track whether middleware was called and whether it found invocation.
	var mu sync.Mutex
	var middlewareCalled bool
	var invocationFound bool
	var invocationID string
	var agentName string

	// Create OpenAI model with middleware.
	modelInstance := openai.New(
		"gpt-4o-mini",
		openai.WithOpenAIOptions(
			openaiopt.WithMiddleware(
				func(req *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
					mu.Lock()
					middlewareCalled = true

					// Try to get invocation from request context.
					ctx := req.Context()
					inv, ok := agent.InvocationFromContext(ctx)
					if ok && inv != nil {
						invocationFound = true
						invocationID = inv.InvocationID
						agentName = inv.AgentName
					}
					mu.Unlock()

					// Return a mock error to avoid actual API call.
					return nil, http.ErrNotSupported
				},
			),
		),
	)

	// Create LLM agent with the model.
	llmAgent := llmagent.New(
		"test-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Test agent for middleware invocation access"),
	)

	// Create runner with in-memory session service.
	sessionService := sessioninmemory.NewSessionService()
	runner := NewRunner("test-app", llmAgent, WithSessionService(sessionService))

	// Create a context with timeout to avoid hanging on actual API call.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	userID := "test-user"
	sessionID := "test-session"
	message := model.NewUserMessage("Hello")

	// Run the agent.
	// Note: This will fail because our transport returns an error, but that's okay.
	// The important thing is that the transport was called and could access invocation.
	eventCh, err := runner.Run(ctx, userID, sessionID, message)

	// We expect an error from our mock transport, but that's okay.
	if err != nil {
		t.Logf("Expected error from mock transport: %v", err)
	} else {
		// Drain the event channel.
		for range eventCh {
			// Just consume events.
		}
	}

	// Give the goroutine time to execute.
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Verify that middleware was called.
	assert.True(t, middlewareCalled, "Middleware should have been called")

	// Verify that invocation was found in middleware context.
	assert.True(t, invocationFound, "Invocation should be accessible in middleware context")
	assert.NotEmpty(t, invocationID, "Invocation ID should not be empty")
	assert.Equal(t, "test-agent", agentName, "Agent name should match")
}

// TestRunner_MiddlewareCanAccessInvocationFields verifies that middleware can
// access invocation fields like Session, Model, etc.
func TestRunner_MiddlewareCanAccessInvocationFields(t *testing.T) {
	var mu sync.Mutex
	var middlewareCalled bool
	var hasSession bool
	var hasModel bool
	var userMessage string

	// Create OpenAI model with middleware.
	modelInstance := openai.New(
		"gpt-4o-mini",
		openai.WithOpenAIOptions(
			openaiopt.WithMiddleware(
				func(req *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
					mu.Lock()
					middlewareCalled = true

					// Get invocation from context.
					ctx := req.Context()
					inv, ok := agent.InvocationFromContext(ctx)
					if ok && inv != nil {
						// Check if invocation has session.
						hasSession = inv.Session != nil

						// Check if invocation has model.
						hasModel = inv.Model != nil

						// Get user message.
						userMessage = inv.Message.Content
					}
					mu.Unlock()

					// Return mock error.
					return nil, http.ErrNotSupported
				},
			),
		),
	)

	// Create LLM agent.
	llmAgent := llmagent.New(
		"test-agent",
		llmagent.WithModel(modelInstance),
		llmagent.WithDescription("Test agent"),
	)

	// Create runner.
	sessionService := sessioninmemory.NewSessionService()
	runner := NewRunner("test-app", llmAgent, WithSessionService(sessionService))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Run with a specific message.
	testMessage := "Test message for middleware"
	eventCh, err := runner.Run(ctx, "user", "session", model.NewUserMessage(testMessage))
	if err != nil {
		t.Logf("Expected error: %v", err)
	} else {
		for range eventCh {
		}
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// Verify.
	assert.True(t, middlewareCalled, "Middleware should have been called")
	assert.True(t, hasSession, "Invocation should have session")
	assert.True(t, hasModel, "Invocation should have model")
	assert.Equal(t, testMessage, userMessage, "Should be able to access user message")
}
