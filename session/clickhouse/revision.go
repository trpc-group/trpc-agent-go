//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package clickhouse

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	sessionrevision "trpc.group/trpc-go/trpc-agent-go/internal/session/revision"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type revisionHead struct {
	record    *sessionrevision.PersistedRecord
	session   *session.Session
	expiresAt *time.Time
}

func revisionVersion(generation, head uint64) (uint64, error) {
	if generation > math.MaxUint32 || head > math.MaxUint32 {
		return 0, session.ErrLatestTurnReplacementUnavailable
	}
	return generation<<32 | head, nil
}

func (s *Service) loadRevisionHead(
	ctx context.Context,
	key session.Key,
) (*revisionHead, bool, error) {
	if s.tableSessionRevisions == "" {
		return nil, false, nil
	}
	rows, err := s.chClient.Query(ctx, fmt.Sprintf(
		`SELECT record, snapshot, expires_at FROM %s FINAL
		WHERE app_name = ? AND user_id = ? AND session_id = ?
		AND (expires_at IS NULL OR expires_at > ?) AND deleted_at IS NULL`,
		s.tableSessionRevisions,
	), key.AppName, key.UserID, key.SessionID, time.Now())
	if err != nil {
		return nil, false, fmt.Errorf("load session revision: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, false, rows.Err()
	}
	var (
		recordRaw   string
		snapshotRaw string
		expiresAt   *time.Time
	)
	if err := rows.Scan(&recordRaw, &snapshotRaw, &expiresAt); err != nil {
		return nil, false, err
	}
	var record sessionrevision.PersistedRecord
	if err := json.Unmarshal([]byte(recordRaw), &record); err != nil {
		return nil, false, fmt.Errorf("decode session revision: %w", err)
	}
	active, err := sessionrevision.DecodeSnapshot([]byte(snapshotRaw))
	if err != nil {
		return nil, false, fmt.Errorf("decode session revision snapshot: %w", err)
	}
	sessionrevision.SetGeneration(active, record.Generation)
	return &revisionHead{record: &record, session: active, expiresAt: expiresAt}, true, nil
}

func (s *Service) publishRevisionHead(
	ctx context.Context,
	key session.Key,
	record *sessionrevision.PersistedRecord,
	active *session.Session,
	expiresAt *time.Time,
	deletedAt *time.Time,
) error {
	version, err := revisionVersion(record.Generation, record.Head)
	if err != nil {
		return err
	}
	recordRaw, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode session revision: %w", err)
	}
	snapshotRaw, err := sessionrevision.Snapshot(active)
	if err != nil {
		return fmt.Errorf("encode session revision snapshot: %w", err)
	}
	if err := s.chClient.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s (app_name, user_id, session_id, generation, head,
		version, record, snapshot, updated_at, expires_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.tableSessionRevisions,
	), key.AppName, key.UserID, key.SessionID, record.Generation, record.Head,
		version, string(recordRaw), string(snapshotRaw), time.Now(), expiresAt,
		deletedAt); err != nil {
		return fmt.Errorf("publish session revision: %w", err)
	}
	return nil
}

func (s *Service) verifyPublishedRevision(
	ctx context.Context,
	key session.Key,
	record *sessionrevision.PersistedRecord,
) error {
	head, ok, err := s.loadRevisionHead(ctx, key)
	if err != nil {
		return err
	}
	if !ok || head.record.Generation != record.Generation ||
		head.record.Head != record.Head {
		return sessionrevision.ErrStaleGeneration
	}
	return nil
}

func (s *Service) authoritativeRevisionSession(
	ctx context.Context,
	key session.Key,
) (*revisionHead, error) {
	if head, ok, err := s.loadRevisionHead(ctx, key); err != nil {
		return nil, err
	} else if ok {
		return head, nil
	}
	legacy, err := s.getLegacySession(ctx, key, 0, time.Time{})
	if err != nil {
		return nil, err
	}
	if legacy == nil {
		return nil, fmt.Errorf("session not found")
	}
	return &revisionHead{
		record:  &sessionrevision.PersistedRecord{},
		session: legacy,
	}, nil
}

func (s *Service) getSession(
	ctx context.Context,
	key session.Key,
	limit int,
	afterTime time.Time,
) (*session.Session, error) {
	head, ok, err := s.loadRevisionHead(ctx, key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return s.getLegacySession(ctx, key, limit, afterTime)
	}
	active := head.session.Clone()
	active.ApplyEventFiltering(
		session.WithEventNum(limit),
		session.WithEventTime(afterTime),
	)
	if len(active.Events) == 0 {
		active.Summaries = nil
	}
	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, err
	}
	userState, err := s.ListUserStates(ctx, session.UserKey{
		AppName: key.AppName, UserID: key.UserID,
	})
	if err != nil {
		return nil, err
	}
	return mergeState(appState, userState, active), nil
}

