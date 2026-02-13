//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package util provides shared utility functions for Redis session implementations.
package util

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ProcessStateCmd processes a HGetAll command result into a StateMap.
func ProcessStateCmd(cmd *redis.MapStringStringCmd) (session.StateMap, error) {
	bytes, err := cmd.Result()
	if err == redis.Nil {
		return make(session.StateMap), nil
	}
	if err != nil {
		return nil, fmt.Errorf("get state failed: %w", err)
	}
	state := make(session.StateMap)
	for k, v := range bytes {
		state[k] = []byte(v)
	}
	return state, nil
}

// ProcessEventCmd processes a ZRange (StringSlice) command result into a list of Events.
func ProcessEventCmd(
	ctx context.Context,
	cmd *redis.StringSliceCmd,
) ([]event.Event, error) {
	eventsBytes, err := cmd.Result()
	if err == redis.Nil || len(eventsBytes) == 0 {
		return []event.Event{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get events failed: %w", err)
	}
	events := make([]event.Event, 0, len(eventsBytes))
	for _, eventBytes := range eventsBytes {
		evt := &event.Event{}
		if err := json.Unmarshal([]byte(eventBytes), evt); err != nil {
			log.WarnfContext(
				ctx,
				"skip malformed event in redis history: %v",
				err,
			)
			continue
		}
		events = append(events, *evt)
	}
	return events, nil
}

// MergeState merges app and user state into the session.
func MergeState(appState, userState session.StateMap, sess *session.Session) *session.Session {
	for k, v := range appState {
		sess.SetState(session.StateAppPrefix+k, v)
	}
	for k, v := range userState {
		sess.SetState(session.StateUserPrefix+k, v)
	}
	return sess
}

// NormalizeSessionEvents returns the first event list or nil.
func NormalizeSessionEvents(events [][]event.Event) []event.Event {
	if len(events) == 0 {
		return nil
	}
	return events[0]
}

// AttachTrackEvents attaches track events to the session.
func AttachTrackEvents(
	sess *session.Session,
	trackEvents []map[session.Track][]session.TrackEvent,
) {
	if len(trackEvents) == 0 || len(trackEvents[0]) == 0 {
		return
	}

	sess.Tracks = make(map[session.Track]*session.TrackEvents, len(trackEvents[0]))
	for trackName, history := range trackEvents[0] {
		sess.Tracks[trackName] = &session.TrackEvents{
			Track:  trackName,
			Events: history,
		}
	}
}

// AttachSummaries attaches summaries to the session.
func AttachSummaries(sess *session.Session, summariesCmd *redis.StringCmd) {
	if len(sess.Events) == 0 || summariesCmd == nil {
		return
	}

	if bytes, err := summariesCmd.Bytes(); err == nil && len(bytes) > 0 {
		var summaries map[string]*session.Summary
		if err := json.Unmarshal(bytes, &summaries); err == nil && len(summaries) > 0 {
			sess.Summaries = summaries
		}
	}
}

// =============================================================================
// Summary Storage
// =============================================================================
//
// Summary Data Structure (shared by V1 and V2):
//
//	Redis Type: Hash
//	Hash Field -> Value: JSON encoded map[filterKey]*session.Summary
//
//	Example stored JSON:
//	{
//	  "": {                               // filterKey="" means full-session summary
//	    "summary": "Conversation about...",
//	    "topics": ["AI", "Programming"],
//	    "updated_at": "2025-01-01T00:00:00Z"
//	  },
//	  "req-123": {                        // filterKey="req-123" is a single-turn summary
//	    "summary": "User asked about...",
//	    "updated_at": "2025-01-01T00:01:00Z"
//	  }
//	}
//
// V1 Summary Key:
//
//	Key:    sesssum:{appName}:{userID}     (or {prefix}:sesssum:{appName}:{userID})
//	Field:  sessionID
//	Hash Tag: {appName} -> all users of same app in one slot (hot spot issue)
//
// V2 Summary Key:
//
//	Key:    v2:sesssum:{appName:userID}:sessionID
//	Field:  "data" (fixed)
//	Hash Tag: {appName:userID} -> distributed by user (no hot spot)
//
// =============================================================================

// LuaSummariesSetIfNewer atomically merges one filterKey summary into the stored
// JSON map only if the incoming UpdatedAt is newer-or-equal.
//
// KEYS[1] = summary key
// ARGV[1] = hash field
// ARGV[2] = filterKey
// ARGV[3] = newSummaryJSON -> {"summary":"...","updated_at":"RFC3339 time"}
//
// Returns 1 if updated, 0 if skipped (existing is newer).
var LuaSummariesSetIfNewer = redis.NewScript(
	"local cur = redis.call('HGET', KEYS[1], ARGV[1])\n" +
		"local fk = ARGV[2]\n" +
		"local newSum = cjson.decode(ARGV[3])\n" +
		"if not cur or cur == '' then\n" +
		"  local m = {}\n" +
		"  m[fk] = newSum\n" +
		"  redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(m))\n" +
		"  return 1\n" +
		"end\n" +
		"local map = cjson.decode(cur)\n" +
		"local old = map[fk]\n" +
		"local old_ts = nil\n" +
		"local new_ts = nil\n" +
		"if old and old['updated_at'] then old_ts = old['updated_at'] end\n" +
		"if newSum and newSum['updated_at'] then new_ts = newSum['updated_at'] end\n" +
		"if not old or (old_ts and new_ts and old_ts <= new_ts) then\n" +
		"  map[fk] = newSum\n" +
		"  redis.call('HSET', KEYS[1], ARGV[1], cjson.encode(map))\n" +
		"  return 1\n" +
		"end\n" +
		"return 0\n",
)
