//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInterruptErrorFormattingAndHelpers(t *testing.T) {
	ie := &InterruptError{NodeID: "N", Step: 2, Value: "ask", Timestamp: time.Now().UTC()}
	msg := ie.Error()
	require.Contains(t, msg, "graph interrupted at node N")
	require.Contains(t, msg, "step 2")

	var err error = ie
	require.True(t, IsInterruptError(err))
	got, ok := GetInterruptError(err)
	require.True(t, ok)
	require.Equal(t, "ask", got.Value)

	require.False(t, IsInterruptError(errors.New("x")))
	_, ok = GetInterruptError(errors.New("x"))
	require.False(t, ok)
}

func TestResumeCommandBuilder(t *testing.T) {
	rc := NewResumeCommand().WithResume("v").WithResumeMap(map[string]any{"a": 1}).AddResumeValue("b", 2)
	require.Equal(t, "v", rc.Resume)
	require.Equal(t, 1, rc.ResumeMap["a"]) //nolint:forcetypeassert
	require.Equal(t, 2, rc.ResumeMap["b"]) //nolint:forcetypeassert
}

func TestResumeCommand_AddResumeValue_CreatesMap(t *testing.T) {
	c := &ResumeCommand{}
	c.AddResumeValue("t1", 123)
	if c.ResumeMap == nil || c.ResumeMap["t1"].(int) != 123 {
		t.Fatalf("resume map not created or value incorrect: %#v", c.ResumeMap)
	}
	// Non-nil path
	c.AddResumeValue("t2", 456)
	if c.ResumeMap["t2"].(int) != 456 {
		t.Fatalf("resume map missing t2")
	}
}

func TestHasResumeValue_Variants(t *testing.T) {
	st := State{}
	if HasResumeValue(st, "k") {
		t.Fatalf("expected false initially")
	}
	// Direct resume channel
	st[ResumeChannel] = 1
	if !HasResumeValue(st, "k") {
		t.Fatalf("expected true with direct resume channel")
	}
	delete(st, ResumeChannel)
	// ResumeMap wrong type ignored
	st[StateKeyResumeMap] = []int{1}
	if HasResumeValue(st, "k") {
		t.Fatalf("expected false for wrong resume map type")
	}
	// ResumeMap correct type with key
	st[StateKeyResumeMap] = map[string]any{"k": 2}
	if !HasResumeValue(st, "k") {
		t.Fatalf("expected true for resume map entry")
	}
}

func TestInterrupt_ResumePaths(t *testing.T) {
	ctx := context.Background()
	st := State{}
	// First call interrupts
	_, err := Interrupt(ctx, st, "k", "prompt")
	require.Error(t, err)
	intr, ok := GetInterruptError(err)
	require.True(t, ok)
	require.Equal(t, "k", intr.TaskID)
	// Provide direct resume value
	st[ResumeChannel] = "answer"
	v, err := Interrupt(ctx, st, "k", "prompt")
	if err != nil || v != "answer" {
		t.Fatalf("expected resume: %v %v", v, err)
	}
	// Reuse same key should return stored used value without consuming new
	v2, err := Interrupt(ctx, st, "k", "prompt")
	if err != nil || v2 != "answer" {
		t.Fatalf("expected repeated resume value: %v %v", v2, err)
	}
}
