//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	goredis "github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessionwindow "trpc.group/trpc-go/trpc-agent-go/session/internal/window"
	"trpc.group/trpc-go/trpc-agent-go/session/redis/internal/hashidx"
)

var _ session.WindowService = (*Service)(nil)

const (
	eventWindowBatchSize = 64
	eventWindowScanCap   = 10000
)

type redisWindowEntry struct {
	rank  int64
	entry session.EventWindowEntry
}

// GetEventWindow loads a small ordered event window around one anchor event.
func (s *Service) GetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
) (*session.EventWindow, error) {
	if err := req.Key.CheckSessionKey(); err != nil {
		return nil, err
	}
	anchorEventID := strings.TrimSpace(req.AnchorEventID)
	if anchorEventID == "" {
		return nil, fmt.Errorf("anchor event id is required")
	}
	req.AnchorEventID = anchorEventID
	if req.Before < 0 || req.After < 0 {
		return nil, fmt.Errorf("event window requires before >= 0 and after >= 0")
	}
	windowSize := uint64(req.Before) + uint64(req.After) + 1
	if windowSize > uint64(eventWindowScanCap) {
		return nil, fmt.Errorf("redis event window exceeds scan cap: %d", eventWindowScanCap)
	}

	zsetExists, hashidxExists, err := s.checkSessionExists(ctx, req.Key)
	if err != nil {
		return nil, err
	}
	if !zsetExists && !hashidxExists {
		return sessionwindow.EventWindowFromOrderedEvents(req.Key, nil, req)
	}

	roleFilter := sessionwindow.MakeRoleFilter(req.Roles)
	if s.compatEnabled() && zsetExists {
		return s.loadZSetEventWindow(ctx, req, anchorEventID, roleFilter)
	}
	if hashidxExists {
		return s.loadHashIdxEventWindow(ctx, req, anchorEventID, roleFilter)
	}
	return sessionwindow.EventWindowFromOrderedEvents(req.Key, nil, req)
}

func (s *Service) loadHashIdxEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
	anchorEventID string,
	roleFilter map[model.Role]struct{},
) (*session.EventWindow, error) {
	eventDataKey := hashidx.GetEventDataKey(s.opts.keyPrefix, req.Key)
	eventIndexKey := hashidx.GetEventTimeIndexKey(s.opts.keyPrefix, req.Key)

	anchorRaw, err := s.redisClient.HGet(ctx, eventDataKey, anchorEventID).Result()
	if err == goredis.Nil {
		return nil, fmt.Errorf("anchor event not found: %s", anchorEventID)
	}
	if err != nil {
		return nil, fmt.Errorf("load redis event window anchor: %w", err)
	}
	anchorRank, err := s.redisClient.ZRank(ctx, eventIndexKey, anchorEventID).Result()
	if err == goredis.Nil {
		return nil, fmt.Errorf("anchor event not found: %s", anchorEventID)
	}
	if err != nil {
		return nil, fmt.Errorf("load redis event window anchor rank: %w", err)
	}
	anchor, err := redisWindowEntryFromJSON(anchorRaw, anchorRank)
	if err != nil {
		return nil, err
	}
	if anchor.entry.Event.ID != anchorEventID ||
		!sessionwindow.EventAllowed(&anchor.entry.Event, roleFilter) {
		return nil, fmt.Errorf("anchor event not found: %s", anchorEventID)
	}

	beforeEntries, err := s.loadHashIdxWindowNeighbors(
		ctx,
		eventDataKey,
		eventIndexKey,
		anchorRank,
		req.Before,
		roleFilter,
		true,
	)
	if err != nil {
		return nil, err
	}
	afterEntries, err := s.loadHashIdxWindowNeighbors(
		ctx,
		eventDataKey,
		eventIndexKey,
		anchorRank,
		req.After,
		roleFilter,
		false,
	)
	if err != nil {
		return nil, err
	}
	return buildRedisEventWindow(req.Key, anchorEventID, anchor.entry, beforeEntries, afterEntries), nil
}

