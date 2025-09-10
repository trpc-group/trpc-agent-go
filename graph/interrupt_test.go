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
