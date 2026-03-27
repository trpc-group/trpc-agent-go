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

// SessionMetaPattern returns the SCAN pattern for listing all session meta keys
// for one user when user session index is disabled.
// Format: [userPrefix:]hashidx:meta:appName:{userID}:*
func (b *keyBuilder) SessionMetaPattern(userKey session.UserKey) string {
	return fmt.Sprintf("%s:meta:%s:%s:*", b.fullPrefix(), userKey.AppName, b.hashTag(userKey.UserID))
}

// SessionIndexKey returns the Hash key that indexes all session IDs for a user.
// Format: [userPrefix:]hashidx:sessidx:appName:{userID}
// This Hash stores sessionID as field and structured JSON metadata as value.
// ListSessions reads this index via HSCAN for Redis Cluster compatibility.
func (b *keyBuilder) SessionIndexKey(userKey session.UserKey) string {
	return fmt.Sprintf("%s:sessidx:%s:%s", b.fullPrefix(), userKey.AppName, b.hashTag(userKey.UserID))
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

// TrackDataKey returns the key for track event data (Hash: field=eventID, value=TrackEvent JSON).
// Format: [userPrefix:]hashidx:trkdata:appName:{userID}:sessionID:trackName
func (b *keyBuilder) TrackDataKey(key session.Key, track session.Track) string {
	return fmt.Sprintf("%s:trkdata:%s:%s:%s:%s", b.fullPrefix(), key.AppName, b.hashTag(key.UserID), key.SessionID, track)
}

// TrackTimeIndexKey returns the key for track event time index (ZSet: member=eventID, score=timestamp).
// Format: [userPrefix:]hashidx:trkidx:time:appName:{userID}:sessionID:trackName
func (b *keyBuilder) TrackTimeIndexKey(key session.Key, track session.Track) string {
	return fmt.Sprintf("%s:trkidx:time:%s:%s:%s:%s", b.fullPrefix(), key.AppName, b.hashTag(key.UserID), key.SessionID, track)
}

// TrackKeys returns all track-related keys for a given session and track.
func (b *keyBuilder) TrackKeys(key session.Key, track session.Track) []string {
	return []string{
		b.TrackDataKey(key, track),
		b.TrackTimeIndexKey(key, track),
	}
}

// SessionKeys returns all keys associated with a session (excluding track keys).
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

// GetSessionMetaPattern returns the SCAN pattern for HashIdx session meta keys (for testing).
func GetSessionMetaPattern(userPrefix string, userKey session.UserKey) string {
	return newKeyBuilder(userPrefix).SessionMetaPattern(userKey)
}

// GetEventDataKey returns the Redis key for HashIdx event data (for testing).
func GetEventDataKey(userPrefix string, key session.Key) string {
	return newKeyBuilder(userPrefix).EventDataKey(key)
}

// GetEventTimeIndexKey returns the Redis key for HashIdx event time index (for testing).
func GetEventTimeIndexKey(userPrefix string, key session.Key) string {
	return newKeyBuilder(userPrefix).EventTimeIndexKey(key)
}

// GetTrackDataKey returns the Redis key for HashIdx track event data (for testing).
func GetTrackDataKey(userPrefix string, key session.Key, track session.Track) string {
	return newKeyBuilder(userPrefix).TrackDataKey(key, track)
}

// GetTrackTimeIndexKey returns the Redis key for HashIdx track event time index (for testing).
func GetTrackTimeIndexKey(userPrefix string, key session.Key, track session.Track) string {
	return newKeyBuilder(userPrefix).TrackTimeIndexKey(key, track)
}

// GetUserStateKey returns the Redis key for HashIdx user state (for testing).
func GetUserStateKey(userPrefix string, appName, userID string) string {
	return newKeyBuilder(userPrefix).UserStateKey(appName, userID)
}

// GetSessionIndexKey returns the Redis key for HashIdx session index (for testing).
func GetSessionIndexKey(userPrefix string, userKey session.UserKey) string {
	return newKeyBuilder(userPrefix).SessionIndexKey(userKey)
}
