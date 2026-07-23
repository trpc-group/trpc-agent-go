//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety_test

import (
	"os"
	"path/filepath"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
)

func TestClampArtifacts(t *testing.T) {
	dir := t.TempDir()
	small := filepath.Join(dir, "small.txt")
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(small, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(big, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	limits := safety.DefaultLimits()
	limits.MaxArtifactFiles = 1
	limits.MaxArtifactFileBytes = 50
	limits.MaxArtifactTotalBytes = 50

	in := []review.ArtifactRef{
		{Name: "big", PathOrRef: big},
		{Name: "small", PathOrRef: small},
	}
	kept, dropped := safety.ClampArtifacts(in, limits)
	if dropped < 1 {
		t.Fatalf("expected drops, kept=%d dropped=%d", len(kept), dropped)
	}
	if len(kept) > 1 {
		t.Fatalf("kept=%d", len(kept))
	}
}
