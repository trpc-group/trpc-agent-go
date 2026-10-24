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
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestTokenTailoringObserver(t *testing.T) {
	var callbacks []TokenTailoringChange
	ctx, observer := ObserveTokenTailoring(
		context.Background(),
		func(change TokenTailoringChange) {
			callbacks = append(callbacks, change)
		},
	)
	record := TokenTailoringRecord{
		Provider:       "test.Model",
		MaxInputTokens: 100,
		BeforeMessages: 3,
		AfterMessages:  2,
	}

	change := TokenTailoringChange{
		Record: record,
		Before: []model.Message{model.NewUserMessage("before")},
		After:  []model.Message{model.NewUserMessage("after")},
	}
	RecordTokenTailoringChange(ctx, change)

	require.Equal(t, []TokenTailoringRecord{record}, observer.Snapshot())
	require.Equal(t, []TokenTailoringChange{change}, callbacks)
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

func TestTokenTailoringProvenanceString(t *testing.T) {
	require.Equal(t, "unknown", TokenTailoringProvenanceUnknown.String())
	require.Equal(t, "preserved", TokenTailoringProvenancePreserved.String())
	require.Equal(t, "dropped", TokenTailoringProvenanceDropped.String())
	require.Equal(t, "unknown", TokenTailoringProvenance(255).String())
}
