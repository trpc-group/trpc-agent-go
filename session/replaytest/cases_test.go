//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllCases(t *testing.T) {
	cases := AllCases()
	require.Len(t, cases, 11)
	seen := map[string]bool{}
	for _, tc := range cases {
		require.NotEmpty(t, tc.Name)
		require.NotEmpty(t, tc.Steps)
		require.False(t, seen[tc.Name])
		seen[tc.Name] = true
	}
}

func TestCasesInjectFault(t *testing.T) {
	for _, tc := range AllCases() {
		a := testSnapshot("a", "hello", "answer")
		b := testSnapshot("b", "hello", "fault-"+tc.Name)
		result := NewComparator().Compare(a, b, tc.AllowedDiffs, InMemoryProfile(), InMemoryProfile())
		require.Equal(t, StatusFailed, result.Status, tc.Name)
	}
}
