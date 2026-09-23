//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summarycontext

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInheritRecordersPreferCurrentAndMaskStaleNext(t *testing.T) {
	var nextCall, currentCall ModelCall
	var nextTrigger, currentTrigger TriggerObservation
	var nextSelection, currentSelection EventSelection
	next := WithModelCallRecorder(context.Background(), &nextCall)
	next = WithTriggerRecorder(next, &nextTrigger)
	next = WithEventSelectionRecorder(next, &nextSelection)
	current := WithModelCallRecorder(context.Background(), &currentCall)
	current = WithTriggerRecorder(current, &currentTrigger)
	current = WithEventSelectionRecorder(current, &currentSelection)

	got := InheritModelCallRecorder(next, current)
	got = InheritTriggerRecorder(got, current)
	got = InheritEventSelectionRecorder(got, current)
	require.Same(t, &currentCall, ModelCallFromContext(got))
	require.Same(t, &currentTrigger, TriggerFromContext(got))
	require.Same(t, &currentSelection, EventSelectionFromContext(got))

	RecordModelCall(got, "standalone")
	RecordTrigger(got, TriggerObservation{Name: "token_threshold"})
	RecordEventSelection(got, EventSelection{Reason: ReasonSelected, Selected: 2})
	require.Equal(t, "standalone", currentCall.Mode)
	require.Equal(t, "token_threshold", currentTrigger.Name)
	require.Equal(t, 2, currentSelection.Selected)
	require.Empty(t, nextCall.Mode)
	require.Empty(t, nextTrigger.Name)
	require.Zero(t, nextSelection.Selected)
}

func TestInheritRecordersMaskNextWhenCurrentHasNone(t *testing.T) {
	var nextCall ModelCall
	var nextTrigger TriggerObservation
	var nextSelection EventSelection
	next := WithModelCallRecorder(context.Background(), &nextCall)
	next = WithTriggerRecorder(next, &nextTrigger)
	next = WithEventSelectionRecorder(next, &nextSelection)

	got := InheritModelCallRecorder(next, context.Background())
	got = InheritTriggerRecorder(got, context.Background())
	got = InheritEventSelectionRecorder(got, context.Background())
	require.Nil(t, ModelCallFromContext(got))
	require.Nil(t, TriggerFromContext(got))
	require.Nil(t, EventSelectionFromContext(got))

	RecordModelCall(got, "standalone")
	RecordTrigger(got, TriggerObservation{Name: "force"})
	RecordEventSelection(got, EventSelection{Reason: ReasonSelected, Selected: 3})
	require.Empty(t, nextCall.Mode)
	require.Empty(t, nextTrigger.Name)
	require.Zero(t, nextSelection.Selected)
}

func TestInheritRecordersReuseMatchingContext(t *testing.T) {
	var call ModelCall
	var trigger TriggerObservation
	var selection EventSelection
	current := WithModelCallRecorder(context.Background(), &call)
	current = WithTriggerRecorder(current, &trigger)
	current = WithEventSelectionRecorder(current, &selection)
	for _, parent := range []context.Context{current, context.Background()} {
		next, cancel := context.WithCancel(parent)
		require.Same(t, next, InheritModelCallRecorder(next, parent))
		require.Same(t, next, InheritTriggerRecorder(next, parent))
		require.Same(t, next, InheritEventSelectionRecorder(next, parent))
		cancel()
	}
}

func TestInheritModelCallRecorderIsolatesConcurrentWrites(t *testing.T) {
	var nextCall, currentCall ModelCall
	next := WithModelCallRecorder(context.Background(), &nextCall)
	current := WithModelCallRecorder(context.Background(), &currentCall)
	inherited := InheritModelCallRecorder(next, current)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 64; i++ {
			RecordModelCall(inherited, "standalone")
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 64; i++ {
			RecordModelCall(next, "custom_response")
		}
	}()
	wg.Wait()
	require.Equal(t, "standalone", currentCall.Mode)
	require.Equal(t, "custom_response", nextCall.Mode)
}