func (s *Service) loadHashIdxWindowNeighbors(
	ctx context.Context,
	eventDataKey string,
	eventIndexKey string,
	anchorRank int64,
	limit int,
	roleFilter map[model.Role]struct{},
	before bool,
) ([]session.EventWindowEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	out := make([]session.EventWindowEntry, 0, limit)
	scanned := 0
	if before {
		cursorRank := anchorRank
		for len(out) < limit && cursorRank > 0 {
			start := cursorRank - eventWindowBatchSize
			if start < 0 {
				start = 0
			}
			ids, err := s.redisClient.ZRange(ctx, eventIndexKey, start, cursorRank-1).Result()
			if err != nil && err != goredis.Nil {
				return nil, fmt.Errorf("load redis event window neighbors: %w", err)
			}
			if len(ids) == 0 {
				break
			}
			rawValues, err := s.redisClient.HMGet(ctx, eventDataKey, ids...).Result()
			if err != nil && err != goredis.Nil {
				return nil, fmt.Errorf("load redis event window neighbor data: %w", err)
			}
			for idx := len(ids) - 1; idx >= 0 && len(out) < limit; idx-- {
				scanned++
				entry, ok, err := redisWindowEntryFromValue(rawValues[idx], start+int64(idx))
				if err != nil {
					return nil, err
				}
				if !ok || !sessionwindow.EventAllowed(&entry.entry.Event, roleFilter) {
					continue
				}
				out = append(out, entry.entry)
			}
			if len(ids) < eventWindowBatchSize {
				break
			}
			cursorRank = start
			if len(out) < limit && scanned >= eventWindowScanCap {
				return nil, fmt.Errorf("redis event window scan cap exceeded: %d", eventWindowScanCap)
			}
		}
		reverseRedisWindowEntries(out)
		return out, nil
	}

	cursorRank := anchorRank + 1
	for len(out) < limit {
		ids, err := s.redisClient.ZRange(
			ctx,
			eventIndexKey,
			cursorRank,
			cursorRank+eventWindowBatchSize-1,
		).Result()
		if err != nil && err != goredis.Nil {
			return nil, fmt.Errorf("load redis event window neighbors: %w", err)
		}
		if len(ids) == 0 {
			break
		}
		rawValues, err := s.redisClient.HMGet(ctx, eventDataKey, ids...).Result()
		if err != nil && err != goredis.Nil {
			return nil, fmt.Errorf("load redis event window neighbor data: %w", err)
		}
		for idx := range ids {
			scanned++
			entry, ok, err := redisWindowEntryFromValue(rawValues[idx], cursorRank+int64(idx))
			if err != nil {
				return nil, err
			}
			if !ok || !sessionwindow.EventAllowed(&entry.entry.Event, roleFilter) {
				continue
			}
			out = append(out, entry.entry)
			if len(out) >= limit {
				break
			}
		}
		if len(ids) < eventWindowBatchSize {
			break
		}
		cursorRank += int64(len(ids))
		if len(out) < limit && scanned >= eventWindowScanCap {
			return nil, fmt.Errorf("redis event window scan cap exceeded: %d", eventWindowScanCap)
		}
	}
	return out, nil
}

func (s *Service) loadZSetEventWindow(
	ctx context.Context,
	req session.EventWindowRequest,
	anchorEventID string,
	roleFilter map[model.Role]struct{},
) (*session.EventWindow, error) {
	eventKey := redisZSetEventKey(s.opts.keyPrefix, req.Key)
	anchor, err := s.findZSetWindowAnchor(ctx, eventKey, anchorEventID, roleFilter)
	if err != nil {
		return nil, err
	}
	if anchor == nil {
		return nil, fmt.Errorf("anchor event not found: %s", anchorEventID)
	}

	beforeEntries, err := s.loadZSetWindowNeighbors(
		ctx,
		eventKey,
		anchor.rank,
		req.Before,
		roleFilter,
		true,
	)
	if err != nil {
		return nil, err
	}
	afterEntries, err := s.loadZSetWindowNeighbors(
		ctx,
		eventKey,
		anchor.rank,
		req.After,
		roleFilter,
		false,
	)
	if err != nil {
		return nil, err
	}
	return buildRedisEventWindow(req.Key, anchorEventID, anchor.entry, beforeEntries, afterEntries), nil
}

func (s *Service) findZSetWindowAnchor(
	ctx context.Context,
	eventKey string,
	anchorEventID string,
	roleFilter map[model.Role]struct{},
) (*redisWindowEntry, error) {
	for offset := int64(0); offset < eventWindowScanCap; {
		stop := offset + eventWindowBatchSize - 1
		if stop >= eventWindowScanCap {
			stop = eventWindowScanCap - 1
		}
		rawEvents, err := s.redisClient.ZRange(ctx, eventKey, offset, stop).Result()
		if err != nil && err != goredis.Nil {
			return nil, fmt.Errorf("load redis event window anchor: %w", err)
		}
		if len(rawEvents) == 0 {
			return nil, nil
		}
		for idx, raw := range rawEvents {
			entry, err := redisWindowEntryFromJSON(raw, offset+int64(idx))
			if err != nil {
				// Legacy zset has no event-id index; malformed unrelated rows
				// cannot be anchor candidates and should not fail the lookup.
				continue
			}
			if entry.entry.Event.ID != anchorEventID {
				continue
			}
			if !sessionwindow.EventAllowed(&entry.entry.Event, roleFilter) {
				return nil, nil
			}
			return entry, nil
		}
		if len(rawEvents) < eventWindowBatchSize {
			return nil, nil
		}
		offset += int64(len(rawEvents))
	}
	return nil, fmt.Errorf(
		"redis event window scan cap exceeded while locating anchor event %q",
		anchorEventID,
	)
}

