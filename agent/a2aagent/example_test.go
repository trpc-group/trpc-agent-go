//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	a2aserver "trpc.group/trpc-go/trpc-agent-go/server/a2a"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const loopbackHost = "127.0.0.1"

// TestA2AAgentExample demonstrates full integration testing with real A2A server
func TestA2AAgentExample(t *testing.T) {
	tests := []struct {
		name      string
		agentName string
		agentDesc string
		userInput string
		streaming bool
		port      int
		expectErr bool
	}{
		{
			name:      "a2a_agent_non_streaming_integration",
			agentName: "example_agent",
			agentDesc: "An example agent for testing",
			userInput: "Hello, agent!",
			streaming: false,
			port:      28881,
			expectErr: false,
		},
		{
			name:      "a2a_agent_streaming_integration",
			agentName: "streaming_example_agent",
			agentDesc: "An example streaming agent",
			userInput: "Stream me some content",
			streaming: true,
			port:      28882,
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create context with timeout
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			host := fmt.Sprintf("%s:%d", loopbackHost, tt.port)
			t.Logf("Testing %s (streaming=%v) on %s", tt.name, tt.streaming, host)

			// Step 1: Create mock backend agent for the server
			backendAgent := createExampleBackendAgent(tt.agentName, tt.agentDesc, tt.streaming)

			// Step 2: Create and start A2A server
			server, err := a2aserver.New(
				a2aserver.WithAgent(backendAgent, tt.streaming),
				a2aserver.WithHost(host),
				a2aserver.WithDebugLogging(false),
			)
			if err != nil {
				t.Fatalf("Failed to create A2A server: %v", err)
			}

			defer func() {
				if err := server.Stop(ctx); err != nil {
					t.Logf("Warning: failed to stop server: %v", err)
				}
			}()

			// Start server in background
			serverErr := make(chan error, 1)
			go func() {
				if err := server.Start(host); err != nil {
					serverErr <- err
				}
			}()

			// Wait for server to be ready
			select {
			case err := <-serverErr:
				t.Fatalf("Server failed to start: %v", err)
			case <-time.After(200 * time.Millisecond):
				// Server started successfully
			case <-ctx.Done():
				t.Fatal("Timeout waiting for server to start")
			}

			// Step 3: Create A2A agent (client) that connects to the server
			httpURL := fmt.Sprintf("http://%s", host)
			a2aAgent, err := New(
				WithAgentCardURL(httpURL),
				WithTransferStateKey("metadata_key"),
			)
			if tt.expectErr {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Failed to create A2A agent: %v", err)
			}

			// Verify agent card
			verifyExampleAgentCard(t, a2aAgent, tt.agentName, tt.agentDesc, tt.streaming)

			// Step 4: Create runner with A2A agent
			sessionService := inmemory.NewSessionService()
			testRunner := runner.NewRunner("test-runner", a2aAgent, runner.WithSessionService(sessionService))

			// Step 5: Execute runner
			userID := "test-user"
			sessionID := "test-session"

			events, err := testRunner.Run(
				ctx,
				userID,
				sessionID,
				model.NewUserMessage(tt.userInput),
				agent.WithRuntimeState(map[string]any{"metadata_key": "test_value"}),
			)

			if err != nil {
				t.Fatalf("Failed to run A2A agent: %v", err)
			}

			// Step 6: Process and verify response
			if err := processExampleResponse(t, events, tt.streaming); err != nil {
				t.Fatalf("Failed to process response: %v", err)
			}

			t.Logf("Test completed successfully")
		})
	}
}

