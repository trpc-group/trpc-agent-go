//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package clickhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type trackAppendLock struct {
	token chan struct{}
	refs  int
}

// AppendTrackEvent persists a track event and appends it to the supplied
// session. Track rows are append-only and use an event index to preserve the
// caller's order within a track. Concurrent appends through the same Service
// are serialized per app, user, and session. Independent service instances
// retain every append, with event IDs providing a deterministic tie-break when
// they concurrently choose the same event index.
func (s *Service) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	trackEvent *session.TrackEvent,
	opts ...session.Option,
) error {
	if sess == nil {
		return fmt.Errorf("session is nil")
	}
	if trackEvent == nil {
		return fmt.Errorf("track event is nil")
	}
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return err
	}
	unlock, err := s.lockTrackAppend(ctx, key)
	if err != nil {
		return fmt.Errorf("wait to append track event: %w", err)
	}
	defer unlock()

	staged := sess.Clone()
	if err := staged.AppendTrackEvent(trackEvent, opts...); err != nil {
		return fmt.Errorf("append track event to session: %w", err)
	}

	rows, err := s.chClient.Query(ctx, fmt.Sprintf(`SELECT count() FROM %s FINAL
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND track = ? AND deleted_at IS NULL`,
		s.tableSessionTrackEvents), key.AppName, key.UserID, key.SessionID, string(trackEvent.Track))
	if err != nil {
		return fmt.Errorf("count track events: %w", err)
	}
	defer rows.Close()
	var eventIndex uint64
	if rows.Next() {
		if err := rows.Scan(&eventIndex); err != nil {
			return fmt.Errorf("scan track event count: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("count track events: %w", err)
	}

	data, err := json.Marshal(trackEvent)
	if err != nil {
		return fmt.Errorf("marshal track event: %w", err)
	}
	now := time.Now().UTC().UnixMicro()
	eventID := uuid.NewString()
	err = s.chClient.Exec(ctx, fmt.Sprintf(`INSERT INTO %s
		(app_name, user_id, session_id, track, event_index, event_id, event, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, fromUnixTimestamp64Micro(?), fromUnixTimestamp64Micro(?))`,
		s.tableSessionTrackEvents), key.AppName, key.UserID, key.SessionID,
		string(trackEvent.Track), eventIndex, eventID, string(data), now, now)
	if err != nil {
		return fmt.Errorf("persist track event: %w", err)
	}
	trackIndex := staged.SnapshotTracksState()
	if err := s.UpdateSessionState(ctx, key, session.StateMap{"tracks": trackIndex}); err != nil {
		rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		rollbackAt := time.Now().UTC().UnixMicro()
		if rollbackAt <= now {
			rollbackAt = now + 1
		}
		if rollbackErr := s.softDeleteTrackEvent(
			rollbackCtx, key, trackEvent.Track, eventID, rollbackAt,
		); rollbackErr != nil {
			return errors.Join(
				fmt.Errorf("persist track index: %w", err),
				fmt.Errorf("compensate track event: %w", rollbackErr),
			)
		}
		return fmt.Errorf("persist track index: %w", err)
	}
	if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
		return fmt.Errorf("commit track event to session: %w", err)
	}
	return nil
}

func (s *Service) softDeleteTrackEvent(
	ctx context.Context,
	key session.Key,
	track session.Track,
	eventID string,
	deletedAt int64,
) error {
	return s.chClient.Exec(ctx, fmt.Sprintf(`INSERT INTO %s
		(app_name, user_id, session_id, track, event_index, event_id, event, created_at, updated_at, deleted_at)
		SELECT app_name, user_id, session_id, track, event_index, event_id, event, created_at,
			fromUnixTimestamp64Micro(?), fromUnixTimestamp64Micro(?)
		FROM %s FINAL
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND track = ?
			AND event_id = ? AND deleted_at IS NULL`,
		s.tableSessionTrackEvents, s.tableSessionTrackEvents),
		deletedAt, deletedAt, key.AppName, key.UserID, key.SessionID, string(track), eventID)
}

