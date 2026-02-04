//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package v2

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

const v2VersionPrefix = "v2"

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

// fullPrefix returns the complete prefix: userPrefix:v2 or just v2
func (b *keyBuilder) fullPrefix() string {
	if b.userPrefix != "" {
		return b.userPrefix + ":" + v2VersionPrefix
	}
	return v2VersionPrefix
}

// hashTag generates the hash tag for user-scoped keys.
func (b *keyBuilder) hashTag(key session.Key) string {
	return fmt.Sprintf("{%s:%s}", key.AppName, key.UserID)
}

// SessionMetaKey returns the key for session metadata.
// Format: [userPrefix:]v2:meta:{appName:userID}:sessionID
func (b *keyBuilder) SessionMetaKey(key session.Key) string {
	return fmt.Sprintf("%s:meta:%s:%s", b.fullPrefix(), b.hashTag(key), key.SessionID)
}

// SessionMetaPattern returns the scan pattern for ListSessions.
// Format: [userPrefix:]v2:meta:{appName:userID}:*
func (b *keyBuilder) SessionMetaPattern(userKey session.UserKey) string {
	return fmt.Sprintf("%s:meta:{%s:%s}:*", b.fullPrefix(), userKey.AppName, userKey.UserID)
}

// EventDataKey returns the key for event data hash.
// Format: [userPrefix:]v2:evtdata:{appName:userID}:sessionID
func (b *keyBuilder) EventDataKey(key session.Key) string {
	return fmt.Sprintf("%s:evtdata:%s:%s", b.fullPrefix(), b.hashTag(key), key.SessionID)
}

// EventTimeIndexKey returns the key for time-based event index.
// Format: [userPrefix:]v2:evtidx:time:{appName:userID}:sessionID
func (b *keyBuilder) EventTimeIndexKey(key session.Key) string {
	return fmt.Sprintf("%s:evtidx:time:%s:%s", b.fullPrefix(), b.hashTag(key), key.SessionID)
}

// SummaryKey returns the key for session summary.
// Format: [userPrefix:]v2:sesssum:{appName:userID}:sessionID
func (b *keyBuilder) SummaryKey(key session.Key) string {
	return fmt.Sprintf("%s:sesssum:%s:%s", b.fullPrefix(), b.hashTag(key), key.SessionID)
}

// TrackKey returns the key for track events.
// Format: [userPrefix:]v2:track:{appName:userID}:sessionID:trackName
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
// Note: AppState is shared between V1 and V2, so no v2 prefix is added.
func (b *keyBuilder) AppStateKey(appName string) string {
	if b.userPrefix != "" {
		return fmt.Sprintf("%s:appstate:{%s}", b.userPrefix, appName)
	}
	return fmt.Sprintf("appstate:{%s}", appName)
}

// UserStateKey returns the key for user-level state.
// Format: [userPrefix:]v2:userstate:{appName:userID}
// Hash tag is {appName:userID} to ensure user-level distribution in Redis Cluster.
// Note: V2 uses different hash tag than V1 to avoid hot spots, so dual-write/fallback is needed.
func (b *keyBuilder) UserStateKey(appName, userID string) string {
	return fmt.Sprintf("%s:userstate:{%s:%s}", b.fullPrefix(), appName, userID)
}

// GetSessionSummaryKey returns the Redis key for V2 session summaries (for external use).
// Format: [userPrefix:]v2:sesssum:{appName:userID}:sessionID
// Hash tag is {appName:userID} to ensure user-level distribution in Redis Cluster.
func GetSessionSummaryKey(userPrefix string, key session.Key) string {
	prefix := v2VersionPrefix
	if userPrefix != "" {
		prefix = userPrefix + ":" + v2VersionPrefix
	}
	return fmt.Sprintf("%s:sesssum:{%s:%s}:%s", prefix, key.AppName, key.UserID, key.SessionID)
}
