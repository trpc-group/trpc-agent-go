//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package compare

import (
	"fmt"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/normalize"
)

func MakeMemoryDiff(
	a *normalize.MemorySnapshot,
	b *normalize.MemorySnapshot,
) map[string]string {
	diff := make(map[string]string)
	if a == b {
		return diff
	}
	if a == nil || b == nil {
		diff["memory_snapshot"] = fmt.Sprintf("a: %+v, b: %+v", a, b)
		return diff
	}
	if !reflect.DeepEqual(a.Read, b.Read) {
		diff["memory.read"] = fmt.Sprintf("a: %+v, b: %+v", a.Read, b.Read)
	}
	if !reflect.DeepEqual(a.Search, b.Search) {
		diff["memory.search"] = fmt.Sprintf(
			"a: %+v, b: %+v",
			a.Search,
			b.Search,
		)
	}
	return diff
}
