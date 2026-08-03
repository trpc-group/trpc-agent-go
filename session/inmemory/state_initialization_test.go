//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package inmemory

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestLoadOrInitializeSessionStateExistingAndReplacement(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}

	t.Run("returns a copied valid value", func(t *testing.T) {
		service := NewSessionService()
		t.Cleanup(func() { require.NoError(t, service.Close()) })
		_, err := service.CreateSession(ctx, key, session.StateMap{"state": []byte("valid")})
		require.NoError(t, err)
		var calls atomic.Int32
		value, didInitialize, err := service.LoadOrInitializeSessionState(
			ctx,
			key,
			"state",
			func(value []byte) bool { return string(value) == "valid" },
			func(context.Context) ([]byte, error) {
				calls.Add(1)
				return []byte("unexpected"), nil
			},
		)
		require.NoError(t, err)
		require.False(t, didInitialize)
		require.Equal(t, "valid", string(value))
		require.Zero(t, calls.Load())

		value[0] = 'X'
		stored, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		storedValue, ok := stored.GetState("state")
		require.True(t, ok)
		require.Equal(t, "valid", string(storedValue))
	})

	t.Run("replaces an invalid value", func(t *testing.T) {
		service := NewSessionService()
		t.Cleanup(func() { require.NoError(t, service.Close()) })
		_, err := service.CreateSession(ctx, key, session.StateMap{"state": []byte("invalid")})
		require.NoError(t, err)
		callbackValue := []byte("replacement")
		value, didInitialize, err := service.LoadOrInitializeSessionState(
			ctx,
			key,
			"state",
			func(value []byte) bool { return string(value) == "replacement" },
			func(context.Context) ([]byte, error) { return callbackValue, nil },
		)
		require.NoError(t, err)
		require.True(t, didInitialize)
		require.Equal(t, "replacement", string(value))

		callbackValue[0] = 'X'
		value[0] = 'Y'
		stored, err := service.GetSession(ctx, key)
		require.NoError(t, err)
		storedValue, ok := stored.GetState("state")
		require.True(t, ok)
		require.Equal(t, "replacement", string(storedValue))
	})
}

