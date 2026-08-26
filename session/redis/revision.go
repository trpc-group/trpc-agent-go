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
	"errors"
	"fmt"

	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/util"
)

var errAsyncPersistenceClosed = errors.New("redis session async persistence is closed")

func (s *Service) prepareTurnStartWrite(
	ctx context.Context,
	storageType string,
	key session.Key,
	write sessionrevision.Write,
) (sessionrevision.Write, error) {
	var (
		base   *session.Session
		active *session.Session
		record *sessionrevision.PersistedRecord
		intact bool
		err    error
	)
	if storageType == util.StorageTypeZset {
		record, err = s.zsetClient.Revision(ctx, key)
		if err == nil {
			base, intact, err = s.zsetClient.RevisionBoundaryBase(
				ctx, key, record.Projection,
			)
		}
	} else {
		record, err = s.hashidxClient.Revision(ctx, key)
		if err == nil {
			base, intact, err = s.hashidxClient.RevisionBoundaryBase(
				ctx, key, record.Projection,
			)
		}
	}
	if err != nil {
		return write, fmt.Errorf("load authoritative pre-turn session: %w", err)
	}
	if base == nil {
		return write, fmt.Errorf("session not found")
	}
	projection := sessionrevision.CloneProjection(record.Projection)
	if !sessionrevision.ProjectionInitialized(record) || !intact {
		if storageType == util.StorageTypeZset {
			active, err = s.zsetClient.RevisionProjection(ctx, key)
		} else {
			active, err = s.hashidxClient.RevisionProjection(ctx, key)
		}
		if err != nil {
			return write, fmt.Errorf("load authoritative pre-turn projection: %w", err)
		}
		if active == nil {
			return write, fmt.Errorf("session not found")
		}
		bootstrap := &sessionrevision.PersistedRecord{}
		if err := sessionrevision.InitializeProjection(bootstrap, active); err != nil {
			return write, fmt.Errorf("initialize session revision projection: %w", err)
		}
		projection = bootstrap.Projection
		base = active
	}
	write.Boundary, err = sessionrevision.NewBoundaryFromProjection(
		base, projection, write.Start.RestoreState,
	)
	if err != nil {
		return write, fmt.Errorf("capture session boundary before latest turn: %w", err)
	}
	write.Projection = sessionrevision.CloneProjection(projection)
	write.BoundaryRequiresSummary = len(base.Summaries) > 0
	if !write.HasExpectedHead {
		write.ExpectedHead = record.Head
		write.HasExpectedHead = true
	}
	return write, nil
}

// Rewind atomically restores a retained pre-request session boundary.
func (s *Service) Rewind(
	ctx context.Context,
	req session.RewindRequest,
) (*session.RewindResult, error) {
	if err := sessionrevision.ValidateRewindRequest(req); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.flushRevisionPersistence(ctx, req.Key); err != nil {
		return nil, fmt.Errorf("flush session persistence before rewind: %w", err)
	}
	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	var (
		active      *session.Session
		storageType string
	)
	if s.compatEnabled() && zsetExists {
		storageType = util.StorageTypeZset
		active, _, err = s.zsetClient.Rewind(
			ctx,
			req.Key,
			req.TargetRequestID,
			req.ExpectedHeadRequestID,
			req.IdempotencyKey,
		)
	} else if hashidxExists {
		storageType = util.StorageTypeHashIdx
		active, _, err = s.hashidxClient.Rewind(
			ctx,
			req.Key,
			req.TargetRequestID,
			req.ExpectedHeadRequestID,
			req.IdempotencyKey,
		)
	} else {
		return nil, sessionrevision.ErrRewindUnavailable
	}
	if err != nil {
		return nil, err
	}
	if active.ServiceMeta == nil {
		active.ServiceMeta = make(map[string]string)
	}
	active.ServiceMeta[util.ServiceMetaStorageTypeKey] = storageType
	active, err = s.mergeAppUserState(ctx, req.Key, active)
	if err != nil {
		return nil, err
	}
	return &session.RewindResult{Session: active}, nil
}

func (s *Service) flushRevisionPersistence(
	ctx context.Context,
	key session.Key,
) error {
	if !s.opts.enableAsyncPersist {
		return nil
	}
	eventErr := flushPairChannel(ctx, s.eventPairChans, key, &sessionEventPair{})
	trackErr := flushTrackPairChannel(ctx, s.trackEventChans, key)
	return errors.Join(eventErr, trackErr)
}

func flushPairChannel(
	ctx context.Context,
	channels []chan *sessionEventPair,
	key session.Key,
	barrier *sessionEventPair,
) (retErr error) {
	if len(channels) == 0 {
		return nil
	}
	defer recoverClosedChannelPanic(&retErr)
	barrier.key = key
	barrier.done = make(chan error)
	barrier.barrierCtx = ctx
	hash := session.NewSession(key.AppName, key.UserID, key.SessionID).Hash
	// Started workers remain alive until Close closes their channels, so a
	// successful send always has a consumer which will complete the barrier.
	select {
	case channels[hash%len(channels)] <- barrier:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-barrier.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func flushTrackPairChannel(
	ctx context.Context,
	channels []chan *trackEventPair,
	key session.Key,
) (retErr error) {
	if len(channels) == 0 {
		return nil
	}
	defer recoverClosedChannelPanic(&retErr)
	barrier := &trackEventPair{
		key: key, done: make(chan error), barrierCtx: ctx,
	}
	hash := session.NewSession(key.AppName, key.UserID, key.SessionID).Hash
	// Started workers remain alive until Close closes their channels, so a
	// successful send always has a consumer which will complete the barrier.
	select {
	case channels[hash%len(channels)] <- barrier:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-barrier.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func recoverClosedChannelPanic(retErr *error) {
	if recovered := recover(); recovered != nil {
		if panicErr, ok := recovered.(error); ok &&
			panicErr.Error() == "send on closed channel" {
			*retErr = errAsyncPersistenceClosed
			return
		}
		panic(recovered)
	}
}
