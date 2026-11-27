//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package summarycontext

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecordModelCall(t *testing.T) {
	RecordModelCall(nil, "standalone")
	RecordModelCall(context.Background(), "standalone")

	var call ModelCall
	ctx := WithModelCallRecorder(nil, &call)
	RecordModelCall(ctx, "standalone")
	require.Equal(t, "standalone", call.Mode)

	RecordModelCall(ctx, "cache_safe_fork")
	require.Equal(t, "cache_safe_fork", call.Mode)

	RecordModelCall(ctx, "custom_response")
	require.Equal(t, "cache_safe_fork", call.Mode,
		"a later custom_response must not hide a provider call")

	var customFirst ModelCall
	customCtx := WithModelCallRecorder(context.Background(), &customFirst)
	RecordModelCall(customCtx, "custom_response")
	RecordModelCall(customCtx, "standalone")
	require.Equal(t, "standalone", customFirst.Mode,
		"a later provider call upgrades custom_response to called")

	RecordModelCall(WithModelCallRecorder(context.Background(), nil), "custom_response")
	require.Equal(t, "cache_safe_fork", call.Mode)
}
