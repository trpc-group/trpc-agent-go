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

// indexType defines the behavior of an index.
type indexType int

const (
	// indexTypeList stores multiple event IDs for one index value (e.g., RequestID -> [ID1, ID2]).
	indexTypeList indexType = iota
)

// eventIndex defines the internal interface for event indexing.
type eventIndex interface {
	// Name returns the index identifier (e.g., "req", "branch").
	Name() string
	// Type returns the index type.
	Type() indexType
	// ExtractKey extracts the index value from an event.
	ExtractKey(evt *event.Event) string
}

// requestIDIndex indexes events by RequestID.
type requestIDIndex struct{}

func (i *requestIDIndex) Name() string    { return "req" }
func (i *requestIDIndex) Type() indexType { return indexTypeList }
func (i *requestIDIndex) ExtractKey(evt *event.Event) string {
	if evt == nil {
		return ""
	}
	return evt.RequestID
}

// branchIndex indexes events by Branch.
type branchIndex struct{}

func (i *branchIndex) Name() string    { return "branch" }
func (i *branchIndex) Type() indexType { return indexTypeList }
func (i *branchIndex) ExtractKey(evt *event.Event) string {
	if evt == nil {
		return ""
	}
	return evt.Branch
}

// hashTag returns the hash tag for a session key.
func hashTag(key session.Key) string {
	return fmt.Sprintf("{%s:%s:%s}", key.AppName, key.UserID, key.SessionID)
}

func sessionMetaKey(key session.Key) string {
	return fmt.Sprintf("meta:%s", hashTag(key))
}

func eventDataKey(key session.Key) string {
	return fmt.Sprintf("evtdata:%s", hashTag(key))
}

func eventTimeIndexKey(key session.Key) string {
	return fmt.Sprintf("evtidx:time:%s", hashTag(key))
}

// eventCustomIndexKey returns the single Hash key that holds all custom indexes for a session.
func eventCustomIndexKey(key session.Key) string {
	return fmt.Sprintf("evtidx:custom:%s", hashTag(key))
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
