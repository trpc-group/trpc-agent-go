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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	defaultStateInitializationLeaseTTL      = 30 * time.Second
	defaultStateInitializationRenewInterval = 10 * time.Second
	defaultStateInitializationPollMin       = 50 * time.Millisecond
	defaultStateInitializationPollMax       = 500 * time.Millisecond
	stateInitializationAbortTimeout         = time.Second
)

func (s *Service) effectiveStateInitializationLeaseTTL() time.Duration {
	if s.stateInitializationLeaseTTL <= 0 {
		return time.Millisecond
	}
	return s.stateInitializationLeaseTTL
}

type stateInitializationRoute uint8

const (
	stateInitializationRouteHashIdx stateInitializationRoute = iota + 1
	stateInitializationRouteZSet
)

func (r stateInitializationRoute) String() string {
	switch r {
	case stateInitializationRouteHashIdx:
		return "hashidx"
	case stateInitializationRouteZSet:
		return "zset"
	default:
		return "unknown"
	}
}

// LoadOrInitializeSessionState implements session.StateInitializationService.
func (s *Service) LoadOrInitializeSessionState(
	ctx context.Context,
	key session.Key,
	stateKey string,
	validate func([]byte) bool,
	initialize func(context.Context) ([]byte, error),
) ([]byte, bool, error) {
	if err := validateStateInitializationArguments(
		key,
		stateKey,
		validate,
		initialize,
	); err != nil {
		return nil, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	route, err := s.resolveStateInitializationRoute(ctx, key)
	if err != nil {
		return nil, false, err
	}
	value, present, expectedGeneration, err := s.loadStateInitializationValue(
		ctx,
		route,
		key,
		stateKey,
	)
	if err != nil {
		return nil, false, err
	}

	pollDelay := s.stateInitializationPollMin
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if present && validate(cloneStateInitializationValue(value)) {
			return cloneStateInitializationValue(value), false, nil
		}

		leaseKey := s.stateInitializationLeaseKey(route, key, stateKey)
		ownerToken := uuid.NewString()
		leaseTTL := s.effectiveStateInitializationLeaseTTL()
		leaseDeadline := time.Now().Add(leaseTTL)
		acquired, err := s.redisClient.SetNX(
			ctx,
			leaseKey,
			ownerToken,
			leaseTTL,
		).Result()
		if err != nil {
			return nil, false, fmt.Errorf("acquire session state initialization lease: %w", err)
		}
		if acquired {
			return s.initializeSessionState(
				ctx,
				route,
				key,
				stateKey,
				leaseKey,
				ownerToken,
				leaseDeadline,
				expectedGeneration,
				validate,
				initialize,
			)
		}

		if err := waitForStateInitializationPoll(ctx, pollDelay); err != nil {
			return nil, false, err
		}
		currentRoute, err := s.resolveStateInitializationRoute(ctx, key)
		if err != nil {
			return nil, false, err
		}
		if currentRoute != route {
			return nil, false, fmt.Errorf(
				"initialize session state: storage route changed from %s to %s",
				route,
				currentRoute,
			)
		}
		var generation string
		value, present, generation, err = s.loadStateInitializationValue(
			ctx,
			route,
			key,
			stateKey,
		)
		if err != nil {
			return nil, false, err
		}
		if generation != expectedGeneration {
			return nil, false, errors.New(
				"initialize session state: session generation changed while waiting for ownership",
			)
		}
		pollDelay *= 2
		if pollDelay > s.stateInitializationPollMax {
			pollDelay = s.stateInitializationPollMax
		}
	}
}

func validateStateInitializationArguments(
	key session.Key,
	stateKey string,
	validate func([]byte) bool,
	initialize func(context.Context) ([]byte, error),
) error {
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	if strings.TrimSpace(stateKey) == "" {
		return errors.New("state key is required")
	}
	if strings.HasPrefix(stateKey, session.StateAppPrefix) {
		return fmt.Errorf(
			"redis session service initialize session state failed: %s is not allowed, use UpdateAppState instead",
			stateKey,
		)
	}
	if strings.HasPrefix(stateKey, session.StateUserPrefix) {
		return fmt.Errorf(
			"redis session service initialize session state failed: %s is not allowed, use UpdateUserState instead",
			stateKey,
		)
	}
	if validate == nil {
		return errors.New("state validation function is required")
	}
	if initialize == nil {
		return errors.New("state initialization function is required")
	}
	return nil
}

