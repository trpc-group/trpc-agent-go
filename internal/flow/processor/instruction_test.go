package processor

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

func TestInstructionRequestProcessor_ProcessRequest(t *testing.T) {
	tests := []struct {
		name         string
		instruction  string
		systemPrompt string
		request      *model.Request
		invocation   *agent.Invocation
		wantMessages int
	}{
		{
			name:         "adds instruction message",
			instruction:  "Be helpful and concise",
			systemPrompt: "",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 1,
		},
		{
			name:         "adds system prompt message",
			instruction:  "",
			systemPrompt: "You are a helpful assistant",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 1,
		},
		{
			name:         "adds both instruction and system prompt",
			instruction:  "Be concise",
			systemPrompt: "You are helpful",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 2,
		},
		{
			name:         "no instruction or system prompt provided",
			instruction:  "",
			systemPrompt: "",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 0,
		},
		{
			name:         "doesn't duplicate instruction",
			instruction:  "Be helpful",
			systemPrompt: "",
			request: &model.Request{
				Messages: []model.Message{
					model.NewSystemMessage("Be helpful"),
				},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
			},
			wantMessages: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := NewInstructionRequestProcessor(tt.instruction, tt.systemPrompt)
			eventCh := make(chan *event.Event, 10)
			ctx := context.Background()

			processor.ProcessRequest(ctx, tt.invocation, tt.request, eventCh)

			if len(tt.request.Messages) != tt.wantMessages {
				t.Errorf("ProcessRequest() got %d messages, want %d", len(tt.request.Messages), tt.wantMessages)
			}

			// Check if instruction was added correctly
			if tt.instruction != "" {
				found := false
				for _, msg := range tt.request.Messages {
					if msg.Role == model.RoleSystem && msg.Content == tt.instruction {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ProcessRequest() instruction message not found in messages")
				}
			}

			// Check if system prompt was added correctly
			if tt.systemPrompt != "" {
				found := false
				for _, msg := range tt.request.Messages {
					if msg.Role == model.RoleSystem && msg.Content == tt.systemPrompt {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ProcessRequest() system prompt message not found in messages")
				}
			}
		})
	}
}

func TestInstructionRequestProcessor_hasInstructionSystemMessage(t *testing.T) {
	tests := []struct {
		name     string
		messages []model.Message
		content  string
		want     bool
	}{
		{
			name:     "empty messages",
			messages: []model.Message{},
			content:  "Be helpful",
			want:     false,
		},
		{
			name: "no matching content",
			messages: []model.Message{
				model.NewSystemMessage("Different content"),
			},
			content: "Be helpful",
			want:    false,
		},
		{
			name: "has matching content",
			messages: []model.Message{
				model.NewSystemMessage("Be helpful"),
			},
			content: "Be helpful",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasInstructionSystemMessage(tt.messages, tt.content); got != tt.want {
				t.Errorf("hasInstructionSystemMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}
