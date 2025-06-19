package processor

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/event"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

func TestContentRequestProcessor_ProcessRequest(t *testing.T) {
	tests := []struct {
		name           string
		request        *model.Request
		invocation     *agent.Invocation
		wantMessages   int
		wantLastMsg    string
		wantLastRole   model.Role
	}{
		{
			name: "adds initial message from invocation",
			request: &model.Request{
				Messages: []model.Message{},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
				Message:      model.NewUserMessage("Hello world"),
			},
			wantMessages: 1,
			wantLastMsg:  "Hello world",
			wantLastRole: model.RoleUser,
		},
		{
			name: "appends user message to existing messages",
			request: &model.Request{
				Messages: []model.Message{
					model.NewSystemMessage("You are helpful"),
				},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
				Message:      model.NewUserMessage("What is AI?"),
			},
			wantMessages: 2,
			wantLastMsg:  "What is AI?",
			wantLastRole: model.RoleUser,
		},
		{
			name: "no message in invocation",
			request: &model.Request{
				Messages: []model.Message{
					model.NewSystemMessage("You are helpful"),
				},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
				Message:      model.Message{}, // Empty message
			},
			wantMessages: 1,
			wantLastMsg:  "You are helpful",
			wantLastRole: model.RoleSystem,
		},
		{
			name: "non-user message with existing messages",
			request: &model.Request{
				Messages: []model.Message{
					model.NewSystemMessage("You are helpful"),
				},
			},
			invocation: &agent.Invocation{
				AgentName:    "test-agent",
				InvocationID: "test-123",
				Message:      model.NewAssistantMessage("I understand"),
			},
			wantMessages: 1, // Should not add non-user message when messages exist
			wantLastMsg:  "You are helpful",
			wantLastRole: model.RoleSystem,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := NewContentRequestProcessor()
			eventCh := make(chan *event.Event, 10)
			ctx := context.Background()

			processor.ProcessRequest(ctx, tt.invocation, tt.request, eventCh)

			if len(tt.request.Messages) != tt.wantMessages {
				t.Errorf("ProcessRequest() got %d messages, want %d", len(tt.request.Messages), tt.wantMessages)
			}

			if tt.wantMessages > 0 {
				lastMsg := tt.request.Messages[len(tt.request.Messages)-1]
				if lastMsg.Content != tt.wantLastMsg {
					t.Errorf("ProcessRequest() last message content = %v, want %v", lastMsg.Content, tt.wantLastMsg)
				}
				if lastMsg.Role != tt.wantLastRole {
					t.Errorf("ProcessRequest() last message role = %v, want %v", lastMsg.Role, tt.wantLastRole)
				}
			}

			// Verify that an event was sent.
			select {
			case evt := <-eventCh:
				if evt.Object != "preprocessing.content" {
					t.Errorf("ProcessRequest() got event object %s, want preprocessing.content", evt.Object)
				}
			default:
				t.Error("ProcessRequest() expected an event to be sent")
			}
		})
	}
} 