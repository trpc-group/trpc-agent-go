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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestWithPreloadMemory(t *testing.T) {
	tests := []struct {
		name          string
		limit         int
		expectedLimit int
	}{
		{
			name:          "positive limit",
			limit:         5,
			expectedLimit: 5,
		},
		{
			name:          "zero disables preloading",
			limit:         0,
			expectedLimit: 0,
		},
		{
			name:          "negative means all",
			limit:         -1,
			expectedLimit: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewContentRequestProcessor(WithPreloadMemory(tt.limit))
			assert.Equal(t, tt.expectedLimit, p.PreloadMemory)
		})
	}
}

func TestFormatMemoriesForPrompt(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name     string
		memories []*memory.Entry
		contains []string
		excludes []string
	}{
		{
			name:     "empty memories",
			memories: []*memory.Entry{},
			contains: []string{"## User Memories", "The following are memories about the user:"},
		},
		{
			name: "single memory",
			memories: []*memory.Entry{
				{
					ID:      "mem-1",
					Memory:  &memory.Memory{Memory: "User likes coffee"},
					AppName: "app",
					UserID:  "user",
				},
			},
			contains: []string{"ID: mem-1", "Memory: User likes coffee"},
		},
		{
			name: "multiple memories",
			memories: []*memory.Entry{
				{
					ID:        "mem-1",
					Memory:    &memory.Memory{Memory: "User likes coffee"},
					AppName:   "app",
					UserID:    "user",
					CreatedAt: now,
				},
				{
					ID:        "mem-2",
					Memory:    &memory.Memory{Memory: "User works in tech"},
					AppName:   "app",
					UserID:    "user",
					CreatedAt: now,
				},
			},
			contains: []string{
				"ID: mem-1", "Memory: User likes coffee",
				"ID: mem-2", "Memory: User works in tech",
			},
		},
		{
			name: "nil entry is skipped",
			memories: []*memory.Entry{
				{
					ID:      "mem-1",
					Memory:  &memory.Memory{Memory: "User likes coffee"},
					AppName: "app",
					UserID:  "user",
				},
				nil,
				{
					ID:      "mem-2",
					Memory:  &memory.Memory{Memory: "User works in tech"},
					AppName: "app",
					UserID:  "user",
				},
			},
			contains: []string{
				"ID: mem-1", "Memory: User likes coffee",
				"ID: mem-2", "Memory: User works in tech",
			},
		},
		{
			name: "nil memory field is skipped",
			memories: []*memory.Entry{
				{
					ID:      "mem-1",
					Memory:  &memory.Memory{Memory: "User likes coffee"},
					AppName: "app",
					UserID:  "user",
				},
				{
					ID:      "mem-2",
					Memory:  nil,
					AppName: "app",
					UserID:  "user",
				},
				{
					ID:      "mem-3",
					Memory:  &memory.Memory{Memory: "User works in tech"},
					AppName: "app",
					UserID:  "user",
				},
			},
			contains: []string{
				"ID: mem-1", "Memory: User likes coffee",
				"ID: mem-3", "Memory: User works in tech",
			},
			excludes: []string{"ID: mem-2"},
		},
		{
			name: "all nil or nil memory returns header only",
			memories: []*memory.Entry{
				nil,
				{ID: "mem-1", Memory: nil, AppName: "app", UserID: "user"},
			},
			contains: []string{
				"## User Memories", "The following are memories about the user:",
			},
			excludes: []string{"ID: mem-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatMemoryContent(tt.memories)
			for _, expected := range tt.contains {
				assert.Contains(t, result, expected)
			}
			for _, excluded := range tt.excludes {
				assert.NotContains(t, result, excluded)
			}
		})
	}
}

// mockMemoryService implements memory.Service for testing.
type mockMemoryService struct {
	memories   []*memory.Entry
	readErr    error
	readCalled bool
	readLimit  int
}

func (m *mockMemoryService) AddMemory(ctx context.Context, userKey memory.UserKey, memoryStr string, topics []string) error {
	return nil
}

func (m *mockMemoryService) UpdateMemory(ctx context.Context, memoryKey memory.Key, memoryStr string, topics []string) error {
	return nil
}

func (m *mockMemoryService) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	return nil
}

func (m *mockMemoryService) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	return nil
}

func (m *mockMemoryService) ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error) {
	m.readCalled = true
	m.readLimit = limit
	if m.readErr != nil {
		return nil, m.readErr
	}
	return m.memories, nil
}

func (m *mockMemoryService) SearchMemories(ctx context.Context, userKey memory.UserKey, query string) ([]*memory.Entry, error) {
	return nil, nil
}

func (m *mockMemoryService) Tools() []tool.Tool {
	return nil
}

