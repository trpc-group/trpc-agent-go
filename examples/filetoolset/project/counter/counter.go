//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// package counter implements a counter.
package counter

func GetCounter(n int) int {
	counter := 0
	for i := 0; i < n; i++ {
		go func() {
			counter++
		}()
	}
	return counter
}
