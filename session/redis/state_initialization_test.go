//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redis

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestLoadOrInitializeSessionStateCoordinatesServiceInstances(t *testing.T) {
	for _, test := range []struct {
		name string
		mode CompatMode
	}{
		{name: "hashidx", mode: CompatModeNone},
		{name: "zset", mode: CompatModeTransition},
	} {
		t.Run(test.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			t.Cleanup(cleanup)
			ownerService := newStateInitializationTestService(t, redisURL, test.mode)
			waiterService := newStateInitializationTestService(t, redisURL, test.mode)
			key := stateInitializationTestKey()
			_, err := ownerService.CreateSession(context.Background(), key, nil)
			require.NoError(t, err)

			ownerStarted := make(chan struct{})
			releaseOwner := make(chan struct{})
			var callbackCalls atomic.Int32
			validate := func(value []byte) bool { return string(value) == "shared" }
			ownerDone := make(chan stateInitializationTestResult, 1)
			go func() {
				value, initialized, ownerErr := ownerService.LoadOrInitializeSessionState(
					context.Background(),
					key,
					"state",
					validate,
					func(ctx context.Context) ([]byte, error) {
						callbackCalls.Add(1)
						close(ownerStarted)
						select {
						case <-releaseOwner:
							return []byte("shared"), nil
						case <-ctx.Done():
							return nil, ctx.Err()
						}
					},
				)
				ownerDone <- stateInitializationTestResult{value, initialized, ownerErr}
			}()
			select {
			case <-ownerStarted:
			case <-time.After(time.Second):
				t.Fatal("redis state initializer did not start")
			}

			waiterDone := make(chan stateInitializationTestResult, 1)
			go func() {
				value, initialized, waiterErr := waiterService.LoadOrInitializeSessionState(
					context.Background(),
					key,
					"state",
					validate,
					func(context.Context) ([]byte, error) {
						callbackCalls.Add(1)
						return []byte("unexpected"), nil
					},
				)
				waiterDone <- stateInitializationTestResult{value, initialized, waiterErr}
			}()

			close(releaseOwner)
			ownerResult := <-ownerDone
			waiterResult := <-waiterDone
			require.NoError(t, ownerResult.err)
			require.NoError(t, waiterResult.err)
			require.True(t, ownerResult.didInitialize)
			require.False(t, waiterResult.didInitialize)
			require.Equal(t, "shared", string(ownerResult.value))
			require.Equal(t, "shared", string(waiterResult.value))
			require.Equal(t, int32(1), callbackCalls.Load())

			stored, err := waiterService.GetSession(context.Background(), key)
			require.NoError(t, err)
			storedValue, ok := stored.GetState("state")
			require.True(t, ok)
			require.Equal(t, "shared", string(storedValue))

			route, err := ownerService.resolveStateInitializationRoute(context.Background(), key)
			require.NoError(t, err)
			leaseKey := ownerService.stateInitializationLeaseKey(route, key, "state")
			exists, err := ownerService.redisClient.Exists(context.Background(), leaseKey).Result()
			require.NoError(t, err)
			require.Zero(t, exists)
		})
	}
}

