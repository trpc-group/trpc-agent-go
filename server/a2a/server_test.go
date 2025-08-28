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
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
	a2a "trpc.group/trpc-go/trpc-a2a-go/server"
	"trpc.group/trpc-go/trpc-a2a-go/taskmanager"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Mock implementations for testing
type mockAgent struct {
	name        string
	description string
	tools       []tool.Tool
	subAgents   []agent.Agent
}

func (m *mockAgent) Info() agent.Info {
	return agent.Info{
		Name:        m.name,
		Description: m.description,
	}
}

func (m *mockAgent) Tools() []tool.Tool {
	return m.tools
}

func (m *mockAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	ch <- &event.Event{
		Response: &model.Response{
			Choices: []model.Choice{
				{
					Message: model.Message{
						Content: "mock response",
					},
				},
			},
		},
	}
	close(ch)
	return ch, nil
}

func (m *mockAgent) SubAgents() []agent.Agent {
	return m.subAgents
}

func (m *mockAgent) FindSubAgent(name string) agent.Agent {
	for _, subAgent := range m.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}

type mockTool struct {
	name        string
	description string
}

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: m.description,
	}
}

func (m *mockTool) Execute(ctx context.Context, input string) (string, error) {
	return "mock tool result", nil
}

type mockSessionService struct{}

func (m *mockSessionService) CreateSession(ctx context.Context, key session.Key, state session.StateMap, options ...session.Option) (*session.Session, error) {
	return &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     state,
		Events:    []event.Event{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (m *mockSessionService) GetSession(ctx context.Context, key session.Key, options ...session.Option) (*session.Session, error) {
	return &session.Session{
		ID:        key.SessionID,
		AppName:   key.AppName,
		UserID:    key.UserID,
		State:     session.StateMap{},
		Events:    []event.Event{},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (m *mockSessionService) ListSessions(ctx context.Context, userKey session.UserKey, options ...session.Option) ([]*session.Session, error) {
	return []*session.Session{}, nil
}

func (m *mockSessionService) DeleteSession(ctx context.Context, key session.Key, options ...session.Option) error {
	return nil
}

func (m *mockSessionService) UpdateAppState(ctx context.Context, appName string, state session.StateMap) error {
	return nil
}

func (m *mockSessionService) DeleteAppState(ctx context.Context, appName string, key string) error {
	return nil
}

func (m *mockSessionService) ListAppStates(ctx context.Context, appName string) (session.StateMap, error) {
	return session.StateMap{}, nil
}

func (m *mockSessionService) UpdateUserState(ctx context.Context, userKey session.UserKey, state session.StateMap) error {
	return nil
}

func (m *mockSessionService) ListUserStates(ctx context.Context, userKey session.UserKey) (session.StateMap, error) {
	return session.StateMap{}, nil
}

func (m *mockSessionService) DeleteUserState(ctx context.Context, userKey session.UserKey, key string) error {
	return nil
}

func (m *mockSessionService) AppendEvent(ctx context.Context, session *session.Session, event *event.Event, options ...session.Option) error {
	return nil
}

func (m *mockSessionService) Close() error {
	return nil
}

type mockA2AToAgentConverter struct{}

func (m *mockA2AToAgentConverter) ConvertToAgentMessage(ctx context.Context, message protocol.Message) (*model.Message, error) {
	return &model.Message{
		Role:    model.RoleUser,
		Content: "converted message",
	}, nil
}

type mockEventToA2AConverter struct{}

func (m *mockEventToA2AConverter) ConvertToA2AMessage(ctx context.Context, event *event.Event) (*protocol.Message, error) {
	return &protocol.Message{
		Role:  protocol.MessageRoleAgent,
		Parts: []protocol.Part{&protocol.TextPart{Text: "converted event"}},
	}, nil
}

func (m *mockEventToA2AConverter) ConvertStreamingToA2AMessage(ctx context.Context, event *event.Event) (*protocol.Message, error) {
	return &protocol.Message{
		Role:  protocol.MessageRoleAgent,
		Parts: []protocol.Part{&protocol.TextPart{Text: "streaming event"}},
	}, nil
}

type mockTaskManager struct{}

func (m *mockTaskManager) ProcessMessage(ctx context.Context, message protocol.Message, options taskmanager.ProcessOptions, handler taskmanager.TaskHandler) (*taskmanager.MessageProcessingResult, error) {
	return &taskmanager.MessageProcessingResult{}, nil
}

func TestNew(t *testing.T) {
	tests := []struct {
		name    string
		opts    []Option
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid configuration",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test description"}, true),
				WithHost("localhost:8080"),
			},
			wantErr: false,
		},
		{
			name: "with custom session service",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test description"}, true),
				WithHost("localhost:8080"),
				WithSessionService(&mockSessionService{}),
			},
			wantErr: false,
		},
		{
			name: "with custom converters",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test description"}, true),
				WithHost("localhost:8080"),
				WithA2AToAgentConverter(&mockA2AToAgentConverter{}),
				WithEventToA2AConverter(&mockEventToA2AConverter{}),
			},
			wantErr: false,
		},
		{
			name: "missing agent",
			opts: []Option{
				WithHost("localhost:9090"),
			},
			wantErr: true,
			errMsg:  "agent is required",
		},
		{
			name: "missing host with empty host",
			opts: []Option{
				WithAgent(&mockAgent{name: "test-agent", description: "test description"}, true),
				WithHost(""),
			},
			wantErr: true,
			errMsg:  "host is required",
		},
		{
			name:    "no options",
			opts:    []Option{},
			wantErr: true,
			errMsg:  "agent is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, err := New(tt.opts...)
			if tt.wantErr {
				if err == nil {
					t.Errorf("New() expected error but got none")
					return
				}
				if tt.errMsg != "" && err.Error() != tt.errMsg {
					t.Errorf("New() error = %v, want %v", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("New() unexpected error = %v", err)
					return
				}
				if server == nil {
					t.Errorf("New() returned nil server")
				}
			}
		})
	}
}