func (s *Service) overlayRevisionHeads(
	ctx context.Context,
	sessions []*session.Session,
	metaOnly bool,
	limit int,
	afterTime time.Time,
) ([]*session.Session, error) {
	for i, legacy := range sessions {
		if legacy == nil {
			continue
		}
		key := session.Key{
			AppName: legacy.AppName, UserID: legacy.UserID, SessionID: legacy.ID,
		}
		head, ok, err := s.loadRevisionHead(ctx, key)
		if err != nil {
			return nil, err
		}
		if !ok {
			if !metaOnly {
				loaded, err := s.getLegacySession(ctx, key, limit, afterTime)
				if err != nil {
					return nil, err
				}
				sessions[i] = loaded
			}
			continue
		}
		active := head.session.Clone()
		if metaOnly {
			active.Events = nil
			active.Summaries = nil
		} else {
			active.ApplyEventFiltering(
				session.WithEventNum(limit),
				session.WithEventTime(afterTime),
			)
			if len(active.Events) == 0 {
				active.Summaries = nil
			}
		}
		appState, err := s.ListAppStates(ctx, key.AppName)
		if err != nil {
			return nil, err
		}
		userState, err := s.ListUserStates(ctx, session.UserKey{
			AppName: key.AppName, UserID: key.UserID,
		})
		if err != nil {
			return nil, err
		}
		sessions[i] = mergeState(appState, userState, active)
	}
	return sessions, nil
}

func (s *Service) addEventWithRevision(
	ctx context.Context,
	key session.Key,
	evt *event.Event,
	write sessionrevision.Write,
) error {
	if s.tableSessionRevisions == "" {
		return s.addEvent(ctx, key, evt)
	}
	head, err := s.authoritativeRevisionSession(ctx, key)
	if err != nil {
		return err
	}
	if write.HasExpectedGeneration &&
		head.record.Generation != write.ExpectedGeneration {
		return sessionrevision.ErrStaleGeneration
	}
	if write.Start != nil {
		write.Snapshot, err = sessionrevision.Snapshot(head.session)
		if err != nil {
			return err
		}
	}
	active := head.session.Clone()
	active.UpdateUserSession(evt)
	persisted := evt != nil && evt.Response != nil &&
		!evt.IsPartial && evt.IsValidContent()
	sessionrevision.ApplyEventWrite(head.record, write, evt, persisted)
	if err := s.addEvent(ctx, key, evt); err != nil {
		return err
	}
	expiresAt := calculateExpiresAt(s.opts.sessionTTL)
	if err := s.publishRevisionHead(
		ctx, key, head.record, active, expiresAt, nil,
	); err != nil {
		return err
	}
	return s.verifyPublishedRevision(ctx, key, head.record)
}

func (s *Service) updateSessionStateWithRevision(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	head, err := s.authoritativeRevisionSession(ctx, key)
	if err != nil {
		return fmt.Errorf("clickhouse session service update session state failed: %w", err)
	}
	write := sessionrevision.NewWrite(ctx, nil)
	if write.HasExpectedGeneration &&
		head.record.Generation != write.ExpectedGeneration {
		return sessionrevision.ErrStaleGeneration
	}
	active := head.session.Clone()
	for k, v := range state {
		active.SetState(k, v)
	}
	sessionrevision.ApplyWrite(head.record, write)
	if err := s.updateSessionStateLegacy(ctx, key, state); err != nil {
		return err
	}
	if err := s.publishRevisionHead(
		ctx, key, head.record, active,
		calculateExpiresAt(s.opts.sessionTTL), nil,
	); err != nil {
		return err
	}
	return s.verifyPublishedRevision(ctx, key, head.record)
}

func (s *Service) publishSummaryRevision(
	ctx context.Context,
	key session.Key,
	filterKey string,
	summary *session.Summary,
	write sessionrevision.Write,
) error {
	head, err := s.authoritativeRevisionSession(ctx, key)
	if err != nil {
		return err
	}
	if write.HasExpectedGeneration &&
		head.record.Generation != write.ExpectedGeneration {
		return sessionrevision.ErrStaleGeneration
	}
	active := head.session.Clone()
	if active.Summaries == nil {
		active.Summaries = make(map[string]*session.Summary)
	}
	if summary == nil {
		delete(active.Summaries, filterKey)
	} else {
		cloned := *summary
		active.Summaries[filterKey] = &cloned
	}
	sessionrevision.ApplyWrite(head.record, write)
	if err := s.publishRevisionHead(
		ctx, key, head.record, active, head.expiresAt, nil,
	); err != nil {
		return err
	}
	return s.verifyPublishedRevision(ctx, key, head.record)
}