func (s *Service) loadZSetWindowNeighbors(
	ctx context.Context,
	eventKey string,
	anchorRank int64,
	limit int,
	roleFilter map[model.Role]struct{},
	before bool,
) ([]session.EventWindowEntry, error) {
	if limit <= 0 {
		return nil, nil
	}
	out := make([]session.EventWindowEntry, 0, limit)
	scanned := 0
	if before {
		cursorRank := anchorRank
		for len(out) < limit && cursorRank > 0 {
			start := cursorRank - eventWindowBatchSize
			if start < 0 {
				start = 0
			}
			rawEvents, err := s.redisClient.ZRange(ctx, eventKey, start, cursorRank-1).Result()
			if err != nil && err != goredis.Nil {
				return nil, fmt.Errorf("load redis event window neighbors: %w", err)
			}
			if len(rawEvents) == 0 {
				break
			}
			for idx := len(rawEvents) - 1; idx >= 0 && len(out) < limit; idx-- {
				scanned++
				entry, err := redisWindowEntryFromJSON(rawEvents[idx], start+int64(idx))
				if err != nil {
					return nil, err
				}
				if !sessionwindow.EventAllowed(&entry.entry.Event, roleFilter) {
					continue
				}
				out = append(out, entry.entry)
			}
			if len(rawEvents) < eventWindowBatchSize {
				break
			}
			cursorRank = start
			if len(out) < limit && scanned >= eventWindowScanCap {
				return nil, fmt.Errorf("redis event window scan cap exceeded: %d", eventWindowScanCap)
			}
		}
		reverseRedisWindowEntries(out)
		return out, nil
	}

	cursorRank := anchorRank + 1
	for len(out) < limit {
		rawEvents, err := s.redisClient.ZRange(
			ctx,
			eventKey,
			cursorRank,
			cursorRank+eventWindowBatchSize-1,
		).Result()
		if err != nil && err != goredis.Nil {
			return nil, fmt.Errorf("load redis event window neighbors: %w", err)
		}
		if len(rawEvents) == 0 {
			break
		}
		for idx, raw := range rawEvents {
			scanned++
			entry, err := redisWindowEntryFromJSON(raw, cursorRank+int64(idx))
			if err != nil {
				return nil, err
			}
			if !sessionwindow.EventAllowed(&entry.entry.Event, roleFilter) {
				continue
			}
			out = append(out, entry.entry)
			if len(out) >= limit {
				break
			}
		}
		if len(rawEvents) < eventWindowBatchSize {
			break
		}
		cursorRank += int64(len(rawEvents))
		if len(out) < limit && scanned >= eventWindowScanCap {
			return nil, fmt.Errorf("redis event window scan cap exceeded: %d", eventWindowScanCap)
		}
	}
	return out, nil
}

func redisWindowEntryFromValue(value any, rank int64) (*redisWindowEntry, bool, error) {
	switch v := value.(type) {
	case nil:
		return nil, false, nil
	case string:
		entry, err := redisWindowEntryFromJSON(v, rank)
		return entry, true, err
	case []byte:
		entry, err := redisWindowEntryFromJSON(string(v), rank)
		return entry, true, err
	default:
		return nil, false, fmt.Errorf("unexpected redis event window value %T", value)
	}
}

func redisWindowEntryFromJSON(raw string, rank int64) (*redisWindowEntry, error) {
	var evt event.Event
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		return nil, fmt.Errorf("unmarshal redis event window entry: %w", err)
	}
	return &redisWindowEntry{
		rank: rank,
		entry: session.EventWindowEntry{
			Event:     evt,
			CreatedAt: evt.Timestamp,
		},
	}, nil
}

func buildRedisEventWindow(
	key session.Key,
	anchorEventID string,
	anchor session.EventWindowEntry,
	before []session.EventWindowEntry,
	after []session.EventWindowEntry,
) *session.EventWindow {
	entries := make([]session.EventWindowEntry, 0, len(before)+1+len(after))
	entries = append(entries, before...)
	entries = append(entries, anchor)
	entries = append(entries, after...)
	return &session.EventWindow{
		SessionKey:    key,
		AnchorEventID: anchorEventID,
		Entries:       entries,
	}
}

func redisZSetEventKey(prefix string, key session.Key) string {
	base := fmt.Sprintf("event:{%s}:%s:%s", key.AppName, key.UserID, key.SessionID)
	if prefix == "" {
		return base
	}
	return prefix + ":" + base
}

func reverseRedisWindowEntries(entries []session.EventWindowEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
}
