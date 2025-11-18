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

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// mockModelWithUsage is a mock model that returns responses with token usage information.
type mockModelWithUsage struct {
	responses []*model.Response
	callCount int
}

func (m *mockModelWithUsage) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	ch := make(chan *model.Response, len(m.responses))
	for _, resp := range m.responses {
		ch <- resp
	}
	close(ch)
	m.callCount++
	return ch, nil
}

func (m *mockModelWithUsage) Info() model.Info {
	return model.Info{Name: "mock-model-with-usage"}
}

// TestLLMAgent_TokenUsageCounting tests that token usage is correctly counted and accumulated.
func TestLLMAgent_TokenUsageCounting(t *testing.T) {
	tests := []struct {
		name             string
		responses        []*model.Response
		expectedPrompt   int
		expectedComplete int
		expectedTotal    int
	}{
		{
			name: "single response with usage",
			responses: []*model.Response{
				{
					Choices: []model.Choice{{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "Test response",
						},
					}},
					Usage: &model.Usage{
						PromptTokens:     10,
						CompletionTokens: 20,
						TotalTokens:      30,
					},
					Done: true,
				},
			},
			expectedPrompt:   10,
			expectedComplete: 20,
			expectedTotal:    30,
		},
		{
			name: "streaming responses with incremental usage",
			responses: []*model.Response{
				{
					Choices: []model.Choice{{
						Delta: model.Message{
							Role:    model.RoleAssistant,
							Content: "First ",
						},
					}},
					Usage: &model.Usage{
						PromptTokens:     15,
						CompletionTokens: 5,
						TotalTokens:      20,
					},
					IsPartial: true,
				},
				{
					Choices: []model.Choice{{
						Delta: model.Message{
							Content: "part ",
						},
					}},
					Usage: &model.Usage{
						PromptTokens:     15,
						CompletionTokens: 10,
						TotalTokens:      25,
					},
					IsPartial: true,
				},
				{
					Choices: []model.Choice{{
						Delta: model.Message{
							Content: "of response",
						},
					}},
					Usage: &model.Usage{
						PromptTokens:     15,
						CompletionTokens: 15,
						TotalTokens:      30,
					},
					IsPartial: true,
				},
				{
					Choices: []model.Choice{{
						Delta: model.Message{
							Content: "First part of response",
						},
					}},
					Usage: &model.Usage{
						PromptTokens:     15,
						CompletionTokens: 15,
						TotalTokens:      30,
					},
					Done: true,
				},
			},
			expectedPrompt:   15,
			expectedComplete: 15,
			expectedTotal:    30,
		},
		{
			name: "streaming responses with no usage and incremental usage",
			responses: []*model.Response{
				{
					Choices: []model.Choice{{
						Delta: model.Message{
							Role:    model.RoleAssistant,
							Content: "First ",
						},
					}},
					Usage: &model.Usage{
						PromptTokens:     0,
						CompletionTokens: 0,
						TotalTokens:      0,
					},
					IsPartial: true,
				},
				{
					Choices: []model.Choice{{
						Delta: model.Message{
							Content: "part ",
						},
					}},
					Usage: &model.Usage{
						PromptTokens:     0,
						CompletionTokens: 0,
						TotalTokens:      0,
					},
					IsPartial: true,
				},
				{
					Choices: []model.Choice{{
						Delta: model.Message{
							Content: "of response",
						},
					}},
					Usage: &model.Usage{
						PromptTokens:     0,
						CompletionTokens: 0,
						TotalTokens:      0,
					},
					IsPartial: true,
				},
				{
					Choices: []model.Choice{{
						Delta: model.Message{
							Content: "First part of response",
						},
					}},
					Usage: &model.Usage{
						PromptTokens:     15,
						CompletionTokens: 15,
						TotalTokens:      30,
					},
					Done: true,
				},
			},
			expectedPrompt:   15,
			expectedComplete: 15,
			expectedTotal:    30,
		},
		{
			name: "response with zero usage",
			responses: []*model.Response{
				{
					Choices: []model.Choice{{
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: "Response with zero usage",
						},
					}},
					Usage: &model.Usage{
						PromptTokens:     0,
						CompletionTokens: 0,
						TotalTokens:      0,
					},
					Done: true,
				},
			},
			expectedPrompt:   0,
			expectedComplete: 0,
			expectedTotal:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockModel := &mockModelWithUsage{
				responses: tt.responses,
			}

			// Create agent with mock model
			agt := New("test-agent", WithModel(mockModel))

			// Create invocation
			inv := agent.NewInvocation(
				agent.WithInvocationID("test-invocation"),
				agent.WithInvocationMessage(model.NewUserMessage("Test message")),
			)
			inv.AgentName = "test-agent"
			inv.Session = &session.Session{ID: "test-session"}

			// Run the agent
			events, err := agt.Run(context.Background(), inv)
			require.NoError(t, err)
			require.NotNil(t, events)

			// Collect all events and check the final usage
			var finalEvent *event.Event
			for evt := range events {
				if evt.Response != nil && !evt.Response.IsPartial {
					finalEvent = evt
				}
			}

			// Verify token usage in final event
			require.NotNil(t, finalEvent, "expected a final event")
			require.NotNil(t, finalEvent.Response, "expected response in final event")
			require.NotNil(t, finalEvent.Response.Usage, "expected usage information")
			require.Equal(t, tt.expectedPrompt, finalEvent.Response.Usage.PromptTokens)
			require.Equal(t, tt.expectedComplete, finalEvent.Response.Usage.CompletionTokens)
			require.Equal(t, tt.expectedTotal, finalEvent.Response.Usage.TotalTokens)
		})
	}
}

