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

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func newTestSession(appName, userID string) *session.Session {
	return session.NewSession(appName, userID, "test-session")
}

func appendSessionMessage(sess *session.Session, ts time.Time, msg model.Message) {
	sess.Events = append(sess.Events, event.Event{
		Timestamp: ts,
		Response:  &model.Response{Choices: []model.Choice{{Message: msg}}},
	})
}

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

	sess := newTestSession("test-app", "user-1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("hello"))

	err := worker.EnqueueJob(context.Background(), sess)

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
	sess1 := newTestSession("", "user-1")
	appendSessionMessage(sess1, time.Now(), model.NewUserMessage("hello"))
	err := worker.EnqueueJob(context.Background(), sess1)
	assert.NoError(t, err)

	// Empty UserID.
	sess2 := newTestSession("test-app", "")
	appendSessionMessage(sess2, time.Now(), model.NewUserMessage("hello"))
	err = worker.EnqueueJob(context.Background(), sess2)
	assert.NoError(t, err)
}

func TestAutoMemoryWorker_EnqueueJob_EmptyMessages(t *testing.T) {
	ext := &mockExtractor{}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor: ext,
	}

	worker := NewAutoMemoryWorker(config, op)

	sess := newTestSession("test-app", "user-1")
	err := worker.EnqueueJob(context.Background(), sess)

	assert.NoError(t, err)
}

func TestScanDeltaSince_SkipsToolMessages(t *testing.T) {
	const (
		userOffset       = time.Second
		toolCallOffset   = 2 * time.Second
		toolResultOffset = 3 * time.Second
		assistOffset     = 4 * time.Second
	)

	sess := newTestSession("test-app", "user-1")
	base := time.Now()

	appendSessionMessage(sess, base.Add(userOffset), model.NewUserMessage("who am I"))
	appendSessionMessage(sess, base.Add(toolCallOffset), model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   "call_1",
			Function: model.FunctionDefinitionParam{
				Name:      memory.SearchToolName,
				Arguments: []byte("{}"),
			},
		}},
	})
	appendSessionMessage(sess, base.Add(toolResultOffset),
		model.NewToolMessage("call_1", memory.SearchToolName, "{\"count\":0}"))
	appendSessionMessage(sess, base.Add(assistOffset), model.NewAssistantMessage("answer"))

	latestTs, msgs := scanDeltaSince(sess, time.Time{})
	require.Equal(t, base.Add(assistOffset), latestTs)
	require.Len(t, msgs, 2)
	assert.Equal(t, model.RoleUser, msgs[0].Role)
	assert.Equal(t, model.RoleAssistant, msgs[1].Role)
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

	sess := newTestSession("test-app", "user-1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("hello"))

	err := worker.EnqueueJob(context.Background(), sess)

	assert.NoError(t, err)
	assert.Equal(t, 1, op.addCalls)
}

