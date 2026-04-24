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
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewEventTunnel(t *testing.T) {
	tests := []struct {
		name          string
		batchSize     int
		flushInterval time.Duration
		expectedBatch int
		expectedFlush time.Duration
	}{
		{
			name:          "valid parameters",
			batchSize:     10,
			flushInterval: 500 * time.Millisecond,
			expectedBatch: 10,
			expectedFlush: 500 * time.Millisecond,
		},
		{
			name:          "zero batch size uses default",
			batchSize:     0,
			flushInterval: 100 * time.Millisecond,
			expectedBatch: defaultBatchSize,
			expectedFlush: 100 * time.Millisecond,
		},
		{
			name:          "negative batch size uses default",
			batchSize:     -5,
			flushInterval: 100 * time.Millisecond,
			expectedBatch: defaultBatchSize,
			expectedFlush: 100 * time.Millisecond,
		},
		{
			name:          "zero flush interval uses default",
			batchSize:     3,
			flushInterval: 0,
			expectedBatch: 3,
			expectedFlush: defaultFlushInterval,
		},
		{
			name:          "negative flush interval uses default",
			batchSize:     3,
			flushInterval: -100 * time.Millisecond,
			expectedBatch: 3,
			expectedFlush: defaultFlushInterval,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			produce := func(context.Context) (*event.Event, bool) { return nil, false }
			consume := func([]*event.Event) (bool, error) { return true, nil }

			tunnel := newEventTunnel(tt.batchSize, tt.flushInterval, produce, consume)

			if tunnel.batchSize != tt.expectedBatch {
				t.Errorf("newEventTunnel() batchSize = %v, want %v", tunnel.batchSize, tt.expectedBatch)
			}
			if tunnel.flushInterval != tt.expectedFlush {
				t.Errorf("newEventTunnel() flushInterval = %v, want %v", tunnel.flushInterval, tt.expectedFlush)
			}
			if tunnel.produce == nil {
				t.Error("newEventTunnel() produce function should not be nil")
			}
			if tunnel.consume == nil {
				t.Error("newEventTunnel() consume function should not be nil")
			}
			if len(tunnel.batch) != 0 {
				t.Errorf("newEventTunnel() initial batch length = %v, want 0", len(tunnel.batch))
			}
			if cap(tunnel.batch) != tt.expectedBatch {
				t.Errorf("newEventTunnel() batch capacity = %v, want %v", cap(tunnel.batch), tt.expectedBatch)
			}
		})
	}
}

func TestEventTunnel_Run_BasicFlow(t *testing.T) {
	tests := []struct {
		name            string
		events          []*event.Event
		batchSize       int
		expectedBatches int
		expectError     bool
	}{
		{
			name: "single event",
			events: []*event.Event{
				createTestEvent("event1", false),
			},
			batchSize:       5,
			expectedBatches: 1,
			expectError:     false,
		},
		{
			name: "multiple events within batch size",
			events: []*event.Event{
				createTestEvent("event1", false),
				createTestEvent("event2", false),
				createTestEvent("event3", true), // final event
			},
			batchSize:       5,
			expectedBatches: 1,
			expectError:     false,
		},
		{
			name: "events exceeding batch size",
			events: []*event.Event{
				createTestEvent("event1", false),
				createTestEvent("event2", false),
				createTestEvent("event3", false),
				createTestEvent("event4", false),
				createTestEvent("event5", false),
				createTestEvent("event6", true), // final event
			},
			batchSize:       3,
			expectedBatches: 2,
			expectError:     false,
		},
		{
			name:            "no events",
			events:          []*event.Event{},
			batchSize:       5,
			expectedBatches: 0,
			expectError:     false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var receivedBatches [][]*event.Event
			var mu sync.Mutex

			eventIndex := 0
			produce := func(context.Context) (*event.Event, bool) {
				if eventIndex >= len(tt.events) {
					return nil, false
				}
				event := tt.events[eventIndex]
				eventIndex++
				return event, true
			}

			consume := func(batch []*event.Event) (bool, error) {
				mu.Lock()
				defer mu.Unlock()

				// Make a copy of the batch to avoid race conditions
				batchCopy := make([]*event.Event, len(batch))
				copy(batchCopy, batch)
				receivedBatches = append(receivedBatches, batchCopy)

				// Check if we should continue processing
				for _, event := range batch {
					if event.Response != nil && event.Response.Done {
						return false, nil
					}
				}
				return true, nil
			}

			tunnel := newEventTunnel(tt.batchSize, 50*time.Millisecond, produce, consume)
			ctx := context.Background()

			err := tunnel.Run(ctx)

			if tt.expectError {
				if err == nil {
					t.Errorf("Run() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Run() unexpected error: %v", err)
				return
			}

			mu.Lock()
			defer mu.Unlock()

			if len(receivedBatches) != tt.expectedBatches {
				t.Errorf("Run() received %d batches, want %d", len(receivedBatches), tt.expectedBatches)
				return
			}

			// Verify all events were processed
			totalProcessed := 0
			for _, batch := range receivedBatches {
				totalProcessed += len(batch)
			}

			if totalProcessed != len(tt.events) {
				t.Errorf("Run() processed %d events, want %d", totalProcessed, len(tt.events))
			}
		})
	}
}

