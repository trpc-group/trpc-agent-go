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

func (s *Service) prepareTurnStartWrite(
	ctx context.Context,
	storageType string,
	key session.Key,
	write sessionrevision.Write,
) (sessionrevision.Write, error) {
	var (
		active *session.Session
		record *sessionrevision.PersistedRecord
		err    error
	)
	if storageType == util.StorageTypeZset {
		record, err = s.zsetClient.Revision(ctx, key)
		if err == nil {
			active, err = s.zsetClient.RevisionProjection(ctx, key)
		}
	} else {
		record, err = s.hashidxClient.Revision(ctx, key)
		if err == nil {
			active, err = s.hashidxClient.RevisionProjection(ctx, key)
		}
	}
	if err != nil {
		return write, fmt.Errorf("load authoritative pre-turn session: %w", err)
	}
	if active == nil {
		return write, fmt.Errorf("session not found")
	}
	write.Snapshot, err = sessionrevision.Snapshot(active)
	if err != nil {
		return write, fmt.Errorf("snapshot session before latest turn: %w", err)
	}
	write.ExpectedHead = record.Head
	write.HasExpectedHead = true
	return write, nil
}

// ReplaceLatestTurn restores the active session projection to the checkpoint
// immediately before its latest persisted turn for Runner.
func (s *Service) ReplaceLatestTurn(
	ctx context.Context,
	req sessionrevision.LatestTurnReplacementRequest,
) (*sessionrevision.LatestTurnReplacementResult, error) {
	if err := sessionrevision.ValidateLatestTurnReplacementRequest(req); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.flushRevisionPersistence(ctx, req.Key); err != nil {
		return nil, fmt.Errorf("flush session persistence before latest-turn replacement: %w", err)
	}
	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	var (
		active      *session.Session
		applied     bool
		storageType string
	)
	if s.compatEnabled() && zsetExists {
		storageType = util.StorageTypeZset
		active, applied, err = s.zsetClient.ReplaceLatestTurn(
			ctx,
			req.Key,
			req.ExpectedRequestID,
			req.IdempotencyKey,
		)
	} else if hashidxExists {
		storageType = util.StorageTypeHashIdx
		active, applied, err = s.hashidxClient.ReplaceLatestTurn(
			ctx,
			req.Key,
			req.ExpectedRequestID,
			req.IdempotencyKey,
		)
	} else {
		return nil, sessionrevision.ErrLatestTurnReplacementUnavailable
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
	return &sessionrevision.LatestTurnReplacementResult{
		ActiveSession: active,
		Applied:       applied,
	}, nil
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
) error {
	if len(channels) == 0 {
		return nil
	}
	barrier.key = key
	barrier.done = make(chan error)
	barrier.barrierCtx = ctx
	hash := session.NewSession(key.AppName, key.UserID, key.SessionID).Hash
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
) error {
	if len(channels) == 0 {
		return nil
	}
	barrier := &trackEventPair{
		key: key, done: make(chan error), barrierCtx: ctx,
	}
	hash := session.NewSession(key.AppName, key.UserID, key.SessionID).Hash
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