func TestAutoMemoryWorker_EnqueueJob_SyncFallback_CancelledContext(t *testing.T) {
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
	// Do not start the worker, so it would fall back to sync.

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	sess := newTestSession("test-app", "user-1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("hello"))

	err := worker.EnqueueJob(ctx, sess)

	// Should skip sync fallback when context is cancelled.
	assert.NoError(t, err)
	assert.Equal(t, 0, op.addCalls)
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

	sess := newTestSession("test-app", "user-1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("hello"))

	err := worker.EnqueueJob(context.Background(), sess)

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
	sess1 := newTestSession("test-app", "user-1")
	appendSessionMessage(sess1, time.Now(), model.NewUserMessage("hello"))
	_ = worker.EnqueueJob(context.Background(), sess1)

	// Wait a bit for the worker to pick up the job.
	time.Sleep(10 * time.Millisecond)

	// Second job fills the queue.
	sess2 := newTestSession("test-app", "user-1")
	appendSessionMessage(sess2, time.Now(), model.NewUserMessage("hello"))
	_ = worker.EnqueueJob(context.Background(), sess2)

	// Third job should fall back to sync (queue is full).
	// Since blockingExt is still blocking, the sync fallback will also block,
	// so we run it in a goroutine and verify the queue was full.
	syncDone := make(chan struct{})
	go func() {
		sess3 := newTestSession("test-app", "user-2")
		appendSessionMessage(sess3, time.Now(), model.NewUserMessage("hello"))
		_ = worker.EnqueueJob(context.Background(), sess3)
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

func TestAutoMemoryWorker_ExecuteOperation_DisabledByEnabledTools(t *testing.T) {
	userKey := memory.UserKey{AppName: "test-app", UserID: "user-1"}

	t.Run("clear disabled", func(t *testing.T) {
		op := newMockOperator()
		worker := &AutoMemoryWorker{
			config: AutoMemoryConfig{
				EnabledTools: map[string]struct{}{
					memory.AddToolName:    {},
					memory.UpdateToolName: {},
					memory.DeleteToolName: {},
				},
			},
			operator: op,
		}
		worker.executeOperation(
			context.Background(), userKey,
			&extractor.Operation{Type: extractor.OperationClear},
		)
		assert.Equal(t, 0, op.clearCalls)
	})

	t.Run("add disabled", func(t *testing.T) {
		op := newMockOperator()
		worker := &AutoMemoryWorker{
			config: AutoMemoryConfig{
				EnabledTools: map[string]struct{}{},
			},
			operator: op,
		}
		worker.executeOperation(
			context.Background(), userKey,
			&extractor.Operation{
				Type:   extractor.OperationAdd,
				Memory: "should be skipped",
			},
		)
		assert.Equal(t, 0, op.addCalls)
	})

	t.Run("enabled tools allows operation", func(t *testing.T) {
		op := newMockOperator()
		worker := &AutoMemoryWorker{
			config: AutoMemoryConfig{
				EnabledTools: map[string]struct{}{
					memory.AddToolName: {},
				},
			},
			operator: op,
		}
		worker.executeOperation(
			context.Background(), userKey,
			&extractor.Operation{
				Type:   extractor.OperationAdd,
				Memory: "allowed",
			},
		)
		assert.Equal(t, 1, op.addCalls)
	})

	t.Run("nil enabled tools allows all", func(t *testing.T) {
		op := newMockOperator()
		worker := &AutoMemoryWorker{
			config:   AutoMemoryConfig{},
			operator: op,
		}
		worker.executeOperation(
			context.Background(), userKey,
			&extractor.Operation{Type: extractor.OperationClear},
		)
		assert.Equal(t, 1, op.clearCalls)
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

	sess := newTestSession("test-app", "user-1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("I love coffee."))
	err := worker.EnqueueJob(context.Background(), sess)

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
			sess := newTestSession("test-app", fmt.Sprintf("user-%d", id))
			appendSessionMessage(sess, time.Now(), model.NewUserMessage("hello"))
			_ = worker.EnqueueJob(context.Background(), sess)
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
	sess := newTestSession("test-app", "user-1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("hello"))

	err := worker.EnqueueJob(context.Background(), sess)

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
	sess := newTestSession("test-app", "user-1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("hello"))

	err := worker.EnqueueJob(context.Background(), sess)

	require.NoError(t, err)

	// Give time for async processing.
	time.Sleep(50 * time.Millisecond)

	// Extract should be called since ShouldExtract returns true.
	assert.Equal(t, 1, ext.getExtractCalls())
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

func TestAutoMemoryWorker_DeltaMessages_UsesTimestamp(t *testing.T) {
	var capturedMessageCount int
	var capturedLastExtractAt *time.Time
	ext := &mockExtractorWithCapture{
		shouldExtract: false,
		captureCtx: func(ctx *extractor.ExtractionContext) {
			capturedMessageCount = len(ctx.Messages)
			capturedLastExtractAt = ctx.LastExtractAt
		},
	}
	op := newMockOperator()
	config := AutoMemoryConfig{Extractor: ext}

	worker := NewAutoMemoryWorker(config, op)

	sess := newTestSession("test-app", "user-1")
	t1 := time.Now().Add(-2 * time.Minute)
	t2 := t1.Add(time.Minute)
	appendSessionMessage(sess, t1, model.NewUserMessage("hello"))
	appendSessionMessage(sess, t2, model.NewAssistantMessage("world"))

	err := worker.EnqueueJob(context.Background(), sess)
	assert.NoError(t, err)
	assert.Equal(t, 2, capturedMessageCount)
	assert.Nil(t, capturedLastExtractAt)

	sess.SetState(memory.SessionStateKeyAutoMemoryLastExtractAt,
		[]byte(t1.UTC().Format(time.RFC3339Nano)))
	err = worker.EnqueueJob(context.Background(), sess)
	assert.NoError(t, err)
	assert.Equal(t, 1, capturedMessageCount)
	require.NotNil(t, capturedLastExtractAt)
	assert.True(t, capturedLastExtractAt.Equal(t1.UTC()))
}

func TestAutoMemoryWorker_EnqueueJob_NilSession(t *testing.T) {
	ext := &mockExtractor{}
	op := newMockOperator()
	config := AutoMemoryConfig{Extractor: ext}
	worker := NewAutoMemoryWorker(config, op)

	err := worker.EnqueueJob(context.Background(), nil)

	assert.NoError(t, err)
	assert.Equal(t, 0, op.addCalls)
}

func TestAutoMemoryWorker_EnqueueJob_SyncFallback_Error(t *testing.T) {
	ext := &mockExtractor{
		err: errors.New("extract error"),
	}
	op := newMockOperator()
	config := AutoMemoryConfig{Extractor: ext}

	// Do not start the worker, so it falls back to sync.
	worker := NewAutoMemoryWorker(config, op)

	sess := newTestSession("test-app", "user-1")
	appendSessionMessage(sess, time.Now(), model.NewUserMessage("hello"))

	err := worker.EnqueueJob(context.Background(), sess)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "extract failed")
}

func TestAutoMemoryWorker_ProcessJob_CreateAutoMemoryError(t *testing.T) {
	ext := &mockExtractor{
		err: errors.New("extract error"),
	}
	op := newMockOperator()
	config := AutoMemoryConfig{
		Extractor:        ext,
		MemoryJobTimeout: time.Second,
	}

	worker := NewAutoMemoryWorker(config, op)

	sess := newTestSession("test-app", "user-1")

	// Should not panic, error is logged internally.
	worker.processJob(&MemoryJob{
		Ctx:     context.Background(),
		UserKey: memory.UserKey{AppName: "test-app", UserID: "user-1"},
		Session: sess,
		Messages: []model.Message{
			model.NewUserMessage("hello"),
		},
	})

	// lastExtractAt should NOT be written on failure.
	_, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	assert.False(t, ok)
}

func TestAutoMemoryWorker_CreateAutoMemory_NilExtractor(t *testing.T) {
	op := newMockOperator()
	worker := &AutoMemoryWorker{
		config:   AutoMemoryConfig{Extractor: nil},
		operator: op,
	}

	err := worker.createAutoMemory(
		context.Background(),
		memory.UserKey{AppName: "test-app", UserID: "user-1"},
		[]model.Message{model.NewUserMessage("hello")},
	)

	assert.NoError(t, err)
	assert.Equal(t, 0, op.addCalls)
}

func TestIsMemoryNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "memory not found",
			err:      fmt.Errorf("memory with id abc123 not found"),
			expected: true,
		},
		{
			name:     "other error",
			err:      errors.New("connection refused"),
			expected: false,
		},
		{
			name:     "partial match - missing marker",
			err:      fmt.Errorf("memory with id abc123 exists"),
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, isMemoryNotFoundError(tt.err))
		})
	}
}

func TestAutoMemoryWorker_ExecuteOperation_UpdateNotFound_FallbackToAdd(t *testing.T) {
	op := newMockOperator()
	op.updateErr = fmt.Errorf("memory with id mem-123 not found")
	worker := &AutoMemoryWorker{operator: op}

	worker.executeOperation(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, &extractor.Operation{
		Type:     extractor.OperationUpdate,
		MemoryID: "mem-123",
		Memory:   "Updated memory.",
		Topics:   []string{"topic1"},
	})

	// Update fails with not-found, should fallback to add.
	assert.Equal(t, 1, op.addCalls)
}

func TestAutoMemoryWorker_ExecuteOperation_UpdateNotFound_AddAlsoFails(t *testing.T) {
	op := newMockOperator()
	op.updateErr = fmt.Errorf("memory with id mem-123 not found")
	op.addErr = errors.New("add also failed")
	worker := &AutoMemoryWorker{operator: op}

	// Should not panic; errors are logged.
	worker.executeOperation(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, &extractor.Operation{
		Type:     extractor.OperationUpdate,
		MemoryID: "mem-123",
		Memory:   "Updated memory.",
	})
}

func TestAutoMemoryWorker_ExecuteOperation_ClearError(t *testing.T) {
	op := newMockOperator()
	op.clearErr = errors.New("clear error")
	worker := &AutoMemoryWorker{operator: op}

	// Should not panic; error is logged.
	worker.executeOperation(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, &extractor.Operation{
		Type: extractor.OperationClear,
	})
}

func TestAutoMemoryWorker_ExecuteOperation_ClearSuccess(t *testing.T) {
	op := newMockOperator()
	worker := &AutoMemoryWorker{operator: op}

	worker.executeOperation(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, &extractor.Operation{
		Type: extractor.OperationClear,
	})

	assert.Equal(t, 1, op.clearCalls)
}

func TestReadLastExtractAt_ParseError(t *testing.T) {
	sess := newTestSession("test-app", "user-1")
	sess.SetState(memory.SessionStateKeyAutoMemoryLastExtractAt,
		[]byte("not-a-valid-timestamp"))

	ts := readLastExtractAt(sess)

	assert.True(t, ts.IsZero())
}

func TestScanDeltaSince_NilResponse(t *testing.T) {
	sess := newTestSession("test-app", "user-1")
	now := time.Now()

	// Add an event with nil response.
	sess.Events = append(sess.Events, event.Event{
		Timestamp: now,
		Response:  nil,
	})
	// Add a normal event after it.
	appendSessionMessage(sess, now.Add(time.Second),
		model.NewUserMessage("hello"))

	latestTs, msgs := scanDeltaSince(sess, time.Time{})

	assert.Equal(t, now.Add(time.Second), latestTs)
	require.Len(t, msgs, 1)
	assert.Equal(t, "hello", msgs[0].Content)
}

func TestScanDeltaSince_SkipsMessagesWithToolCalls(t *testing.T) {
	sess := newTestSession("test-app", "user-1")
	now := time.Now()

	// Add a message with ToolCalls but also has content.
	appendSessionMessage(sess, now, model.Message{
		Role:    model.RoleAssistant,
		Content: "I'll search for that.",
		ToolCalls: []model.ToolCall{{
			Type: "function",
			ID:   "call_1",
			Function: model.FunctionDefinitionParam{
				Name:      "search",
				Arguments: []byte("{}"),
			},
		}},
	})
	// Add a normal message.
	appendSessionMessage(sess, now.Add(time.Second),
		model.NewAssistantMessage("Here is the result."))

	_, msgs := scanDeltaSince(sess, time.Time{})

	// Only the normal message should be included.
	require.Len(t, msgs, 1)
	assert.Equal(t, "Here is the result.", msgs[0].Content)
}

func TestScanDeltaSince_ContentParts(t *testing.T) {
	sess := newTestSession("test-app", "user-1")
	now := time.Now()

	// Add a message with ContentParts but no Content string.
	textContent := "hi"
	appendSessionMessage(sess, now, model.Message{
		Role:         model.RoleUser,
		ContentParts: []model.ContentPart{{Type: "text", Text: &textContent}},
	})

	_, msgs := scanDeltaSince(sess, time.Time{})

	require.Len(t, msgs, 1)
	assert.Equal(t, model.RoleUser, msgs[0].Role)
	assert.Len(t, msgs[0].ContentParts, 1)
}

func TestScanDeltaSince_EmptyContentSkipped(t *testing.T) {
	sess := newTestSession("test-app", "user-1")
	now := time.Now()

	// Message with no content and no content parts.
	appendSessionMessage(sess, now, model.Message{
		Role: model.RoleAssistant,
	})

	_, msgs := scanDeltaSince(sess, time.Time{})

	assert.Empty(t, msgs)
}

func TestAutoMemoryWorker_WritesLastExtractAt_OnSuccess(t *testing.T) {
	ext := &mockExtractor{}
	op := newMockOperator()
	config := AutoMemoryConfig{Extractor: ext}

	worker := NewAutoMemoryWorker(config, op)

	sess := newTestSession("test-app", "user-1")
	t1 := time.Now().Add(-2 * time.Minute)
	t2 := t1.Add(time.Minute)
	appendSessionMessage(sess, t1, model.NewUserMessage("m1"))
	appendSessionMessage(sess, t2, model.NewAssistantMessage("m2"))

	err := worker.EnqueueJob(context.Background(), sess)
	assert.NoError(t, err)

	raw, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	require.True(t, ok)
	require.NotEmpty(t, raw)

	ts, parseErr := time.Parse(time.RFC3339Nano, string(raw))
	require.NoError(t, parseErr)
	assert.True(t, ts.Equal(t2.UTC()))
}

// configurableExtractor is a mock extractor implementing
// EnabledToolsConfigurer for testing.
type configurableExtractor struct {
	mockExtractor
	enabledTools map[string]struct{}
}

func (e *configurableExtractor) SetEnabledTools(
	enabled map[string]struct{},
) {
	e.enabledTools = enabled
}

func TestConfigureExtractorEnabledTools(t *testing.T) {
	t.Run("configurer receives enabled tools", func(t *testing.T) {
		ext := &configurableExtractor{}
		enabled := map[string]struct{}{
			memory.AddToolName: {},
		}
		ConfigureExtractorEnabledTools(ext, enabled)
		assert.Equal(t, enabled, ext.enabledTools)
	})

	t.Run("non-configurer is no-op", func(t *testing.T) {
		ext := &mockExtractor{}
		// Should not panic.
		ConfigureExtractorEnabledTools(ext, map[string]struct{}{
			memory.AddToolName: {},
		})
	})
}

func TestAutoMemoryWorker_IsToolEnabled(t *testing.T) {
	tests := []struct {
		name         string
		enabledTools map[string]struct{}
		toolName     string
		expected     bool
	}{
		{
			name:         "nil map allows all",
			enabledTools: nil,
			toolName:     memory.AddToolName,
			expected:     true,
		},
		{
			name:         "empty map allows all",
			enabledTools: map[string]struct{}{},
			toolName:     memory.AddToolName,
			expected:     true,
		},
		{
			name: "tool present in allow-list",
			enabledTools: map[string]struct{}{
				memory.AddToolName:    {},
				memory.SearchToolName: {},
			},
			toolName: memory.AddToolName,
			expected: true,
		},
		{
			name: "tool absent from allow-list",
			enabledTools: map[string]struct{}{
				memory.SearchToolName: {},
			},
			toolName: memory.AddToolName,
			expected: false,
		},
		{
			name: "delete disabled",
			enabledTools: map[string]struct{}{
				memory.AddToolName:    {},
				memory.UpdateToolName: {},
			},
			toolName: memory.DeleteToolName,
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &AutoMemoryWorker{
				config: AutoMemoryConfig{
					EnabledTools: tt.enabledTools,
				},
			}
			assert.Equal(t, tt.expected,
				w.isToolEnabled(tt.toolName))
		})
	}
}

func TestAutoMemoryWorker_ExecuteOperation_UpdateNotFound_AddDisabled(t *testing.T) {
	op := newMockOperator()
	op.updateErr = fmt.Errorf("memory with id mem-456 not found")
	worker := &AutoMemoryWorker{
		config: AutoMemoryConfig{
			// Only update and search enabled; add is NOT.
			EnabledTools: map[string]struct{}{
				memory.UpdateToolName: {},
				memory.SearchToolName: {},
			},
		},
		operator: op,
	}

	worker.executeOperation(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, &extractor.Operation{
		Type:     extractor.OperationUpdate,
		MemoryID: "mem-456",
		Memory:   "Updated memory.",
		Topics:   []string{"topic1"},
	})

	// Fallback add should be skipped because add is disabled.
	assert.Equal(t, 0, op.addCalls)
}

func TestAutoMemoryWorker_ExecuteOperation_UpdateNotFound_AddEnabled(t *testing.T) {
	op := newMockOperator()
	op.updateErr = fmt.Errorf("memory with id mem-789 not found")
	worker := &AutoMemoryWorker{
		config: AutoMemoryConfig{
			// Both update and add are enabled.
			EnabledTools: map[string]struct{}{
				memory.UpdateToolName: {},
				memory.AddToolName:    {},
			},
		},
		operator: op,
	}

	worker.executeOperation(context.Background(), memory.UserKey{
		AppName: "test-app",
		UserID:  "user-1",
	}, &extractor.Operation{
		Type:     extractor.OperationUpdate,
		MemoryID: "mem-789",
		Memory:   "Updated memory.",
		Topics:   []string{"topic1"},
	})

	// Fallback add should proceed because add is enabled.
	assert.Equal(t, 1, op.addCalls)
}