func TestLoadOrInitializeSessionStateCoordinatesCallers(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	service := NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	_, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	ownerStarted := make(chan struct{})
	releaseOwner := make(chan struct{})
	var callbackCalls atomic.Int32
	initialize := func(ctx context.Context) ([]byte, error) {
		if callbackCalls.Add(1) == 1 {
			close(ownerStarted)
		}
		select {
		case <-releaseOwner:
			return []byte("shared"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	validate := func(value []byte) bool { return string(value) == "shared" }

	type result struct {
		value         []byte
		didInitialize bool
		err           error
	}
	const callers = 8
	results := make(chan result, callers)
	for i := 0; i < callers; i++ {
		go func() {
			value, didInitialize, err := service.LoadOrInitializeSessionState(
				ctx,
				key,
				"state",
				validate,
				initialize,
			)
			results <- result{value: value, didInitialize: didInitialize, err: err}
		}()
	}
	select {
	case <-ownerStarted:
	case <-time.After(time.Second):
		t.Fatal("state initializer did not start")
	}
	close(releaseOwner)

	initialized := 0
	for i := 0; i < callers; i++ {
		result := <-results
		require.NoError(t, result.err)
		require.Equal(t, "shared", string(result.value))
		if result.didInitialize {
			initialized++
		}
	}
	require.Equal(t, int32(1), callbackCalls.Load())
	require.Equal(t, 1, initialized)

	service.stateInitializationMu.Lock()
	require.Empty(t, service.stateInitializationGates)
	service.stateInitializationMu.Unlock()
}

func TestLoadOrInitializeSessionStateWaiterCancellation(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	service := NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	_, err := service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	ownerStarted := make(chan struct{})
	releaseOwner := make(chan struct{})
	ownerDone := make(chan error, 1)
	go func() {
		_, _, ownerErr := service.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"state",
			func(value []byte) bool { return len(value) > 0 },
			func(context.Context) ([]byte, error) {
				close(ownerStarted)
				<-releaseOwner
				return []byte("value"), nil
			},
		)
		ownerDone <- ownerErr
	}()
	<-ownerStarted

	waiterCtx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, _, waiterErr := service.LoadOrInitializeSessionState(
			waiterCtx,
			key,
			"state",
			func(value []byte) bool { return len(value) > 0 },
			func(context.Context) ([]byte, error) { return []byte("waiter"), nil },
		)
		waiterDone <- waiterErr
	}()
	cancel()
	require.ErrorIs(t, <-waiterDone, context.Canceled)
	close(releaseOwner)
	require.NoError(t, <-ownerDone)
}

func TestLoadOrInitializeSessionStateFailureAndPanicReleaseOwnership(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	service := NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	_, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	validate := func(value []byte) bool { return string(value) == "valid" }

	wantErr := errors.New("callback failed")
	_, _, err = service.LoadOrInitializeSessionState(
		ctx,
		key,
		"state",
		validate,
		func(context.Context) ([]byte, error) { return nil, wantErr },
	)
	require.ErrorIs(t, err, wantErr)

	require.Panics(t, func() {
		_, _, _ = service.LoadOrInitializeSessionState(
			ctx,
			key,
			"state",
			validate,
			func(context.Context) ([]byte, error) { panic("boom") },
		)
	})

	value, didInitialize, err := service.LoadOrInitializeSessionState(
		ctx,
		key,
		"state",
		validate,
		func(context.Context) ([]byte, error) { return []byte("valid"), nil },
	)
	require.NoError(t, err)
	require.True(t, didInitialize)
	require.Equal(t, "valid", string(value))
}

func TestLoadOrInitializeSessionStateFencesDeletedAndRecreatedSession(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	service := NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	_, err := service.CreateSession(ctx, key, session.StateMap{
		"state": []byte("invalid"),
	})
	require.NoError(t, err)

	ownerStarted := make(chan struct{})
	releaseOwner := make(chan struct{})
	ownerDone := make(chan error, 1)
	go func() {
		_, _, ownerErr := service.LoadOrInitializeSessionState(
			ctx,
			key,
			"state",
			func(value []byte) bool { return string(value) == "old" },
			func(context.Context) ([]byte, error) {
				close(ownerStarted)
				<-releaseOwner
				return []byte("old"), nil
			},
		)
		ownerDone <- ownerErr
	}()
	<-ownerStarted

	waiterObservedGeneration := make(chan struct{})
	var waiterCallbackCalls atomic.Int32
	waiterDone := make(chan error, 1)
	go func() {
		_, _, waiterErr := service.LoadOrInitializeSessionState(
			ctx,
			key,
			"state",
			func(value []byte) bool {
				if string(value) == "invalid" {
					close(waiterObservedGeneration)
				}
				return string(value) == "new"
			},
			func(context.Context) ([]byte, error) {
				waiterCallbackCalls.Add(1)
				return []byte("new"), nil
			},
		)
		waiterDone <- waiterErr
	}()
	<-waiterObservedGeneration

	require.NoError(t, service.DeleteSession(ctx, key))
	_, err = service.CreateSession(ctx, key, nil)
	require.NoError(t, err)
	close(releaseOwner)
	require.ErrorContains(t, <-ownerDone, "session generation changed")
	require.ErrorContains(t, <-waiterDone, "session generation changed")
	require.Zero(t, waiterCallbackCalls.Load())

	newSession, err := service.GetSession(ctx, key)
	require.NoError(t, err)
	_, present := newSession.GetState("state")
	require.False(t, present)

	value, didInitialize, err := service.LoadOrInitializeSessionState(
		ctx,
		key,
		"state",
		func(value []byte) bool { return string(value) == "new" },
		func(context.Context) ([]byte, error) { return []byte("new"), nil },
	)
	require.NoError(t, err)
	require.True(t, didInitialize)
	require.Equal(t, "new", string(value))
}

func TestLoadOrInitializeSessionStateValidatesBeforeCallback(t *testing.T) {
	ctx := context.Background()
	validKey := session.Key{AppName: "app", UserID: "user", SessionID: "missing"}
	service := NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	var callbackCalls atomic.Int32
	initialize := func(context.Context) ([]byte, error) {
		callbackCalls.Add(1)
		return []byte("value"), nil
	}
	validate := func([]byte) bool { return true }

	tests := []struct {
		name       string
		key        session.Key
		stateKey   string
		validate   func([]byte) bool
		initialize func(context.Context) ([]byte, error)
	}{
		{name: "invalid session key", key: session.Key{}, stateKey: "state", validate: validate, initialize: initialize},
		{name: "missing state key", key: validKey, validate: validate, initialize: initialize},
		{name: "app state", key: validKey, stateKey: session.StateAppPrefix + "state", validate: validate, initialize: initialize},
		{name: "user state", key: validKey, stateKey: session.StateUserPrefix + "state", validate: validate, initialize: initialize},
		{name: "missing validator", key: validKey, stateKey: "state", initialize: initialize},
		{name: "missing initializer", key: validKey, stateKey: "state", validate: validate},
		{name: "missing session", key: validKey, stateKey: "state", validate: validate, initialize: initialize},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := service.LoadOrInitializeSessionState(
				ctx,
				test.key,
				test.stateKey,
				test.validate,
				test.initialize,
			)
			require.Error(t, err)
		})
	}
	require.Zero(t, callbackCalls.Load())
}

