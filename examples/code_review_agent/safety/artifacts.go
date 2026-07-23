//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"os"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
)

// ClampArtifacts enforces artifact count/size limits.
// Oversized or excess artifacts are dropped; dropped count is returned.
func ClampArtifacts(arts []review.ArtifactRef, limits Limits) (kept []review.ArtifactRef, dropped int) {
	maxFiles := limits.MaxArtifactFiles
	if maxFiles <= 0 {
		maxFiles = 32
	}
	maxFile := limits.MaxArtifactFileBytes
	if maxFile <= 0 {
		maxFile = 2 << 20
	}
	maxTotal := limits.MaxArtifactTotalBytes
	if maxTotal <= 0 {
		maxTotal = 8 << 20
	}

	var total int64
	for _, a := range arts {
		if len(kept) >= maxFiles {
			dropped++
			continue
		}
		size := a.SizeBytes
		if size <= 0 && a.PathOrRef != "" {
			if st, err := os.Stat(a.PathOrRef); err == nil {
				size = st.Size()
			}
		}
		if size > maxFile {
			dropped++
			continue
		}
		if total+size > maxTotal {
			dropped++
			continue
		}
		a.SizeBytes = size
		kept = append(kept, a)
		total += size
	}
	return kept, dropped
}