func TestEventTunnel_Run_ContextCancellation(t *testing.T) {
	var receivedBatches [][]*event.Event
	var mu sync.Mutex

	eventCount := 0
	produce := func(context.Context) (*event.Event, bool) {
		eventCount++
		// Simulate slow event production
		time.Sleep(10 * time.Millisecond)
		return createTestEvent("event", false), true
	}

	consume := func(batch []*event.Event) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		receivedBatches = append(receivedBatches, batch)
		return true, nil
	}

	tunnel := newEventTunnel(5, 100*time.Millisecond, produce, consume)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := tunnel.Run(ctx)

	if err != context.DeadlineExceeded {
		t.Errorf("Run() error = %v, want %v", err, context.DeadlineExceeded)
	}

	// Should have processed some events before cancellation
	mu.Lock()
	defer mu.Unlock()

	if len(receivedBatches) == 0 {
		t.Error("Run() should have processed some batches before cancellation")
	}
}

func TestEventTunnel_Run_ContextAwareProduceCancellation(t *testing.T) {
	produceStarted := make(chan struct{})

	produce := func(ctx context.Context) (*event.Event, bool) {
		select {
		case <-produceStarted:
		default:
			close(produceStarted)
		}

		select {
		case <-ctx.Done():
			return nil, false
		case <-time.After(time.Second):
			t.Fatal("produce should have been cancelled before timeout")
			return nil, false
		}
	}

	consume := func(batch []*event.Event) (bool, error) {
		return true, nil
	}

	tunnel := newEventTunnel(5, 20*time.Millisecond, produce, consume)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- tunnel.Run(ctx)
	}()

	select {
	case <-produceStarted:
	case <-time.After(time.Second):
		t.Fatal("produce did not start")
	}

	cancel()
	assert.ErrorIs(t, <-runDone, context.Canceled)
}

func TestEventTunnel_Run_ContextCancellationReturnsFlushError(t *testing.T) {
	flushErr := errors.New("cancel flush failed")
	secondProduceStarted := make(chan struct{})
	call := 0

	produce := func(ctx context.Context) (*event.Event, bool) {
		call++
		switch call {
		case 1:
			return createTestEvent("pending", false), true
		case 2:
			close(secondProduceStarted)
			<-ctx.Done()
			return nil, false
		default:
			return nil, false
		}
	}

	consume := func(batch []*event.Event) (bool, error) {
		assert.Len(t, batch, 1)
		return false, flushErr
	}

	tunnel := newEventTunnel(10, time.Hour, produce, consume)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- tunnel.Run(ctx)
	}()

	select {
	case <-secondProduceStarted:
	case <-time.After(time.Second):
		t.Fatal("produce did not reach the cancellation wait")
	}

	cancel()
	err := <-runDone
	assert.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, err, flushErr)
	assert.Contains(t, err.Error(), "tunnel error during cancel flush")
}

func TestEventTunnel_Run_ConsumeError(t *testing.T) {
	expectedError := errors.New("consume error")

	produce := func(context.Context) (*event.Event, bool) {
		return createTestEvent("event", false), true
	}

	consume := func(batch []*event.Event) (bool, error) {
		return false, expectedError
	}

	tunnel := newEventTunnel(2, 50*time.Millisecond, produce, consume)
	ctx := context.Background()

	err := tunnel.Run(ctx)

	if err == nil {
		t.Error("Run() expected error but got none")
		return
	}

	if !containsString(err.Error(), "consume error") {
		t.Errorf("Run() error = %v, should contain 'consume error'", err)
	}
}

