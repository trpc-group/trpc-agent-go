//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package compare provides snapshot and memory comparison helpers for replay tests.
package compare

import "trpc.group/trpc-go/trpc-agent-go/session/replaytest/normalize"

// CompareDeep reports whether two normalized snapshots are deeply equal.
func CompareDeep(a, b *normalize.SnapShot) bool {
	// true 表示两边一致
	return len(MakeDiff(a, b)) == 0
}
