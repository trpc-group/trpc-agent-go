//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeInnerTextMode(t *testing.T) {
	require.Equal(
		t,
		InnerTextModeInclude,
		NormalizeInnerTextMode(InnerTextModeDefault),
	)
	require.Equal(
		t,
		InnerTextModeInclude,
		NormalizeInnerTextMode(InnerTextModeInclude),
	)
	require.Equal(
		t,
		InnerTextModeExclude,
		NormalizeInnerTextMode(InnerTextModeExclude),
	)
	require.Equal(
		t,
		InnerTextModeInclude,
		NormalizeInnerTextMode(InnerTextMode("unexpected")),
	)
}
