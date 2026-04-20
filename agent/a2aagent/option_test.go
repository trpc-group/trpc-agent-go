//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2aagent

import (
	"testing"

	"trpc.group/trpc-go/trpc-a2a-go/protocol"
)

func TestWithStreamingChannelBufSize(t *testing.T) {
	tests := []struct {
		name        string
		inputSize   int
		wantBufSize int
	}{
		{
			name:        "positive buffer size",
			inputSize:   1024,
			wantBufSize: 1024,
		},
		{
			name:        "zero buffer size",
			inputSize:   0,
			wantBufSize: 0,
		},
		{
			name:        "negative size uses default",
			inputSize:   -1,
			wantBufSize: defaultStreamingChannelSize,
		},
		{
			name:        "large buffer size",
			inputSize:   65536,
			wantBufSize: 65536,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent := &A2AAgent{}

			option := WithStreamingChannelBufSize(tt.inputSize)
			option(agent)

			if agent.streamingBufSize != tt.wantBufSize {
				t.Errorf("got buf size %d, want %d", agent.streamingBufSize, tt.wantBufSize)
			}
		})
	}
}

func TestWithA2ADataPartMapper(t *testing.T) {
	agent := &A2AAgent{}
	WithA2ADataPartMapper(func(part *protocol.DataPart, result *A2ADataPartMappingResult) (bool, error) {
		return false, nil
	})(agent)

	if len(agent.dataPartMappers) != 1 {
		t.Fatalf("expected one data part mapper, got %d", len(agent.dataPartMappers))
	}
}
