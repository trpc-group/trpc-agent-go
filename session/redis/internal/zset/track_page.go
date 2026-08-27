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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackpage"
)

// TrackEventPageCursorKind is the cursor kind emitted by zset track pages.
const TrackEventPageCursorKind = "redis-zset"

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
	var cursor trackpage.Cursor
	hasCursor := req.Cursor != ""
	if hasCursor {
		var err error
		cursor, err = trackpage.Decode(req.Cursor)
		if err != nil {
			return nil, err
		}
		if err := trackpage.ValidateBinding(cursor, TrackEventPageCursorKind, req.Key, req.Track); err != nil {
			return nil, err
		}
	}
	rows, hasMore, err := c.queryTrackEventPageRows(ctx, req, cursor, hasCursor)
	if err != nil {
		return nil, err
	}
	entries, err := zsetTrackEventPageEntries(req, rows)
	if err != nil {
		return nil, err
	}
	return &session.TrackEventPage{
		Track:   req.Track,
		Entries: entries,
		HasMore: hasMore,
	}, nil
}

func (c *Client) queryTrackEventPageRows(
	ctx context.Context,
	req session.TrackEventPageRequest,
	cursor trackpage.Cursor,
	hasCursor bool,
) ([]trackEventPageRow, bool, error) {
	selected := make([]scoredTrackEventMember, 0, req.EventLimit+1)
	batchLimit := int64(req.EventLimit + 1)
	var offset int64
	cursorSeen := false
	for {
		rangeBy := &redis.ZRangeBy{
			Min:    "-inf",
			Max:    "+inf",
			Offset: offset,
			Count:  batchLimit,
		}
		if hasCursor {
			rangeBy.Max = fmt.Sprintf("%d", cursor.CreatedAt)
		}
		zs, err := c.client.ZRevRangeByScoreWithScores(ctx, c.trackKey(req.Key, req.Track), rangeBy).Result()
		if err != nil {
			return nil, false, fmt.Errorf("query track event page index: %w", err)
		}
		for _, z := range zs {
			member := fmt.Sprint(z.Member)
			score := int64(z.Score)
			if hasCursor && !zsetTrackPageOlder(score, member, cursor, &cursorSeen) {
				continue
			}
			selected = append(selected, scoredTrackEventMember{member: member, score: score})
			if len(selected) >= req.EventLimit+1 {
				break
			}
		}
		if len(selected) >= req.EventLimit+1 {
			break
		}
		if !hasCursor || int64(len(zs)) < batchLimit {
			break
		}
		offset += int64(len(zs))
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
	rows []trackEventPageRow,
) ([]session.TrackEventPageEntry, error) {
	entries := make([]session.TrackEventPageEntry, 0, len(rows))
	for _, row := range rows {
		cursor, err := trackpage.CursorForUnixNano(
			TrackEventPageCursorKind,
			req.Key,
			req.Track,
			row.score,
			zsetTrackPageCursorID(row.member),
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

func zsetTrackPageOlder(score int64, member string, cursor trackpage.Cursor, cursorSeen *bool) bool {
	if score < cursor.CreatedAt {
		return true
	}
	if score != cursor.CreatedAt {
		return false
	}
	if *cursorSeen {
		return true
	}
	if zsetTrackPageCursorID(member) == cursor.ID {
		*cursorSeen = true
	}
	return false
}

func zsetTrackPageCursorID(member string) string {
	sum := sha256.Sum256([]byte(member))
	return hex.EncodeToString(sum[:])
}
