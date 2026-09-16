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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecordTrigger(t *testing.T) {
	RecordTrigger(nil, TriggerObservation{Name: "token_threshold"})
	RecordTrigger(context.Background(), TriggerObservation{Name: "token_threshold"})

	var obs TriggerObservation
	ctx := WithTriggerRecorder(nil, &obs)
	RecordTrigger(ctx, TriggerObservation{Name: "event_threshold", CheckCount: 1})
	require.True(t, obs.Published)
	require.Equal(t, "event_threshold", obs.Name)
	require.Equal(t, 1, obs.CheckCount)

	RecordTrigger(WithTriggerRecorder(context.Background(), nil), TriggerObservation{Name: "force"})
	require.Equal(t, "event_threshold", obs.Name)
}
