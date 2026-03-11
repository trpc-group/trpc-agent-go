//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func TestFormatExistingMemory_IncludesFactTimeMetadata(t *testing.T) {
	eventTime := time.Date(2024, 5, 7, 0, 0, 0, 0, time.UTC)
	entry := &memory.Entry{
		ID: "mem-1",
		Memory: &memory.Memory{
			Memory:       "User started painting in 2022",
			Kind:         memory.KindFact,
			EventTime:    &eventTime,
			Participants: []string{"Alice"},
			Location:     "Tokyo",
		},
	}

	got := formatExistingMemory(entry)

	assert.Contains(t, got, "kind=fact")
	assert.Contains(t, got, "event_time=2024-05-07")
	assert.Contains(t, got, "participants=Alice")
	assert.Contains(t, got, "location=Tokyo")
}