func (s *Service) initializeSessionState(
	ctx context.Context,
	route stateInitializationRoute,
	key session.Key,
	stateKey string,
	leaseKey string,
	ownerToken string,
	leaseDeadline time.Time,
	expectedGeneration string,
	validate func([]byte) bool,
	initialize func(context.Context) ([]byte, error),
) ([]byte, bool, error) {
	owned := true
	initializeCtx, cancelInitialize := context.WithCancel(ctx)
	defer cancelInitialize()
	renewal := s.startStateInitializationRenewal(
		ctx,
		leaseKey,
		ownerToken,
		cancelInitialize,
		leaseDeadline,
	)
	defer func() {
		_ = renewal.stop()
		if owned {
			s.abortStateInitializationLeaseForCleanup(
				ctx,
				leaseKey,
				ownerToken,
			)
		}
	}()

	currentRoute, err := s.resolveStateInitializationRoute(initializeCtx, key)
	if err != nil {
		return nil, false, err
	}
	if currentRoute != route {
		return nil, false, fmt.Errorf(
			"initialize session state: storage route changed from %s to %s",
			route,
			currentRoute,
		)
	}
	value, present, generation, err := s.loadStateInitializationValue(
		initializeCtx,
		route,
		key,
		stateKey,
	)
	if err != nil {
		return nil, false, err
	}
	if generation != expectedGeneration {
		return nil, false, errors.New(
			"initialize session state: session generation changed before ownership was established",
		)
	}
	if present && validate(cloneStateInitializationValue(value)) {
		if renewalErr := renewal.stop(); renewalErr != nil {
			return nil, false, renewalErr
		}
		released, releaseErr := s.abortStateInitializationLease(
			initializeCtx,
			leaseKey,
			ownerToken,
		)
		if releaseErr != nil {
			return nil, false, releaseErr
		}
		if !released {
			return nil, false, errors.New("initialize session state: lease ownership lost before recheck completed")
		}
		owned = false
		return cloneStateInitializationValue(value), false, nil
	}

	renewStarted := time.Now()
	ownedNow, renewErr := s.renewStateInitializationLease(
		initializeCtx,
		leaseKey,
		ownerToken,
	)
	if renewErr != nil {
		if renewalErr := renewal.stop(); renewalErr != nil {
			return nil, false, errors.Join(renewalErr, renewErr)
		}
		return nil, false, renewErr
	}
	if !ownedNow {
		if renewalErr := renewal.stop(); renewalErr != nil {
			return nil, false, renewalErr
		}
		return nil, false, errors.New("initialize session state: lease ownership lost before callback")
	}
	renewal.extendDeadline(
		renewStarted.Add(s.effectiveStateInitializationLeaseTTL()),
	)
	value, callbackErr := initialize(initializeCtx)
	renewalErr := renewal.stop()
	if callbackErr != nil {
		callbackErr = fmt.Errorf("initialize session state: %w", callbackErr)
		if renewalErr != nil {
			return nil, false, errors.Join(renewalErr, callbackErr)
		}
		return nil, false, callbackErr
	}
	if err := initializeCtx.Err(); err != nil {
		if renewalErr != nil {
			return nil, false, errors.Join(renewalErr, err)
		}
		return nil, false, err
	}
	if renewalErr != nil {
		return nil, false, renewalErr
	}
	value = cloneStateInitializationValue(value)
	if !validate(cloneStateInitializationValue(value)) {
		return nil, false, errors.New("initialize session state: callback returned an invalid value")
	}

	result, err := s.commitStateInitialization(
		initializeCtx,
		route,
		key,
		stateKey,
		value,
		expectedGeneration,
		leaseKey,
		ownerToken,
	)
	if err != nil {
		return nil, false, err
	}
	switch result {
	case 1:
		owned = false
		return cloneStateInitializationValue(value), true, nil
	case 0:
		owned = false
		return nil, false, errors.New("initialize session state: lease ownership lost before commit")
	case -1:
		owned = false
		return nil, false, errors.New("initialize session state: session not found during commit")
	case -2:
		owned = false
		return nil, false, errors.New("initialize session state: session generation changed during commit")
	default:
		return nil, false, fmt.Errorf(
			"initialize session state: unexpected commit result %d",
			result,
		)
	}
}

