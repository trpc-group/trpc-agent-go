//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubUpdatesClient struct {
	mu       sync.Mutex
	calls    int
	offsets  []int
	timeouts []time.Duration
	results  [][]Update
}

func (c *stubUpdatesClient) GetUpdates(
	_ context.Context,
	offset int,
	timeout time.Duration,
) ([]Update, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls++
	c.offsets = append(c.offsets, offset)
	c.timeouts = append(c.timeouts, timeout)
	if len(c.results) == 0 {
		return nil, nil
	}
	out := c.results[0]
	c.results = c.results[1:]
	return out, nil
}

func TestPoller_BootstrapAndHandleMessage(t *testing.T) {
	t.Parallel()

	client := &stubUpdatesClient{
		results: [][]Update{
			{
				{UpdateID: 10},
				{UpdateID: 11},
			},
			nil,
			{
				{
					UpdateID: 12,
					Message: &Message{
						MessageID: 1,
						From:      &User{ID: 1},
						Chat:      &Chat{ID: 2, Type: chatTypePrivate},
						Text:      "hi",
					},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	handled := make(chan struct{}, 1)
	poller, err := NewPoller(
		client,
		WithPollTimeout(3*time.Second),
		WithStartFromLatest(true),
		WithMessageHandler(func(_ context.Context, msg Message) error {
			require.Equal(t, "hi", msg.Text)
			handled <- struct{}{}
			cancel()
			return nil
		}),
	)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- poller.Run(ctx) }()

	select {
	case <-handled:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for handler")
	}

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for poller stop")
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	require.GreaterOrEqual(t, client.calls, 3)
	require.Equal(t, 0, client.offsets[0])
	require.Equal(t, 0*time.Second, client.timeouts[0])
	require.Equal(t, 12, client.offsets[2])
	require.Equal(t, 3*time.Second, client.timeouts[2])
}

type stubOffsetStore struct {
	mu         sync.Mutex
	readOffset int
	readOK     bool
	readErr    error
	writes     []int
}

func (s *stubOffsetStore) Read(
	_ context.Context,
) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readOffset, s.readOK, s.readErr
}

func (s *stubOffsetStore) Write(
	_ context.Context,
	offset int,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes = append(s.writes, offset)
	return nil
}

func TestPoller_HandlerError_DoesNotAdvanceOffset(t *testing.T) {
	t.Parallel()

	client := &stubUpdatesClient{
		results: [][]Update{
			{
				{
					UpdateID: 1,
					Message: &Message{
						MessageID: 1,
						From:      &User{ID: 1},
						Chat:      &Chat{ID: 2, Type: chatTypePrivate},
						Text:      "hi",
					},
				},
			},
			{
				{
					UpdateID: 1,
					Message: &Message{
						MessageID: 1,
						From:      &User{ID: 1},
						Chat:      &Chat{ID: 2, Type: chatTypePrivate},
						Text:      "hi",
					},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	handled := make(chan struct{}, 2)
	var calls int
	poller, err := NewPoller(
		client,
		WithStartFromLatest(false),
		WithPollTimeout(0),
		WithErrorBackoff(1*time.Millisecond),
		WithMessageHandler(func(_ context.Context, _ Message) error {
			calls++
			handled <- struct{}{}
			if calls == 1 {
				return errors.New("fail")
			}
			cancel()
			return nil
		}),
	)
	require.NoError(t, err)

	errCh := make(chan error, 1)
	go func() { errCh <- poller.Run(ctx) }()

	for i := 0; i < 2; i++ {
		select {
		case <-handled:
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for handler")
		}
	}

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for poller stop")
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	require.GreaterOrEqual(t, client.calls, 2)
	require.Equal(t, 0, client.offsets[0])
	require.Equal(t, 0, client.offsets[1])
}

func TestPoller_OffsetStore_Resume(t *testing.T) {
	t.Parallel()

	store := &stubOffsetStore{
		readOffset: 100,
		readOK:     true,
	}

	client := &stubUpdatesClient{
		results: [][]Update{
			{
				{
					UpdateID: 100,
					Message: &Message{
						MessageID: 1,
						From:      &User{ID: 1},
						Chat:      &Chat{ID: 2, Type: chatTypePrivate},
						Text:      "hi",
					},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	poller, err := NewPoller(
		client,
		WithStartFromLatest(true),
		WithPollTimeout(0),
		WithOffsetStore(store),
		WithMessageHandler(func(_ context.Context, _ Message) error {
			cancel()
			return nil
		}),
	)
	require.NoError(t, err)
	require.NoError(t, poller.Run(ctx))

	client.mu.Lock()
	defer client.mu.Unlock()
	require.GreaterOrEqual(t, client.calls, 1)
	require.Equal(t, 100, client.offsets[0])

	store.mu.Lock()
	defer store.mu.Unlock()
	require.NotEmpty(t, store.writes)
	require.Equal(t, 101, store.writes[len(store.writes)-1])
}

func TestNewPoller_ValidationErrors(t *testing.T) {
	t.Parallel()

	_, err := NewPoller(nil)
	require.Error(t, err)

	client := &stubUpdatesClient{}

	_, err = NewPoller(client)
	require.Error(t, err)

	_, err = NewPoller(
		client,
		WithMessageHandler(func(context.Context, Message) error {
			return nil
		}),
		WithPollTimeout(-1*time.Second),
	)
	require.Error(t, err)

	_, err = NewPoller(
		client,
		WithMessageHandler(func(context.Context, Message) error {
			return nil
		}),
		WithErrorBackoff(-1*time.Second),
	)
	require.Error(t, err)

	_, err = NewPoller(
		client,
		WithMessageHandler(func(context.Context, Message) error {
			return nil
		}),
		WithOnError(nil),
	)
	require.Error(t, err)
}

func TestPoller_SkipsNonTextAndBotMessages(t *testing.T) {
	t.Parallel()

	client := &stubUpdatesClient{
		results: [][]Update{
			{
				{UpdateID: 1},
				{
					UpdateID: 2,
					Message: &Message{
						MessageID: 2,
						From:      &User{ID: 1},
						Chat:      &Chat{ID: 2, Type: chatTypePrivate},
					},
				},
				{
					UpdateID: 3,
					Message: &Message{
						MessageID: 3,
						From:      &User{ID: 1, IsBot: true},
						Chat:      &Chat{ID: 2, Type: chatTypePrivate},
						Text:      "hi",
					},
				},
				{
					UpdateID: 4,
					Message: &Message{
						MessageID: 4,
						From:      &User{ID: 1},
						Chat:      &Chat{ID: 2, Type: chatTypePrivate},
						Text:      "ok",
					},
				},
			},
		},
	}

	store := &stubOffsetStore{}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	handled := make(chan struct{}, 1)
	poller, err := NewPoller(
		client,
		WithStartFromLatest(false),
		WithPollTimeout(0),
		WithOffsetStore(store),
		WithMessageHandler(func(_ context.Context, msg Message) error {
			require.Equal(t, "ok", msg.Text)
			handled <- struct{}{}
			cancel()
			return nil
		}),
	)
	require.NoError(t, err)

	require.NoError(t, poller.Run(ctx))

	select {
	case <-handled:
	default:
		t.Fatal("expected handler to be called")
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	require.NotEmpty(t, store.writes)
	require.Equal(t, 5, store.writes[len(store.writes)-1])
}

type errWriteOffsetStore struct {
	err error
}

func (s *errWriteOffsetStore) Read(context.Context) (int, bool, error) {
	return 0, false, nil
}

func (s *errWriteOffsetStore) Write(
	context.Context,
	int,
) error {
	return s.err
}

func TestPoller_OffsetStoreWriteError_CallsOnError(t *testing.T) {
	t.Parallel()

	expected := errors.New("write fail")
	store := &errWriteOffsetStore{err: expected}

	client := &stubUpdatesClient{
		results: [][]Update{
			{
				{
					UpdateID: 1,
					Message: &Message{
						MessageID: 1,
						From:      &User{ID: 1},
						Chat:      &Chat{ID: 2, Type: chatTypePrivate},
						Text:      "hi",
					},
				},
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	var got error
	poller, err := NewPoller(
		client,
		WithStartFromLatest(false),
		WithPollTimeout(0),
		WithOffsetStore(store),
		WithErrorBackoff(0),
		WithOnError(func(err error) { got = err }),
		WithMessageHandler(func(_ context.Context, _ Message) error {
			cancel()
			return nil
		}),
	)
	require.NoError(t, err)

	require.NoError(t, poller.Run(ctx))
	require.ErrorIs(t, got, expected)
}

type errReadOffsetStore struct {
	err error
}

func (s *errReadOffsetStore) Read(
	context.Context,
) (int, bool, error) {
	return 0, false, s.err
}

func (s *errReadOffsetStore) Write(context.Context, int) error {
	return nil
}

func TestPoller_OffsetStoreReadError_ReturnsError(t *testing.T) {
	t.Parallel()

	expected := errors.New("read fail")
	store := &errReadOffsetStore{err: expected}

	client := &stubUpdatesClient{}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	poller, err := NewPoller(
		client,
		WithOffsetStore(store),
		WithMessageHandler(func(context.Context, Message) error {
			return nil
		}),
	)
	require.NoError(t, err)

	err = poller.Run(ctx)
	require.ErrorIs(t, err, expected)
}

func TestSleepWithContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.True(t, sleepWithContext(ctx, 0))
	require.False(t, sleepWithContext(ctx, time.Second))
}
