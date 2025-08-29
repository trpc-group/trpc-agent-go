//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// package counter implements a counter.
package counter

import "sync/atomic"

func GetCounter(n int) int {
	var counter int64
	for i := 0; i < n; i++ {
		go func() {
			atomic.AddInt64(&counter, 1)
		}()
	}
	return int(counter)
}
