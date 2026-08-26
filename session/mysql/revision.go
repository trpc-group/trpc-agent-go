//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/internal/session/sqlrevision"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func (s *Service) revisionStore() sqlrevision.Store {
	return sqlrevision.Store{
		Dialect:          sqlrevision.MySQL,
		SoftDelete:       s.opts.softDelete,
		ReuseSummaryRows: true,
		Tables: sqlrevision.Tables{
			States:    s.tableSessionStates,
			Events:    s.tableSessionEvents,
			Tracks:    s.tableSessionTracks,
			Summaries: s.tableSessionSummaries,
		},
	}
}

func (s *Service) revisionGeneration(
	ctx context.Context,
	key session.Key,
) (uint64, error) {
	var generation uint64
	err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		generation, err = s.revisionStore().Generation(ctx, tx, key)
		return err
	})
	return generation, err
}

func (s *Service) revisionGenerations(
	ctx context.Context,
	keys []session.Key,
) (map[session.Key]uint64, error) {
	if len(keys) == 0 {
		return make(map[session.Key]uint64), nil
	}
	var generations map[session.Key]uint64
	err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		generations, err = s.revisionStore().Generations(ctx, tx, keys)
		return err
	})
	return generations, err
}

func (s *Service) loadStableProjection(
	ctx context.Context,
	key session.Key,
	readProjection func(context.Context) (*session.Session, error),
) (*session.Session, error) {
	projection, err := readProjection(ctx)
	if err != nil || projection == nil {
		return projection, err
	}
	before, ok := sessionrevision.Generation(projection)
	if !ok || !sessionrevision.RecordActive(projection) {
		return projection, nil
	}
	after, err := s.revisionGeneration(ctx, key)
	if err != nil {
		return nil, err
	}
	if before == after {
		return projection, nil
	}
	return sessionrevision.LoadStableProjection(
		ctx,
		func(ctx context.Context) (uint64, error) {
			return s.revisionGeneration(ctx, key)
		},
		readProjection,
	)
}

func (s *Service) flushRevisionPersistence(
	ctx context.Context,
	key session.Key,
) error {
	if !s.opts.enableAsyncPersist {
		return nil
	}
	return errors.Join(
		s.flushEventPersistence(ctx, key),
		s.flushTrackPersistence(ctx, key),
	)
}

func (s *Service) flushEventPersistence(
	ctx context.Context,
	key session.Key,
) error {
	if !s.opts.enableAsyncPersist {
		return nil
	}
	hash := session.NewSession(key.AppName, key.UserID, key.SessionID).Hash
	eventBarrier := &sessionEventPair{
		key: key, done: make(chan error), barrierCtx: ctx,
	}
	select {
	case s.eventPairChans[hash%len(s.eventPairChans)] <- eventBarrier:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-eventBarrier.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) flushTrackPersistence(ctx context.Context, key session.Key) error {
	if !s.opts.enableAsyncPersist {
		return nil
	}
	var flushErr error
	for _, ch := range s.trackEventChans {
		barrier := &trackEventPair{
			key: key, done: make(chan error), barrierCtx: ctx,
		}
		select {
		case ch <- barrier:
		case <-ctx.Done():
			return errors.Join(flushErr, ctx.Err())
		}
		select {
		case err := <-barrier.done:
			flushErr = errors.Join(flushErr, err)
		case <-ctx.Done():
			return errors.Join(flushErr, ctx.Err())
		}
	}
	return flushErr
}

// Rewind atomically restores a retained pre-request session boundary.
func (s *Service) Rewind(
	ctx context.Context,
	req session.RewindRequest,
) (*session.RewindResult, error) {
	if err := sessionrevision.ValidateRewindRequest(req); err != nil {
		return nil, err
	}
	if err := s.flushRevisionPersistence(ctx, req.Key); err != nil {
		return nil, err
	}
	var result *sessionrevision.StorageRewindResult
	err := s.mysqlClient.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = s.revisionStore().Rewind(ctx, tx, req)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("rewind session: %w", err)
	}
	active, err := s.rewindResultWithScopedState(ctx, req.Key, result)
	if err != nil {
		return nil, err
	}
	return &session.RewindResult{Session: active.ActiveSession}, nil
}

func (s *Service) rewindResultWithScopedState(
	ctx context.Context,
	key session.Key,
	result *sessionrevision.StorageRewindResult,
) (*sessionrevision.StorageRewindResult, error) {
	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, err
	}
	userState, err := s.ListUserStates(ctx, session.UserKey{
		AppName: key.AppName,
		UserID:  key.UserID,
	})
	if err != nil {
		return nil, err
	}
	result.ActiveSession = mergeState(appState, userState, result.ActiveSession)
	return result, nil
}
