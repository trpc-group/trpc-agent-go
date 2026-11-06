//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summarytoken

import (
	"context"
	"sync"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestConcurrentReplacementAndCounting(t *testing.T) {
	t.Cleanup(func() { Set(nil) })
	custom := model.NewSimpleTokenCounter(model.WithApproxRunesPerToken(1.5))
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				Set(custom)
				tokens, err := Get().CountTokens(context.Background(), model.NewUserMessage("你好世界你好世界你好世界"))
				if err != nil || (tokens != 3 && tokens != 8) {
					t.Errorf("count during replacement = %d, %v; want either complete counter", tokens, err)
				}
				Set(nil)
			}
		}()
	}
	wg.Wait()
}
