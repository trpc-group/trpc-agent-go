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
// Hash tag: {appName:userID}
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
func (b *keyBuilder) hashTag(key session.Key) string {
	return fmt.Sprintf("{%s:%s}", key.AppName, key.UserID)
}

// SessionMetaKey returns the key for session metadata.
// Format: [userPrefix:]hashidx:meta:{appName:userID}:sessionID
func (b *keyBuilder) SessionMetaKey(key session.Key) string {
	return fmt.Sprintf("%s:meta:%s:%s", b.fullPrefix(), b.hashTag(key), key.SessionID)
}

// SessionMetaPattern returns the scan pattern for ListSessions.
// Format: [userPrefix:]hashidx:meta:{appName:userID}:*
func (b *keyBuilder) SessionMetaPattern(userKey session.UserKey) string {
	return fmt.Sprintf("%s:meta:{%s:%s}:*", b.fullPrefix(), userKey.AppName, userKey.UserID)
}

// EventDataKey returns the key for event data hash.
// Format: [userPrefix:]hashidx:evtdata:{appName:userID}:sessionID
func (b *keyBuilder) EventDataKey(key session.Key) string {
	return fmt.Sprintf("%s:evtdata:%s:%s", b.fullPrefix(), b.hashTag(key), key.SessionID)
}

// EventTimeIndexKey returns the key for time-based event index.
// Format: [userPrefix:]hashidx:evtidx:time:{appName:userID}:sessionID
func (b *keyBuilder) EventTimeIndexKey(key session.Key) string {
	return fmt.Sprintf("%s:evtidx:time:%s:%s", b.fullPrefix(), b.hashTag(key), key.SessionID)
}

// SummaryKey returns the key for session summary.
// Format: [userPrefix:]hashidx:sesssum:{appName:userID}:sessionID
func (b *keyBuilder) SummaryKey(key session.Key) string {
	return fmt.Sprintf("%s:sesssum:%s:%s", b.fullPrefix(), b.hashTag(key), key.SessionID)
}

// TrackKey returns the key for track events.
// Format: [userPrefix:]hashidx:track:{appName:userID}:sessionID:trackName
func (b *keyBuilder) TrackKey(key session.Key, track session.Track) string {
	return fmt.Sprintf("%s:track:%s:%s:%s", b.fullPrefix(), b.hashTag(key), key.SessionID, track)
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
		return fmt.Sprintf("%s:appstate:{%s}", b.userPrefix, appName)
	}
	return fmt.Sprintf("appstate:{%s}", appName)
}

// UserStateKey returns the key for user-level state.
// Format: [userPrefix:]hashidx:userstate:{appName:userID}
// Hash tag is {appName:userID} to ensure user-level distribution in Redis Cluster.
// Note: HashIdx uses different hash tag than zset to avoid hot spots, so dual-write/fallback is needed.
func (b *keyBuilder) UserStateKey(appName, userID string) string {
	return fmt.Sprintf("%s:userstate:{%s:%s}", b.fullPrefix(), appName, userID)
}

// GetSessionSummaryKey returns the Redis key for HashIdx session summaries (for external use).
// Format: [userPrefix:]hashidx:sesssum:{appName:userID}:sessionID
// Hash tag is {appName:userID} to ensure user-level distribution in Redis Cluster.
func GetSessionSummaryKey(userPrefix string, key session.Key) string {
	prefix := hashidxVersionPrefix
	if userPrefix != "" {
		prefix = userPrefix + ":" + hashidxVersionPrefix
	}
	return fmt.Sprintf("%s:sesssum:{%s:%s}:%s", prefix, key.AppName, key.UserID, key.SessionID)
}

// GetSessionMetaKey returns the Redis key for HashIdx session metadata (for external use/testing).
// Format: [userPrefix:]hashidx:meta:{appName:userID}:sessionID
func GetSessionMetaKey(userPrefix string, key session.Key) string {
	prefix := hashidxVersionPrefix
	if userPrefix != "" {
		prefix = userPrefix + ":" + hashidxVersionPrefix
	}
	return fmt.Sprintf("%s:meta:{%s:%s}:%s", prefix, key.AppName, key.UserID, key.SessionID)
}

// GetEventDataKey returns the Redis key for HashIdx event data (for external use/testing).
// Format: [userPrefix:]hashidx:evtdata:{appName:userID}:sessionID
func GetEventDataKey(userPrefix string, key session.Key) string {
	prefix := hashidxVersionPrefix
	if userPrefix != "" {
		prefix = userPrefix + ":" + hashidxVersionPrefix
	}
	return fmt.Sprintf("%s:evtdata:{%s:%s}:%s", prefix, key.AppName, key.UserID, key.SessionID)
}

// GetEventTimeIndexKey returns the Redis key for HashIdx event time index (for external use/testing).
// Format: [userPrefix:]hashidx:evtidx:time:{appName:userID}:sessionID
func GetEventTimeIndexKey(userPrefix string, key session.Key) string {
	prefix := hashidxVersionPrefix
	if userPrefix != "" {
		prefix = userPrefix + ":" + hashidxVersionPrefix
	}
	return fmt.Sprintf("%s:evtidx:time:{%s:%s}:%s", prefix, key.AppName, key.UserID, key.SessionID)
}

// GetTrackKey returns the Redis key for HashIdx track events (for external use/testing).
// Format: [userPrefix:]hashidx:track:{appName:userID}:sessionID:trackName
func GetTrackKey(userPrefix string, key session.Key, track session.Track) string {
	prefix := hashidxVersionPrefix
	if userPrefix != "" {
		prefix = userPrefix + ":" + hashidxVersionPrefix
	}
	return fmt.Sprintf("%s:track:{%s:%s}:%s:%s", prefix, key.AppName, key.UserID, key.SessionID, track)
}

// GetUserStateKey returns the Redis key for HashIdx user state (for external use/testing).
// Format: [userPrefix:]hashidx:userstate:{appName:userID}
func GetUserStateKey(userPrefix string, appName, userID string) string {
	prefix := hashidxVersionPrefix
	if userPrefix != "" {
		prefix = userPrefix + ":" + hashidxVersionPrefix
	}
	return fmt.Sprintf("%s:userstate:{%s:%s}", prefix, appName, userID)
}
