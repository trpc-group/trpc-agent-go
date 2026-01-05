//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type mockSummarizer struct {
	shouldSummarize bool
	summaryText     string
	err             error
}

func (m *mockSummarizer) ShouldSummarize(_ *session.Session) bool {
	return m.shouldSummarize
}

func (m *mockSummarizer) Summarize(_ context.Context, _ *session.Session) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.summaryText, nil
}

func (m *mockSummarizer) FilterEventsForSummary(events []event.Event) []event.Event {
	return events
}

func (m *mockSummarizer) SetPrompt(prompt string) {}

func (m *mockSummarizer) SetModel(mdl model.Model) {}

func (m *mockSummarizer) Metadata() map[string]any { return nil }

func TestNewAsyncSummaryWorker(t *testing.T) {
	summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
	config := AsyncSummaryConfig{
		Summarizer:        summarizer,
		AsyncSummaryNum:   2,
		SummaryQueueSize:  10,
		SummaryJobTimeout: time.Second,
		CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
			return nil
		},
	}

	worker := NewAsyncSummaryWorker(config)
	require.NotNil(t, worker)
	assert.Equal(t, config.Summarizer, worker.config.Summarizer)
	assert.Equal(t, config.AsyncSummaryNum, worker.config.AsyncSummaryNum)
	assert.Equal(t, config.SummaryQueueSize, worker.config.SummaryQueueSize)
	assert.Equal(t, config.SummaryJobTimeout, worker.config.SummaryJobTimeout)
	assert.NotNil(t, worker.config.CreateSummaryFunc)
	assert.False(t, worker.started)
	assert.Nil(t, worker.jobChans)
}

func TestAsyncSummaryWorker_Start(t *testing.T) {
	t.Run("start successfully", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   3,
			SummaryQueueSize:  5,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()

		assert.True(t, worker.started)
		assert.Len(t, worker.jobChans, 3)
		for i, ch := range worker.jobChans {
			assert.NotNil(t, ch, "Channel %d should not be nil", i)
			assert.Equal(t, 5, cap(ch), "Channel %d should have capacity 5", i)
		}

		worker.Stop()
	})

	t.Run("start with nil summarizer", func(t *testing.T) {
		config := AsyncSummaryConfig{
			Summarizer:        nil,
			AsyncSummaryNum:   2,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()

		assert.False(t, worker.started)
		assert.Nil(t, worker.jobChans)
	})

	t.Run("start with default values", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   0, // Should default to 1
			SummaryQueueSize:  0, // Should default to 10
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()

		assert.True(t, worker.started)
		assert.Len(t, worker.jobChans, 1)
		assert.Equal(t, 10, cap(worker.jobChans[0]))

		worker.Stop()
	})

	t.Run("start multiple times", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   2,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()
		chans1 := worker.jobChans

		worker.Start() // Second call should not recreate channels
		chans2 := worker.jobChans

		assert.Equal(t, chans1, chans2, "Channels should not be recreated")

		worker.Stop()
	})
}

func TestAsyncSummaryWorker_Stop(t *testing.T) {
	t.Run("stop successfully", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   2,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()
		assert.True(t, worker.started)

		worker.Stop()
		assert.False(t, worker.started)
		assert.Nil(t, worker.jobChans)
	})

	t.Run("stop when not started", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   2,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		require.NotPanics(t, func() {
			worker.Stop()
		})
	})

	t.Run("stop multiple times", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   2,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()
		worker.Stop()

		require.NotPanics(t, func() {
			worker.Stop()
			worker.Stop()
		})
	})
}

