//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func (s *Service) getEventsList(
	ctx context.Context,
	sessionKeys []session.Key,
	sessionCreatedAts []time.Time,
	limit int,
	afterTime time.Time,
) ([][]event.Event, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}

	placeholders := make([]string, len(sessionKeys))
	args := make([]any, 0, len(sessionKeys)*3)
	for i, key := range sessionKeys {
		placeholders[i] = "(?, ?, ?)"
		args = append(args, key.AppName, key.UserID, key.SessionID)
	}

	query := fmt.Sprintf(
		`SELECT app_name, user_id, session_id, event, created_at FROM %s
WHERE (app_name, user_id, session_id) IN (%s)
AND deleted_at IS NULL
ORDER BY app_name, user_id, session_id, created_at ASC`,
		s.tableSessionEvents,
		strings.Join(placeholders, ","),
	)

	createdAtMap := make(map[string]time.Time, len(sessionKeys))
	for i, key := range sessionKeys {
		keyStr := fmt.Sprintf(
			"%s:%s:%s",
			key.AppName,
			key.UserID,
			key.SessionID,
		)
		createdAtMap[keyStr] = sessionCreatedAts[i]
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("batch get events: %w", err)
	}
	defer rows.Close()

	eventsMap := make(map[string][]event.Event)
	for rows.Next() {
		var (
			appName    string
			userID     string
			sessionID  string
			eventBytes []byte
			createdNs  int64
		)
		if err := rows.Scan(
			&appName,
			&userID,
			&sessionID,
			&eventBytes,
			&createdNs,
		); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		keyStr := fmt.Sprintf("%s:%s:%s", appName, userID, sessionID)
		if sessCreatedAt, ok := createdAtMap[keyStr]; ok {
			if unixNanoToTime(createdNs).Before(sessCreatedAt) {
				continue
			}
		}

		var evt event.Event
		if err := json.Unmarshal(eventBytes, &evt); err != nil {
			return nil, fmt.Errorf("unmarshal event: %w", err)
		}
		eventsMap[keyStr] = append(eventsMap[keyStr], evt)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}

	if limit <= 0 {
		limit = s.opts.sessionEventLimit
	}
	if afterTime.IsZero() && s.opts.sessionTTL > 0 {
		afterTime = time.Now().Add(-s.opts.sessionTTL)
	}

	out := make([][]event.Event, len(sessionKeys))
	for i, key := range sessionKeys {
		keyStr := fmt.Sprintf(
			"%s:%s:%s",
			key.AppName,
			key.UserID,
			key.SessionID,
		)
		sess := session.Session{Events: eventsMap[keyStr]}
		sess.ApplyEventFiltering(
			session.WithEventNum(limit),
			session.WithEventTime(afterTime),
		)
		out[i] = sess.Events
	}
	return out, nil
}

func (s *Service) getTrackEvents(
	ctx context.Context,
	sessionKeys []session.Key,
	sessionStates []*SessionState,
	limit int,
	afterTime time.Time,
) ([]map[session.Track][]session.TrackEvent, error) {
	if len(sessionKeys) == 0 {
		return nil, nil
	}
	if len(sessionStates) != len(sessionKeys) {
		return nil, fmt.Errorf(
			"session states count mismatch: %d != %d",
			len(sessionStates),
			len(sessionKeys),
		)
	}

	type trackQuery struct {
		sessionIdx int
		track      session.Track
		query      string
		args       []any
	}

	queries := make([]*trackQuery, 0)
	nowNs := time.Now().UTC().UnixNano()
	afterNs := afterTime.UTC().UnixNano()

	for i, key := range sessionKeys {
		tracks, err := session.TracksFromState(sessionStates[i].State)
		if err != nil {
			return nil, fmt.Errorf("get tracks from state: %w", err)
		}
		for _, track := range tracks {
			var (
				query string
				args  []any
			)
			if limit > 0 {
				query = fmt.Sprintf(
					`SELECT event FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND track = ?
AND (expires_at IS NULL OR expires_at > ?)
AND created_at > ? AND deleted_at IS NULL
ORDER BY created_at DESC
LIMIT ?`,
					s.tableSessionTracks,
				)
				args = []any{
					key.AppName,
					key.UserID,
					key.SessionID,
					track,
					nowNs,
					afterNs,
					limit,
				}
			} else {
				query = fmt.Sprintf(
					`SELECT event FROM %s
WHERE app_name = ? AND user_id = ? AND session_id = ? AND track = ?
AND (expires_at IS NULL OR expires_at > ?)
AND created_at > ? AND deleted_at IS NULL
ORDER BY created_at DESC`,
					s.tableSessionTracks,
				)
				args = []any{
					key.AppName,
					key.UserID,
					key.SessionID,
					track,
					nowNs,
					afterNs,
				}
			}
			queries = append(queries, &trackQuery{
				sessionIdx: i,
				track:      track,
				query:      query,
				args:       args,
			})
		}
	}

	out := make(
		[]map[session.Track][]session.TrackEvent,
		len(sessionKeys),
	)
	for _, q := range queries {
		rows, err := s.db.QueryContext(ctx, q.query, q.args...)
		if err != nil {
			return nil, fmt.Errorf("query track events: %w", err)
		}

		events := make([]session.TrackEvent, 0)
		for rows.Next() {
			var eventBytes []byte
			if err := rows.Scan(&eventBytes); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan track event: %w", err)
			}
			var evt session.TrackEvent
			if err := json.Unmarshal(eventBytes, &evt); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf(
					"unmarshal track event: %w",
					err,
				)
			}
			events = append(events, evt)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate track events: %w", err)
		}
		_ = rows.Close()

		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}

		if out[q.sessionIdx] == nil {
			out[q.sessionIdx] = make(
				map[session.Track][]session.TrackEvent,
			)
		}
		out[q.sessionIdx][q.track] = events
	}

	for i := range out {
		if out[i] == nil {
			out[i] = make(map[session.Track][]session.TrackEvent)
		}
	}

	return out, nil
}
