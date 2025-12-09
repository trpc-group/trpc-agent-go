//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//
//

package event

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestEvent_Clone_DeepCopy(t *testing.T) {
	e := &Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
		},
		InvocationID:       "inv-1",
		Author:             "tester",
		LongRunningToolIDs: map[string]struct{}{"a": {}, "b": {}},
		StateDelta:         map[string][]byte{"k": []byte("v")},
	}

	c := e.Clone()
	if c == nil || c == e {
		t.Fatalf("expected a distinct clone instance")
	}
	// Mutate clone and ensure original not affected.
	c.LongRunningToolIDs["c"] = struct{}{}
	c.StateDelta["k"][0] = 'x'
	if _, ok := e.LongRunningToolIDs["c"]; ok {
		t.Errorf("original LongRunningToolIDs mutated by clone")
	}
	if string(e.StateDelta["k"]) == string(c.StateDelta["k"]) {
		t.Errorf("expected deep copy of StateDelta")
	}
}

func TestEvent_Clone_LegacyVersionMigratesFilterKey(t *testing.T) {
	e := &Event{
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Done:   true,
		},
		InvocationID: "inv-legacy",
		Author:       "tester",
		Branch:       "legacy/branch",
		Version:      InitVersion,
	}

	clone := e.Clone()
	if clone == nil {
		t.Fatalf("expected clone to be non-nil")
	}
	if clone.Version != CurrentVersion {
		t.Fatalf("expected clone version %d, got %d", CurrentVersion, clone.Version)
	}
	if clone.FilterKey != e.Branch {
		t.Fatalf("expected clone filter key %q from legacy branch, got %q", e.Branch, clone.FilterKey)
	}
	if clone.Branch != e.Branch {
		t.Fatalf("expected clone branch to remain %q, got %q", e.Branch, clone.Branch)
	}
	if clone.ID == e.ID {
		t.Fatalf("expected clone to generate a new ID")
	}
}
