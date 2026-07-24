//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"fmt"
	"sync"
)

// IdentityNamespace separates event, invocation, tool-call, response, and memory IDs.
type IdentityNamespace string

// Identity namespaces distinguish generated identifiers by semantic role.
const (
	IdentityEvent      IdentityNamespace = "event"
	IdentityInvocation IdentityNamespace = "invocation"
	IdentityToolCall   IdentityNamespace = "tool-call"
	IdentityMemory     IdentityNamespace = "memory"
)

// IdentityLedger maps backend-generated identifiers to case-defined logical IDs.
type IdentityLedger struct {
	mu        sync.RWMutex
	byRaw     map[IdentityNamespace]map[string]string
	byLogical map[IdentityNamespace]map[string]string
}

// NewIdentityLedger creates an empty identity ledger.
func NewIdentityLedger() *IdentityLedger {
	return &IdentityLedger{
		byRaw:     make(map[IdentityNamespace]map[string]string),
		byLogical: make(map[IdentityNamespace]map[string]string),
	}
}

// Register records one raw-to-logical identity relation. A logical ID may not
// silently point at two different backend objects because that would hide duplicates.
func (l *IdentityLedger) Register(namespace IdentityNamespace, raw, logical string) error {
	if l == nil {
		return fmt.Errorf("identity ledger is nil")
	}
	if namespace == "" || raw == "" || logical == "" {
		return fmt.Errorf("identity namespace, raw id, and logical id are required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byRaw[namespace] == nil {
		l.byRaw[namespace] = make(map[string]string)
	}
	if l.byLogical[namespace] == nil {
		l.byLogical[namespace] = make(map[string]string)
	}
	if existing, ok := l.byRaw[namespace][raw]; ok && existing != logical {
		return fmt.Errorf("%s raw id %q already maps to logical id %q", namespace, raw, existing)
	}
	if existing, ok := l.byLogical[namespace][logical]; ok && existing != raw {
		return fmt.Errorf("%s logical id %q already maps to raw id %q", namespace, logical, existing)
	}
	l.byRaw[namespace][raw] = logical
	l.byLogical[namespace][logical] = raw
	return nil
}

// Logical resolves a backend ID to a stable logical ID.
func (l *IdentityLedger) Logical(namespace IdentityNamespace, raw string) (string, bool) {
	if l == nil || raw == "" {
		return "", false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	logical, ok := l.byRaw[namespace][raw]
	return logical, ok
}

// Raw resolves a logical ID to the backend ID used by one replay run.
func (l *IdentityLedger) Raw(namespace IdentityNamespace, logical string) (string, bool) {
	if l == nil || logical == "" {
		return "", false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	raw, ok := l.byLogical[namespace][logical]
	return raw, ok
}

// Clone returns an independent copy.
func (l *IdentityLedger) Clone() *IdentityLedger {
	out := NewIdentityLedger()
	if l == nil {
		return out
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	for namespace, values := range l.byRaw {
		out.byRaw[namespace] = make(map[string]string, len(values))
		for raw, logical := range values {
			out.byRaw[namespace][raw] = logical
		}
	}
	for namespace, values := range l.byLogical {
		out.byLogical[namespace] = make(map[string]string, len(values))
		for logical, raw := range values {
			out.byLogical[namespace][logical] = raw
		}
	}
	return out
}

// Replace updates a logical ID after a backend rotates its physical identifier.
func (l *IdentityLedger) Replace(namespace IdentityNamespace, oldRaw, newRaw, logical string) error {
	if l == nil {
		return fmt.Errorf("identity ledger is nil")
	}
	if namespace == "" || newRaw == "" || logical == "" {
		return fmt.Errorf("identity namespace, new raw id, and logical id are required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.byRaw[namespace] == nil {
		l.byRaw[namespace] = make(map[string]string)
	}
	if l.byLogical[namespace] == nil {
		l.byLogical[namespace] = make(map[string]string)
	}
	if current, ok := l.byLogical[namespace][logical]; ok && oldRaw != "" && current != oldRaw {
		return fmt.Errorf("%s logical id %q maps to %q, not %q", namespace, logical, current, oldRaw)
	}
	if existing, ok := l.byRaw[namespace][newRaw]; ok && existing != logical {
		return fmt.Errorf("%s raw id %q already maps to logical id %q", namespace, newRaw, existing)
	}
	if oldRaw != "" && oldRaw != newRaw {
		delete(l.byRaw[namespace], oldRaw)
	}
	l.byRaw[namespace][newRaw] = logical
	l.byLogical[namespace][logical] = newRaw
	return nil
}
