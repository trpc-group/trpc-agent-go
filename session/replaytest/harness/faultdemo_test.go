//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backends"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/harness"
)

func TestRunFaultDemoSurfacesInconsistentRow(t *testing.T) {
	bs, err := backends.EnabledBackends(harness.NewMockSummarizer())
	require.NoError(t, err)
	defer func() {
		for _, b := range bs {
			_ = b.Close()
		}
	}()

	c := &harness.ReplayCase{
		Name: "demo", Key: harness.CaseKey{AppName: "a", UserID: "u", SessionID: "s"},
		FaultInjection: backends.FaultDuplicateEvent,
		Operations: []harness.Operation{
			{Type: "append_event", Event: &harness.EventSpec{Author: "user", Role: "user", Content: "hi"}},
		},
	}
	cr, err := harness.RunFaultDemo(context.Background(), bs, c)
	require.NoError(t, err)

	found := false
	for _, r := range cr.Results {
		if r.Verdict == harness.VerdictInconsistent {
			found = true
		}
	}
	require.True(t, found, "fault demo must yield an inconsistent row, got %+v", cr.Results)
}

func TestRunFaultDemoRequiresCompareBackend(t *testing.T) {
	bs, err := backends.EnabledBackends(harness.NewMockSummarizer())
	require.NoError(t, err)
	defer func() {
		for _, b := range bs {
			_ = b.Close()
		}
	}()
	_, err = harness.RunFaultDemo(context.Background(), bs[:1], &harness.ReplayCase{Name: "x"})
	require.Error(t, err)
}