func TestBuildAgentCard(t *testing.T) {
	tests := []struct {
		name     string
		options  *options
		expected a2a.AgentCard
	}{
		{
			name: "agent with no tools",
			options: &options{
				agent: &mockAgent{
					name:        "test-agent",
					description: "test description",
					tools:       []tool.Tool{},
				},
				host:            "localhost:8080",
				enableStreaming: true,
			},
			expected: a2a.AgentCard{
				Name:        "test-agent",
				Description: "test description",
				URL:         "http://localhost:8080",
				Capabilities: a2a.AgentCapabilities{
					Streaming: boolPtr(true),
				},
				Skills: []a2a.AgentSkill{
					{
						Name:        "test-agent",
						Description: stringPtr("test description"),
						InputModes:  []string{"text"},
						OutputModes: []string{"text"},
						Tags:        []string{"default"},
					},
				},
				DefaultInputModes:  []string{"text"},
				DefaultOutputModes: []string{"text"},
			},
		},
		{
			name: "agent with tools",
			options: &options{
				agent: &mockAgent{
					name:        "tool-agent",
					description: "agent with tools",
					tools: []tool.Tool{
						&mockTool{name: "calculator", description: "math tool"},
						&mockTool{name: "weather", description: "weather tool"},
					},
				},
				host:            "localhost:9090",
				enableStreaming: false,
			},
			expected: a2a.AgentCard{
				Name:        "tool-agent",
				Description: "agent with tools",
				URL:         "http://localhost:9090",
				Capabilities: a2a.AgentCapabilities{
					Streaming: boolPtr(false),
				},
				Skills: []a2a.AgentSkill{
					{
						Name:        "tool-agent",
						Description: stringPtr("agent with tools"),
						InputModes:  []string{"text"},
						OutputModes: []string{"text"},
						Tags:        []string{"default"},
					},
					{
						Name:        "calculator",
						Description: stringPtr("math tool"),
						InputModes:  []string{"text"},
						OutputModes: []string{"text"},
						Tags:        []string{"tool"},
					},
					{
						Name:        "weather",
						Description: stringPtr("weather tool"),
						InputModes:  []string{"text"},
						OutputModes: []string{"text"},
						Tags:        []string{"tool"},
					},
				},
				DefaultInputModes:  []string{"text"},
				DefaultOutputModes: []string{"text"},
			},
		},
		{
			name: "custom agent card",
			options: &options{
				agent: &mockAgent{
					name:        "custom-agent",
					description: "custom description",
				},
				host: "localhost:8080",
				agentCard: &a2a.AgentCard{
					Name:        "override-name",
					Description: "override description",
					URL:         "http://override.com",
				},
			},
			expected: a2a.AgentCard{
				Name:        "override-name",
				Description: "override description",
				URL:         "http://override.com",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildAgentCard(tt.options)
			if !compareAgentCards(result, tt.expected) {
				t.Errorf("buildAgentCard() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

func TestBuildProcessor(t *testing.T) {
	tests := []struct {
		name    string
		agent   agent.Agent
		session session.Service
		options *options
	}{
		{
			name:    "default converters",
			agent:   &mockAgent{name: "test-agent", description: "test description"},
			session: inmemory.NewSessionService(),
			options: &options{},
		},
		{
			name:    "custom converters",
			agent:   &mockAgent{name: "test-agent", description: "test description"},
			session: inmemory.NewSessionService(),
			options: &options{
				a2aToAgentConverter: &mockA2AToAgentConverter{},
				eventToA2AConverter: &mockEventToA2AConverter{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := buildProcessor(tt.agent, tt.session, tt.options)
			if processor == nil {
				t.Errorf("buildProcessor() returned nil")
				return
			}
			if processor.runner == nil {
				t.Errorf("buildProcessor() runner is nil")
			}
			if processor.a2aToAgentConverter == nil {
				t.Errorf("buildProcessor() a2aToAgentConverter is nil")
			}
			if processor.eventToA2AConverter == nil {
				t.Errorf("buildProcessor() eventToA2AConverter is nil")
			}
		})
	}
}

func TestBuildSkillsFromTools(t *testing.T) {
	tests := []struct {
		name      string
		agent     agent.Agent
		agentName string
		agentDesc string
		expected  []a2a.AgentSkill
	}{
		{
			name:      "no tools",
			agent:     &mockAgent{tools: []tool.Tool{}},
			agentName: "test-agent",
			agentDesc: "test description",
			expected: []a2a.AgentSkill{
				{
					Name:        "test-agent",
					Description: stringPtr("test description"),
					InputModes:  []string{"text"},
					OutputModes: []string{"text"},
					Tags:        []string{"default"},
				},
			},
		},
		{
			name: "with tools",
			agent: &mockAgent{
				tools: []tool.Tool{
					&mockTool{name: "calculator", description: "math tool"},
					&mockTool{name: "weather", description: "weather tool"},
				},
			},
			agentName: "tool-agent",
			agentDesc: "agent with tools",
			expected: []a2a.AgentSkill{
				{
					Name:        "tool-agent",
					Description: stringPtr("agent with tools"),
					InputModes:  []string{"text"},
					OutputModes: []string{"text"},
					Tags:        []string{"default"},
				},
				{
					Name:        "calculator",
					Description: stringPtr("math tool"),
					InputModes:  []string{"text"},
					OutputModes: []string{"text"},
					Tags:        []string{"tool"},
				},
				{
					Name:        "weather",
					Description: stringPtr("weather tool"),
					InputModes:  []string{"text"},
					OutputModes: []string{"text"},
					Tags:        []string{"tool"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildSkillsFromTools(tt.agent, tt.agentName, tt.agentDesc)
			if !compareSkills(result, tt.expected) {
				t.Errorf("buildSkillsFromTools() = %+v, want %+v", result, tt.expected)
			}
		})
	}
}

// Helper functions
func boolPtr(b bool) *bool {
	return &b
}

func compareAgentCards(a, b a2a.AgentCard) bool {
	if a.Name != b.Name || a.Description != b.Description || a.URL != b.URL {
		return false
	}
	if a.Capabilities.Streaming != nil && b.Capabilities.Streaming != nil {
		if *a.Capabilities.Streaming != *b.Capabilities.Streaming {
			return false
		}
	} else if a.Capabilities.Streaming != b.Capabilities.Streaming {
		return false
	}
	return compareSkills(a.Skills, b.Skills) &&
		compareStringSlices(a.DefaultInputModes, b.DefaultInputModes) &&
		compareStringSlices(a.DefaultOutputModes, b.DefaultOutputModes)
}

func compareSkills(a, b []a2a.AgentSkill) bool {
	if len(a) != len(b) {
		return false
	}
	for i, skillA := range a {
		skillB := b[i]
		if skillA.Name != skillB.Name {
			return false
		}
		if skillA.Description != nil && skillB.Description != nil {
			if *skillA.Description != *skillB.Description {
				return false
			}
		} else if skillA.Description != skillB.Description {
			return false
		}
		if !compareStringSlices(skillA.InputModes, skillB.InputModes) ||
			!compareStringSlices(skillA.OutputModes, skillB.OutputModes) ||
			!compareStringSlices(skillA.Tags, skillB.Tags) {
			return false
		}
	}
	return true
}

func compareStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i, strA := range a {
		if strA != b[i] {
			return false
		}
	}
	return true
}