func TestLoadOrInitializeSessionStateWaiterCancellation(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	t.Cleanup(cleanup)
	ownerService := newStateInitializationTestService(t, redisURL, CompatModeNone)
	waiterService := newStateInitializationTestService(t, redisURL, CompatModeNone)
	key := stateInitializationTestKey()
	_, err := ownerService.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	ownerStarted := make(chan struct{})
	releaseOwner := make(chan struct{})
	ownerDone := make(chan error, 1)
	go func() {
		_, _, ownerErr := ownerService.LoadOrInitializeSessionState(
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

	waiterCtx, cancel := context.WithCancel(context.Background())
	waiterDone := make(chan error, 1)
	go func() {
		_, _, waiterErr := waiterService.LoadOrInitializeSessionState(
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

func TestLoadOrInitializeSessionStateRenewsLease(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	ownerService := newStateInitializationTestService(
		t,
		"redis://"+mr.Addr(),
		CompatModeNone,
	)
	waiterService := newStateInitializationTestService(
		t,
		"redis://"+mr.Addr(),
		CompatModeNone,
	)
	for _, service := range []*Service{ownerService, waiterService} {
		service.stateInitializationLeaseTTL = 120 * time.Millisecond
		service.stateInitializationRenewInterval = 20 * time.Millisecond
		service.stateInitializationPollMin = 5 * time.Millisecond
		service.stateInitializationPollMax = 10 * time.Millisecond
	}
	key := stateInitializationTestKey()
	_, err = ownerService.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	ownerStarted := make(chan struct{})
	releaseOwner := make(chan struct{})
	var callbackCalls atomic.Int32
	ownerDone := make(chan error, 1)
	go func() {
		_, _, ownerErr := ownerService.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"state",
			func(value []byte) bool { return string(value) == "owner" },
			func(ctx context.Context) ([]byte, error) {
				callbackCalls.Add(1)
				close(ownerStarted)
				select {
				case <-releaseOwner:
					return []byte("owner"), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		)
		ownerDone <- ownerErr
	}()
	<-ownerStarted

	waiterDone := make(chan error, 1)
	go func() {
		_, _, waiterErr := waiterService.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"state",
			func(value []byte) bool { return string(value) == "owner" },
			func(context.Context) ([]byte, error) {
				callbackCalls.Add(1)
				return []byte("waiter"), nil
			},
		)
		waiterDone <- waiterErr
	}()

	for i := 0; i < 4; i++ {
		mr.FastForward(80 * time.Millisecond)
		time.Sleep(35 * time.Millisecond)
		select {
		case err := <-waiterDone:
			t.Fatalf("waiter completed before owner commit: %v", err)
		default:
		}
	}
	close(releaseOwner)
	require.NoError(t, <-ownerDone)
	require.NoError(t, <-waiterDone)
	require.Equal(t, int32(1), callbackCalls.Load())
}

func TestLoadOrInitializeSessionStateDoesNotCallbackAfterLeaseExpiryBeforeRecheck(
	t *testing.T,
) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	service := newStateInitializationTestService(
		t,
		"redis://"+mr.Addr(),
		CompatModeNone,
	)
	service.stateInitializationLeaseTTL = 50 * time.Millisecond
	service.stateInitializationRenewInterval = 10 * time.Millisecond
	key := stateInitializationTestKey()
	_, err = service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	route, err := service.resolveStateInitializationRoute(context.Background(), key)
	require.NoError(t, err)
	_, _, generation, err := service.loadStateInitializationValue(
		context.Background(),
		route,
		key,
		"state",
	)
	require.NoError(t, err)
	leaseKey := service.stateInitializationLeaseKey(route, key, "state")
	ownerToken := "expired-owner"
	require.NoError(t, service.redisClient.Set(
		context.Background(),
		leaseKey,
		ownerToken,
		service.stateInitializationLeaseTTL,
	).Err())
	mr.FastForward(100 * time.Millisecond)

	var callbackCalls atomic.Int32
	_, _, err = service.initializeSessionState(
		context.Background(),
		route,
		key,
		"state",
		leaseKey,
		ownerToken,
		time.Now().Add(-time.Second),
		generation,
		func(value []byte) bool { return len(value) > 0 },
		func(context.Context) ([]byte, error) {
			callbackCalls.Add(1)
			return []byte("stale"), nil
		},
	)
	require.Error(t, err)
	require.Zero(t, callbackCalls.Load())
}

func TestLoadOrInitializeSessionStateOwnerFailureReleasesLease(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	t.Cleanup(cleanup)
	service := newStateInitializationTestService(t, redisURL, CompatModeNone)
	key := stateInitializationTestKey()
	_, err := service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	wantErr := errors.New("callback failed")
	_, _, err = service.LoadOrInitializeSessionState(
		context.Background(),
		key,
		"state",
		func(value []byte) bool { return string(value) == "valid" },
		func(context.Context) ([]byte, error) { return nil, wantErr },
	)
	require.ErrorIs(t, err, wantErr)

	route, err := service.resolveStateInitializationRoute(context.Background(), key)
	require.NoError(t, err)
	leaseKey := service.stateInitializationLeaseKey(route, key, "state")
	exists, err := service.redisClient.Exists(context.Background(), leaseKey).Result()
	require.NoError(t, err)
	require.Zero(t, exists)

	value, didInitialize, err := service.LoadOrInitializeSessionState(
		context.Background(),
		key,
		"state",
		func(value []byte) bool { return string(value) == "valid" },
		func(context.Context) ([]byte, error) { return []byte("valid"), nil },
	)
	require.NoError(t, err)
	require.True(t, didInitialize)
	require.Equal(t, "valid", string(value))
}

func TestLoadOrInitializeSessionStateFencesDeletedAndRecreatedSession(t *testing.T) {
	for _, test := range []struct {
		name string
		mode CompatMode
	}{
		{name: "hashidx", mode: CompatModeNone},
		{name: "zset", mode: CompatModeTransition},
	} {
		t.Run(test.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			t.Cleanup(cleanup)
			service := newStateInitializationTestService(t, redisURL, test.mode)
			key := stateInitializationTestKey()
			_, err := service.CreateSession(
				context.Background(),
				key,
				session.StateMap{"state": []byte("invalid")},
			)
			require.NoError(t, err)

			ownerStarted := make(chan struct{})
			releaseOwner := make(chan struct{})
			ownerDone := make(chan error, 1)
			go func() {
				_, _, ownerErr := service.LoadOrInitializeSessionState(
					context.Background(),
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
					context.Background(),
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

			require.NoError(t, service.DeleteSession(context.Background(), key))
			_, err = service.CreateSession(context.Background(), key, nil)
			require.NoError(t, err)
			close(releaseOwner)
			require.ErrorContains(t, <-ownerDone, "session generation changed")
			require.ErrorContains(t, <-waiterDone, "session generation changed")
			require.Zero(t, waiterCallbackCalls.Load())

			newSession, err := service.GetSession(context.Background(), key)
			require.NoError(t, err)
			_, present := newSession.GetState("state")
			require.False(t, present)

			value, didInitialize, err := service.LoadOrInitializeSessionState(
				context.Background(),
				key,
				"state",
				func(value []byte) bool { return string(value) == "new" },
				func(context.Context) ([]byte, error) { return []byte("new"), nil },
			)
			require.NoError(t, err)
			require.True(t, didInitialize)
			require.Equal(t, "new", string(value))
		})
	}
}

func TestLoadOrInitializeSessionStatePersistsUniqueSessionGeneration(t *testing.T) {
	for _, test := range []struct {
		name string
		mode CompatMode
	}{
		{name: "hashidx", mode: CompatModeNone},
		{name: "zset", mode: CompatModeTransition},
	} {
		t.Run(test.name, func(t *testing.T) {
			redisURL, cleanup := setupTestRedis(t)
			t.Cleanup(cleanup)
			const configuredTTL = 10 * time.Minute
			const remainingTTL = 3 * time.Minute
			service := newStateInitializationTestService(
				t,
				redisURL,
				test.mode,
				WithSessionTTL(configuredTTL),
			)
			key := stateInitializationTestKey()
			_, err := service.CreateSession(context.Background(), key, nil)
			require.NoError(t, err)

			redisKey := redisSessionStorageKey(test.mode, key)
			require.NoError(t, service.redisClient.PExpire(
				context.Background(),
				redisKey,
				remainingTTL,
			).Err())
			removeRedisSessionGeneration(t, service, test.mode, key)
			ttlBeforeMigration, err := service.redisClient.PTTL(
				context.Background(),
				redisKey,
			).Result()
			require.NoError(t, err)
			require.Equal(t, remainingTTL, ttlBeforeMigration)
			route, err := service.resolveStateInitializationRoute(
				context.Background(),
				key,
			)
			require.NoError(t, err)
			_, _, firstGeneration, err := service.loadStateInitializationValue(
				context.Background(),
				route,
				key,
				"state",
			)
			require.NoError(t, err)
			_, err = uuid.Parse(firstGeneration)
			require.NoError(t, err)
			ttlAfterMigration, err := service.redisClient.PTTL(
				context.Background(),
				redisKey,
			).Result()
			require.NoError(t, err)
			require.Equal(t, ttlBeforeMigration, ttlAfterMigration)
			require.Less(t, ttlAfterMigration, configuredTTL)

			_, _, persistedGeneration, err := service.loadStateInitializationValue(
				context.Background(),
				route,
				key,
				"state",
			)
			require.NoError(t, err)
			require.Equal(t, firstGeneration, persistedGeneration)

			require.NoError(t, service.DeleteSession(context.Background(), key))
			_, err = service.CreateSession(context.Background(), key, nil)
			require.NoError(t, err)
			route, err = service.resolveStateInitializationRoute(
				context.Background(),
				key,
			)
			require.NoError(t, err)
			_, _, recreatedGeneration, err := service.loadStateInitializationValue(
				context.Background(),
				route,
				key,
				"state",
			)
			require.NoError(t, err)
			_, err = uuid.Parse(recreatedGeneration)
			require.NoError(t, err)
			require.NotEqual(t, firstGeneration, recreatedGeneration)
		})
	}
}

func removeRedisSessionGeneration(
	t *testing.T,
	service *Service,
	mode CompatMode,
	key session.Key,
) {
	t.Helper()
	ctx := context.Background()
	var (
		raw []byte
		err error
	)
	redisKey := redisSessionStorageKey(mode, key)
	if mode == CompatModeTransition {
		raw, err = service.redisClient.HGet(ctx, redisKey, key.SessionID).Bytes()
	} else {
		raw, err = service.redisClient.Get(ctx, redisKey).Bytes()
	}
	require.NoError(t, err)

	var stored map[string]any
	require.NoError(t, json.Unmarshal(raw, &stored))
	delete(stored, "generation")
	raw, err = json.Marshal(stored)
	require.NoError(t, err)
	if mode == CompatModeTransition {
		err = service.redisClient.HSet(ctx, redisKey, key.SessionID, raw).Err()
	} else {
		err = service.redisClient.Set(ctx, redisKey, raw, goredis.KeepTTL).Err()
	}
	require.NoError(t, err)
}

func redisSessionStorageKey(mode CompatMode, key session.Key) string {
	if mode == CompatModeTransition {
		return "sess:{" + key.AppName + "}:" + key.UserID
	}
	return "hashidx:meta:" + key.AppName + ":{" + key.UserID + "}:" + key.SessionID
}

func TestLoadOrInitializeSessionStateOwnerCancellationReleasesLease(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	t.Cleanup(cleanup)
	service := newStateInitializationTestService(t, redisURL, CompatModeNone)
	key := stateInitializationTestKey()
	_, err := service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	ownerStarted := make(chan struct{})
	ownerDone := make(chan error, 1)
	go func() {
		_, _, ownerErr := service.LoadOrInitializeSessionState(
			ownerCtx,
			key,
			"state",
			func(value []byte) bool { return string(value) == "valid" },
			func(ctx context.Context) ([]byte, error) {
				close(ownerStarted)
				<-ctx.Done()
				return nil, ctx.Err()
			},
		)
		ownerDone <- ownerErr
	}()
	<-ownerStarted
	cancelOwner()
	require.ErrorIs(t, <-ownerDone, context.Canceled)

	route, err := service.resolveStateInitializationRoute(
		context.Background(),
		key,
	)
	require.NoError(t, err)
	leaseKey := service.stateInitializationLeaseKey(route, key, "state")
	exists, err := service.redisClient.Exists(
		context.Background(),
		leaseKey,
	).Result()
	require.NoError(t, err)
	require.Zero(t, exists)

	value, didInitialize, err := service.LoadOrInitializeSessionState(
		context.Background(),
		key,
		"state",
		func(value []byte) bool { return string(value) == "valid" },
		func(context.Context) ([]byte, error) { return []byte("valid"), nil },
	)
	require.NoError(t, err)
	require.True(t, didInitialize)
	require.Equal(t, "valid", string(value))
}

func TestLoadOrInitializeSessionStateFencesStaleOwner(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	t.Cleanup(cleanup)
	service := newStateInitializationTestService(t, redisURL, CompatModeNone)
	key := stateInitializationTestKey()
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
				return []byte("stale"), nil
			},
		)
		ownerDone <- ownerErr
	}()
	<-ownerStarted

	route, err := service.resolveStateInitializationRoute(context.Background(), key)
	require.NoError(t, err)
	leaseKey := service.stateInitializationLeaseKey(route, key, "state")
	require.NoError(t, service.redisClient.Set(
		context.Background(),
		leaseKey,
		"replacement-owner",
		time.Minute,
	).Err())
	close(releaseOwner)
	require.ErrorContains(t, <-ownerDone, "lease ownership lost")

	stored, err := service.GetSession(context.Background(), key)
	require.NoError(t, err)
	_, present := stored.GetState("state")
	require.False(t, present)
}

func TestLoadOrInitializeSessionStateRenewalLossCancelsOwner(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	t.Cleanup(cleanup)
	service := newStateInitializationTestService(t, redisURL, CompatModeNone)
	service.stateInitializationLeaseTTL = time.Second
	service.stateInitializationRenewInterval = 10 * time.Millisecond
	key := stateInitializationTestKey()
	_, err := service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	ownerStarted := make(chan struct{})
	ownerDone := make(chan error, 1)
	go func() {
		_, _, ownerErr := service.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"state",
			func(value []byte) bool { return len(value) > 0 },
			func(ctx context.Context) ([]byte, error) {
				close(ownerStarted)
				<-ctx.Done()
				return nil, ctx.Err()
			},
		)
		ownerDone <- ownerErr
	}()
	<-ownerStarted

	route, err := service.resolveStateInitializationRoute(context.Background(), key)
	require.NoError(t, err)
	leaseKey := service.stateInitializationLeaseKey(route, key, "state")
	require.NoError(t, service.redisClient.Set(
		context.Background(),
		leaseKey,
		"replacement-owner",
		time.Minute,
	).Err())

	select {
	case err := <-ownerDone:
		require.ErrorContains(t, err, "ownership lost")
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("lease renewal loss did not cancel the initializer")
	}
}

func TestLoadOrInitializeSessionStatePersistsNilValue(t *testing.T) {
	for _, mode := range []CompatMode{CompatModeNone, CompatModeTransition} {
		redisURL, cleanup := setupTestRedis(t)
		service := newStateInitializationTestService(t, redisURL, mode)
		key := stateInitializationTestKey()
		_, err := service.CreateSession(context.Background(), key, nil)
		require.NoError(t, err)

		value, didInitialize, err := service.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"nil-state",
			func(value []byte) bool { return value == nil },
			func(context.Context) ([]byte, error) { return nil, nil },
		)
		require.NoError(t, err)
		require.True(t, didInitialize)
		require.Nil(t, value)

		var calls atomic.Int32
		value, didInitialize, err = service.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"nil-state",
			func(value []byte) bool { return value == nil },
			func(context.Context) ([]byte, error) {
				calls.Add(1)
				return []byte("unexpected"), nil
			},
		)
		require.NoError(t, err)
		require.False(t, didInitialize)
		require.Nil(t, value)
		require.Zero(t, calls.Load())
		require.NoError(t, service.Close())
		cleanup()
	}
}

