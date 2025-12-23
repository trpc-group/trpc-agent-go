//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// mockExtractor is a mock implementation of extractor.MemoryExtractor.
type mockExtractor struct {
	ops []*extractor.Operation
	err error
}

func (m *mockExtractor) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*extractor.Operation, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.ops, nil
}

func (m *mockExtractor) SetPrompt(prompt string) {}

func (m *mockExtractor) SetModel(model model.Model) {}

func (m *mockExtractor) Metadata() map[string]any {
	return map[string]any{}
}

// mockOperator is a mock implementation of MemoryOperator.
type mockOperator struct {
	mu          sync.Mutex
	memories    map[string]*memory.Entry
	addCalls    int
	updateCalls int
	deleteCalls int
	readErr     error
	addErr      error
	updateErr   error
	deleteErr   error
}

func newMockOperator() *mockOperator {
	return &mockOperator{
		memories: make(map[string]*memory.Entry),
	}
}

func (m *mockOperator) ReadMemories(
	ctx context.Context,
	userKey memory.UserKey,
	limit int,
) ([]*memory.Entry, error) {
	if m.readErr != nil {
		return nil, m.readErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var results []*memory.Entry
	for _, entry := range m.memories {
		if entry.AppName == userKey.AppName && entry.UserID == userKey.UserID {
			results = append(results, entry)
			if limit > 0 && len(results) >= limit {
				break
			}
		}
	}
	return results, nil
}

func (m *mockOperator) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	memoryStr string,
	topics []string,
) error {
	if m.addErr != nil {
		return m.addErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addCalls++
	return nil
}

func (m *mockOperator) UpdateMemory(
	ctx context.Context,
	memoryKey memory.Key,
	memoryStr string,
	topics []string,
) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateCalls++
	return nil
}

func (m *mockOperator) DeleteMemory(ctx context.Context, memoryKey memory.Key) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteCalls++
	return nil
}

func TestNewAutoMemoryWorker(t *testing.T) {
	ext := &mockExtractor{}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)

	require.NotNil(t, worker)
	assert.Equal(t, ext, worker.config.Extractor)
	assert.Equal(t, op, worker.operator)
	assert.False(t, worker.started)
}

func TestAutoMemoryWorker_StartStop(t *testing.T) {
	ext := &mockExtractor{}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor:       ext,
		AsyncMemoryNum:  2,
		MemoryQueueSize: 10,
	}

	worker := NewAutoMemoryWorker(config, op)

	// Start the worker.
	worker.Start()
	assert.True(t, worker.started)
	assert.Len(t, worker.jobChans, 2)

	// Start again should be no-op.
	worker.Start()
	assert.True(t, worker.started)

	// Stop the worker.
	worker.Stop()
	assert.False(t, worker.started)
	assert.Nil(t, worker.jobChans)

	// Stop again should be no-op.
	worker.Stop()
	assert.False(t, worker.started)
}

func TestAutoMemoryWorker_StartWithoutExtractor(t *testing.T) {
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: nil,
	}

	worker := NewAutoMemoryWorker(config, op)
	worker.Start()

	assert.False(t, worker.started)
	assert.Nil(t, worker.jobChans)
}

func TestAutoMemoryWorker_StartWithDefaultConfig(t *testing.T) {
	ext := &mockExtractor{}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor:       ext,
		AsyncMemoryNum:  0, // Should use default.
		MemoryQueueSize: 0, // Should use default.
	}

	worker := NewAutoMemoryWorker(config, op)
	worker.Start()
	defer worker.Stop()

	assert.True(t, worker.started)
	assert.Len(t, worker.jobChans, DefaultAsyncMemoryNum)
}

func TestAutoMemoryWorker_EnqueueJob_NoExtractor(t *testing.T) {
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: nil,
	}

	worker := NewAutoMemoryWorker(config, op)

	err := worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})

	assert.NoError(t, err)
}

func TestAutoMemoryWorker_EnqueueJob_EmptyUserKey(t *testing.T) {
	ext := &mockExtractor{}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)

	// Empty AppName.
	err := worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "",
		UserID:  "user-1",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})
	assert.NoError(t, err)

	// Empty UserID.
	err = worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})
	assert.NoError(t, err)
}

func TestAutoMemoryWorker_EnqueueJob_EmptyMessages(t *testing.T) {
	ext := &mockExtractor{}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)

	err := worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, nil)

	assert.NoError(t, err)
}

