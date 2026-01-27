//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redisv2

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// eventIndex defines the abstract interface for event indexes (internal).
type eventIndex interface {
	// name returns the index name, used for generating Redis key.
	name() string

	// extractKey extracts the index key from an event.
	// Returns empty string if this event doesn't need this index.
	extractKey(evt *event.Event) string

	// buildRedisKey builds the full Redis key for this index.
	buildRedisKey(tag string) string
}

// requestIDIndex indexes events by RequestID.
type requestIDIndex struct{}

func (i *requestIDIndex) name() string { return "req" }

func (i *requestIDIndex) extractKey(evt *event.Event) string {
	if evt == nil {
		return ""
	}
	return evt.RequestID
}

func (i *requestIDIndex) buildRedisKey(tag string) string {
	return fmt.Sprintf("evtidx:req:%s", tag)
}

// defaultIndexes are the indexes enabled by default (internal).
var defaultIndexes = []eventIndex{
	&requestIDIndex{},
}

// hashTag returns the hash tag for a session key.
// Hash tags ensure all keys for a session land in the same Redis Cluster slot.
func hashTag(key session.Key) string {
	return fmt.Sprintf("{%s:%s:%s}", key.AppName, key.UserID, key.SessionID)
}

// Key generation functions for different data types.

func metaKey(key session.Key) string {
	return fmt.Sprintf("meta:%s", hashTag(key))
}

func eventDataKey(key session.Key) string {
	return fmt.Sprintf("evtdata:%s", hashTag(key))
}

func eventTimeIndexKey(key session.Key) string {
	return fmt.Sprintf("evtidx:time:%s", hashTag(key))
}

func eventReqIndexKey(key session.Key) string {
	return fmt.Sprintf("evtidx:req:%s", hashTag(key))
}

func summaryKey(key session.Key) string {
	return fmt.Sprintf("sesssum:%s", hashTag(key))
}

func appStateKey(appName string) string {
	return fmt.Sprintf("appstate:{%s}", appName)
}

func userStateKey(appName, userID string) string {
	return fmt.Sprintf("userstate:{%s}:%s", appName, userID)
}