func TestLoadOrInitializeSessionStateRejectsInvalidArguments(t *testing.T) {
	redisURL, cleanup := setupTestRedis(t)
	t.Cleanup(cleanup)
	service := newStateInitializationTestService(t, redisURL, CompatModeNone)
	key := stateInitializationTestKey()
	_, err := service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)
	initialize := func(context.Context) ([]byte, error) { return []byte("value"), nil }
	validate := func([]byte) bool { return true }

	_, _, err = service.LoadOrInitializeSessionState(
		context.Background(),
		key,
		session.StateAppPrefix+"state",
		validate,
		initialize,
	)
	require.Error(t, err)
	_, _, err = service.LoadOrInitializeSessionState(
		context.Background(),
		key,
		"state",
		nil,
		initialize,
	)
	require.Error(t, err)
	_, _, err = service.LoadOrInitializeSessionState(
		context.Background(),
		key,
		"state",
		validate,
		nil,
	)
	require.Error(t, err)
}

type stateInitializationTestResult struct {
	value         []byte
	didInitialize bool
	err           error
}

func stateInitializationTestKey() session.Key {
	return session.Key{AppName: "app", UserID: "user", SessionID: "session"}
}

func newStateInitializationTestService(
	t *testing.T,
	redisURL string,
	mode CompatMode,
	additionalOpts ...ServiceOpt,
) *Service {
	t.Helper()
	opts := []ServiceOpt{
		WithRedisClientURL(redisURL),
		WithCompatMode(mode),
	}
	opts = append(opts, additionalOpts...)
	service, err := NewService(opts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, service.Close()) })
	return service
}

var _ session.StateInitializationService = (*Service)(nil)