func TestAutoMemoryWorker_EnqueueJob_SyncFallback(t *testing.T) {
	ext := &mockExtractor{
		ops: []*extractor.Operation{
			{
				Type:   extractor.OperationAdd,
				Memory: "Test memory.",
			},
		},
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)
	// Do not start the worker, so it falls back to sync.

	err := worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, op.addCalls)
}

func TestAutoMemoryWorker_EnqueueJob_Async(t *testing.T) {
	ext := &mockExtractor{
		ops: []*extractor.Operation{
			{
				Type:   extractor.OperationAdd,
				Memory: "Test memory.",
			},
		},
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor:        ext,
		AsyncMemoryNum:   2,
		MemoryQueueSize:  10,
		MemoryJobTimeout: time.Second,
	}

	worker := NewAutoMemoryWorker(config, op)
	worker.Start()
	defer worker.Stop()

	err := worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})

	assert.NoError(t, err)

	// Wait for async processing.
	time.Sleep(100 * time.Millisecond)

	op.mu.Lock()
	addCalls := op.addCalls
	op.mu.Unlock()
	assert.Equal(t, 1, addCalls)
}

func TestAutoMemoryWorker_EnqueueJob_QueueFull(t *testing.T) {
	ext := &mockExtractor{
		ops: []*extractor.Operation{
			{
				Type:   extractor.OperationAdd,
				Memory: "Test memory.",
			},
		},
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor:       ext,
		AsyncMemoryNum:  1,
		MemoryQueueSize: 1,
	}

	worker := NewAutoMemoryWorker(config, op)
	worker.Start()

	// Fill the queue by blocking the worker.
	blockCh := make(chan struct{})

	// Create a blocking extractor.
	blockingExt := &blockingExtractor{blockCh: blockCh}
	worker.config.Extractor = blockingExt

	// First job blocks the worker.
	_ = worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})

	// Wait a bit for the worker to pick up the job.
	time.Sleep(10 * time.Millisecond)

	// Second job fills the queue.
	_ = worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})

	// Third job should fall back to sync.
	worker.config.Extractor = ext
	ext.ops = []*extractor.Operation{
		{
			Type:   extractor.OperationAdd,
			Memory: "Test memory.",
		},
	}

	err := worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-2",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})

	assert.NoError(t, err)

	// Unblock the worker and stop.
	close(blockCh)
	worker.Stop()
}

type blockingExtractor struct {
	blockCh chan struct{}
}

func (e *blockingExtractor) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*extractor.Operation, error) {
	<-e.blockCh
	return nil, nil
}

func (e *blockingExtractor) SetPrompt(prompt string) {}

func (e *blockingExtractor) SetModel(m model.Model) {}

func (e *blockingExtractor) Metadata() map[string]any {
	return map[string]any{}
}

func TestAutoMemoryWorker_CreateAutoMemory_ExtractError(t *testing.T) {
	ext := &mockExtractor{
		err: errors.New("extract error"),
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)

	// Should return error when extract fails.
	err := worker.createAutoMemory(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "extract failed")
}

func TestAutoMemoryWorker_CreateAutoMemory_ReadError(t *testing.T) {
	ext := &mockExtractor{
		ops: []*extractor.Operation{
			{
				Type:   extractor.OperationAdd,
				Memory: "Test memory.",
			},
		},
	}
	op := newMockOperator()
	op.readErr = errors.New("read error")
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)

	// Should still succeed even if read fails.
	err := worker.createAutoMemory(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})

	assert.NoError(t, err)
	assert.Equal(t, 1, op.addCalls)
}

func TestAutoMemoryWorker_ExecuteOperation_Add(t *testing.T) {
	op := newMockOperator()
	worker := &AutoMemoryWorker{operator: op}

	worker.executeOperation(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, &extractor.Operation{
		Type:   extractor.OperationAdd,
		Memory: "Test memory.",
		Topics: []string{"topic1"},
	})

	assert.Equal(t, 1, op.addCalls)
}

func TestAutoMemoryWorker_ExecuteOperation_Update(t *testing.T) {
	op := newMockOperator()
	worker := &AutoMemoryWorker{operator: op}

	worker.executeOperation(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, &extractor.Operation{
		Type:     extractor.OperationUpdate,
		MemoryID: "mem-123",
		Memory:   "Updated memory.",
	})

	assert.Equal(t, 1, op.updateCalls)
}

