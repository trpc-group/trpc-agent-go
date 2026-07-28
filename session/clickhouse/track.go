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
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// AppendTrackEvent persists a track event and appends it to the supplied
// session. Track rows are append-only and use an event index to preserve the
// caller's order within a track.
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
	if err := sess.AppendTrackEvent(trackEvent, opts...); err != nil {
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

	data, err := json.Marshal(trackEvent)
	if err != nil {
		return fmt.Errorf("marshal track event: %w", err)
	}
	now := time.Now().UTC().UnixMicro()
	err = s.chClient.Exec(ctx, fmt.Sprintf(`INSERT INTO %s
		(app_name, user_id, session_id, track, event_index, event, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, fromUnixTimestamp64Micro(?), fromUnixTimestamp64Micro(?))`,
		s.tableSessionTrackEvents), key.AppName, key.UserID, key.SessionID,
		string(trackEvent.Track), eventIndex, string(data), now, now)
	if err != nil {
		return fmt.Errorf("persist track event: %w", err)
	}
	trackIndex := sess.State["tracks"]
	if err := s.UpdateSessionState(ctx, key, session.StateMap{"tracks": trackIndex}); err != nil {
		return fmt.Errorf("persist track index: %w", err)
	}
	return nil
}

func (s *Service) getTrackEvents(
	ctx context.Context,
	key session.Key,
	sessionCreatedAt time.Time,
) (map[session.Track]*session.TrackEvents, error) {
	rows, err := s.chClient.Query(ctx, fmt.Sprintf(`SELECT track, event FROM %s FINAL
		WHERE app_name = ? AND user_id = ? AND session_id = ? AND created_at >= ?
		AND deleted_at IS NULL ORDER BY track ASC, event_index ASC`, s.tableSessionTrackEvents),
		key.AppName, key.UserID, key.SessionID, sessionCreatedAt)
	if err != nil {
		return nil, fmt.Errorf("get track events: %w", err)
	}
	defer rows.Close()

	tracks := make(map[session.Track]*session.TrackEvents)
	for rows.Next() {
		var trackName, data string
		if err := rows.Scan(&trackName, &data); err != nil {
			return nil, fmt.Errorf("scan track event: %w", err)
		}
		var trackEvent session.TrackEvent
		if err := json.Unmarshal([]byte(data), &trackEvent); err != nil {
			return nil, fmt.Errorf("unmarshal track event: %w", err)
		}
		track := session.Track(trackName)
		trackEvent.Track = track
		if tracks[track] == nil {
			tracks[track] = &session.TrackEvents{Track: track}
		}
		tracks[track].Events = append(tracks[track].Events, trackEvent)
	}
	return tracks, nil
}
