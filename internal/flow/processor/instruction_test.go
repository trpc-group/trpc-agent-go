//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	testInstructionContent   = "Be helpful and concise"
	testSystemPromptContent  = "You are a helpful assistant"
	testAgentName            = "test-agent"
	testInvocationID         = "test-123"
	testDynamicInstruction   = "dynamic instruction"
	testDynamicSystemPrompt  = "dynamic system prompt"
	testResolvedInstruction  = "resolved instruction"
	testResolvedSystemPrompt = "resolved system prompt"
	testRunInstruction       = "run instruction override"
	testRunSystemPrompt      = "run system prompt override"
	jsonSchemaTitle          = "test schema"
)

func TestInstructionProc_Request(t *testing.T) {
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
			name:         "adds both instruction and system prompt as one message",
			instruction:  "Be concise",
			systemPrompt: "You are helpful",
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
			name:         "doesn't duplicate instruction when already exists",
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
		{
			name:         "appends instruction to existing system message",
			instruction:  "Be concise",
			systemPrompt: "",
			request: &model.Request{
				Messages: []model.Message{
					model.NewSystemMessage("You are helpful"),
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
			if tt.instruction != "" && tt.wantMessages > 0 {
				found := false
				for _, msg := range tt.request.Messages {
					if msg.Role == model.RoleSystem && strings.Contains(msg.Content, tt.instruction) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ProcessRequest() instruction content not found in system messages")
				}
			}

			// Check if system prompt was added correctly
			if tt.systemPrompt != "" && tt.wantMessages > 0 {
				found := false
				for _, msg := range tt.request.Messages {
					if msg.Role == model.RoleSystem && strings.Contains(msg.Content, tt.systemPrompt) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("ProcessRequest() system prompt content not found in system messages")
				}
			}
		})
	}
}

