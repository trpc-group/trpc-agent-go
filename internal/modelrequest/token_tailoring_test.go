//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package modelrequest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenTailoringObserver(t *testing.T) {
	var callbacks []TokenTailoringRecord
	ctx, observer := ObserveTokenTailoring(
		context.Background(),
		func(record TokenTailoringRecord) {
			callbacks = append(callbacks, record)
		},
	)
	record := TokenTailoringRecord{
		Provider:       "test.Model",
		MaxInputTokens: 100,
		BeforeMessages: 3,
		AfterMessages:  2,
	}

	RecordTokenTailoring(ctx, record)

	require.Equal(t, []TokenTailoringRecord{record}, observer.Snapshot())
	require.Equal(t, []TokenTailoringRecord{record}, callbacks)
	copyOfSnapshot := observer.Snapshot()
	copyOfSnapshot[0].Provider = "mutated"
	require.Equal(t, "test.Model", observer.Snapshot()[0].Provider)
}

func TestTokenTailoringObserverHandlesMissingObserver(t *testing.T) {
	RecordTokenTailoring(nil, TokenTailoringRecord{})
	RecordTokenTailoring(context.Background(), TokenTailoringRecord{})
	ctx, observer := ObserveTokenTailoring(nil, nil)
	RecordTokenTailoring(ctx, TokenTailoringRecord{Provider: "test.Model"})
	require.Len(t, observer.Snapshot(), 1)
	require.Nil(t, (*TokenTailoringObserver)(nil).Snapshot())
}