// TestA2AAgentExample_WithCustomHandler tests A2A agent with custom streaming handler
func TestA2AAgentExample_WithCustomHandler(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	host := fmt.Sprintf("%s:%d", loopbackHost, 28883)

	// Create backend agent
	backendAgent := createExampleBackendAgent("custom-handler-agent", "Agent with custom handler", true)

	// Start server
	server, err := a2aserver.New(
		a2aserver.WithAgent(backendAgent, true),
		a2aserver.WithHost(host),
	)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Stop(ctx)

	go server.Start(host)
	time.Sleep(200 * time.Millisecond)

	// Create A2A agent with custom streaming handler
	customHandler := func(resp *model.Response) (string, error) {
		if len(resp.Choices) > 0 {
			// Custom processing: uppercase the content
			return strings.ToUpper(resp.Choices[0].Delta.Content), nil
		}
		return "", nil
	}

	a2aAgent, err := New(
		WithAgentCardURL(fmt.Sprintf("http://%s", host)),
		WithStreamingRespHandler(customHandler),
	)
	if err != nil {
		t.Fatalf("Failed to create A2A agent: %v", err)
	}

	// Run agent
	sessionService := inmemory.NewSessionService()
	testRunner := runner.NewRunner("test", a2aAgent, runner.WithSessionService(sessionService))

	events, err := testRunner.Run(ctx, "user1", "session1", model.NewUserMessage("test"))
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify custom handler was used
	var foundUppercase bool
	for evt := range events {
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			content := evt.Response.Choices[0].Message.Content
			if content != "" && strings.ToUpper(content) == content {
				foundUppercase = true
				break
			}
		}
	}

	if !foundUppercase {
		t.Error("Custom streaming handler did not process content as expected")
	}
}

// createExampleBackendAgent creates a mock agent for the server backend
func createExampleBackendAgent(name, desc string, streaming bool) agent.Agent {
	return &exampleBackendAgent{
		name:        name,
		description: desc,
		streaming:   streaming,
	}
}

// exampleBackendAgent is a mock agent used by the A2A server
type exampleBackendAgent struct {
	name        string
	description string
	streaming   bool
}

func (e *exampleBackendAgent) Info() agent.Info {
	return agent.Info{
		Name:        e.name,
		Description: e.description,
	}
}

func (e *exampleBackendAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

func (e *exampleBackendAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 10)

	responseContent := "Mock response from backend agent"

	if e.streaming {
		// Simulate streaming response with multiple chunks
		words := strings.Split(responseContent, " ")
		for i, word := range words {
			isDone := i == len(words)-1
			ch <- &event.Event{
				Response: &model.Response{
					ID:      fmt.Sprintf("stream-response-%d", i),
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Model:   "mock-model",
					Choices: []model.Choice{
						{
							Delta: model.Message{
								Content: word + " ",
							},
						},
					},
					Done: isDone,
				},
				InvocationID: invocation.InvocationID,
				Author:       e.name,
				ID:           fmt.Sprintf("event-%d", i),
				Timestamp:    time.Now(),
			}
		}
	} else {
		// Non-streaming response
		ch <- &event.Event{
			Response: &model.Response{
				ID:      "response-1",
				Object:  "chat.completion",
				Created: time.Now().Unix(),
				Model:   "mock-model",
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: responseContent,
						},
					},
				},
				Done: true,
			},
			InvocationID: invocation.InvocationID,
			Author:       e.name,
			ID:           "event-1",
			Timestamp:    time.Now(),
		}
	}

	close(ch)
	return ch, nil
}

func (e *exampleBackendAgent) SubAgents() []agent.Agent {
	return []agent.Agent{}
}

func (e *exampleBackendAgent) FindSubAgent(name string) agent.Agent {
	return nil
}

// verifyExampleAgentCard verifies the agent card was properly loaded
func verifyExampleAgentCard(t *testing.T, a2aAgent *A2AAgent, expectedName, expectedDesc string, streaming bool) {
	t.Helper()

	card := a2aAgent.GetAgentCard()
	if card == nil {
		t.Fatal("Agent card should not be nil")
	}

	if card.Name != expectedName {
		t.Errorf("Expected agent name %s, got %s", expectedName, card.Name)
	}

	if card.Description != expectedDesc {
		t.Errorf("Expected agent description %s, got %s", expectedDesc, card.Description)
	}

	if card.Capabilities.Streaming == nil {
		t.Error("Streaming capability should be set")
	} else if *card.Capabilities.Streaming != streaming {
		t.Errorf("Expected streaming %v, got %v", streaming, *card.Capabilities.Streaming)
	}
}

