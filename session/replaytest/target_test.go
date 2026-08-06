//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// TestCapabilityNames covers every branch of Names: the empty set, each
// single capability and the full set in declaration order.
func TestCapabilityNames(t *testing.T) {
	assert.Empty(t, replaytest.Capability{}.Names())
	assert.Equal(t,
		[]string{"session", "memory", "summary", "track", "state", "memory_search"},
		replaytest.CapAll.Names())

	tests := []struct {
		cap  replaytest.Capability
		want []string
	}{
		{replaytest.Capability{Session: true}, []string{"session"}},
		{replaytest.Capability{Memory: true}, []string{"memory"}},
		{replaytest.Capability{Summary: true}, []string{"summary"}},
		{replaytest.Capability{Tracks: true}, []string{"track"}},
		{replaytest.Capability{State: true}, []string{"state"}},
		{replaytest.Capability{MemorySearch: true}, []string{"memory_search"}},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.cap.Names())
	}
}

// TestCapabilityMissing covers both sides of every per-capability check.
func TestCapabilityMissing(t *testing.T) {
	assert.Equal(t, replaytest.Capability{},
		replaytest.Capability{}.Missing(replaytest.Capability{}))
	assert.Equal(t, replaytest.Capability{},
		replaytest.CapAll.Missing(replaytest.CapAll))
	assert.Equal(t, replaytest.CapAll,
		replaytest.Capability{}.Missing(replaytest.CapAll))

	have := replaytest.Capability{Session: true, Summary: true}
	want := replaytest.Capability{Session: true, Memory: true, Summary: true}
	assert.Equal(t, replaytest.Capability{Memory: true}, have.Missing(want))
}
