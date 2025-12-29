//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package chat

import (
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type stubRunner struct{}

func (stubRunner) Run(
	_ context.Context,
	_ string,
	_ string,
	_ model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	eventChannel := make(chan *event.Event)
	close(eventChannel)
	return eventChannel, nil
}

func (stubRunner) Close() error { return nil }

func TestRun_Validation(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		cfg  LoopConfig
	}{
		{
			name: "nil runner",
			cfg: LoopConfig{
				Runner:    nil,
				UserID:    "user",
				SessionID: "session",
				Timeout:   time.Second,
			},
		},
		{
			name: "empty user id",
			cfg: LoopConfig{
				Runner:    stubRunner{},
				UserID:    "",
				SessionID: "session",
				Timeout:   time.Second,
			},
		},
		{
			name: "empty session id",
			cfg: LoopConfig{
				Runner:    stubRunner{},
				UserID:    "user",
				SessionID: "",
				Timeout:   time.Second,
			},
		},
		{
			name: "non-positive timeout",
			cfg: LoopConfig{
				Runner:    stubRunner{},
				UserID:    "user",
				SessionID: "session",
				Timeout:   0,
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := Run(ctx, test.cfg); err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}