func TestAsyncSummaryWorker_EnqueueJob(t *testing.T) {
	t.Run("enqueue successfully", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		var mu sync.Mutex
		var createSummaryCalled bool
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   2,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				mu.Lock()
				createSummaryCalled = true
				mu.Unlock()
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()
		defer worker.Stop()

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
		}

		err := worker.EnqueueJob(context.Background(), sess, "", false)
		require.NoError(t, err)

		// Wait for async processing
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		assert.True(t, createSummaryCalled)
		mu.Unlock()
	})

	t.Run("enqueue with nil summarizer", func(t *testing.T) {
		config := AsyncSummaryConfig{
			Summarizer:        nil,
			AsyncSummaryNum:   2,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
		}

		err := worker.EnqueueJob(context.Background(), sess, "", false)
		require.NoError(t, err)
	})

	t.Run("enqueue with nil session", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   2,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		err := worker.EnqueueJob(context.Background(), nil, "", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nil session")
	})

	t.Run("enqueue with invalid session key", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   2,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		sess := &session.Session{
			ID:      "", // Invalid: empty session ID
			AppName: "test-app",
			UserID:  "test-user",
		}

		err := worker.EnqueueJob(context.Background(), sess, "", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "check session key failed")
	})

	t.Run("enqueue when not started - fallback to sync", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		var mu sync.Mutex
		var createSummaryCalled bool
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   2,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				mu.Lock()
				createSummaryCalled = true
				mu.Unlock()
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		// Don't call Start()

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
		}

		err := worker.EnqueueJob(context.Background(), sess, "", false)
		require.NoError(t, err)
		mu.Lock()
		assert.True(t, createSummaryCalled, "Should fallback to synchronous processing")
		mu.Unlock()
	})

	t.Run("enqueue when queue full - fallback to sync", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		var mu sync.Mutex
		var syncCalled bool
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   1,
			SummaryQueueSize:  1, // Small queue
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				mu.Lock()
				syncCalled = true
				mu.Unlock()
				time.Sleep(10 * time.Millisecond) // Block to fill queue
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()
		defer worker.Stop()

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
		}

		// Fill the queue
		_ = worker.EnqueueJob(context.Background(), sess, "", false)

		// This should fallback to sync
		mu.Lock()
		syncCalled = false
		mu.Unlock()
		err := worker.EnqueueJob(context.Background(), sess, "", false)
		require.NoError(t, err)
		mu.Lock()
		assert.True(t, syncCalled, "Should fallback to synchronous processing when queue is full")
		mu.Unlock()
	})

	t.Run("enqueue with cancelled context", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   1,
			SummaryQueueSize:  1,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()
		defer worker.Stop()

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
		}

		// Fill the queue
		_ = worker.EnqueueJob(context.Background(), sess, "", false)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		err := worker.EnqueueJob(ctx, sess, "", false)
		require.NoError(t, err) // Should fallback to sync, not error
	})

	t.Run("enqueue with filter key", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		filterKeyCh := make(chan string, 10)
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   2,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(_ context.Context, _ *session.Session, fk string, _ bool) error {
				filterKeyCh <- fk
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()
		defer worker.Stop()

		now := time.Now()
		// Use NewSession to properly initialize Hash field.
		sess := session.NewSession("test-app", "test-user", "test-session")
		// Add events with multiple filterKeys to ensure cascade creates both summaries.
		// Set Version to CurrentVersion so Filter() uses FilterKey instead of Branch.
		sess.Events = []event.Event{
			{FilterKey: "branch1", Timestamp: now.Add(-2 * time.Minute), Version: event.CurrentVersion,
				Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "e1"}}}}},
			{FilterKey: "branch2", Timestamp: now.Add(-1 * time.Minute), Version: event.CurrentVersion,
				Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "e2"}}}}},
		}

		err := worker.EnqueueJob(context.Background(), sess, "branch1", false)
		require.NoError(t, err)

		// Should create both branch1 and full-session summary (cascade).
		seenBranch := false
		seenFull := false
		assert.Eventually(t, func() bool {
			select {
			case fk := <-filterKeyCh:
				switch fk {
				case "branch1":
					seenBranch = true
				case "":
					seenFull = true
				}
			default:
			}
			return seenBranch && seenFull
		}, 2*time.Second, 10*time.Millisecond, "expected CreateSummaryFunc to be called for both branch and full-session summaries")
	})
}

