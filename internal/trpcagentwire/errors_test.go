//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package trpcagentwire

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestDirectRunErrorKinds(t *testing.T) {
	tests := []struct {
		name string
		err  error
		kind DirectRunErrorKind
	}{
		{
			name: "invalid",
			err:  session.ErrInvalidRewindRequest,
			kind: directRunErrorLatestTurnReplacementInvalid,
		},
		{
			name: "unsupported",
			err:  session.ErrRewindUnsupported,
			kind: directRunErrorLatestTurnReplacementUnsupported,
		},
		{
			name: "conflict",
			err:  session.ErrRewindConflict,
			kind: directRunErrorLatestTurnReplacementConflict,
		},
		{
			name: "unavailable",
			err:  session.ErrRewindUnavailable,
			kind: directRunErrorLatestTurnReplacementUnavailable,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind := DirectRunErrorKindOf(fmt.Errorf("wrapped: %w", tt.err))
			assert.Equal(t, tt.kind, kind)
			require.Error(t, kind.Sentinel())
			assert.ErrorIs(t, kind.Sentinel(), tt.err)
		})
	}
}

func TestDirectRunErrorKindUnknown(t *testing.T) {
	assert.Empty(t, DirectRunErrorKindOf(nil))
	assert.Empty(t, DirectRunErrorKindOf(errors.New("other")))
	assert.NoError(t, DirectRunErrorKind("future_kind").Sentinel())
}
