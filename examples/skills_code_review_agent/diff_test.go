//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseUnifiedDiff(t *testing.T) {
	const patch = `diff --git a/internal/server/server.go b/internal/server/server.go
index 1111111..2222222 100644
--- a/internal/server/server.go
+++ b/internal/server/server.go
@@ -8,2 +8,4 @@ package server
 import "context"
+func run(ctx context.Context) {
+	go work()
 }
`
	got, err := ParseUnifiedDiff([]byte(patch))
	require.NoError(t, err)
	require.Len(t, got.Files, 1)
	require.Equal(t, "internal/server/server.go", got.Files[0].Path)
	require.Equal(t, "server", got.Files[0].Package)
	require.Len(t, got.Files[0].Hunks, 1)
	require.Equal(t, 9, got.Files[0].Hunks[0].Lines[1].NewLine)
	require.Equal(t, byte('+'), got.Files[0].Hunks[0].Lines[1].Kind)
}

func TestParseUnifiedDiffRejectsUnsafePath(t *testing.T) {
	const patch = `diff --git a/a.go b/../../etc/passwd
--- a/a.go
+++ b/../../etc/passwd
@@ -1 +1 @@
-old
+new
`
	_, err := ParseUnifiedDiff([]byte(patch))
	require.ErrorContains(t, err, "unsafe diff path")
}

func TestParseUnifiedDiffLimitsInput(t *testing.T) {
	_, err := ParseUnifiedDiff([]byte(strings.Repeat("x", maxDiffBytes+1)))
	require.ErrorContains(t, err, "exceeds")
}
