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
	"sync"
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
	route, err := ownerService.resolveStateInitializationRoute(
		context.Background(), key,
	)
	require.NoError(t, err)
	leaseKey := ownerService.stateInitializationLeaseKey(route, key, "state")
	ownerToken, err := ownerService.redisClient.Get(
		context.Background(), leaseKey,
	).Result()
	require.NoError(t, err)

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
		require.Eventually(t, func() bool {
			currentToken, getErr := ownerService.redisClient.Get(
				context.Background(), leaseKey,
			).Result()
			return getErr == nil &&
				currentToken == ownerToken &&
				mr.TTL(leaseKey) > 80*time.Millisecond
		}, time.Second, 5*time.Millisecond)
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
			var waiterObservedGenerationOnce sync.Once
			var waiterCallbackCalls atomic.Int32
			waiterDone := make(chan error, 1)
			go func() {
				_, _, waiterErr := service.LoadOrInitializeSessionState(
					context.Background(),
					key,
					"state",
					func(value []byte) bool {
						if string(value) == "invalid" {
							waiterObservedGenerationOnce.Do(func() {
								close(waiterObservedGeneration)
							})
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

func TestLoadOrInitializeSessionStateRenewalLossPreservesCauseWhenCallbackReturnsValue(
	t *testing.T,
) {
	redisURL, cleanup := setupTestRedis(t)
	t.Cleanup(cleanup)
	service := newStateInitializationTestService(t, redisURL, CompatModeNone)
	service.stateInitializationLeaseTTL = time.Second
	service.stateInitializationRenewInterval = 10 * time.Millisecond
	key := stateInitializationTestKey()
	_, err := service.CreateSession(context.Background(), key, nil)
	require.NoError(t, err)

	ownerStarted := make(chan struct{})
	ownerDone := make(chan stateInitializationTestResult, 1)
	go func() {
		value, initialized, ownerErr := service.LoadOrInitializeSessionState(
			context.Background(),
			key,
			"state",
			func(value []byte) bool { return len(value) > 0 },
			func(ctx context.Context) ([]byte, error) {
				close(ownerStarted)
				<-ctx.Done()
				return []byte("ignored-cancellation"), nil
			},
		)
		ownerDone <- stateInitializationTestResult{
			value:         value,
			didInitialize: initialized,
			err:           ownerErr,
		}
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
	case result := <-ownerDone:
		require.Nil(t, result.value)
		require.False(t, result.didInitialize)
		require.ErrorContains(t, result.err, "ownership lost")
		require.ErrorIs(t, result.err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("lease renewal loss did not finish the initializer")
	}
	stored, err := service.GetSession(context.Background(), key)
	require.NoError(t, err)
	_, present := stored.GetState("state")
	require.False(t, present)
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

func TestStateInitializationOwnerRecheckAndFailurePaths(t *testing.T) {
	type ownerFixture struct {
		service    *Service
		key        session.Key
		route      stateInitializationRoute
		generation string
		leaseKey   string
		ownerToken string
	}
	newOwner := func(t *testing.T, state session.StateMap) ownerFixture {
		t.Helper()
		redisURL, cleanup := setupTestRedis(t)
		t.Cleanup(cleanup)
		service := newStateInitializationTestService(t, redisURL, CompatModeNone)
		key := stateInitializationTestKey()
		_, err := service.CreateSession(context.Background(), key, state)
		require.NoError(t, err)
		route, err := service.resolveStateInitializationRoute(context.Background(), key)
		require.NoError(t, err)
		_, _, generation, err := service.loadStateInitializationValue(
			context.Background(), route, key, "state",
		)
		require.NoError(t, err)
		leaseKey := service.stateInitializationLeaseKey(route, key, "state")
		ownerToken := uuid.NewString()
		require.NoError(t, service.redisClient.Set(
			context.Background(), leaseKey, ownerToken, time.Minute,
		).Err())
		return ownerFixture{
			service:    service,
			key:        key,
			route:      route,
			generation: generation,
			leaseKey:   leaseKey,
			ownerToken: ownerToken,
		}
	}
	run := func(
		fixture ownerFixture,
		ctx context.Context,
		route stateInitializationRoute,
		generation string,
		validate func([]byte) bool,
		initialize func(context.Context) ([]byte, error),
	) ([]byte, bool, error) {
		return fixture.service.initializeSessionState(
			ctx,
			route,
			fixture.key,
			"state",
			fixture.leaseKey,
			fixture.ownerToken,
			time.Now().Add(time.Minute),
			generation,
			validate,
			initialize,
		)
	}

	t.Run("uses value committed before owner recheck", func(t *testing.T) {
		fixture := newOwner(t, session.StateMap{"state": []byte("ready")})
		value, initialized, err := run(
			fixture,
			context.Background(),
			fixture.route,
			fixture.generation,
			func(value []byte) bool { return string(value) == "ready" },
			func(context.Context) ([]byte, error) {
				t.Fatal("initializer must not run for a valid rechecked value")
				return nil, nil
			},
		)
		require.NoError(t, err)
		require.False(t, initialized)
		require.Equal(t, "ready", string(value))
		require.Zero(t, fixture.service.redisClient.Exists(
			context.Background(), fixture.leaseKey,
		).Val())
	})

	t.Run("rejects storage route change", func(t *testing.T) {
		fixture := newOwner(t, nil)
		_, _, err := run(
			fixture,
			context.Background(),
			stateInitializationRouteZSet,
			fixture.generation,
			func([]byte) bool { return false },
			func(context.Context) ([]byte, error) { return []byte("value"), nil },
		)
		require.ErrorContains(t, err, "storage route changed")
	})

	t.Run("rejects generation change before callback", func(t *testing.T) {
		fixture := newOwner(t, nil)
		_, _, err := run(
			fixture,
			context.Background(),
			fixture.route,
			"stale-generation",
			func([]byte) bool { return false },
			func(context.Context) ([]byte, error) { return []byte("value"), nil },
		)
		require.ErrorContains(t, err, "generation changed before ownership")
	})

	t.Run("rejects owner lost before callback", func(t *testing.T) {
		fixture := newOwner(t, nil)
		require.NoError(t, fixture.service.redisClient.Set(
			context.Background(), fixture.leaseKey, "new-owner", time.Minute,
		).Err())
		_, _, err := run(
			fixture,
			context.Background(),
			fixture.route,
			fixture.generation,
			func([]byte) bool { return false },
			func(context.Context) ([]byte, error) { return []byte("value"), nil },
		)
		require.ErrorContains(t, err, "lease ownership lost before callback")
	})

	t.Run("rejects invalid callback value", func(t *testing.T) {
		fixture := newOwner(t, nil)
		_, _, err := run(
			fixture,
			context.Background(),
			fixture.route,
			fixture.generation,
			func(value []byte) bool { return string(value) == "valid" },
			func(context.Context) ([]byte, error) { return []byte("invalid"), nil },
		)
		require.ErrorContains(t, err, "callback returned an invalid value")
	})

	t.Run("honors caller cancellation after callback", func(t *testing.T) {
		fixture := newOwner(t, nil)
		ctx, cancel := context.WithCancel(context.Background())
		_, _, err := run(
			fixture,
			ctx,
			fixture.route,
			fixture.generation,
			func([]byte) bool { return true },
			func(context.Context) ([]byte, error) {
				cancel()
				return []byte("value"), nil
			},
		)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("releases lease before callback panic propagates", func(t *testing.T) {
		fixture := newOwner(t, nil)
		require.PanicsWithValue(t, "boom", func() {
			_, _, _ = run(
				fixture,
				context.Background(),
				fixture.route,
				fixture.generation,
				func([]byte) bool { return false },
				func(context.Context) ([]byte, error) { panic("boom") },
			)
		})
		require.Zero(t, fixture.service.redisClient.Exists(
			context.Background(), fixture.leaseKey,
		).Val())

		value, initialized, err := fixture.service.LoadOrInitializeSessionState(
			context.Background(),
			fixture.key,
			"state",
			func(value []byte) bool { return string(value) == "valid" },
			func(context.Context) ([]byte, error) { return []byte("valid"), nil },
		)
		require.NoError(t, err)
		require.True(t, initialized)
		require.Equal(t, "valid", string(value))
	})

	t.Run("reports session deleted during callback", func(t *testing.T) {
		fixture := newOwner(t, nil)
		_, _, err := run(
			fixture,
			context.Background(),
			fixture.route,
			fixture.generation,
			func(value []byte) bool { return string(value) == "value" },
			func(context.Context) ([]byte, error) {
				require.NoError(t, fixture.service.DeleteSession(
					context.Background(), fixture.key,
				))
				return []byte("value"), nil
			},
		)
		require.ErrorContains(t, err, "session not found during commit")
	})
}

func TestStateInitializationHelpers(t *testing.T) {
	require.Equal(t, "hashidx", stateInitializationRouteHashIdx.String())
	require.Equal(t, "zset", stateInitializationRouteZSet.String())
	require.Equal(t, "unknown", stateInitializationRoute(99).String())

	validate := func([]byte) bool { return true }
	initialize := func(context.Context) ([]byte, error) { return []byte("value"), nil }
	for _, test := range []struct {
		name     string
		key      session.Key
		stateKey string
		validate func([]byte) bool
		init     func(context.Context) ([]byte, error)
	}{
		{
			name:     "invalid session key",
			key:      session.Key{},
			stateKey: "state",
			validate: validate,
			init:     initialize,
		},
		{
			name:     "empty state key",
			key:      stateInitializationTestKey(),
			stateKey: "  ",
			validate: validate,
			init:     initialize,
		},
		{
			name:     "user state key",
			key:      stateInitializationTestKey(),
			stateKey: session.StateUserPrefix + "state",
			validate: validate,
			init:     initialize,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			require.Error(t, validateStateInitializationArguments(
				test.key, test.stateKey, test.validate, test.init,
			))
		})
	}

	redisURL, cleanup := setupTestRedis(t)
	t.Cleanup(cleanup)
	service := newStateInitializationTestService(
		t,
		redisURL,
		CompatModeNone,
		WithDisableScriptCache(true),
	)
	service.stateInitializationLeaseTTL = 0
	require.Equal(t, time.Millisecond, service.effectiveStateInitializationLeaseTTL())
	_, err := service.resolveStateInitializationRoute(
		context.Background(), stateInitializationTestKey(),
	)
	require.ErrorContains(t, err, "session not found")
	_, _, _, err = service.loadStateInitializationValue(
		context.Background(), stateInitializationRoute(99), stateInitializationTestKey(), "state",
	)
	require.ErrorContains(t, err, "unknown storage route")
	_, err = service.commitStateInitialization(
		context.Background(),
		stateInitializationRoute(99),
		stateInitializationTestKey(),
		"state",
		[]byte("value"),
		"generation",
		"lease",
		"owner",
	)
	require.ErrorContains(t, err, "unknown storage route")

	leaseKey := "state-initialization-helper-lease"
	require.NoError(t, service.redisClient.Set(
		context.Background(), leaseKey, "owner", time.Minute,
	).Err())
	renewed, err := service.renewStateInitializationLease(
		context.Background(), leaseKey, "owner",
	)
	require.NoError(t, err)
	require.True(t, renewed)
	released, err := service.abortStateInitializationLease(
		context.Background(), leaseKey, "owner",
	)
	require.NoError(t, err)
	require.True(t, released)
	service.abortStateInitializationLeaseForCleanup(nil, leaseKey, "owner")

	var nilRenewal *stateInitializationRenewal
	require.NoError(t, nilRenewal.stop())
	renewal := &stateInitializationRenewal{
		lost:     make(chan error, 1),
		deadline: time.Now().Add(time.Second),
	}
	canceled := false
	renewal.reportLoss(nil, func() { canceled = true })
	require.False(t, canceled)
	deadline := renewal.currentDeadline()
	renewal.extendDeadline(deadline.Add(-time.Second))
	require.Equal(t, deadline, renewal.currentDeadline())
	require.NoError(t, waitForStateInitializationPoll(context.Background(), 0))
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(
		t,
		waitForStateInitializationPoll(canceledCtx, time.Second),
		context.Canceled,
	)
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