func (s *Service) flushRevisionPersistence(
	ctx context.Context,
	key session.Key,
) error {
	if !s.opts.enableAsyncPersist {
		return nil
	}
	if len(s.eventPairChans) == 0 {
		return fmt.Errorf("async persist workers are not initialized")
	}
	barrier := &sessionEventPair{done: make(chan error, 1)}
	index := session.NewSession(key.AppName, key.UserID, key.SessionID).Hash %
		len(s.eventPairChans)
	select {
	case s.eventPairChans[index] <- barrier:
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

// ReplaceLatestTurn publishes one new canonical revision head after archiving
// the discarded projection. Readers never observe the staged archive as active.
func (s *Service) ReplaceLatestTurn(
	ctx context.Context,
	req session.LatestTurnReplacementRequest,
) (*session.LatestTurnReplacementResult, error) {
	if err := sessionrevision.ValidateLatestTurnReplacementRequest(req); err != nil {
		return nil, err
	}
	if s.tableSessionRevisions == "" || s.tableRevisionArchives == "" {
		return nil, session.ErrLatestTurnReplacementUnsupported
	}
	if err := s.flushRevisionPersistence(ctx, req.Key); err != nil {
		return nil, err
	}
	head, err := s.authoritativeRevisionSession(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	if _, replayed, err := sessionrevision.LatestTurnReplacementReplay(
		head.record, req.ExpectedRequestID, req.IdempotencyKey,
	); err != nil {
		return nil, err
	} else if replayed {
		active, err := s.replacementResultWithScopedState(ctx, req.Key, head.session)
		if err != nil {
			return nil, err
		}
		return &session.LatestTurnReplacementResult{ActiveSession: active}, nil
	}
	checkpoint, err := sessionrevision.LatestTurnReplacementCheckpoint(
		head.record, req.ExpectedRequestID,
	)
	if err != nil {
		return nil, err
	}
	if head.record.Generation >= math.MaxUint32 {
		return nil, session.ErrLatestTurnReplacementUnavailable
	}
	restored, err := sessionrevision.DecodeSnapshot(checkpoint.Snapshot)
	if err != nil {
		return nil, fmt.Errorf("decode latest-turn checkpoint: %w", err)
	}
	archive, err := sessionrevision.Snapshot(head.session)
	if err != nil {
		return nil, err
	}
	if err := s.chClient.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s (app_name, user_id, session_id, generation,
		snapshot, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.tableRevisionArchives,
	), req.Key.AppName, req.Key.UserID, req.Key.SessionID,
		head.record.Generation, string(archive), time.Now(), head.expiresAt); err != nil {
		return nil, fmt.Errorf("archive discarded revision: %w", err)
	}
	head.record.Generation++
	head.record.Head++
	head.record.Checkpoint = nil
	if head.record.Replays == nil {
		head.record.Replays = make(map[string]sessionrevision.PersistedReplay)
	}
	head.record.Replays[req.IdempotencyKey] = sessionrevision.PersistedReplay{
		RequestID:  req.ExpectedRequestID,
		Generation: head.record.Generation,
		Head:       head.record.Head,
	}
	if err := s.publishRevisionHead(
		ctx, req.Key, head.record, restored, head.expiresAt, nil,
	); err != nil {
		return nil, err
	}
	if err := s.verifyPublishedRevision(ctx, req.Key, head.record); err != nil {
		return nil, err
	}
	sessionrevision.SetGeneration(restored, head.record.Generation)
	active, err := s.replacementResultWithScopedState(ctx, req.Key, restored)
	if err != nil {
		return nil, err
	}
	return &session.LatestTurnReplacementResult{
		ActiveSession: active,
		Applied:       true,
	}, nil
}

func (s *Service) replacementResultWithScopedState(
	ctx context.Context,
	key session.Key,
	active *session.Session,
) (*session.Session, error) {
	appState, err := s.ListAppStates(ctx, key.AppName)
	if err != nil {
		return nil, err
	}
	userState, err := s.ListUserStates(ctx, session.UserKey{
		AppName: key.AppName, UserID: key.UserID,
	})
	if err != nil {
		return nil, err
	}
	return mergeState(appState, userState, active), nil
}

func (s *Service) deleteRevisionHead(
	ctx context.Context,
	key session.Key,
) error {
	head, ok, err := s.loadRevisionHead(ctx, key)
	if err != nil || !ok {
		return err
	}
	if head.record.Head >= math.MaxUint32 {
		return session.ErrLatestTurnReplacementUnavailable
	}
	head.record.Head++
	now := time.Now()
	if err := s.publishRevisionHead(
		ctx, key, head.record, head.session, head.expiresAt, &now,
	); err != nil {
		return err
	}
	if s.tableRevisionArchives != "" {
		if err := s.chClient.Exec(ctx, fmt.Sprintf(
			`ALTER TABLE %s DELETE WHERE app_name = ? AND user_id = ? AND session_id = ?`,
			s.tableRevisionArchives,
		), key.AppName, key.UserID, key.SessionID); err != nil {
			return fmt.Errorf("delete session revision archives: %w", err)
		}
	}
	return nil
}
