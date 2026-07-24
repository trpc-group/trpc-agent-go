//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

type replayRuntime struct {
	backend Backend
	cfg     RunConfig
	sess    *session.Session
	mu      sync.Mutex
}

func executeCase(ctx context.Context, backend Backend, cfg RunConfig, tc ReplayCase) (Snapshot, error) {
	if backend.Session == nil {
		return Snapshot{}, fmt.Errorf("backend %q has no session service", backend.Name)
	}
	if cfg.AppName == "" || cfg.UserID == "" || cfg.SessionID == "" {
		return Snapshot{}, fmt.Errorf("run config requires appName, userID, and sessionID")
	}
	rt := &replayRuntime{backend: backend, cfg: cfg}
	for i := range tc.Operations {
		if err := rt.apply(ctx, tc.Operations[i]); err != nil {
			return Snapshot{}, fmt.Errorf("case %s op %d %s: %w", tc.Name, i, tc.Operations[i].Kind, err)
		}
	}
	return Normalize(ctx, backend, cfg, tc)
}

func (rt *replayRuntime) apply(ctx context.Context, op Operation) error {
	switch op.Kind {
	case OperationCreateSession:
		return rt.createSession(ctx, op.State)
	case OperationAppendEvent:
		if err := rt.ensureSession(ctx); err != nil {
			return err
		}
		if op.Event == nil {
			return fmt.Errorf("event operation is nil")
		}
		return rt.backend.Session.AppendEvent(ctx, rt.sess, op.Event)
	case OperationUpdateSessionState:
		if err := rt.ensureSession(ctx); err != nil {
			return err
		}
		key := rt.sessionKey()
		if err := rt.backend.Session.UpdateSessionState(ctx, key, op.State); err != nil {
			return err
		}
		return rt.refreshSession(ctx)
	case OperationAddMemory:
		if op.Memory == nil {
			return fmt.Errorf("memory operation is nil")
		}
		if rt.backend.Memory == nil {
			return nil
		}
		userKey := memory.UserKey{AppName: rt.cfg.AppName, UserID: rt.cfg.UserID}
		opts := memoryAddOptions(op.Memory)
		return rt.backend.Memory.AddMemory(ctx, userKey, op.Memory.Content, op.Memory.Topics, opts...)
	case OperationUpdateMemory:
		if op.Memory == nil {
			return fmt.Errorf("memory operation is nil")
		}
		if rt.backend.Memory == nil {
			return nil
		}
		memoryID, err := rt.resolveMemoryID(ctx, op.Memory)
		if err != nil {
			return err
		}
		opts := memoryUpdateOptions(op.Memory)
		key := memory.Key{AppName: rt.cfg.AppName, UserID: rt.cfg.UserID, MemoryID: memoryID}
		return rt.backend.Memory.UpdateMemory(ctx, key, op.Memory.Content, op.Memory.Topics, opts...)
	case OperationDeleteMemory:
		if op.Memory == nil {
			return fmt.Errorf("memory operation is nil")
		}
		if rt.backend.Memory == nil {
			return nil
		}
		memoryID, err := rt.resolveMemoryID(ctx, op.Memory)
		if err != nil {
			return err
		}
		key := memory.Key{AppName: rt.cfg.AppName, UserID: rt.cfg.UserID, MemoryID: memoryID}
		return rt.backend.Memory.DeleteMemory(ctx, key)
	case OperationCreateSummary:
		if err := rt.ensureSession(ctx); err != nil {
			return err
		}
		if op.Summary == nil {
			return fmt.Errorf("summary operation is nil")
		}
		if err := rt.backend.Session.CreateSessionSummary(
			ctx,
			rt.sess,
			op.Summary.FilterKey,
			op.Summary.Force,
		); err != nil {
			return err
		}
		return rt.refreshSession(ctx)
	case OperationAppendTrack:
		if err := rt.ensureSession(ctx); err != nil {
			return err
		}
		trackSvc, ok := rt.backend.Session.(session.TrackService)
		if !ok {
			return nil
		}
		if err := trackSvc.AppendTrackEvent(ctx, rt.sess, op.Track); err != nil {
			return err
		}
		return rt.refreshSession(ctx)
	case OperationConcurrent:
		return rt.applyConcurrent(ctx, op.Operations)
	case OperationRetry:
		count := op.RetryCount
		if count <= 0 {
			count = 2
		}
		for i := 0; i < count; i++ {
			for j := range op.Operations {
				if err := rt.apply(ctx, op.Operations[j]); err != nil {
					return err
				}
			}
		}
		return nil
	case OperationExpectError:
		for i := range op.Operations {
			if err := rt.apply(ctx, op.Operations[i]); err == nil {
				return fmt.Errorf("expected operation %d to fail", i)
			}
		}
		return rt.refreshSession(ctx)
	default:
		return fmt.Errorf("unsupported operation kind %q", op.Kind)
	}
}