func TestLoadOrInitializeSessionStateCloseWakesWaiter(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	service := NewSessionService()
	_, err := service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	ownerStarted := make(chan struct{})
	releaseOwner := make(chan struct{})
	ownerDone := make(chan error, 1)
	go func() {
		_, _, ownerErr := service.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"state",
			func(value []byte) bool { return len(value) > 0 },
			func(context.Context) ([]byte, error) {
				close(ownerStarted)
				<-releaseOwner
				return []byte("owner"), nil
			},
		)
		ownerDone <- ownerErr
	}()
	<-ownerStarted

	waiterDone := make(chan error, 1)
	go func() {
		_, _, waiterErr := service.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"state",
			func(value []byte) bool { return len(value) > 0 },
			func(context.Context) ([]byte, error) { return []byte("waiter"), nil },
		)
		waiterDone <- waiterErr
	}()
	require.NoError(t, service.Close())
	require.ErrorIs(t, <-waiterDone, errStateInitializationClosed)
	close(releaseOwner)
	require.ErrorIs(t, <-ownerDone, errStateInitializationClosed)
}

func TestLoadOrInitializeSessionStateCloseCancelsOwner(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	service := NewSessionService()
	_, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	ownerStarted := make(chan struct{})
	ownerDone := make(chan error, 1)
	go func() {
		_, _, ownerErr := service.LoadOrInitializeSessionState(
			ctx,
			key,
			"state",
			func(value []byte) bool { return len(value) > 0 },
			func(initializeCtx context.Context) ([]byte, error) {
				close(ownerStarted)
				<-initializeCtx.Done()
				return nil, initializeCtx.Err()
			},
		)
		ownerDone <- ownerErr
	}()
	<-ownerStarted

	require.NoError(t, service.Close())
	require.ErrorIs(t, <-ownerDone, context.Canceled)
}

func TestStateInitializationPreservesZeroValueClose(t *testing.T) {
	service := &SessionService{}
	require.NoError(t, service.Close())
	require.NoError(t, service.Close())
}

func TestLoadOrInitializeSessionStateDifferentKeysDoNotBlock(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	service := NewSessionService()
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	_, err := service.CreateSession(ctx, key, nil)
	require.NoError(t, err)

	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		_, _, firstErr := service.LoadOrInitializeSessionState(
			ctx,
			key,
			"first",
			func(value []byte) bool { return len(value) > 0 },
			func(context.Context) ([]byte, error) {
				close(firstStarted)
				<-releaseFirst
				return []byte("first"), nil
			},
		)
		firstDone <- firstErr
	}()
	<-firstStarted

	value, didInitialize, err := service.LoadOrInitializeSessionState(
		ctx,
		key,
		"second",
		func(value []byte) bool { return len(value) > 0 },
		func(context.Context) ([]byte, error) { return []byte("second"), nil },
	)
	require.NoError(t, err)
	require.True(t, didInitialize)
	require.Equal(t, "second", string(value))
	close(releaseFirst)
	require.NoError(t, <-firstDone)
}

var _ session.StateInitializationService = (*SessionService)(nil)
