//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package progressevent validates progress extensions shared by stream filters
// and protocol adapters.
package progressevent

import "trpc.group/trpc-go/trpc-agent-go/event"

// Get returns a decodable progress snapshot with the required identity fields
// and an optional percentage in range. Unknown statuses and kinds are preserved.
func Get(e *event.Event) (*event.Progress, bool) {
	p, ok, err := event.GetExtension[*event.Progress](e, event.ProgressExtensionKey)
	if err != nil || !ok || p == nil || p.OperationID == "" || p.Kind == "" || p.Status == "" {
		return nil, false
	}
	if p.Percent != nil && (*p.Percent < 0 || *p.Percent > 100) {
		return nil, false
	}
	return p, true
}