func (rt *replayRuntime) applyConcurrent(ctx context.Context, ops []Operation) error {
	if len(ops) == 0 {
		return nil
	}
	if err := rt.ensureSession(ctx); err != nil {
		return err
	}
	var wg sync.WaitGroup
	errCh := make(chan error, len(ops))
	for i := range ops {
		op := ops[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			if op.DelayMillis > 0 {
				time.Sleep(time.Duration(op.DelayMillis) * time.Millisecond)
			}
			rt.mu.Lock()
			err := rt.apply(ctx, op)
			rt.mu.Unlock()
			if err != nil {
				errCh <- err
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return rt.refreshSession(ctx)
}

func (rt *replayRuntime) createSession(ctx context.Context, state session.StateMap) error {
	sess, err := rt.backend.Session.CreateSession(ctx, rt.sessionKey(), state)
	if err != nil {
		return err
	}
	rt.sess = sess
	return nil
}

func (rt *replayRuntime) ensureSession(ctx context.Context) error {
	if rt.sess != nil {
		return nil
	}
	sess, err := rt.backend.Session.GetSession(ctx, rt.sessionKey())
	if err != nil {
		return err
	}
	if sess != nil {
		rt.sess = sess
		return nil
	}
	return rt.createSession(ctx, nil)
}

func (rt *replayRuntime) refreshSession(ctx context.Context) error {
	sess, err := rt.backend.Session.GetSession(ctx, rt.sessionKey())
	if err != nil {
		return err
	}
	if sess != nil {
		rt.sess = sess
	}
	return nil
}

func (rt *replayRuntime) sessionKey() session.Key {
	return session.Key{
		AppName:   rt.cfg.AppName,
		UserID:    rt.cfg.UserID,
		SessionID: rt.cfg.SessionID,
	}
}

func (rt *replayRuntime) resolveMemoryID(ctx context.Context, op *MemoryOperation) (string, error) {
	if op.ID != "" {
		return op.ID, nil
	}
	entries, err := rt.backend.Memory.ReadMemories(
		ctx,
		memory.UserKey{AppName: rt.cfg.AppName, UserID: rt.cfg.UserID},
		0,
	)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry != nil && entry.Memory != nil && entry.Memory.Memory == op.Content {
			return entry.ID, nil
		}
	}
	if len(entries) == 1 && entries[0] != nil {
		return entries[0].ID, nil
	}
	return "", fmt.Errorf("memory id not found for content %q", op.Content)
}

func memoryAddOptions(op *MemoryOperation) []memory.AddOption {
	if op.Metadata == nil {
		return nil
	}
	return []memory.AddOption{memory.WithMetadata(op.Metadata)}
}

func memoryUpdateOptions(op *MemoryOperation) []memory.UpdateOption {
	if op.Metadata == nil {
		return nil
	}
	return []memory.UpdateOption{memory.WithUpdateMetadata(op.Metadata)}
}