func (s *Service) resolveStateInitializationRoute(
	ctx context.Context,
	key session.Key,
) (stateInitializationRoute, error) {
	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, key)
	if err != nil {
		return 0, fmt.Errorf("initialize session state: check session existence: %w", err)
	}
	if s.compatEnabled() && zsetExists {
		return stateInitializationRouteZSet, nil
	}
	if hashidxExists {
		return stateInitializationRouteHashIdx, nil
	}
	return 0, errors.New("initialize session state: session not found")
}

func (s *Service) loadStateInitializationValue(
	ctx context.Context,
	route stateInitializationRoute,
	key session.Key,
	stateKey string,
) ([]byte, bool, string, error) {
	var (
		value      []byte
		present    bool
		generation string
		exists     bool
		err        error
	)
	switch route {
	case stateInitializationRouteHashIdx:
		value, present, generation, exists, err = s.hashidxClient.LoadSessionStateValue(
			ctx,
			key,
			stateKey,
		)
	case stateInitializationRouteZSet:
		value, present, generation, exists, err = s.zsetClient.LoadSessionStateValue(
			ctx,
			key,
			stateKey,
		)
	default:
		return nil, false, "", fmt.Errorf("initialize session state: unknown storage route %d", route)
	}
	if err != nil {
		return nil, false, "", err
	}
	if !exists {
		return nil, false, "", errors.New("initialize session state: session not found")
	}
	if generation == "" {
		return nil, false, "", errors.New("initialize session state: session generation is missing")
	}
	return value, present, generation, nil
}

func (s *Service) stateInitializationLeaseKey(
	route stateInitializationRoute,
	key session.Key,
	stateKey string,
) string {
	digest := sha256.Sum256([]byte(stateKey))
	encodedDigest := hex.EncodeToString(digest[:])
	switch route {
	case stateInitializationRouteZSet:
		return s.zsetClient.StateInitializationLeaseKey(key, encodedDigest)
	default:
		return s.hashidxClient.StateInitializationLeaseKey(key, encodedDigest)
	}
}

func (s *Service) commitStateInitialization(
	ctx context.Context,
	route stateInitializationRoute,
	key session.Key,
	stateKey string,
	value []byte,
	generation string,
	leaseKey string,
	ownerToken string,
) (int, error) {
	switch route {
	case stateInitializationRouteHashIdx:
		return s.hashidxClient.CommitStateInitialization(
			ctx,
			key,
			stateKey,
			value,
			generation,
			leaseKey,
			ownerToken,
		)
	case stateInitializationRouteZSet:
		return s.zsetClient.CommitStateInitialization(
			ctx,
			key,
			stateKey,
			value,
			generation,
			leaseKey,
			ownerToken,
		)
	default:
		return 0, fmt.Errorf("initialize session state: unknown storage route %d", route)
	}
}

func (s *Service) runStateInitializationScript(
	ctx context.Context,
	script *redis.Script,
	keys []string,
	args ...any,
) *redis.Cmd {
	if s.opts.disableScriptCache {
		return script.Eval(ctx, s.redisClient, keys, args...)
	}
	return script.Run(ctx, s.redisClient, keys, args...)
}

func (s *Service) renewStateInitializationLease(
	ctx context.Context,
	leaseKey string,
	ownerToken string,
) (bool, error) {
	ttlMillis := s.effectiveStateInitializationLeaseTTL().Milliseconds()
	if ttlMillis <= 0 {
		ttlMillis = 1
	}
	result, err := s.runStateInitializationScript(
		ctx,
		luaRenewStateInitializationLease,
		[]string{leaseKey},
		ownerToken,
		ttlMillis,
	).Int()
	if err != nil {
		return false, fmt.Errorf("renew session state initialization lease: %w", err)
	}
	return result == 1, nil
}

func (s *Service) abortStateInitializationLease(
	ctx context.Context,
	leaseKey string,
	ownerToken string,
) (bool, error) {
	result, err := s.runStateInitializationScript(
		ctx,
		luaAbortStateInitializationLease,
		[]string{leaseKey},
		ownerToken,
	).Int()
	if err != nil {
		return false, fmt.Errorf("abort session state initialization lease: %w", err)
	}
	return result == 1, nil
}