func (m *mockMemoryService) EnqueueAutoMemoryJob(ctx context.Context, sess *session.Session) error {
	return nil
}

func (m *mockMemoryService) Close() error {
	return nil
}

func TestGetPreloadMemoryMessage(t *testing.T) {
	t.Run("nil memory service", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(-1))
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "user",
			}),
		)
		inv.MemoryService = nil
		msg := p.getPreloadMemoryMessage(context.Background(), inv)
		assert.Nil(t, msg)
	})

	t.Run("nil session", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(-1))
		inv := agent.NewInvocation()
		inv.MemoryService = &mockMemoryService{}
		inv.Session = nil
		msg := p.getPreloadMemoryMessage(context.Background(), inv)
		assert.Nil(t, msg)
	})

	t.Run("empty app name", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(-1))
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "",
				UserID:  "user",
			}),
		)
		inv.MemoryService = &mockMemoryService{}
		msg := p.getPreloadMemoryMessage(context.Background(), inv)
		assert.Nil(t, msg)
	})

	t.Run("empty user ID", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(-1))
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "",
			}),
		)
		inv.MemoryService = &mockMemoryService{}
		msg := p.getPreloadMemoryMessage(context.Background(), inv)
		assert.Nil(t, msg)
	})

	t.Run("read error returns nil", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(-1))
		mockSvc := &mockMemoryService{
			readErr: assert.AnError,
		}
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "user",
			}),
		)
		inv.MemoryService = mockSvc
		msg := p.getPreloadMemoryMessage(context.Background(), inv)
		assert.Nil(t, msg)
		assert.True(t, mockSvc.readCalled)
	})

	t.Run("empty memories returns nil", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(-1))
		mockSvc := &mockMemoryService{
			memories: []*memory.Entry{},
		}
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "user",
			}),
		)
		inv.MemoryService = mockSvc
		msg := p.getPreloadMemoryMessage(context.Background(), inv)
		assert.Nil(t, msg)
	})

	t.Run("returns formatted memories", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(-1))
		mockSvc := &mockMemoryService{
			memories: []*memory.Entry{
				{
					ID:     "mem-1",
					Memory: &memory.Memory{Memory: "User likes coffee"},
				},
			},
		}
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "user",
			}),
		)
		inv.MemoryService = mockSvc
		msg := p.getPreloadMemoryMessage(context.Background(), inv)
		assert.NotNil(t, msg)
		assert.Equal(t, model.RoleSystem, msg.Role)
		assert.Contains(t, msg.Content, "User likes coffee")
		assert.Contains(t, msg.Content, "mem-1")
	})

	t.Run("preload disabled returns nil without calling service", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(0))
		mockSvc := &mockMemoryService{
			memories: []*memory.Entry{
				{ID: "mem-1", Memory: &memory.Memory{Memory: "test"}},
			},
		}
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "user",
			}),
		)
		inv.MemoryService = mockSvc
		msg := p.getPreloadMemoryMessage(context.Background(), inv)
		assert.Nil(t, msg)
		assert.False(t, mockSvc.readCalled)
	})

	t.Run("negative preload converts to zero limit", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(-1))
		mockSvc := &mockMemoryService{
			memories: []*memory.Entry{
				{ID: "mem-1", Memory: &memory.Memory{Memory: "test"}},
			},
		}
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "user",
			}),
		)
		inv.MemoryService = mockSvc
		p.getPreloadMemoryMessage(context.Background(), inv)
		assert.Equal(t, 0, mockSvc.readLimit)
		assert.True(t, mockSvc.readCalled)
	})

	t.Run("positive preload uses limit", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(5))
		mockSvc := &mockMemoryService{
			memories: []*memory.Entry{
				{ID: "mem-1", Memory: &memory.Memory{Memory: "test"}},
			},
		}
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "user",
			}),
		)
		inv.MemoryService = mockSvc
		p.getPreloadMemoryMessage(context.Background(), inv)
		assert.Equal(t, 5, mockSvc.readLimit)
		assert.True(t, mockSvc.readCalled)
	})

	t.Run("zero preload disabled", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(0))
		mockSvc := &mockMemoryService{
			memories: []*memory.Entry{
				{ID: "mem-1", Memory: &memory.Memory{Memory: "test"}},
			},
		}
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "user",
			}),
		)
		inv.MemoryService = mockSvc
		msg := p.getPreloadMemoryMessage(context.Background(), inv)
		assert.Nil(t, msg)
		assert.False(t, mockSvc.readCalled)
	})
}

