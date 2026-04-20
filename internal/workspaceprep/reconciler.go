//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceprep

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/skillstage"
)

// defaultReconciler is the process-local, single-node implementation
// of Reconciler. It uses a keyed mutex on ws.Path to serialize
// reconciles for the same workspace, reads/writes WorkspaceMetadata
// through the shared skillstage helpers, and enforces a fixed phase
// order (PhaseFile -> PhaseSkill -> PhaseCommand).
type defaultReconciler struct {
	locker *keyedLocker
	stager *skillstage.Stager
}

// NewReconciler returns the default Reconciler used by workspace_exec
// and other workspace-aware tools.
func NewReconciler() Reconciler {
	return &defaultReconciler{
		locker: newKeyedLocker(),
		stager: skillstage.New(),
	}
}

// Reconcile implements Reconciler.
func (r *defaultReconciler) Reconcile(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	reqs []Requirement,
) ([]string, error) {
	if len(reqs) == 0 {
		return nil, nil
	}
	if eng == nil {
		return nil, fmt.Errorf("workspaceprep: engine is required")
	}

	reqs = dedupeRequirements(reqs)
	sortRequirements(reqs)

	unlock := r.locker.lock(ws.Path)
	defer unlock()

	md, err := r.stager.LoadWorkspaceMetadata(ctx, eng, ws)
	if err != nil {
		return nil, fmt.Errorf("workspaceprep: load metadata: %w", err)
	}
	if md.Prepared == nil {
		md.Prepared = map[string]codeexecutor.PreparedRecord{}
	}

	rctx := ApplyContext{
		Engine:    eng,
		Workspace: ws,
		Metadata:  &md,
	}
	if inv, ok := agent.InvocationFromContext(ctx); ok {
		rctx.Invocation = inv
	}

	var warnings []string
	changed := false
	for _, req := range reqs {
		applied, warn, err := r.runOne(ctx, rctx, req)
		if warn != "" {
			warnings = append(warnings, warn)
		}
		if err != nil {
			if !req.Required() {
				warnings = append(warnings, fmt.Sprintf(
					"optional requirement %q failed: %v",
					req.Key(), err,
				))
				continue
			}
			if changed {
				_ = r.stager.SaveWorkspaceMetadata(ctx, eng, ws, md)
			}
			return warnings, fmt.Errorf(
				"workspaceprep: required requirement %q failed: %w",
				req.Key(), err,
			)
		}
		if applied {
			changed = true
		}
	}
	if changed {
		if err := r.stager.SaveWorkspaceMetadata(
			ctx, eng, ws, md,
		); err != nil {
			warnings = append(warnings, fmt.Sprintf(
				"save metadata: %v", err,
			))
		}
	}
	return warnings, nil
}

// runOne applies a single requirement. It returns whether work was
// done (so the caller knows to persist metadata), a non-empty warning
// string that callers should surface, and an error on hard failure.
func (r *defaultReconciler) runOne(
	ctx context.Context,
	rctx ApplyContext,
	req Requirement,
) (bool, string, error) {
	key := req.Key()
	expected, err := req.Fingerprint(ctx, rctx)
	if err != nil {
		return false, "", fmt.Errorf("fingerprint: %w", err)
	}
	prev, hasPrev := rctx.Metadata.Prepared[key]
	if hasPrev && prev.Fingerprint == expected {
		ok, err := req.SentinelExists(ctx, rctx)
		if err != nil {
			return false, "", fmt.Errorf("sentinel: %w", err)
		}
		if ok {
			return false, "", nil
		}
	}
	if err := req.Apply(ctx, rctx); err != nil {
		return false, "", err
	}
	rctx.Metadata.Prepared[key] = codeexecutor.PreparedRecord{
		Key:         key,
		Kind:        string(req.Kind()),
		Fingerprint: expected,
		Target:      req.Target(),
		PreparedAt:  time.Now(),
	}
	return true, "", nil
}

// sortRequirements orders requirements by Phase and, within a phase,
// by their original position (Go's sort.SliceStable preserves
// insertion order for equal keys). Callers should pass the slice in
// the order Providers were registered so that behavior is
// deterministic.
func sortRequirements(reqs []Requirement) {
	sort.SliceStable(reqs, func(i, j int) bool {
		return reqs[i].Phase() < reqs[j].Phase()
	})
}

// dedupeRequirements removes duplicate requirements by Key while
// preserving the first occurrence. This lets multiple Providers
// contribute overlapping requirements without forcing them to
// coordinate; the reconciler simply honors the first one it saw.
func dedupeRequirements(in []Requirement) []Requirement {
	out := make([]Requirement, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, r := range in {
		if r == nil {
			continue
		}
		key := strings.TrimSpace(r.Key())
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	return out
}

// keyedLocker is a small process-local keyed mutex used to serialize
// reconciles for the same workspace. A sync.Map-backed implementation
// would be acceptable too; the simple map+mutex version is chosen for
// clarity because contention is rare (same-session parallel tool calls
// reconciling at the same time).
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

// lock acquires the mutex for the given key and returns an unlock
// function. The lock is reference-counted so parallel callers for
// different keys never contend on the outer mutex for longer than
// needed.
func (k *keyedLocker) lock(key string) func() {
	if key == "" {
		// Fall back to a shared lock for empty keys so callers still
		// get serialization even when ws.Path is unexpectedly empty.
		key = "__empty__"
	}
	k.mu.Lock()
	kl, ok := k.locks[key]
	if !ok {
		kl = &keyedLock{}
		k.locks[key] = kl
	}
	kl.refs++
	k.mu.Unlock()

	kl.mu.Lock()
	return func() {
		kl.mu.Unlock()
		k.mu.Lock()
		kl.refs--
		if kl.refs == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}
