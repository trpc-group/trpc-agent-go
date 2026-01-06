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
	"fmt"
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

func (m *mockExtractor) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	return true // Always extract by default.
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
	clearCalls  int
	readErr     error
	addErr      error
	updateErr   error
	deleteErr   error
	clearErr    error
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

func (m *mockOperator) ClearMemories(ctx context.Context, userKey memory.UserKey) error {
	if m.clearErr != nil {
		return m.clearErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clearCalls++
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
	// Fill the queue by blocking the worker.
	blockCh := make(chan struct{})

	// Create a blocking extractor that will hold the worker busy.
	blockingExt := &blockingExtractor{blockCh: blockCh}

	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor:       blockingExt,
		AsyncMemoryNum:  1,
		MemoryQueueSize: 1,
	}

	worker := NewAutoMemoryWorker(config, op)
	worker.Start()

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

	// Third job should fall back to sync (queue is full).
	// Since blockingExt is still blocking, the sync fallback will also block,
	// so we run it in a goroutine and verify the queue was full.
	syncDone := make(chan struct{})
	go func() {
		_ = worker.EnqueueJob(context.Background(), memory.UserKey{
			AppName: "test-app",
			UserID:  "user-2",
		}, []model.Message{
			model.NewUserMessage("hello"),
		})
		close(syncDone)
	}()

	// Give time for the third job to attempt enqueue and fall back to sync.
	time.Sleep(10 * time.Millisecond)

	// Unblock all and stop.
	close(blockCh)
	<-syncDone
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

func (e *blockingExtractor) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	return true
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
			hash := hashUserKey(tt.userKey)
			assert.GreaterOrEqual(t, hash, 0)

			// Same key should produce same hash.
			hash2 := hashUserKey(tt.userKey)
			assert.Equal(t, hash, hash2)
		})
	}

	// Different keys should produce different hashes (usually).
	hash1 := hashUserKey(memory.UserKey{AppName: "app1", UserID: "user1"})
	hash2 := hashUserKey(memory.UserKey{AppName: "app2", UserID: "user2"})
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
		Extractor:        ext,
		AsyncMemoryNum:   5,
		MemoryQueueSize:  200,
		MemoryJobTimeout: time.Minute,
	}

	assert.Equal(t, ext, config.Extractor)
	assert.Equal(t, 5, config.AsyncMemoryNum)
	assert.Equal(t, 200, config.MemoryQueueSize)
	assert.Equal(t, time.Minute, config.MemoryJobTimeout)
}

func TestDefaultConstants(t *testing.T) {
	assert.Equal(t, 1, DefaultAsyncMemoryNum)
	assert.Equal(t, 10, DefaultMemoryQueueSize)
	assert.Equal(t, 30*time.Second, DefaultMemoryJobTimeout)
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

// TestAutoMemoryWorker_EnqueueJob_RaceWithStop tests the data race between
// EnqueueJob and Stop. Before the fix, this test would panic with
// "integer divide by zero" because EnqueueJob reads w.jobChans outside the
// lock while Stop sets it to nil.
func TestAutoMemoryWorker_EnqueueJob_RaceWithStop(t *testing.T) {
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
		AsyncMemoryNum:  2,
		MemoryQueueSize: 10,
	}

	worker := NewAutoMemoryWorker(config, op)
	worker.Start()

	// Run many concurrent EnqueueJob and Stop calls to trigger the race.
	var wg sync.WaitGroup
	const numGoroutines = 100

	// Half goroutines call EnqueueJob.
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			// This should not panic even if Stop() is called concurrently.
			_ = worker.EnqueueJob(context.Background(), memory.UserKey{
				AppName: "test-app",
				UserID:  fmt.Sprintf("user-%d", id),
			}, []model.Message{
				model.NewUserMessage("hello"),
			})
		}(i)
	}

	// Half goroutines call Stop then Start to trigger the race.
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.Stop()
			worker.Start()
		}()
	}

	wg.Wait()
	worker.Stop()
}

// checkerExtractor is a mock extractor that tracks ShouldExtract calls.
type checkerExtractor struct {
	shouldExtract bool
	extractCalls  int
	mu            sync.Mutex
}

func (e *checkerExtractor) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*extractor.Operation, error) {
	e.mu.Lock()
	e.extractCalls++
	e.mu.Unlock()
	return nil, nil
}

