// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"testing"
	"time"
)

func TestSteps_TypeAndKey(t *testing.T) {
	key := SessionKeyFor("steps_meta")
	uk := UserKeyDefault()
	muk := MemoryUserKeyDefault()
	tests := []struct {
		step Step
		typ  string
		k    string
	}{
		{AppendEventStep{StepKey: "a", SessionKey: key, Event: UserEvent("a", "x")}, "append_event", "a"},
		{UpdateStateStep{StepKey: "u", Scope: "session", SessionKey: key}, "update_state", "u"},
		{AddMemoryStep{StepKey: "m", UserKey: muk, Memory: "x"}, "add_memory", "m"},
		{CaptureMemoryStep{StepKey: "cm", UserKey: muk}, "capture_memory", "cm"},
		{CreateSummaryStep{StepKey: "cs", SessionKey: key}, "create_summary", "cs"},
		{WaitSummaryStep{StepKey: "ws", SessionKey: key, Timeout: time.Millisecond}, "wait_summary", "ws"},
		{AppendTrackStep{StepKey: "at", SessionKey: key, Event: TrackPayload("tool", `{}`)}, "append_track", "at"},
		{GetSessionStep{StepKey: "gs", SessionKey: key}, "get_session", "gs"},
		{ListAppStatesStep{StepKey: "la", AppName: DefaultApp}, "list_app_states", "la"},
		{ListUserStatesStep{StepKey: "lu", UserKey: uk}, "list_user_states", "lu"},
		{ReloadSessionStep{StepKey: "rs", SessionKey: key}, "reload_session", "rs"},
		{ParallelGroupStep{StepKey: "pg", Branches: [][]Step{}}, "parallel_group", "pg"},
	}
	for _, tt := range tests {
		if got := tt.step.Type(); got != tt.typ {
			t.Errorf("%T Type=%q want %q", tt.step, got, tt.typ)
		}
		if got := tt.step.Key(); got != tt.k {
			t.Errorf("%T Key=%q want %q", tt.step, got, tt.k)
		}
	}
}
