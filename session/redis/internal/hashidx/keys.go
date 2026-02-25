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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

const hashidxVersionPrefix = "hashidx"

// keyBuilder generates Redis keys using user-level hash tag strategy.
// Hash tag: {userID}
// All keys for the same user are in the same Redis Cluster slot,
// enabling Lua script atomic operations across session keys.
type keyBuilder struct {
	userPrefix string // optional user-defined key prefix
}

func newKeyBuilder(userPrefix string) *keyBuilder {
	return &keyBuilder{userPrefix: userPrefix}
}

// fullPrefix returns the complete prefix: userPrefix:hashidx or just hashidx
func (b *keyBuilder) fullPrefix() string {
	if b.userPrefix != "" {
		return b.userPrefix + ":" + hashidxVersionPrefix
	}
	return hashidxVersionPrefix
}

// hashTag generates the hash tag for user-scoped keys.
func (b *keyBuilder) hashTag(userID string) string {
	return fmt.Sprintf("{%s}", userID)
}

// SessionMetaKey returns the key for session metadata.
// Format: [userPrefix:]hashidx:meta:appName:{userID}:sessionID
func (b *keyBuilder) SessionMetaKey(key session.Key) string {
	return fmt.Sprintf("%s:meta:%s:%s:%s", b.fullPrefix(), key.AppName, b.hashTag(key.UserID), key.SessionID)
}

// SessionMetaPattern returns the scan pattern for ListSessions.
// Format: [userPrefix:]hashidx:meta:appName:{userID}:*
func (b *keyBuilder) SessionMetaPattern(userKey session.UserKey) string {
	return fmt.Sprintf("%s:meta:%s:%s:*", b.fullPrefix(), userKey.AppName, b.hashTag(userKey.UserID))
}

// EventDataKey returns the key for event data hash.
// Format: [userPrefix:]hashidx:evtdata:appName:{userID}:sessionID
func (b *keyBuilder) EventDataKey(key session.Key) string {
	return fmt.Sprintf("%s:evtdata:%s:%s:%s", b.fullPrefix(), key.AppName, b.hashTag(key.UserID), key.SessionID)
}

// EventTimeIndexKey returns the key for time-based event index.
// Format: [userPrefix:]hashidx:evtidx:time:appName:{userID}:sessionID
func (b *keyBuilder) EventTimeIndexKey(key session.Key) string {
	return fmt.Sprintf("%s:evtidx:time:%s:%s:%s", b.fullPrefix(), key.AppName, b.hashTag(key.UserID), key.SessionID)
}

// SummaryKey returns the key for session summary.
// Format: [userPrefix:]hashidx:sesssum:appName:{userID}:sessionID
func (b *keyBuilder) SummaryKey(key session.Key) string {
	return fmt.Sprintf("%s:sesssum:%s:%s:%s", b.fullPrefix(), key.AppName, b.hashTag(key.UserID), key.SessionID)
}

// TrackKey returns the key for track events.
// Format: [userPrefix:]hashidx:track:appName:{userID}:sessionID:trackName
func (b *keyBuilder) TrackKey(key session.Key, track session.Track) string {
	return fmt.Sprintf("%s:track:%s:%s:%s:%s", b.fullPrefix(), key.AppName, b.hashTag(key.UserID), key.SessionID, track)
}

// SessionKeys returns all keys associated with a session.
// Useful for batch TTL operations and cleanup.
func (b *keyBuilder) SessionKeys(key session.Key) []string {
	return []string{
		b.SessionMetaKey(key),
		b.EventDataKey(key),
		b.EventTimeIndexKey(key),
		b.SummaryKey(key),
	}
}

// AppStateKey returns the key for app-level state.
// Format: [userPrefix:]appstate:{appName}
// Note: AppState is shared between zset and HashIdx, so no v2 prefix is added.
func (b *keyBuilder) AppStateKey(appName string) string {
	if b.userPrefix != "" {
		return fmt.Sprintf("%s:appstate:%s", b.userPrefix, b.hashTag(appName))
	}
	return fmt.Sprintf("appstate:%s", b.hashTag(appName))
}

// UserStateKey returns the key for user-level state.
// Format: [userPrefix:]hashidx:userstate:appName:{userID}
// Hash tag is {userID} to ensure user-level distribution in Redis Cluster.
// Note: HashIdx uses different hash tag than zset to avoid hot spots, so dual-write/fallback is needed.
func (b *keyBuilder) UserStateKey(appName, userID string) string {
	return fmt.Sprintf("%s:userstate:%s:%s", b.fullPrefix(), appName, b.hashTag(userID))
}

// GetSessionSummaryKey returns the Redis key for HashIdx session summaries (for testing).
func GetSessionSummaryKey(userPrefix string, key session.Key) string {
	return newKeyBuilder(userPrefix).SummaryKey(key)
}

// GetSessionMetaKey returns the Redis key for HashIdx session metadata (for testing).
func GetSessionMetaKey(userPrefix string, key session.Key) string {
	return newKeyBuilder(userPrefix).SessionMetaKey(key)
}

// GetEventDataKey returns the Redis key for HashIdx event data (for testing).
func GetEventDataKey(userPrefix string, key session.Key) string {
	return newKeyBuilder(userPrefix).EventDataKey(key)
}

// GetEventTimeIndexKey returns the Redis key for HashIdx event time index (for testing).
func GetEventTimeIndexKey(userPrefix string, key session.Key) string {
	return newKeyBuilder(userPrefix).EventTimeIndexKey(key)
}

// GetTrackKey returns the Redis key for HashIdx track events (for testing).
func GetTrackKey(userPrefix string, key session.Key, track session.Track) string {
	return newKeyBuilder(userPrefix).TrackKey(key, track)
}

// GetUserStateKey returns the Redis key for HashIdx user state (for testing).
func GetUserStateKey(userPrefix string, appName, userID string) string {
	return newKeyBuilder(userPrefix).UserStateKey(appName, userID)
}
