//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package postgres

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
		Dialect: sqlrevision.PostgreSQL,
		Tables: sqlrevision.Tables{
			States:    s.tableSessionStates,
			Events:    s.tableSessionEvents,
			Tracks:    s.tableSessionTracks,
			Summaries: s.tableSessionSummaries,
			Revisions: s.tableSessionRevisions,
			Archives:  s.tableRevisionArchives,
		},
	}
}

func (s *Service) attachRevisionGeneration(
	ctx context.Context,
	key session.Key,
	sess *session.Session,
) error {
	if s.tableSessionRevisions == "" {
		return nil
	}
	return s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
		return s.revisionStore().AttachGeneration(ctx, tx, key, sess)
	})
}

func (s *Service) revisionGeneration(
	ctx context.Context,
	key session.Key,
) (uint64, error) {
	if s.tableSessionRevisions == "" {
		return 0, nil
	}
	var generation uint64
	err := s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
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
	if s.tableSessionRevisions == "" || len(keys) == 0 {
		return make(map[session.Key]uint64), nil
	}
	var generations map[session.Key]uint64
	err := s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		generations, err = s.revisionStore().Generations(ctx, tx, keys)
		return err
	})
	return generations, err
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

// ReplaceLatestTurn atomically restores the projection immediately before the
// latest persisted runner turn.
func (s *Service) ReplaceLatestTurn(
	ctx context.Context,
	req sessionrevision.LatestTurnReplacementRequest,
) (*sessionrevision.LatestTurnReplacementResult, error) {
	if err := sessionrevision.ValidateLatestTurnReplacementRequest(req); err != nil {
		return nil, err
	}
	if err := s.flushRevisionPersistence(ctx, req.Key); err != nil {
		return nil, err
	}
	var result *sessionrevision.LatestTurnReplacementResult
	err := s.pgClient.Transaction(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = s.revisionStore().ReplaceLatestTurn(ctx, tx, req)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("replace latest turn: %w", err)
	}
	return s.replacementResultWithScopedState(ctx, req.Key, result)
}

func (s *Service) replacementResultWithScopedState(
	ctx context.Context,
	key session.Key,
	result *sessionrevision.LatestTurnReplacementResult,
) (*sessionrevision.LatestTurnReplacementResult, error) {
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