func TestEventTunnel_Run_TimerFlush(t *testing.T) {
	var receivedBatches [][]*event.Event
	var mu sync.Mutex

	eventsSent := 0
	produce := func(context.Context) (*event.Event, bool) {
		if eventsSent >= 2 {
			return nil, false
		}
		eventsSent++
		return createTestEvent("event", false), true
	}

	consume := func(batch []*event.Event) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		receivedBatches = append(receivedBatches, batch)
		return true, nil
	}

	// Use small flush interval to trigger timer-based flush
	tunnel := newEventTunnel(10, 20*time.Millisecond, produce, consume)
	ctx := context.Background()

	err := tunnel.Run(ctx)

	if err != nil {
		t.Errorf("Run() unexpected error: %v", err)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	if len(receivedBatches) == 0 {
		t.Error("Run() should have flushed batch due to timer")
		return
	}

	// Should have received one batch with 2 events
	if len(receivedBatches) != 1 {
		t.Errorf("Run() received %d batches, want 1", len(receivedBatches))
		return
	}

	if len(receivedBatches[0]) != 2 {
		t.Errorf("Run() first batch has %d events, want 2", len(receivedBatches[0]))
	}
}

func TestEventTunnel_Run_FinalFlush(t *testing.T) {
	var mu sync.Mutex
	flushCount := 0
	produced := false

	produce := func(context.Context) (*event.Event, bool) {
		if produced {
			return nil, false
		}
		produced = true
		return createTestEvent("pending", false), true
	}

	consume := func(batch []*event.Event) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		flushCount++
		assert.Equal(t, 1, len(batch))
		if len(batch) == 1 {
			assert.NotNil(t, batch[0].Response)
			if batch[0].Response != nil {
				assert.NotEmpty(t, batch[0].Response.Choices)
				if len(batch[0].Response.Choices) > 0 {
					assert.Equal(t, "pending", batch[0].Response.Choices[0].Message.Content)
				}
			}
		}
		return true, nil
	}

	tunnel := newEventTunnel(4, 200*time.Millisecond, produce, consume)
	assert.NoError(t, tunnel.Run(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, flushCount)
}

func TestEventTunnel_Run_TimerFlushError(t *testing.T) {
	const flushInterval = 5 * time.Millisecond
	call := 0
	produce := func(context.Context) (*event.Event, bool) {
		call++
		switch call {
		case 1:
			return createTestEvent("pending", false), true
		case 2:
			time.Sleep(2 * flushInterval)
			return nil, true
		default:
			return nil, false
		}
	}

	consume := func(batch []*event.Event) (bool, error) {
		return true, errors.New("timer flush failed")
	}

	tunnel := newEventTunnel(10, flushInterval, produce, consume)
	err := tunnel.Run(context.Background())
	assert.Error(t, err)
}

func TestEventTunnel_Run_TimerFlushStop(t *testing.T) {
	const flushInterval = 5 * time.Millisecond
	call := 0
	produce := func(context.Context) (*event.Event, bool) {
		call++
		switch call {
		case 1:
			return createTestEvent("pending", false), true
		case 2:
			time.Sleep(2 * flushInterval)
			return nil, true
		default:
			return nil, false
		}
	}

	consume := func(batch []*event.Event) (bool, error) {
		return false, nil
	}

	tunnel := newEventTunnel(10, flushInterval, produce, consume)
	assert.NoError(t, tunnel.Run(context.Background()))
}

func TestEventTunnel_Run_TimerFlushWhileProduceBlocked(t *testing.T) {
	const flushInterval = 20 * time.Millisecond

	blockedProduce := make(chan struct{})
	releaseProduce := make(chan struct{})
	flushed := make(chan struct{}, 1)
	call := 0

	produce := func(ctx context.Context) (*event.Event, bool) {
		call++
		switch call {
		case 1:
			return createTestEvent("pending", false), true
		case 2:
			close(blockedProduce)
			select {
			case <-ctx.Done():
				return nil, false
			case <-releaseProduce:
				return nil, false
			}
		default:
			return nil, false
		}
	}

	consume := func(batch []*event.Event) (bool, error) {
		assert.Len(t, batch, 1)
		select {
		case flushed <- struct{}{}:
		default:
		}
		return true, nil
	}

	tunnel := newEventTunnel(10, flushInterval, produce, consume)
	runDone := make(chan error, 1)
	go func() {
		runDone <- tunnel.Run(context.Background())
	}()

	select {
	case <-blockedProduce:
	case <-time.After(time.Second):
		t.Fatal("produce did not enter blocked state")
	}

	select {
	case <-flushed:
	case <-time.After(5 * flushInterval):
		t.Fatal("expected timer flush while produce is blocked")
	}

	close(releaseProduce)
	assert.NoError(t, <-runDone)
}

