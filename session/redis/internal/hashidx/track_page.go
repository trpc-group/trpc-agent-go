//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package hashidx

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/internal/trackpage"
)

// TrackEventPageCursorKind is the cursor kind emitted by hash-index track pages.
const TrackEventPageCursorKind = "redis-hashidx"

type trackEventPageRow struct {
	id    string
	score int64
	event session.TrackEvent
}

type scoredTrackEventID struct {
	id    string
	seq   int64
	score int64
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
	entries, err := hashIdxTrackEventPageEntries(req, rows)
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
	candidates := make([]scoredTrackEventID, 0, req.EventLimit+1)
	batchLimit := int64(req.EventLimit + 1)
	var cursorSeq int64
	if hasCursor {
		var err error
		cursorSeq, err = trackpage.ParseIntID(cursor.ID)
		if err != nil {
			return nil, false, err
		}
	}
	var offset int64
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
		zs, err := c.client.ZRevRangeByScoreWithScores(ctx, c.keys.TrackTimeIndexKey(req.Key, req.Track), rangeBy).Result()
		if err != nil {
			return nil, false, fmt.Errorf("query track event page index: %w", err)
		}
		if len(zs) == 0 {
			break
		}
		for _, z := range zs {
			id := fmt.Sprint(z.Member)
			seq, err := trackpage.ParseIntID(id)
			if err != nil {
				return nil, false, err
			}
			score := int64(z.Score)
			if hasCursor && !redisTrackPageOlder(score, seq, cursor.CreatedAt, cursorSeq) {
				continue
			}
			candidates = append(candidates, scoredTrackEventID{id: id, seq: seq, score: score})
		}
		sortScoredTrackEventIDs(candidates)
		if len(candidates) >= req.EventLimit+1 && int64(zs[len(zs)-1].Score) < candidates[req.EventLimit].score {
			break
		}
		if int64(len(zs)) < batchLimit {
			break
		}
		offset += int64(len(zs))
	}
	selected := candidates
	hasMore := len(selected) > req.EventLimit
	if hasMore {
		selected = selected[:req.EventLimit]
	}
	if len(selected) == 0 {
		return nil, hasMore, nil
	}
	ids := make([]string, 0, len(selected))
	for _, item := range selected {
		ids = append(ids, item.id)
	}
	rawEvents, err := c.client.HMGet(ctx, c.keys.TrackDataKey(req.Key, req.Track), ids...).Result()
	if err != nil {
		return nil, false, fmt.Errorf("load track event page data: %w", err)
	}
	rows := make([]trackEventPageRow, 0, len(rawEvents))
	for i, raw := range rawEvents {
		text, ok := raw.(string)
		if !ok || text == "" {
			continue
		}
		var event session.TrackEvent
		if err := json.Unmarshal([]byte(text), &event); err != nil {
			return nil, false, fmt.Errorf("unmarshal track event: %w", err)
		}
		rows = append(rows, trackEventPageRow{
			id:    selected[i].id,
			score: selected[i].score,
			event: event,
		})
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, hasMore, nil
}

func hashIdxTrackEventPageEntries(
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
			row.id,
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

func sortScoredTrackEventIDs(items []scoredTrackEventID) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		return items[i].seq > items[j].seq
	})
}

func redisTrackPageOlder(score int64, seq int64, cursorScore int64, cursorSeq int64) bool {
	if score < cursorScore {
		return true
	}
	return score == cursorScore && seq < cursorSeq
}
