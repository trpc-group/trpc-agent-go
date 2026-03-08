//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telegram

import (
	"context"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

const sentFileRecordTTL = 5 * time.Minute

type sentFileKey struct {
	RequestID string
	ChatID    int64
	ThreadID  int
}

type sentFileTracker struct {
	mu     sync.Mutex
	byPath map[sentFileKey]map[string]time.Time
}

func newSentFileTracker() *sentFileTracker {
	return &sentFileTracker{
		byPath: make(map[sentFileKey]map[string]time.Time),
	}
}

func (t *sentFileTracker) Record(
	requestID string,
	chatID int64,
	threadID int,
	paths ...string,
) {
	if t == nil || strings.TrimSpace(requestID) == "" {
		return
	}

	key := sentFileKey{
		RequestID: strings.TrimSpace(requestID),
		ChatID:    chatID,
		ThreadID:  threadID,
	}
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	t.pruneLocked(now)

	entry := t.byPath[key]
	if entry == nil {
		entry = make(map[string]time.Time)
		t.byPath[key] = entry
	}
	for _, path := range paths {
		clean := cleanReplyFilePath(path)
		if clean == "" {
			continue
		}
		entry[clean] = now
	}
}

func (t *sentFileTracker) Consume(
	requestID string,
	chatID int64,
	threadID int,
) map[string]struct{} {
	if t == nil || strings.TrimSpace(requestID) == "" {
		return nil
	}

	key := sentFileKey{
		RequestID: strings.TrimSpace(requestID),
		ChatID:    chatID,
		ThreadID:  threadID,
	}
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	t.pruneLocked(now)

	entry := t.byPath[key]
	delete(t.byPath, key)
	if len(entry) == 0 {
		return nil
	}

	out := make(map[string]struct{}, len(entry))
	for path := range entry {
		out[path] = struct{}{}
	}
	return out
}

func (t *sentFileTracker) pruneLocked(now time.Time) {
	if len(t.byPath) == 0 {
		return
	}

	cutoff := now.Add(-sentFileRecordTTL)
	for key, entry := range t.byPath {
		for path, ts := range entry {
			if ts.Before(cutoff) {
				delete(entry, path)
			}
		}
		if len(entry) == 0 {
			delete(t.byPath, key)
		}
	}
}

func currentRequestIDFromContext(ctx context.Context) string {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil {
		return ""
	}
	return strings.TrimSpace(inv.RunOptions.RequestID)
}
