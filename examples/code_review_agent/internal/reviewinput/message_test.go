// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package reviewinput

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildReviewMessageHonorsUTF8ByteBudget(t *testing.T) {
	parsed := parsedInput{
		ChangedFiles: []ChangedFile{{Path: "你好.go"}},
		ChangedHunks: []ChangedHunk{{
			ID:   "你好.go:1:1",
			File: "你好.go",
			Body: "+" + strings.Repeat("界", 100),
		}},
	}
	limits := Limits{MaxMessageBytes: 256, MaxFiles: 1, MaxHunks: 1, MaxHunkBytes: 64}
	message := buildReviewMessage(InputKindDiffFile, ReviewModePatchOnly, parsed, limits)
	if len(message) > limits.MaxMessageBytes {
		t.Fatalf("message length = %d, want at most %d", len(message), limits.MaxMessageBytes)
	}
	if !utf8.ValidString(message) {
		t.Fatal("message truncation produced invalid UTF-8")
	}
}
