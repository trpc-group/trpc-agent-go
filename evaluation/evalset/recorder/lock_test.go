//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package recorder

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestKeyedLocker_DoesNotHoldGlobalLockWhileWaiting(t *testing.T) {
	prev := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prev)
	l := newKeyedLocker()
	key := "k"
	l.lock(key)
	acquired := make(chan struct{})
	go func() {
		l.lock(key)
		close(acquired)
		l.unlock(key)
	}()
	time.Sleep(10 * time.Millisecond)
	globalLocked := make(chan struct{})
	go func() {
		l.mu.Lock()
		close(globalLocked)
		l.mu.Unlock()
	}()
	select {
	case <-globalLocked:
	case <-time.After(100 * time.Millisecond):
		assert.Fail(t, "keyedLocker holds global mutex while waiting for per-key lock")
	}
	l.unlock(key)
	select {
	case <-acquired:
	case <-time.After(100 * time.Millisecond):
		assert.FailNow(t, "keyedLocker contender did not acquire per-key lock")
	}
}

func TestKeyedLocker_RemovesLockAfterUnlock(t *testing.T) {
	l := newKeyedLocker()
	key := "k"
	l.lock(key)
	l.unlock(key)
	l.mu.Lock()
	_, ok := l.locks[key]
	l.mu.Unlock()
	assert.False(t, ok)
}

func TestKeyedLocker_UnlockMissingKeyIsNoop(t *testing.T) {
	l := newKeyedLocker()
	l.unlock("missing")
	assert.Empty(t, l.locks)
}