func (s *Service) getTrackEvents(
	ctx context.Context,
	key session.Key,
	sessionCreatedAt time.Time,
) (map[session.Track]*session.TrackEvents, error) {
	tracksList, err := s.getTrackEventsList(
		ctx,
		[]session.Key{key},
		[]time.Time{sessionCreatedAt},
	)
	if err != nil {
		return nil, err
	}
	return tracksList[0], nil
}

func (s *Service) getTrackEventsList(
	ctx context.Context,
	sessionKeys []session.Key,
	sessionCreatedAts []time.Time,
) ([]map[session.Track]*session.TrackEvents, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}
	if len(sessionKeys) != len(sessionCreatedAts) {
		return nil, fmt.Errorf("session keys and createdAts length mismatch")
	}

	conditions := make([]string, len(sessionKeys))
	args := make([]any, 0, len(sessionKeys)*4)
	for i, key := range sessionKeys {
		conditions[i] = "(app_name = ? AND user_id = ? AND session_id = ? AND created_at >= ?)"
		args = append(args, key.AppName, key.UserID, key.SessionID, sessionCreatedAts[i])
	}

	rows, err := s.chClient.Query(ctx, fmt.Sprintf(`SELECT app_name, user_id, session_id, track, event FROM %s FINAL
		WHERE (%s) AND deleted_at IS NULL
		ORDER BY app_name ASC, user_id ASC, session_id ASC, track ASC, event_index ASC, event_id ASC`,
		s.tableSessionTrackEvents, strings.Join(conditions, " OR ")), args...)
	if err != nil {
		return nil, fmt.Errorf("batch get track events: %w", err)
	}
	defer rows.Close()

	tracksBySession := make(map[string]map[session.Track]*session.TrackEvents)
	for rows.Next() {
		var appName, userID, sessionID, trackName, data string
		if err := rows.Scan(&appName, &userID, &sessionID, &trackName, &data); err != nil {
			return nil, fmt.Errorf("scan track event: %w", err)
		}
		var trackEvent session.TrackEvent
		if err := json.Unmarshal([]byte(data), &trackEvent); err != nil {
			return nil, fmt.Errorf("unmarshal track event: %w", err)
		}
		track := session.Track(trackName)
		trackEvent.Track = track
		sessionKey := trackSessionKey(appName, userID, sessionID)
		tracks := tracksBySession[sessionKey]
		if tracks == nil {
			tracks = make(map[session.Track]*session.TrackEvents)
			tracksBySession[sessionKey] = tracks
		}
		if tracks[track] == nil {
			tracks[track] = &session.TrackEvents{Track: track}
		}
		tracks[track].Events = append(tracks[track].Events, trackEvent)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate track events: %w", err)
	}

	result := make([]map[session.Track]*session.TrackEvents, len(sessionKeys))
	for i, key := range sessionKeys {
		tracks := tracksBySession[trackSessionKey(key.AppName, key.UserID, key.SessionID)]
		if tracks == nil {
			tracks = make(map[session.Track]*session.TrackEvents)
		}
		result[i] = tracks
	}
	return result, nil
}

func (s *Service) lockTrackAppend(ctx context.Context, key session.Key) (func(), error) {
	lockKey := trackSessionKey(key.AppName, key.UserID, key.SessionID)

	s.trackLocksMu.Lock()
	if s.trackLocks == nil {
		s.trackLocks = make(map[string]*trackAppendLock)
	}
	lock := s.trackLocks[lockKey]
	if lock == nil {
		lock = &trackAppendLock{token: make(chan struct{}, 1)}
		lock.token <- struct{}{}
		s.trackLocks[lockKey] = lock
	}
	lock.refs++
	s.trackLocksMu.Unlock()

	select {
	case <-ctx.Done():
		s.trackLocksMu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(s.trackLocks, lockKey)
		}
		s.trackLocksMu.Unlock()
		return nil, ctx.Err()
	case <-lock.token:
		return func() {
			lock.token <- struct{}{}

			s.trackLocksMu.Lock()
			lock.refs--
			if lock.refs == 0 {
				delete(s.trackLocks, lockKey)
			}
			s.trackLocksMu.Unlock()
		}, nil
	}
}

func trackSessionKey(appName, userID, sessionID string) string {
	return appName + "\x00" + userID + "\x00" + sessionID
}