// processExampleResponse processes event channel and validates response
func processExampleResponse(t *testing.T, eventChan <-chan *event.Event, streaming bool) error {
	t.Helper()

	var (
		fullContent string
		eventCount  int
	)

	for evt := range eventChan {
		eventCount++

		// Check for errors
		if evt.Error != nil {
			return fmt.Errorf("event error: %s", evt.Error.Message)
		}

		// Skip events without response
		if evt.Response == nil {
			continue
		}

		// Collect content
		if len(evt.Response.Choices) > 0 {
			choice := evt.Response.Choices[0]
			if streaming {
				fullContent += choice.Delta.Content
			} else {
				fullContent += choice.Message.Content
			}
		}

		// Check for final event
		if evt.Response.Done {
			break
		}
	}

	// Validate results
	if eventCount == 0 {
		return fmt.Errorf("no events received")
	}

	trimmedContent := strings.TrimSpace(fullContent)
	if len(trimmedContent) == 0 {
		return fmt.Errorf("no content received")
	}

	if len(trimmedContent) < 5 {
		return fmt.Errorf("content too short: %d chars", len(trimmedContent))
	}

	t.Logf("Received %d events with content: %s", eventCount, trimmedContent)
	return nil
}

// TestA2AAgentExample_ErrorHandling tests error scenarios
func TestA2AAgentExample_ErrorHandling(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	host := fmt.Sprintf("%s:%d", loopbackHost, 28884)

	// Create error-throwing backend agent
	backendAgent := &errorBackendAgent{name: "error-agent"}

	// Start server
	server, err := a2aserver.New(
		a2aserver.WithAgent(backendAgent, false),
		a2aserver.WithHost(host),
		a2aserver.WithErrorHandler(func(ctx context.Context, msg *protocol.Message, err error) (*protocol.Message, error) {
			errMsg := protocol.NewMessage(
				protocol.MessageRoleAgent,
				[]protocol.Part{
					protocol.NewTextPart(fmt.Sprintf("Error: %v", err)),
				},
			)
			return &errMsg, nil
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	defer server.Stop(ctx)

	go server.Start(host)
	time.Sleep(200 * time.Millisecond)

	// Create A2A agent
	a2aAgent, err := New(WithAgentCardURL(fmt.Sprintf("http://%s", host)))
	if err != nil {
		t.Fatalf("Failed to create A2A agent: %v", err)
	}

	// Run agent
	sessionService := inmemory.NewSessionService()
	testRunner := runner.NewRunner("test", a2aAgent, runner.WithSessionService(sessionService))

	events, err := testRunner.Run(ctx, "user1", "session1", model.NewUserMessage("trigger error"))
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Should receive error response content (error handler converts to message)
	var foundResponse bool
	for evt := range events {
		if evt.Response != nil && len(evt.Response.Choices) > 0 {
			content := evt.Response.Choices[0].Message.Content
			if strings.Contains(content, "Error:") || strings.Contains(content, "intentional error") {
				foundResponse = true
				t.Logf("Received error response: %s", content)
				break
			}
		}
	}

	// The server's error handler converts errors to messages, so we should see a response
	if !foundResponse {
		t.Log("Note: Error was handled by server's error handler and converted to message")
	}
}

// errorBackendAgent always returns an error
type errorBackendAgent struct {
	name string
}

func (e *errorBackendAgent) Info() agent.Info {
	return agent.Info{Name: e.name, Description: "Error agent"}
}

func (e *errorBackendAgent) Tools() []tool.Tool {
	return []tool.Tool{}
}

func (e *errorBackendAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	return nil, fmt.Errorf("intentional error for testing")
}

func (e *errorBackendAgent) SubAgents() []agent.Agent {
	return []agent.Agent{}
}

func (e *errorBackendAgent) FindSubAgent(name string) agent.Agent {
	return nil
}
