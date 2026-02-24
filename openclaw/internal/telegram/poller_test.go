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
