//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package recorder

import (
	"sync"
)

type keyedLocker struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	mu   sync.Mutex
	refs int
}

func newKeyedLocker() *keyedLocker {
	return &keyedLocker{locks: make(map[string]*keyedLock)}
}

func (l *keyedLocker) lock(key string) {
	l.mu.Lock()
	entry, ok := l.locks[key]
	if !ok || entry == nil {
		entry = &keyedLock{}
		l.locks[key] = entry
	}
	entry.refs++
	l.mu.Unlock()
	entry.mu.Lock()
}

func (l *keyedLocker) unlock(key string) {
	l.mu.Lock()
	entry, ok := l.locks[key]
	if !ok || entry == nil {
		l.mu.Unlock()
		return
	}
	entry.refs--
	if entry.refs == 0 {
		entry.mu.Unlock()
		delete(l.locks, key)
		l.mu.Unlock()
		return
	}
	l.mu.Unlock()
	entry.mu.Unlock()
}
