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
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestKeyBuilder_SessionMetaKey(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		key      session.Key
		expected string
	}{
		{
			name:     "no prefix",
			prefix:   "",
			key:      session.Key{AppName: "myapp", UserID: "u1", SessionID: "s1"},
			expected: "hashidx:meta:myapp:{u1}:s1",
		},
		{
			name:     "with prefix",
			prefix:   "prod",
			key:      session.Key{AppName: "myapp", UserID: "u1", SessionID: "s1"},
			expected: "prod:hashidx:meta:myapp:{u1}:s1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := newKeyBuilder(tt.prefix)
			assert.Equal(t, tt.expected, kb.SessionMetaKey(tt.key))
		})
	}
}

func TestKeyBuilder_SessionMetaPattern(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		userKey  session.UserKey
		expected string
	}{
		{
			name:     "no prefix",
			prefix:   "",
			userKey:  session.UserKey{AppName: "myapp", UserID: "u1"},
			expected: "hashidx:meta:myapp:{u1}:*",
		},
		{
			name:     "with prefix",
			prefix:   "prod",
			userKey:  session.UserKey{AppName: "myapp", UserID: "u1"},
			expected: "prod:hashidx:meta:myapp:{u1}:*",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := newKeyBuilder(tt.prefix)
			assert.Equal(t, tt.expected, kb.SessionMetaPattern(tt.userKey))
		})
	}
}

func TestKeyBuilder_SessionIndexKey(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		userKey  session.UserKey
		expected string
	}{
		{
			name:     "no prefix",
			prefix:   "",
			userKey:  session.UserKey{AppName: "myapp", UserID: "u1"},
			expected: "hashidx:sessidx:myapp:{u1}",
		},
		{
			name:     "with prefix",
			prefix:   "staging",
			userKey:  session.UserKey{AppName: "myapp", UserID: "u1"},
			expected: "staging:hashidx:sessidx:myapp:{u1}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kb := newKeyBuilder(tt.prefix)
			assert.Equal(t, tt.expected, kb.SessionIndexKey(tt.userKey))
		})
	}
}

func TestKeyBuilder_EventDataKey(t *testing.T) {
	kb := newKeyBuilder("")
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	assert.Equal(t, "hashidx:evtdata:app:{u}:s", kb.EventDataKey(key))

	kb2 := newKeyBuilder("pfx")
	assert.Equal(t, "pfx:hashidx:evtdata:app:{u}:s", kb2.EventDataKey(key))
}

func TestKeyBuilder_EventTimeIndexKey(t *testing.T) {
	kb := newKeyBuilder("")
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	assert.Equal(t, "hashidx:evtidx:time:app:{u}:s", kb.EventTimeIndexKey(key))

	kb2 := newKeyBuilder("pfx")
	assert.Equal(t, "pfx:hashidx:evtidx:time:app:{u}:s", kb2.EventTimeIndexKey(key))
}

func TestKeyBuilder_SummaryKey(t *testing.T) {
	kb := newKeyBuilder("")
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	assert.Equal(t, "hashidx:sesssum:app:{u}:s", kb.SummaryKey(key))

	kb2 := newKeyBuilder("pfx")
	assert.Equal(t, "pfx:hashidx:sesssum:app:{u}:s", kb2.SummaryKey(key))
}

func TestKeyBuilder_TrackDataKey(t *testing.T) {
	kb := newKeyBuilder("")
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	assert.Equal(t, "hashidx:trkdata:app:{u}:s:actions", kb.TrackDataKey(key, "actions"))

	kb2 := newKeyBuilder("pfx")
	assert.Equal(t, "pfx:hashidx:trkdata:app:{u}:s:actions", kb2.TrackDataKey(key, "actions"))
}