func TestProcessRequest_WithPreloadMemory(t *testing.T) {
	t.Run("preload disabled does not call memory service", func(t *testing.T) {
		p := NewContentRequestProcessor(WithPreloadMemory(0))
		mockSvc := &mockMemoryService{
			memories: []*memory.Entry{
				{ID: "mem-1", Memory: &memory.Memory{Memory: "test"}},
			},
		}
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "user",
			}),
		)
		inv.MemoryService = mockSvc
		req := &model.Request{Messages: []model.Message{}}
		p.ProcessRequest(context.Background(), inv, req, nil)
		assert.False(t, mockSvc.readCalled)
	})

	t.Run("preload enabled merges memory into system message", func(t *testing.T) {
		p := NewContentRequestProcessor(
			WithPreloadMemory(-1),
			WithAddSessionSummary(true),
		)
		mockSvc := &mockMemoryService{
			memories: []*memory.Entry{
				{ID: "mem-1", Memory: &memory.Memory{Memory: "User prefers dark mode"}},
			},
		}
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "user",
			}),
		)
		inv.MemoryService = mockSvc
		req := &model.Request{
			Messages: []model.Message{
				{Role: model.RoleSystem, Content: "You are a helpful assistant."},
				{Role: model.RoleUser, Content: "hello"},
			},
		}
		p.ProcessRequest(context.Background(), inv, req, nil)
		assert.True(t, mockSvc.readCalled)
		// Memory should be merged into the system message.
		assert.Equal(t, 2, len(req.Messages))
		assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
		assert.Contains(t, req.Messages[0].Content, "You are a helpful assistant.")
		assert.Contains(t, req.Messages[0].Content, "User Memories")
		assert.Contains(t, req.Messages[0].Content, "User prefers dark mode")
	})

	t.Run("preload with no system message prepends memory", func(t *testing.T) {
		p := NewContentRequestProcessor(
			WithPreloadMemory(-1),
			WithAddSessionSummary(true),
		)
		mockSvc := &mockMemoryService{
			memories: []*memory.Entry{
				{ID: "mem-1", Memory: &memory.Memory{Memory: "User prefers dark mode"}},
			},
		}
		inv := agent.NewInvocation(
			agent.WithInvocationSession(&session.Session{
				AppName: "app",
				UserID:  "user",
			}),
		)
		inv.MemoryService = mockSvc
		req := &model.Request{
			Messages: []model.Message{
				{Role: model.RoleUser, Content: "hello"},
			},
		}
		p.ProcessRequest(context.Background(), inv, req, nil)
		assert.True(t, mockSvc.readCalled)
		// Memory message should be prepended.
		assert.GreaterOrEqual(t, len(req.Messages), 2)
		assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
		assert.Contains(t, req.Messages[0].Content, "User Memories")
	})
}

func TestProcessRequest_MergesPreloadMemory(t *testing.T) {
	p := NewContentRequestProcessor(
		WithPreloadMemory(-1),
	)
	mockSvc := &mockMemoryService{
		memories: []*memory.Entry{
			{ID: "mem-1", Memory: &memory.Memory{Memory: "User likes tea"}},
		},
	}
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			AppName: "app",
			UserID:  "user",
		}),
	)
	inv.MemoryService = mockSvc
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "Base system prompt"},
			{Role: model.RoleUser, Content: "hello"},
		},
	}

	p.ProcessRequest(context.Background(), inv, req, nil)
	assert.True(t, mockSvc.readCalled)

	systemCount := 0
	for _, msg := range req.Messages {
		if msg.Role == model.RoleSystem {
			systemCount++
			assert.Contains(t, msg.Content, "Base system prompt")
			assert.Contains(t, msg.Content, "User Memories")
			assert.Contains(t, msg.Content, "User likes tea")
		}
	}
	assert.Equal(t, 1, systemCount)
}

func TestProcessRequest_MergesSummary(t *testing.T) {
	p := NewContentRequestProcessor(
		WithAddSessionSummary(true),
	)
	inv := agent.NewInvocation(
		agent.WithInvocationSession(&session.Session{
			Summaries: map[string]*session.Summary{
				"": {
					Summary: "summary text",
				},
			},
		}),
	)
	req := &model.Request{
		Messages: []model.Message{
			{Role: model.RoleSystem, Content: "Base system prompt"},
			{Role: model.RoleUser, Content: "hello"},
		},
	}

	p.ProcessRequest(context.Background(), inv, req, nil)

	systemCount := 0
	for _, msg := range req.Messages {
		if msg.Role == model.RoleSystem {
			systemCount++
			assert.Contains(t, msg.Content, "Base system prompt")
			assert.Contains(t, msg.Content, "summary text")
			assert.Contains(t, msg.Content,
				"summary_of_previous_interactions")
		}
	}
	assert.Equal(t, 1, systemCount)
}
