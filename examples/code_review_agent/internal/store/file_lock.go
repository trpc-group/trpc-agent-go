//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package store

import (
	"context"
	"fmt"
	"os"
	"time"
)

const storeLockPollInterval = 10 * time.Millisecond

type storeFileLock struct {
	file *os.File
}

func acquireStoreFileLock(ctx context.Context, path string) (*storeFileLock, error) {
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open store lock: %w", err)
	}
	lock := &storeFileLock{file: file}
	for {
		locked, err := tryLockStoreFile(file)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("lock store: %w", err)
		}
		if locked {
			return lock, nil
		}
		timer := time.NewTimer(storeLockPollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *storeFileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockStoreFile(l.file)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