func TestKeyBuilder_TrackTimeIndexKey(t *testing.T) {
	kb := newKeyBuilder("")
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	assert.Equal(t, "hashidx:trkidx:time:app:{u}:s:actions", kb.TrackTimeIndexKey(key, "actions"))

	kb2 := newKeyBuilder("pfx")
	assert.Equal(t, "pfx:hashidx:trkidx:time:app:{u}:s:actions", kb2.TrackTimeIndexKey(key, "actions"))
}

func TestKeyBuilder_TrackKeys(t *testing.T) {
	kb := newKeyBuilder("")
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	keys := kb.TrackKeys(key, "alpha")
	assert.Len(t, keys, 2)
	assert.Equal(t, "hashidx:trkdata:app:{u}:s:alpha", keys[0])
	assert.Equal(t, "hashidx:trkidx:time:app:{u}:s:alpha", keys[1])
}

func TestKeyBuilder_SessionKeys(t *testing.T) {
	kb := newKeyBuilder("")
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}
	keys := kb.SessionKeys(key)
	assert.Len(t, keys, 4)
	assert.Equal(t, "hashidx:meta:app:{u}:s", keys[0])
	assert.Equal(t, "hashidx:evtdata:app:{u}:s", keys[1])
	assert.Equal(t, "hashidx:evtidx:time:app:{u}:s", keys[2])
	assert.Equal(t, "hashidx:sesssum:app:{u}:s", keys[3])
}

func TestKeyBuilder_AppStateKey(t *testing.T) {
	kb := newKeyBuilder("")
	assert.Equal(t, "appstate:{myapp}", kb.AppStateKey("myapp"))

	kb2 := newKeyBuilder("pfx")
	assert.Equal(t, "pfx:appstate:{myapp}", kb2.AppStateKey("myapp"))
}

func TestKeyBuilder_UserStateKey(t *testing.T) {
	kb := newKeyBuilder("")
	assert.Equal(t, "hashidx:userstate:app:{u1}", kb.UserStateKey("app", "u1"))

	kb2 := newKeyBuilder("pfx")
	assert.Equal(t, "pfx:hashidx:userstate:app:{u1}", kb2.UserStateKey("app", "u1"))
}

func TestExportedKeyHelpers(t *testing.T) {
	key := session.Key{AppName: "app", UserID: "u", SessionID: "s"}

	assert.Equal(t, "hashidx:sesssum:app:{u}:s", GetSessionSummaryKey("", key))
	assert.Equal(t, "pfx:hashidx:sesssum:app:{u}:s", GetSessionSummaryKey("pfx", key))

	assert.Equal(t, "hashidx:meta:app:{u}:s", GetSessionMetaKey("", key))
	assert.Equal(t, "pfx:hashidx:meta:app:{u}:s", GetSessionMetaKey("pfx", key))
	assert.Equal(t, "hashidx:meta:app:{u}:*", GetSessionMetaPattern("", session.UserKey{AppName: "app", UserID: "u"}))
	assert.Equal(t, "pfx:hashidx:meta:app:{u}:*", GetSessionMetaPattern("pfx", session.UserKey{AppName: "app", UserID: "u"}))

	assert.Equal(t, "hashidx:evtdata:app:{u}:s", GetEventDataKey("", key))
	assert.Equal(t, "hashidx:evtidx:time:app:{u}:s", GetEventTimeIndexKey("", key))

	assert.Equal(t, "hashidx:trkdata:app:{u}:s:t", GetTrackDataKey("", key, "t"))
	assert.Equal(t, "hashidx:trkidx:time:app:{u}:s:t", GetTrackTimeIndexKey("", key, "t"))

	assert.Equal(t, "hashidx:userstate:app:{u}", GetUserStateKey("", "app", "u"))

	userKey := session.UserKey{AppName: "app", UserID: "u"}
	assert.Equal(t, "hashidx:sessidx:app:{u}", GetSessionIndexKey("", userKey))
	assert.Equal(t, "pfx:hashidx:sessidx:app:{u}", GetSessionIndexKey("pfx", userKey))
}