func (e *checkerExtractor) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	return e.shouldExtract
}

func (e *checkerExtractor) SetPrompt(prompt string) {}

func (e *checkerExtractor) SetModel(m model.Model) {}

func (e *checkerExtractor) Metadata() map[string]any {
	return map[string]any{}
}

func (e *checkerExtractor) getExtractCalls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.extractCalls
}

func TestAutoMemoryWorker_ShouldExtract_Skipped(t *testing.T) {
	ext := &checkerExtractor{shouldExtract: false}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)
	worker.Start()
	defer worker.Stop()

	// Enqueue job - should be skipped by checker.
	err := worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})

	require.NoError(t, err)

	// Give time for async processing.
	time.Sleep(50 * time.Millisecond)

	// Extract should not be called since ShouldExtract returns false.
	assert.Equal(t, 0, ext.getExtractCalls())
}

func TestAutoMemoryWorker_ShouldExtract_Proceeds(t *testing.T) {
	ext := &checkerExtractor{shouldExtract: true}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)
	worker.Start()
	defer worker.Stop()

	// Enqueue job - should proceed.
	err := worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, []model.Message{
		model.NewUserMessage("hello"),
	})

	require.NoError(t, err)

	// Give time for async processing.
	time.Sleep(50 * time.Millisecond)

	// Extract should be called since ShouldExtract returns true.
	assert.Equal(t, 1, ext.getExtractCalls())
}

func TestAutoMemoryWorker_ExtractionState_TotalTurns(t *testing.T) {
	var capturedCtx *extractor.ExtractionContext
	ext := &mockExtractorWithCapture{
		shouldExtract: true,
		captureCtx:    func(ctx *extractor.ExtractionContext) { capturedCtx = ctx },
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)

	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}
	messages := []model.Message{model.NewUserMessage("hello")}

	// First call.
	_ = worker.EnqueueJob(context.Background(), userKey, messages)
	assert.Equal(t, 1, capturedCtx.TotalTurns)
	assert.Nil(t, capturedCtx.LastExtractAt)

	// Second call.
	_ = worker.EnqueueJob(context.Background(), userKey, messages)
	assert.Equal(t, 2, capturedCtx.TotalTurns)
	assert.NotNil(t, capturedCtx.LastExtractAt)

	// Third call.
	_ = worker.EnqueueJob(context.Background(), userKey, messages)
	assert.Equal(t, 3, capturedCtx.TotalTurns)
}

func TestAutoMemoryWorker_ExtractionState_PerUser(t *testing.T) {
	var capturedCtx *extractor.ExtractionContext
	ext := &mockExtractorWithCapture{
		shouldExtract: true,
		captureCtx:    func(ctx *extractor.ExtractionContext) { capturedCtx = ctx },
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)

	messages := []model.Message{model.NewUserMessage("hello")}

	// User 1 first call.
	_ = worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "app",
		UserID:  "user-1",
	}, messages)
	assert.Equal(t, 1, capturedCtx.TotalTurns)

	// User 2 first call (should have its own counter).
	_ = worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "app",
		UserID:  "user-2",
	}, messages)
	assert.Equal(t, 1, capturedCtx.TotalTurns)

	// User 1 second call.
	_ = worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "app",
		UserID:  "user-1",
	}, messages)
	assert.Equal(t, 2, capturedCtx.TotalTurns)
}

// mockExtractorWithCapture captures ExtractionContext for testing.
type mockExtractorWithCapture struct {
	shouldExtract bool
	captureCtx    func(*extractor.ExtractionContext)
}

func (e *mockExtractorWithCapture) Extract(
	ctx context.Context,
	messages []model.Message,
	existing []*memory.Entry,
) ([]*extractor.Operation, error) {
	return nil, nil
}

func (e *mockExtractorWithCapture) ShouldExtract(ctx *extractor.ExtractionContext) bool {
	if e.captureCtx != nil {
		e.captureCtx(ctx)
	}
	return e.shouldExtract
}

func (e *mockExtractorWithCapture) SetPrompt(prompt string) {}

func (e *mockExtractorWithCapture) SetModel(m model.Model) {}

func (e *mockExtractorWithCapture) Metadata() map[string]any {
	return map[string]any{}
}