func TestAsyncSummaryWorker_ProcessJob(t *testing.T) {
	t.Run("process job successfully", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		var mu sync.Mutex
		var createSummaryCalled bool
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   1,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				mu.Lock()
				createSummaryCalled = true
				mu.Unlock()
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()
		defer worker.Stop()

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
		}

		err := worker.EnqueueJob(context.Background(), sess, "", false)
		require.NoError(t, err)

		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		assert.True(t, createSummaryCalled)
		mu.Unlock()
	})

	t.Run("process job with error", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   1,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				return errors.New("create summary failed")
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()
		defer worker.Stop()

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
		}

		err := worker.EnqueueJob(context.Background(), sess, "", false)
		require.NoError(t, err) // Enqueue should succeed

		// Wait for processing - should not panic
		time.Sleep(50 * time.Millisecond)
	})

	t.Run("process job with timeout", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   1,
			SummaryQueueSize:  10,
			SummaryJobTimeout: 10 * time.Millisecond,
			CreateSummaryFunc: func(ctx context.Context, _ *session.Session, _ string, _ bool) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
					return nil
				}
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()
		defer worker.Stop()

		sess := &session.Session{
			ID:      "test-session",
			AppName: "test-app",
			UserID:  "test-user",
		}

		err := worker.EnqueueJob(context.Background(), sess, "", false)
		require.NoError(t, err)

		// Wait for processing - should timeout but not panic
		time.Sleep(100 * time.Millisecond)
	})

	t.Run("process job with nil context", func(t *testing.T) {
		summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
		var mu sync.Mutex
		var createSummaryCalled bool
		config := AsyncSummaryConfig{
			Summarizer:        summarizer,
			AsyncSummaryNum:   1,
			SummaryQueueSize:  10,
			SummaryJobTimeout: time.Second,
			CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
				mu.Lock()
				createSummaryCalled = true
				mu.Unlock()
				return nil
			},
		}

		worker := NewAsyncSummaryWorker(config)
		worker.Start()
		defer worker.Stop()

		// Manually create a job with nil context
		job := &summaryJob{
			ctx:       nil,
			filterKey: "",
			force:     false,
			session: &session.Session{
				ID:      "test-session",
				AppName: "test-app",
				UserID:  "test-user",
			},
		}

		// Manually enqueue to test nil context handling
		worker.jobChans[0] <- job

		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		assert.True(t, createSummaryCalled)
		mu.Unlock()
	})
}

func TestAsyncSummaryWorker_ConcurrentEnqueue(t *testing.T) {
	summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
	var mu sync.Mutex
	var callCount int
	config := AsyncSummaryConfig{
		Summarizer:        summarizer,
		AsyncSummaryNum:   3,
		SummaryQueueSize:  10,
		SummaryJobTimeout: time.Second,
		CreateSummaryFunc: func(context.Context, *session.Session, string, bool) error {
			mu.Lock()
			callCount++
			mu.Unlock()
			return nil
		},
	}

	worker := NewAsyncSummaryWorker(config)
	worker.Start()
	defer worker.Stop()

	const numJobs = 20
	var wg sync.WaitGroup
	wg.Add(numJobs)

	for i := 0; i < numJobs; i++ {
		go func(i int) {
			defer wg.Done()
			sess := &session.Session{
				ID:      "test-session",
				AppName: "test-app",
				UserID:  "test-user",
			}
			err := worker.EnqueueJob(context.Background(), sess, "", false)
			assert.NoError(t, err)
		}(i)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond) // Wait for processing

	mu.Lock()
	assert.GreaterOrEqual(t, callCount, numJobs)
	mu.Unlock()
}

func TestAsyncSummaryWorker_EnqueueJob_RaceWithStop(t *testing.T) {
	summarizer := &mockSummarizer{shouldSummarize: true, summaryText: "test"}
	var mu sync.Mutex
	callCount := 0

	config := AsyncSummaryConfig{
		Summarizer:        summarizer,
		AsyncSummaryNum:   1,
		SummaryQueueSize:  10,
		SummaryJobTimeout: time.Second,
		CreateSummaryFunc: func(ctx context.Context, sess *session.Session, filterKey string, force bool) error {
			mu.Lock()
			callCount++
			mu.Unlock()
			return nil
		},
	}

	worker := NewAsyncSummaryWorker(config)
	worker.Start()

	sess := &session.Session{
		ID:      "test-session",
		AppName: "test-app",
		UserID:  "test-user",
	}

	// Concurrent EnqueueJob and Stop calls.
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = worker.EnqueueJob(context.Background(), sess, "", false)
		}()
		go func() {
			defer wg.Done()
			worker.Stop()
		}()
		// Restart for next iteration.
		time.Sleep(1 * time.Millisecond)
		worker.Start()
	}
	wg.Wait()
}