func TestAutoMemoryWorker_ExecuteOperation_Delete(t *testing.T) {
	op := newMockOperator()
	worker := &AutoMemoryWorker{operator: op}

	worker.executeOperation(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, &extractor.Operation{
		Type:     extractor.OperationDelete,
		MemoryID: "mem-456",
	})

	assert.Equal(t, 1, op.deleteCalls)
}

func TestAutoMemoryWorker_ExecuteOperation_Unknown(t *testing.T) {
	op := newMockOperator()
	worker := &AutoMemoryWorker{operator: op}

	// Should not panic.
	worker.executeOperation(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, &extractor.Operation{
		Type: "unknown",
	})

	assert.Equal(t, 0, op.addCalls)
	assert.Equal(t, 0, op.updateCalls)
	assert.Equal(t, 0, op.deleteCalls)
}

func TestAutoMemoryWorker_ExecuteOperation_Errors(t *testing.T) {
	t.Run("add error", func(t *testing.T) {
		op := newMockOperator()
		op.addErr = errors.New("add error")
		worker := &AutoMemoryWorker{operator: op}

		// Should not panic.
		worker.executeOperation(context.Background(), memory.UserKey{
			AppName: "test-app",
			UserID:  "user-1",
		}, &extractor.Operation{
			Type:   extractor.OperationAdd,
			Memory: "Test memory.",
		})
	})

	t.Run("update error", func(t *testing.T) {
		op := newMockOperator()
		op.updateErr = errors.New("update error")
		worker := &AutoMemoryWorker{operator: op}

		// Should not panic.
		worker.executeOperation(context.Background(), memory.UserKey{
			AppName: "test-app",
			UserID:  "user-1",
		}, &extractor.Operation{
			Type:     extractor.OperationUpdate,
			MemoryID: "mem-123",
			Memory:   "Updated memory.",
		})
	})

	t.Run("delete error", func(t *testing.T) {
		op := newMockOperator()
		op.deleteErr = errors.New("delete error")
		worker := &AutoMemoryWorker{operator: op}

		// Should not panic.
		worker.executeOperation(context.Background(), memory.UserKey{
			AppName: "test-app",
			UserID:  "user-1",
		}, &extractor.Operation{
			Type:     extractor.OperationDelete,
			MemoryID: "mem-456",
		})
	})
}

func TestHashUserKey(t *testing.T) {
	tests := []struct {
		name    string
		userKey memory.UserKey
	}{
		{
			name: "normal key",
			userKey: memory.UserKey{
				AppName: "test-app",
				UserID:  "user-1",
			},
		},
		{
			name: "empty app name",
			userKey: memory.UserKey{
				AppName: "",
				UserID:  "user-1",
			},
		},
		{
			name: "empty user id",
			userKey: memory.UserKey{
				AppName: "test-app",
				UserID:  "",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := HashUserKey(tt.userKey)
			assert.GreaterOrEqual(t, hash, 0)

			// Same key should produce same hash.
			hash2 := HashUserKey(tt.userKey)
			assert.Equal(t, hash, hash2)
		})
	}

	// Different keys should produce different hashes (usually).
	hash1 := HashUserKey(memory.UserKey{AppName: "app1", UserID: "user1"})
	hash2 := HashUserKey(memory.UserKey{AppName: "app2", UserID: "user2"})
	// Not strictly required, but very likely.
	assert.NotEqual(t, hash1, hash2)
}

func TestAutoMemoryWorker_ProcessJob_NilContext(t *testing.T) {
	ext := &mockExtractor{
		ops: []*extractor.Operation{
			{
				Type:   extractor.OperationAdd,
				Memory: "Test memory.",
			},
		},
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor:        ext,
		MemoryJobTimeout: time.Second,
	}

	worker := NewAutoMemoryWorker(config, op)

	// Process job with nil context.
	worker.processJob(&MemoryJob{
		Ctx: nil,
		UserKey: memory.UserKey{
			AppName: "test-app",
			UserID:  "user-1",
		},
		Messages: []model.Message{
			model.NewUserMessage("hello"),
		},
	})

	assert.Equal(t, 1, op.addCalls)
}