func TestAutoMemoryWorker_PendingMessages_Accumulation(t *testing.T) {
	var capturedMessages []model.Message
	ext := &mockExtractorWithCapture{
		shouldExtract: false, // Don't extract, accumulate messages.
		captureCtx: func(ctx *extractor.ExtractionContext) {
			capturedMessages = ctx.Messages
		},
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)

	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}

	// First turn - not extracted.
	msg1 := model.NewUserMessage("hello")
	_ = worker.EnqueueJob(context.Background(), userKey, []model.Message{msg1})
	assert.Len(t, capturedMessages, 1)
	assert.Equal(t, "hello", capturedMessages[0].Content)

	// Second turn - not extracted, messages should accumulate.
	msg2 := model.NewUserMessage("world")
	_ = worker.EnqueueJob(context.Background(), userKey, []model.Message{msg2})
	assert.Len(t, capturedMessages, 2)
	assert.Equal(t, "hello", capturedMessages[0].Content)
	assert.Equal(t, "world", capturedMessages[1].Content)

	// Third turn - not extracted, messages should continue accumulating.
	msg3 := model.NewUserMessage("foo")
	_ = worker.EnqueueJob(context.Background(), userKey, []model.Message{msg3})
	assert.Len(t, capturedMessages, 3)
}

func TestAutoMemoryWorker_PendingMessages_ClearedAfterExtraction(t *testing.T) {
	extractCount := 0
	var capturedMessages []model.Message
	ext := &mockExtractorWithCapture{
		shouldExtract: false,
		captureCtx: func(ctx *extractor.ExtractionContext) {
			capturedMessages = ctx.Messages
		},
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)

	userKey := memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}

	// First two turns - not extracted.
	_ = worker.EnqueueJob(context.Background(), userKey, []model.Message{
		model.NewUserMessage("msg1"),
	})
	_ = worker.EnqueueJob(context.Background(), userKey, []model.Message{
		model.NewUserMessage("msg2"),
	})
	assert.Len(t, capturedMessages, 2)

	// Now enable extraction.
	ext.shouldExtract = true
	ext.captureCtx = func(ctx *extractor.ExtractionContext) {
		capturedMessages = ctx.Messages
		extractCount++
	}

	// Third turn - should extract all accumulated messages.
	_ = worker.EnqueueJob(context.Background(), userKey, []model.Message{
		model.NewUserMessage("msg3"),
	})
	assert.Len(t, capturedMessages, 3)
	assert.Equal(t, "msg1", capturedMessages[0].Content)
	assert.Equal(t, "msg2", capturedMessages[1].Content)
	assert.Equal(t, "msg3", capturedMessages[2].Content)

	// Fourth turn - pending should be cleared, only new message.
	_ = worker.EnqueueJob(context.Background(), userKey, []model.Message{
		model.NewUserMessage("msg4"),
	})
	assert.Len(t, capturedMessages, 1)
	assert.Equal(t, "msg4", capturedMessages[0].Content)
}

func TestAutoMemoryWorker_PendingMessages_PerUser(t *testing.T) {
	var capturedMessages []model.Message
	ext := &mockExtractorWithCapture{
		shouldExtract: false,
		captureCtx: func(ctx *extractor.ExtractionContext) {
			capturedMessages = ctx.Messages
		},
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)

	// User 1 accumulates messages.
	_ = worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "app",
		UserID:  "user-1",
	}, []model.Message{model.NewUserMessage("user1-msg1")})
	assert.Len(t, capturedMessages, 1)

	_ = worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "app",
		UserID:  "user-1",
	}, []model.Message{model.NewUserMessage("user1-msg2")})
	assert.Len(t, capturedMessages, 2)

	// User 2 has its own pending messages.
	_ = worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "app",
		UserID:  "user-2",
	}, []model.Message{model.NewUserMessage("user2-msg1")})
	assert.Len(t, capturedMessages, 1) // Only user2's message.
	assert.Equal(t, "user2-msg1", capturedMessages[0].Content)

	// User 1 still has accumulated messages.
	_ = worker.EnqueueJob(context.Background(), memory.UserKey{
		AppName: "app",
		UserID:  "user-1",
	}, []model.Message{model.NewUserMessage("user1-msg3")})
	assert.Len(t, capturedMessages, 3) // user1's 3 messages.
}
