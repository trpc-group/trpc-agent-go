//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestAsyncAuditor_DropsWhenFullAndFlushesOnClose(t *testing.T) {
	t.Parallel()
	slow := &blockingAuditor{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	a := NewAsyncAuditor(slow, 2)

	require.NoError(t, a.Append(AuditEvent{ToolName: "hold", RuleID: "0"}))
	select {
	case <-slow.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not enter inner Append")
	}
	require.NoError(t, a.Append(AuditEvent{ToolName: "a", RuleID: "1"}))
	require.NoError(t, a.Append(AuditEvent{ToolName: "b", RuleID: "2"}))
	// Capacity 2 is full while the worker still holds the in-flight event.
	require.NoError(t, a.Append(AuditEvent{ToolName: "c", RuleID: "3"}))
	require.GreaterOrEqual(t, a.Dropped(), uint64(1))

	close(slow.release)
	require.NoError(t, a.Close())
	require.GreaterOrEqual(t, slow.seen.Load(), int64(1))
}

type blockingAuditor struct {
	started chan struct{}
	release chan struct{}
	seen    atomic.Int64
	once    atomic.Bool
}

func (b *blockingAuditor) Append(ev AuditEvent) error {
	if b.once.CompareAndSwap(false, true) {
		close(b.started)
	}
	<-b.release
	b.seen.Add(1)
	return nil
}

func TestAsyncAuditor_CanceledSkipsEnqueue(t *testing.T) {
	t.Parallel()
	inner := NewMemoryAuditor()
	a := NewAsyncAuditor(inner, 8)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, a.AppendContext(ctx, AuditEvent{ToolName: "x"}))
	require.NoError(t, a.Close())
	require.Empty(t, inner.Events())
}

func TestWithAuditor_WrapsFileAuditorAsync(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	fa, err := NewFileAuditor(path)
	require.NoError(t, err)
	g := NewGuard(WithAuditor(fa))
	t.Cleanup(func() { _ = g.Close() })

	_, ok := g.audit.(*AsyncAuditor)
	require.True(t, ok, "FileAuditor must be wrapped for the hot path")

	dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  "workspace_exec",
		Arguments: []byte(`{"command":"rm -rf /"}`),
	})
	require.NoError(t, err)
	require.Equal(t, tool.PermissionActionDeny, dec.Action)
	require.NoError(t, g.Close())

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, data)
	line := splitFirstLine(string(data))
	var ev AuditEvent
	require.NoError(t, json.Unmarshal([]byte(line), &ev))
	require.Equal(t, DecisionDeny, ev.Decision)
}

func splitFirstLine(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

func TestNewAsyncFileAuditor(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a.jsonl")
	a, err := NewAsyncFileAuditor(path, 4)
	require.NoError(t, err)
	require.NoError(t, a.Append(AuditEvent{ToolName: "t", Decision: DecisionAllow, RuleID: "allow"}))
	require.NoError(t, a.Close())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), `"allow"`)
}
