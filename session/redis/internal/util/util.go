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

const (
	// ServiceMetaStorageTypeKey is the key in Session.ServiceMeta to store the data version.
	ServiceMetaStorageTypeKey = "storage_type"
	// StorageTypeHashIdx indicates the session is stored in HashIdx format.
	StorageTypeHashIdx = "hashidx"
	// StorageTypeZset indicates the session is stored in Hash format.
	StorageTypeZset = "zset"
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
