//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/state/sessionstate"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const (
	pendingAutoMemoryBatchVersion  = 1
	pendingAutoMemoryBatchStateKey = "memory:pending_operation_batch"
)

type pendingAutoMemoryBatch struct {
	Version    int                    `json:"version"`
	LatestTs   time.Time              `json:"latest_ts"`
	Next       int                    `json:"next"`
	Operations []*extractor.Operation `json:"operations"`
}

func (w *AutoMemoryWorker) processAutoMemoryDelta(
	ctx context.Context,
	userKey memory.UserKey,
	sess *session.Session,
	latestTs time.Time,
	messages []model.Message,
) error {
	policy := updatePolicyFor(w.config.Extractor)
	if policy == extractor.UpdatePolicyMergeSimilar || sess == nil {
		if err := w.createAutoMemory(ctx, userKey, messages); err != nil {
			return err
		}
		if sess != nil {
			writeLastExtractAt(sess, latestTs)
		}
		return nil
	}

	lock := &w.userLock[hashUserKey(userKey)%autoMemoryUserLockCount]
	lock.Lock()
	defer lock.Unlock()

	current, err := loadAutoMemorySession(ctx, sess)
	if err != nil {
		return err
	}
	return w.processReliableAutoMemoryDelta(ctx, userKey, current)
}

func loadAutoMemorySession(
	ctx context.Context,
	sess *session.Session,
) (*session.Session, error) {
	service, ok := sessionstate.ServiceFromContext(ctx)
	if !ok {
		return sess, nil
	}
	current, err := service.GetSession(ctx, session.Key{
		AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("auto_memory: reload session state: %w", err)
	}
	if current == nil {
		// A no-op session service intentionally returns no persisted session.
		// Keep processing its transient live session in that configuration.
		return sess, nil
	}
	// Refresh only worker-owned state. The queued session remains the source of
	// events because a session backend may apply an event-window limit when it
	// reloads the session, while the job already captured the complete delta.
	for _, key := range []string{
		pendingAutoMemoryBatchStateKey,
		memory.SessionStateKeyAutoMemoryLastExtractAt,
	} {
		value, exists := current.GetState(key)
		if !exists || len(value) == 0 {
			sess.DeleteState(key)
			continue
		}
		sess.SetState(key, value)
	}
	return sess, nil
}

func (w *AutoMemoryWorker) processReliableAutoMemoryDelta(
	ctx context.Context,
	userKey memory.UserKey,
	sess *session.Session,
) error {
	pending, err := readPendingAutoMemoryBatch(sess)
	if err != nil {
		return err
	}
	since := readLastExtractAt(sess)
	if pending != nil && !since.Before(pending.LatestTs) {
		if err := persistPendingAutoMemoryBatch(ctx, sess, nil); err != nil {
			return err
		}
		pending = nil
	}
	if pending != nil {
		if err := w.executePendingAutoMemoryBatch(ctx, userKey, sess, pending); err != nil {
			return err
		}
		if err := persistLastExtractAt(ctx, sess, pending.LatestTs); err != nil {
			return err
		}
		if err := persistPendingAutoMemoryBatch(ctx, sess, nil); err != nil {
			return err
		}
		since = pending.LatestTs
	}

	latestTs, messages := scanDeltaSince(sess, since)
	if len(messages) == 0 {
		return nil
	}
	var lastExtractAt *time.Time
	if !since.IsZero() {
		sinceUTC := since.UTC()
		lastExtractAt = &sinceUTC
	}
	if !w.config.Extractor.ShouldExtract(&extractor.ExtractionContext{
		UserKey: userKey, Messages: messages, LastExtractAt: lastExtractAt,
	}) {
		return nil
	}

	ops, err := w.prepareAutoMemoryOperations(ctx, userKey, messages)
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return persistLastExtractAt(ctx, sess, latestTs)
	}
	pending = &pendingAutoMemoryBatch{
		Version:    pendingAutoMemoryBatchVersion,
		LatestTs:   latestTs,
		Operations: ops,
	}
	if err := persistPendingAutoMemoryBatch(ctx, sess, pending); err != nil {
		return err
	}
	if err := w.executePendingAutoMemoryBatch(ctx, userKey, sess, pending); err != nil {
		return err
	}
	if err := persistLastExtractAt(ctx, sess, latestTs); err != nil {
		return err
	}
	return persistPendingAutoMemoryBatch(ctx, sess, nil)
}

func (w *AutoMemoryWorker) executePendingAutoMemoryBatch(
	ctx context.Context,
	userKey memory.UserKey,
	sess *session.Session,
	pending *pendingAutoMemoryBatch,
) error {
	for pending.Next < len(pending.Operations) {
		op := pending.Operations[pending.Next]
		if err := w.executeOperation(ctx, userKey, op); err != nil {
			return err
		}
		pending.Next++
		if err := persistPendingAutoMemoryBatch(ctx, sess, pending); err != nil {
			return err
		}
	}
	return nil
}

func readPendingAutoMemoryBatch(sess *session.Session) (*pendingAutoMemoryBatch, error) {
	raw, ok := sess.GetState(pendingAutoMemoryBatchStateKey)
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var pending pendingAutoMemoryBatch
	if err := json.Unmarshal(raw, &pending); err != nil {
		return nil, fmt.Errorf("auto_memory: decode pending operation batch: %w", err)
	}
	if pending.Version != pendingAutoMemoryBatchVersion || pending.LatestTs.IsZero() ||
		len(pending.Operations) == 0 || pending.Next < 0 ||
		pending.Next > len(pending.Operations) {
		return nil, errors.New("auto_memory: invalid pending operation batch")
	}
	for _, op := range pending.Operations {
		if op == nil {
			return nil, errors.New("auto_memory: invalid pending operation batch")
		}
	}
	return &pending, nil
}

func persistPendingAutoMemoryBatch(
	ctx context.Context,
	sess *session.Session,
	pending *pendingAutoMemoryBatch,
) error {
	if pending == nil {
		return persistAutoMemorySessionState(
			ctx, sess, pendingAutoMemoryBatchStateKey, nil,
		)
	}
	raw, err := json.Marshal(pending)
	if err != nil {
		return fmt.Errorf("auto_memory: encode pending operation batch: %w", err)
	}
	return persistAutoMemorySessionState(
		ctx, sess, pendingAutoMemoryBatchStateKey, raw,
	)
}

func persistLastExtractAt(
	ctx context.Context,
	sess *session.Session,
	ts time.Time,
) error {
	return persistAutoMemorySessionState(
		ctx,
		sess,
		memory.SessionStateKeyAutoMemoryLastExtractAt,
		[]byte(ts.UTC().Format(time.RFC3339Nano)),
	)
}

func persistAutoMemorySessionState(
	ctx context.Context,
	sess *session.Session,
	key string,
	value []byte,
) error {
	if service, ok := sessionstate.ServiceFromContext(ctx); ok {
		err := service.UpdateSessionState(ctx, session.Key{
			AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID,
		}, session.StateMap{key: value})
		if err != nil {
			return fmt.Errorf("auto_memory: persist session state %q: %w", key, err)
		}
	}
	if value == nil {
		sess.DeleteState(key)
	} else {
		sess.SetState(key, value)
	}
	return nil
}