func (s *Service) abortStateInitializationLeaseForCleanup(
	ctx context.Context,
	leaseKey string,
	ownerToken string,
) {
	if ctx == nil {
		ctx = context.Background()
	} else {
		ctx = context.WithoutCancel(ctx)
	}
	cleanupCtx, cancel := context.WithTimeout(
		ctx,
		stateInitializationAbortTimeout,
	)
	defer cancel()
	_, _ = s.abortStateInitializationLease(cleanupCtx, leaseKey, ownerToken)
}

type stateInitializationRenewal struct {
	cancel     context.CancelFunc
	done       chan struct{}
	lost       chan error
	once       sync.Once
	deadlineMu sync.Mutex
	deadline   time.Time
}

func (s *Service) startStateInitializationRenewal(
	ctx context.Context,
	leaseKey string,
	ownerToken string,
	cancelInitialize context.CancelFunc,
	leaseDeadline time.Time,
) *stateInitializationRenewal {
	renewCtx, cancel := context.WithCancel(ctx)
	renewal := &stateInitializationRenewal{
		cancel:   cancel,
		done:     make(chan struct{}),
		lost:     make(chan error, 1),
		deadline: leaseDeadline,
	}
	interval := s.stateInitializationRenewInterval
	if interval <= 0 {
		interval = s.effectiveStateInitializationLeaseTTL() / 3
	}
	if interval <= 0 {
		interval = time.Millisecond
	}
	go func() {
		defer close(renewal.done)
		for {
			delay := interval
			if deadline := renewal.currentDeadline(); !deadline.IsZero() {
				untilDeadline := time.Until(deadline)
				if untilDeadline <= 0 {
					err := errors.New(
						"renew session state initialization lease: lease deadline exceeded",
					)
					renewal.reportLoss(err, cancelInitialize)
					return
				}
				if untilDeadline < delay {
					delay = untilDeadline
				}
			}
			timer := time.NewTimer(delay)
			select {
			case <-renewCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			deadline := renewal.currentDeadline()
			if !deadline.IsZero() && !time.Now().Before(deadline) {
				renewal.reportLoss(
					errors.New("renew session state initialization lease: lease deadline exceeded"),
					cancelInitialize,
				)
				return
			}
			renewStarted := time.Now()
			renewCtxForCall := renewCtx
			cancelRenewCtx := func() {}
			if !deadline.IsZero() {
				renewCtxForCall, cancelRenewCtx = context.WithDeadline(renewCtx, deadline)
			}
			renewed, err := s.renewStateInitializationLease(
				renewCtxForCall,
				leaseKey,
				ownerToken,
			)
			cancelRenewCtx()
			if renewCtx.Err() != nil {
				return
			}
			if err == nil && !renewed {
				err = errors.New("renew session state initialization lease: ownership lost")
			}
			if err != nil {
				renewal.reportLoss(err, cancelInitialize)
				return
			}
			renewal.extendDeadline(
				renewStarted.Add(s.effectiveStateInitializationLeaseTTL()),
			)
		}
	}()
	return renewal
}

func (r *stateInitializationRenewal) reportLoss(
	err error,
	cancelInitialize context.CancelFunc,
) {
	if err == nil {
		return
	}
	select {
	case r.lost <- err:
	default:
	}
	cancelInitialize()
}

func (r *stateInitializationRenewal) currentDeadline() time.Time {
	r.deadlineMu.Lock()
	defer r.deadlineMu.Unlock()
	return r.deadline
}

func (r *stateInitializationRenewal) extendDeadline(deadline time.Time) {
	r.deadlineMu.Lock()
	if deadline.After(r.deadline) {
		r.deadline = deadline
	}
	r.deadlineMu.Unlock()
}

func (r *stateInitializationRenewal) stop() error {
	if r == nil {
		return nil
	}
	r.once.Do(r.cancel)
	<-r.done
	select {
	case err := <-r.lost:
		return err
	default:
		return nil
	}
}

func waitForStateInitializationPoll(
	ctx context.Context,
	delay time.Duration,
) error {
	if delay <= 0 {
		delay = time.Millisecond
	}
	half := delay / 2
	if half > 0 {
		delay = half + time.Duration(rand.Int63n(int64(half)+1))
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cloneStateInitializationValue(value []byte) []byte {
	if value == nil {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)
	return cloned
}
