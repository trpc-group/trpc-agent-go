//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package scenario

import "testing"

func TestSessionCasesDefined(t *testing.T) {
	cases := []*Case{
		Case01_SingleTurn,
		Case02_MultiTurn,
		Case03_UpdateState,
		Case04_ToolCall,
		Case06_Summary,
		Case06_SummaryFilterKey,
		Case07_SummaryWithTruncation,
		Case08_Track,
		Case09_ConcurrentAppend,
		Case10_Recovery,
	}
	for _, c := range cases {
		if c == nil || c.Name == "" || len(c.Ops) == 0 {
			t.Fatalf("case 定义不完整: %+v", c)
		}
	}
}