func TestAutoMemoryWorker_ProcessJob_DefaultTimeout(t *testing.T) {
	ext := &mockExtractor{
		ops: []*extractor.Operation{
			{
				Type:   extractor.OperationAdd,
				Memory: "Test memory.",
			},
		},
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor:        ext,
		MemoryJobTimeout: 0, // Should use default.
	}

	worker := NewAutoMemoryWorker(config, op)

	worker.processJob(&MemoryJob{
		Ctx: context.Background(),
		UserKey: memory.UserKey{
			AppName: "test-app",
			UserID:  "user-1",
		},
		Messages: []model.Message{
			model.NewUserMessage("hello"),
		},
	})

	assert.Equal(t, 1, op.addCalls)
}

func TestAutoMemoryWorker_TryEnqueueJob_CancelledContext(t *testing.T) {
	ext := &mockExtractor{}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor:       ext,
		AsyncMemoryNum:  1,
		MemoryQueueSize: 10,
	}

	worker := NewAutoMemoryWorker(config, op)
	worker.Start()
	defer worker.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	result := worker.tryEnqueueJob(ctx, memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, &MemoryJob{})

	assert.False(t, result)
}

func TestMemoryJob(t *testing.T) {
	job := &MemoryJob{
		Ctx: context.Background(),
		UserKey: memory.UserKey{
			AppName: "test-app",
			UserID:  "user-1",
		},
		Messages: []model.Message{
			model.NewUserMessage("hello"),
		},
	}

	assert.NotNil(t, job.Ctx)
	assert.Equal(t, "test-app", job.UserKey.AppName)
	assert.Equal(t, "user-1", job.UserKey.UserID)
	assert.Len(t, job.Messages, 1)
}

func TestAutoMemoryConfig(t *testing.T) {
	ext := &mockExtractor{}
	config := AutoMemoryConfig{
		Extractor:           ext,
		AsyncMemoryNum:      5,
		MemoryQueueSize:     200,
		MemoryJobTimeout:    time.Minute,
		MaxExistingMemories: 100,
	}

	assert.Equal(t, ext, config.Extractor)
	assert.Equal(t, 5, config.AsyncMemoryNum)
	assert.Equal(t, 200, config.MemoryQueueSize)
	assert.Equal(t, time.Minute, config.MemoryJobTimeout)
	assert.Equal(t, 100, config.MaxExistingMemories)
}

func TestDefaultConstants(t *testing.T) {
	assert.Equal(t, 1, DefaultAsyncMemoryNum)
	assert.Equal(t, 10, DefaultMemoryQueueSize)
	assert.Equal(t, 30*time.Second, DefaultMemoryJobTimeout)
	assert.Equal(t, 50, DefaultMaxExistingMemories)
}

// mockModel is a mock implementation of model.Model for testing.
type mockModel struct {
	name      string
	responses []*model.Response
	err       error
}

func (m *mockModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan *model.Response, len(m.responses))
	for _, rsp := range m.responses {
		ch <- rsp
	}
	close(ch)
	return ch, nil
}

func (m *mockModel) Info() model.Info {
	return model.Info{Name: m.name}
}

// newMockModelWithToolCalls creates a mock model that returns tool calls.
func newMockModelWithToolCalls(toolCalls []model.ToolCall) *mockModel {
	return &mockModel{
		name: "test-model",
		responses: []*model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							ToolCalls: toolCalls,
						},
					},
				},
			},
		},
	}
}

func TestAutoMemoryWorker_IntegrationWithRealExtractor(t *testing.T) {
	// Create a mock model that returns add operation.
	args, _ := json.Marshal(map[string]any{
		"memory": "User likes coffee.",
		"topics": []any{"preferences"},
	})
	mockMdl := newMockModelWithToolCalls([]model.ToolCall{
		{
			Type: "function",
			Function: model.FunctionDefinitionParam{
				Name:      memory.AddToolName,
				Arguments: args,
			},
		},
	})

	// Create real extractor with mock model.
	ext := extractor.NewExtractor(mockMdl)

	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor:       ext,
		AsyncMemoryNum:  1,
		MemoryQueueSize: 10,
	}

	worker := NewAutoMemoryWorker(config, op)
	worker.Start()
	defer worker.Stop()

	err := worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, []model.Message{
		model.NewUserMessage("I love coffee."),
	})

	assert.NoError(t, err)

	// Wait for async processing.
	time.Sleep(100 * time.Millisecond)

	op.mu.Lock()
	addCalls := op.addCalls
	op.mu.Unlock()
	assert.Equal(t, 1, addCalls)
}
