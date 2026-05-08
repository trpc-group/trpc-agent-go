//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// applyIngestOpts mirrors the canonical apply-in-place loop documented on
// IngestOptions so the test suite exercises helpers through the same path as
// real Ingestor implementations.
func applyIngestOpts(opts ...IngestOption) IngestOptions {
	var got IngestOptions
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		opt(&got)
	}
	return got
}

func TestWithIngestMetadata_Sets(t *testing.T) {
	got := applyIngestOpts(WithIngestMetadata(map[string]any{"k": "v"}))
	require.Equal(t, map[string]any{"k": "v"}, got.Metadata)
}

func TestWithIngestMetadata_EmptyAndNilIgnored(t *testing.T) {
	got := applyIngestOpts(
		WithIngestMetadata(nil),
		WithIngestMetadata(map[string]any{}),
	)
	assert.Nil(t, got.Metadata, "empty/nil metadata maps must not allocate storage")
}

func TestWithIngestMetadata_MergesAndOverwrites(t *testing.T) {
	got := applyIngestOpts(
		WithIngestMetadata(map[string]any{"shared": "first", "only_in_first": 1}),
		WithIngestMetadata(map[string]any{"shared": "second", "only_in_second": true}),
	)
	require.Len(t, got.Metadata, 3)
	assert.Equal(t, "second", got.Metadata["shared"], "later calls must overwrite duplicate keys")
	assert.Equal(t, 1, got.Metadata["only_in_first"])
	assert.Equal(t, true, got.Metadata["only_in_second"])
}

func TestWithIngestMetadata_ResolvedMapIsIndependent(t *testing.T) {
	caller := map[string]any{"k": "v"}
	got := applyIngestOpts(WithIngestMetadata(caller))

	caller["k"] = "mutated"
	caller["new"] = "injected"

	assert.Equal(t, "v", got.Metadata["k"], "resolved map must not alias the caller map after apply")
	_, present := got.Metadata["new"]
	assert.False(t, present, "resolved map must not observe post-apply caller mutations")
}

func TestWithIngestAgentID(t *testing.T) {
	t.Run("sets agent id", func(t *testing.T) {
		got := applyIngestOpts(WithIngestAgentID("agent-a"))
		assert.Equal(t, "agent-a", got.AgentID)
	})
	t.Run("empty value is ignored", func(t *testing.T) {
		got := applyIngestOpts(
			WithIngestAgentID("agent-a"),
			WithIngestAgentID(""),
		)
		assert.Equal(t, "agent-a", got.AgentID, "empty string must not clear an earlier value")
	})
	t.Run("later non-empty overrides", func(t *testing.T) {
		got := applyIngestOpts(
			WithIngestAgentID("agent-a"),
			WithIngestAgentID("agent-b"),
		)
		assert.Equal(t, "agent-b", got.AgentID)
	})
}

func TestWithIngestRunID(t *testing.T) {
	t.Run("sets run id", func(t *testing.T) {
		got := applyIngestOpts(WithIngestRunID("run-a"))
		assert.Equal(t, "run-a", got.RunID)
	})
	t.Run("empty value is ignored", func(t *testing.T) {
		got := applyIngestOpts(
			WithIngestRunID("run-a"),
			WithIngestRunID(""),
		)
		assert.Equal(t, "run-a", got.RunID, "empty string must not clear an earlier value")
	})
	t.Run("later non-empty overrides", func(t *testing.T) {
		got := applyIngestOpts(
			WithIngestRunID("run-a"),
			WithIngestRunID("run-b"),
		)
		assert.Equal(t, "run-b", got.RunID)
	})
}

func TestIngestOptions_CombinedApply(t *testing.T) {
	got := applyIngestOpts(
		WithIngestMetadata(map[string]any{"tag": "x"}),
		WithIngestAgentID("agent-1"),
		WithIngestRunID("run-1"),
	)
	assert.Equal(t, "agent-1", got.AgentID)
	assert.Equal(t, "run-1", got.RunID)
	assert.Equal(t, "x", got.Metadata["tag"])
}

// stubIngestor verifies the Ingestor interface can be satisfied by a plain
// struct, guarding against accidental signature changes.
type stubIngestor struct {
	last IngestOptions
}

func (s *stubIngestor) IngestSession(_ context.Context, _ *Session, opts ...IngestOption) error {
	s.last = applyIngestOpts(opts...)
	return nil
}

func TestIngestor_InterfaceSatisfaction(t *testing.T) {
	var impl Ingestor = &stubIngestor{}
	err := impl.IngestSession(
		context.Background(),
		nil,
		WithIngestAgentID("agent-z"),
		WithIngestRunID("run-z"),
	)
	require.NoError(t, err)
	stub, ok := impl.(*stubIngestor)
	require.True(t, ok)
	assert.Equal(t, "agent-z", stub.last.AgentID)
	assert.Equal(t, "run-z", stub.last.RunID)
}
