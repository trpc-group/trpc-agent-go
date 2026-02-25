//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"strings"
	"sync"
)

const (
	commandPrefix = "/"

	commandHelp   = "help"
	commandCancel = "cancel"
)

const helpMessage = `Commands:
/help   Show help
/cancel Cancel the current run`

func parseCommand(text string, bot BotInfo) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, commandPrefix) {
		return ""
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}

	token := strings.TrimPrefix(fields[0], commandPrefix)
	if token == "" {
		return ""
	}

	cmd := token
	target := ""
	if idx := strings.IndexByte(token, '@'); idx > 0 {
		cmd = token[:idx]
		target = token[idx+1:]
	}
	cmd = strings.ToLower(strings.TrimSpace(cmd))
	if cmd == "" {
		return ""
	}

	if target == "" || strings.TrimSpace(bot.Username) == "" {
		return cmd
	}

	if strings.EqualFold(target, bot.Username) {
		return cmd
	}
	return ""
}

type inflightRequests struct {
	mu sync.Mutex
	m  map[string]string
}

func newInflightRequests() *inflightRequests {
	return &inflightRequests{m: make(map[string]string)}
}

func (r *inflightRequests) Get(sessionID string) string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.m[sessionID]
}

func (r *inflightRequests) Set(sessionID, requestID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.m[sessionID] = requestID
}

func (r *inflightRequests) Clear(sessionID, requestID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m[sessionID] == requestID {
		delete(r.m, sessionID)
	}
}

type laneLocker struct {
	mu    sync.Mutex
	lanes map[string]*laneEntry
}

type laneEntry struct {
	lock sync.Mutex
	refs int
}

func newLaneLocker() *laneLocker {
	return &laneLocker{lanes: make(map[string]*laneEntry)}
}

func (l *laneLocker) withLock(key string, fn func()) {
	if l == nil {
		fn()
		return
	}

	entry := l.acquire(key)
	entry.lock.Lock()
	defer func() {
		entry.lock.Unlock()
		l.release(key, entry)
	}()
	fn()
}

func (l *laneLocker) acquire(key string) *laneEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, ok := l.lanes[key]
	if ok {
		entry.refs++
		return entry
	}

	entry = &laneEntry{refs: 1}
	l.lanes[key] = entry
	return entry
}

func (l *laneLocker) release(key string, entry *laneEntry) {
	if l == nil || entry == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	current, ok := l.lanes[key]
	if !ok || current != entry {
		return
	}

	entry.refs--
	if entry.refs > 0 {
		return
	}
	delete(l.lanes, key)
}