func TestEventTunnel_FlushBatch(t *testing.T) {
	tests := []struct {
		name          string
		initialBatch  []*event.Event
		consumeResult bool
		consumeError  error
		expectError   bool
		expectOK      bool
	}{
		{
			name:          "empty batch",
			initialBatch:  []*event.Event{},
			consumeResult: true,
			consumeError:  nil,
			expectError:   false,
			expectOK:      true,
		},
		{
			name: "successful flush",
			initialBatch: []*event.Event{
				createTestEvent("event1", false),
				createTestEvent("event2", false),
			},
			consumeResult: true,
			consumeError:  nil,
			expectError:   false,
			expectOK:      true,
		},
		{
			name: "consume returns false",
			initialBatch: []*event.Event{
				createTestEvent("event1", true),
			},
			consumeResult: false,
			consumeError:  nil,
			expectError:   false,
			expectOK:      false,
		},
		{
			name: "consume returns error",
			initialBatch: []*event.Event{
				createTestEvent("event1", false),
			},
			consumeResult: false,
			consumeError:  errors.New("consume failed"),
			expectError:   true,
			expectOK:      false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var consumedBatch []*event.Event

			consume := func(batch []*event.Event) (bool, error) {
				consumedBatch = make([]*event.Event, len(batch))
				copy(consumedBatch, batch)
				return tt.consumeResult, tt.consumeError
			}

			tunnel := newEventTunnel(5, 100*time.Millisecond, nil, consume)
			tunnel.batch = make([]*event.Event, len(tt.initialBatch))
			copy(tunnel.batch, tt.initialBatch)

			ok, err := tunnel.flushBatch()

			if tt.expectError {
				if err == nil {
					t.Errorf("flushBatch() expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("flushBatch() unexpected error: %v", err)
				return
			}

			if ok != tt.expectOK {
				t.Errorf("flushBatch() ok = %v, want %v", ok, tt.expectOK)
			}

			// Verify batch was cleared
			if len(tunnel.batch) != 0 {
				t.Errorf("flushBatch() batch length = %v, want 0", len(tunnel.batch))
			}

			// Verify consume was called with correct batch
			if len(tt.initialBatch) > 0 {
				if len(consumedBatch) != len(tt.initialBatch) {
					t.Errorf("flushBatch() consumed batch length = %v, want %v", len(consumedBatch), len(tt.initialBatch))
				}
			}
		})
	}
}

func TestEventTunnel_Run_NilEvents(t *testing.T) {
	var receivedBatches [][]*event.Event
	var mu sync.Mutex

	eventsSent := 0
	produce := func(context.Context) (*event.Event, bool) {
		if eventsSent >= 3 {
			return nil, false
		}
		eventsSent++
		if eventsSent == 2 {
			return nil, true // Send nil event
		}
		return createTestEvent("event", false), true
	}

	consume := func(batch []*event.Event) (bool, error) {
		mu.Lock()
		defer mu.Unlock()
		receivedBatches = append(receivedBatches, batch)
		return true, nil
	}

	tunnel := newEventTunnel(5, 50*time.Millisecond, produce, consume)
	ctx := context.Background()

	err := tunnel.Run(ctx)

	if err != nil {
		t.Errorf("Run() unexpected error: %v", err)
		return
	}

	mu.Lock()
	defer mu.Unlock()

	// Should have received one batch with 2 non-nil events
	if len(receivedBatches) != 1 {
		t.Errorf("Run() received %d batches, want 1", len(receivedBatches))
		return
	}

	// Should have 2 events (nil event should be dropped from the batch)
	if len(receivedBatches[0]) != 2 {
		t.Errorf("Run() first batch has %d events, want 2", len(receivedBatches[0]))
	}
}

// Helper functions for testing
func createTestEvent(content string, done bool) *event.Event {
	return &event.Event{
		Response: &model.Response{
			ID:      "test-response",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "test-model",
			Choices: []model.Choice{
				{
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: content,
					},
				},
			},
			Done: done,
		},
		InvocationID: "test-invocation",
		Author:       "test-agent",
		ID:           "test-event",
		Timestamp:    time.Now(),
	}
}