func TestFindSystemMessageIndex(t *testing.T) {
	tests := []struct {
		name     string
		messages []model.Message
		want     int
	}{
		{
			name:     "empty messages",
			messages: []model.Message{},
			want:     -1,
		},
		{
			name: "no system message",
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
			},
			want: -1,
		},
		{
			name: "has system message at start",
			messages: []model.Message{
				model.NewSystemMessage("System prompt"),
				{Role: model.RoleUser, Content: "Hello"},
			},
			want: 0,
		},
		{
			name: "has system message in middle",
			messages: []model.Message{
				{Role: model.RoleUser, Content: "Hello"},
				model.NewSystemMessage("System prompt"),
				{Role: model.RoleAssistant, Content: "Hi"},
			},
			want: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := findSystemMessageIndex(tt.messages); got != tt.want {
				t.Errorf("findSystemMessageIndex() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInstructionProcessor_DynamicGetters(t *testing.T) {
	ctx := context.Background()
	req := &model.Request{
		Messages: []model.Message{},
	}
	inv := &agent.Invocation{
		AgentName:    testAgentName,
		InvocationID: testInvocationID,
	}
	eventCh := make(chan *event.Event, 1)

	var instructionCalls, systemPromptCalls int
	processor := NewInstructionRequestProcessor(
		testInstructionContent,
		testSystemPromptContent,
		WithInstructionGetter(func() string {
			instructionCalls++
			return testDynamicInstruction
		}),
		WithSystemPromptGetter(func() string {
			systemPromptCalls++
			return testDynamicSystemPrompt
		}),
	)

	processor.ProcessRequest(ctx, inv, req, eventCh)

	require.Equal(t, 1, instructionCalls)
	require.Equal(t, 1, systemPromptCalls)
	if len(req.Messages) == 0 {
		t.Fatalf("expected system message added")
	}
	sysMsg := req.Messages[0]
	if sysMsg.Role != model.RoleSystem {
		t.Fatalf("expected system role, got %s", sysMsg.Role)
	}
	if !strings.Contains(sysMsg.Content, testDynamicInstruction) {
		t.Fatalf("expected dynamic instruction in content")
	}
	if !strings.Contains(sysMsg.Content, testDynamicSystemPrompt) {
		t.Fatalf("expected dynamic system prompt in content")
	}
}

func TestInstructionProcessor_DynamicResolvers(t *testing.T) {
	ctx := context.Background()
	req := &model.Request{
		Messages: []model.Message{},
	}
	inv := &agent.Invocation{
		AgentName:    testAgentName,
		InvocationID: testInvocationID,
	}
	eventCh := make(chan *event.Event, 1)

	var instructionCalls, systemPromptCalls int
	var getterInstructionCalls, getterSystemPromptCalls int
	processor := NewInstructionRequestProcessor(
		testInstructionContent,
		testSystemPromptContent,
		WithInstructionGetter(func() string {
			getterInstructionCalls++
			return testDynamicInstruction
		}),
		WithSystemPromptGetter(func() string {
			getterSystemPromptCalls++
			return testDynamicSystemPrompt
		}),
		WithInstructionResolver(func(inv *agent.Invocation) string {
			require.Equal(t, testInvocationID, inv.InvocationID)
			instructionCalls++
			return testResolvedInstruction
		}),
		WithSystemPromptResolver(func(inv *agent.Invocation) string {
			require.Equal(t, testInvocationID, inv.InvocationID)
			systemPromptCalls++
			return testResolvedSystemPrompt
		}),
	)

	processor.ProcessRequest(ctx, inv, req, eventCh)

	require.Equal(t, 1, instructionCalls)
	require.Equal(t, 1, systemPromptCalls)
	require.Equal(t, 0, getterInstructionCalls)
	require.Equal(t, 0, getterSystemPromptCalls)
	require.NotEmpty(t, req.Messages)
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	require.Contains(t, req.Messages[0].Content, testResolvedInstruction)
	require.Contains(t, req.Messages[0].Content, testResolvedSystemPrompt)
	require.NotContains(t, req.Messages[0].Content, testDynamicInstruction)
	require.NotContains(t, req.Messages[0].Content, testDynamicSystemPrompt)
}

func TestInstructionProcessor_RunOptionsOverride(t *testing.T) {
	ctx := context.Background()
	req := &model.Request{
		Messages: []model.Message{},
	}
	inv := &agent.Invocation{
		AgentName:    testAgentName,
		InvocationID: testInvocationID,
		RunOptions: agent.RunOptions{
			Instruction:       testRunInstruction,
			GlobalInstruction: testRunSystemPrompt,
		},
	}
	eventCh := make(chan *event.Event, 1)

	processor := NewInstructionRequestProcessor(
		testInstructionContent,
		testSystemPromptContent,
		WithInstructionGetter(func() string {
			return testDynamicInstruction
		}),
		WithSystemPromptGetter(func() string {
			return testDynamicSystemPrompt
		}),
		WithInstructionResolver(func(*agent.Invocation) string {
			return testResolvedInstruction
		}),
		WithSystemPromptResolver(func(*agent.Invocation) string {
			return testResolvedSystemPrompt
		}),
	)

	processor.ProcessRequest(ctx, inv, req, eventCh)

	require.NotEmpty(t, req.Messages)
	sysMsg := req.Messages[0]
	require.Equal(t, model.RoleSystem, sysMsg.Role)
	require.Contains(t, sysMsg.Content, testRunInstruction)
	require.Contains(t, sysMsg.Content, testRunSystemPrompt)
	require.NotContains(t, sysMsg.Content, testResolvedInstruction)
	require.NotContains(t, sysMsg.Content, testResolvedSystemPrompt)
	require.NotContains(t, sysMsg.Content, testDynamicInstruction)
	require.NotContains(t, sysMsg.Content, testDynamicSystemPrompt)
}

func TestInstructionProcessor_ProcessRequest_NilRequest(t *testing.T) {
	ctx := context.Background()
	inv := &agent.Invocation{
		AgentName:    testAgentName,
		InvocationID: testInvocationID,
	}
	eventCh := make(chan *event.Event, 1)

	processor := NewInstructionRequestProcessor(
		testInstructionContent,
		testSystemPromptContent,
	)

	processor.ProcessRequest(ctx, inv, nil, eventCh)

	require.Equal(t, 0, len(eventCh))
}

func TestInstructionProcessor_SendPreprocessingEvent(t *testing.T) {
	ctx := context.Background()
	inv := &agent.Invocation{
		AgentName:    testAgentName,
		InvocationID: testInvocationID,
	}
	eventCh := make(chan *event.Event, 1)

	processor := NewInstructionRequestProcessor(
		testInstructionContent,
		testSystemPromptContent,
	)

	processor.sendPreprocessingEvent(ctx, inv, eventCh)

	select {
	case evt := <-eventCh:
		require.NotNil(t, evt)
		require.Equal(t, inv.InvocationID, evt.InvocationID)
		require.Equal(t, inv.AgentName, evt.Author)
		require.Equal(
			t,
			model.ObjectTypePreprocessingInstruction,
			evt.Object,
		)
	default:
		t.Fatalf("expected preprocessing event emitted")
	}
}

func TestInstructionProcessor_SendPreprocessingEvent_ContextCanceled(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	inv := &agent.Invocation{
		AgentName:    testAgentName,
		InvocationID: testInvocationID,
	}
	eventCh := make(chan *event.Event, 1)

	processor := NewInstructionRequestProcessor(
		testInstructionContent,
		testSystemPromptContent,
	)

	processor.sendPreprocessingEvent(ctx, inv, eventCh)

	require.Equal(t, 0, len(eventCh))
}

func TestInstructionProcessor_GenerateJSONInstructions(t *testing.T) {
	processor := NewInstructionRequestProcessor(
		testInstructionContent,
		testSystemPromptContent,
	)

	schema := map[string]any{
		"title": jsonSchemaTitle,
		"type":  "object",
		"properties": map[string]any{
			"field": map[string]any{
				"type": "string",
			},
		},
	}

	instruction := processor.generateJSONInstructions(schema)

	require.NotEmpty(t, instruction)
	require.Contains(t, instruction, jsonSchemaTitle)
	require.Contains(t, instruction, "IMPORTANT: Return ONLY a JSON object")
}