// TestLLMAgent_TokenUsageMultipleInvocation tests that token usage correctly across multiple invocations.
func TestLLMAgent_TokenUsageMultipleInvocation(t *testing.T) {
	mockModel := &mockModelWithUsage{
		responses: []*model.Response{
			{
				Choices: []model.Choice{{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: "First response",
					},
				}},
				Usage: &model.Usage{
					PromptTokens:     10,
					CompletionTokens: 5,
					TotalTokens:      15,
				},
				Done: true,
			},
		},
	}

	agt := New("test-agent", WithModel(mockModel))

	// First invocation
	inv1 := agent.NewInvocation(
		agent.WithInvocationID("test-invocation-1"),
		agent.WithInvocationMessage(model.NewUserMessage("First message")),
	)
	inv1.AgentName = "test-agent"
	inv1.Session = &session.Session{ID: "test-session"}

	events1, err := agt.Run(context.Background(), inv1)
	require.NoError(t, err)

	var usage1 *model.Usage
	for evt := range events1 {
		if evt.Response != nil && evt.Response.Usage != nil {
			usage1 = evt.Response.Usage
		}
	}

	require.NotNil(t, usage1)
	require.Equal(t, 10, usage1.PromptTokens)
	require.Equal(t, 5, usage1.CompletionTokens)
	require.Equal(t, 15, usage1.TotalTokens)

	// Second invocation with different usage
	mockModel.responses = []*model.Response{
		{
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Second response",
				},
			}},
			Usage: &model.Usage{
				PromptTokens:     20,
				CompletionTokens: 10,
				TotalTokens:      30,
			},
			Done: true,
		},
	}

	inv2 := agent.NewInvocation(
		agent.WithInvocationID("test-invocation-2"),
		agent.WithInvocationMessage(model.NewUserMessage("Second message")),
	)
	inv2.AgentName = "test-agent"
	inv2.Session = &session.Session{ID: "test-session"}

	events2, err := agt.Run(context.Background(), inv2)
	require.NoError(t, err)

	var usage2 *model.Usage
	for evt := range events2 {
		if evt.Response != nil && evt.Response.Usage != nil {
			usage2 = evt.Response.Usage
		}
	}

	require.NotNil(t, usage2)
	require.Equal(t, 20, usage2.PromptTokens)
	require.Equal(t, 10, usage2.CompletionTokens)
	require.Equal(t, 30, usage2.TotalTokens)

	// Note: Each invocation has its own usage, they don't accumulate across invocations
	// This is expected behavior - each invocation tracks its own usage
}
