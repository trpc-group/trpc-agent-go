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
	"trpc.group/trpc-go/trpc-agent-go/event"
)

// indexType defines the behavior of an index.
type IndexType int

const (
	// IndexTypeList stores multiple event IDs for one index value (e.g., RequestID -> [ID1, ID2]).
	IndexTypeList IndexType = iota
)

// EventIndex defines the internal interface for event indexing.
type EventIndex interface {
	// Name returns the index identifier (e.g., "req", "branch").
	Name() string
	// Type returns the index type.
	Type() IndexType
	// ExtractKey extracts the index value from an event.
	ExtractKey(evt *event.Event) string
}

// RequestIDIndex indexes events by RequestID.
type RequestIDIndex struct{}

// Name returns the index name.
func (i *RequestIDIndex) Name() string { return "req" }

// Type returns the index type.
func (i *RequestIDIndex) Type() IndexType { return IndexTypeList }

// ExtractKey extracts the index value from an event.
func (i *RequestIDIndex) ExtractKey(evt *event.Event) string {
	if evt == nil {
		return ""
	}
	return evt.RequestID
}
