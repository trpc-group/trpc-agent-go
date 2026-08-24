//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package zset

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackpage"
)

const trackEventPageCursorKindZSet = "redis-zset"

type trackEventPageRow struct {
	member string
	score  int64
	event  session.TrackEvent
}

type scoredTrackEventMember struct {
	member string
	score  int64
}

// GetTrackEventPage returns a cursor page of persisted track events.
func (c *Client) GetTrackEventPage(
	ctx context.Context,
	req session.TrackEventPageRequest,
) (*session.TrackEventPage, error) {
	if err := trackpage.ValidateRequest(req); err != nil {
		return nil, err
	}
	sessState, ok, err := c.getTrackEventPageSessionState(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	if !ok {
		return &session.TrackEventPage{Track: req.Track}, nil
	}
	var cursor trackpage.Cursor
	hasCursor := req.Cursor != ""
	if hasCursor {
		cursor, err = trackpage.Decode(req.Cursor)
		if err != nil {
			return nil, err
		}
		if err := trackpage.ValidateBinding(cursor, trackEventPageCursorKindZSet, req.Key, req.Track, sessState.CreatedAt); err != nil {
			return nil, err
		}
	}
	rows, hasMore, err := c.queryTrackEventPageRows(ctx, req, cursor, hasCursor)
	if err != nil {
		return nil, err
	}
	entries, err := zsetTrackEventPageEntries(req, sessState.CreatedAt, rows)
	if err != nil {
		return nil, err
	}
	return &session.TrackEventPage{
		Track:   req.Track,
		Entries: entries,
		HasMore: hasMore,
	}, nil
}

func (c *Client) getTrackEventPageSessionState(
	ctx context.Context,
	key session.Key,
) (*SessionState, bool, error) {
	raw, err := c.client.HGet(ctx, c.sessionStateKey(key), key.SessionID).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("get session state: %w", err)
	}
	var sessState SessionState
	if err := json.Unmarshal(raw, &sessState); err != nil {
		return nil, false, fmt.Errorf("unmarshal session state: %w", err)
	}
	return &sessState, true, nil
}

func (c *Client) queryTrackEventPageRows(
	ctx context.Context,
	req session.TrackEventPageRequest,
	cursor trackpage.Cursor,
	hasCursor bool,
) ([]trackEventPageRow, bool, error) {
	rangeBy := &redis.ZRangeBy{
		Min: "-inf",
		Max: "+inf",
	}
	if hasCursor {
		rangeBy.Max = fmt.Sprintf("%d", cursor.CreatedAt)
	} else {
		rangeBy.Offset = 0
		rangeBy.Count = int64(req.EventLimit + 1)
	}
	zs, err := c.client.ZRevRangeByScoreWithScores(ctx, c.trackKey(req.Key, req.Track), rangeBy).Result()
	if err != nil {
		return nil, false, fmt.Errorf("query track event page index: %w", err)
	}
	selected := make([]scoredTrackEventMember, 0, req.EventLimit+1)
	for _, z := range zs {
		member := fmt.Sprint(z.Member)
		score := int64(z.Score)
		if hasCursor && !redisTrackPageOlder(score, member, cursor.CreatedAt, cursor.ID) {
			continue
		}
		selected = append(selected, scoredTrackEventMember{member: member, score: score})
		if len(selected) >= req.EventLimit+1 {
			break
		}
	}
	hasMore := len(selected) > req.EventLimit
	if hasMore {
		selected = selected[:req.EventLimit]
	}
	rows := make([]trackEventPageRow, 0, len(selected))
	for _, item := range selected {
		var event session.TrackEvent
		if err := json.Unmarshal([]byte(item.member), &event); err != nil {
			return nil, false, fmt.Errorf("unmarshal track event: %w", err)
		}
		rows = append(rows, trackEventPageRow{
			member: item.member,
			score:  item.score,
			event:  event,
		})
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, hasMore, nil
}

func zsetTrackEventPageEntries(
	req session.TrackEventPageRequest,
	sessionCreatedAt time.Time,
	rows []trackEventPageRow,
) ([]session.TrackEventPageEntry, error) {
	entries := make([]session.TrackEventPageEntry, 0, len(rows))
	for _, row := range rows {
		cursor, err := trackpage.CursorForUnixNano(
			trackEventPageCursorKindZSet,
			req.Key,
			req.Track,
			sessionCreatedAt,
			row.score,
			row.member,
		)
		if err != nil {
			return nil, err
		}
		entries = append(entries, session.TrackEventPageEntry{
			Event:  row.event,
			Cursor: cursor,
		})
	}
	return entries, nil
}

func redisTrackPageOlder(score int64, id string, cursorScore int64, cursorID string) bool {
	if score < cursorScore {
		return true
	}
	return score == cursorScore && id < cursorID
}
