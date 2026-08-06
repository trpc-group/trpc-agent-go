//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package assistantmemory

import "testing"

func TestIsText(t *testing.T) {
	if !IsText("  " + Prefix + "recommended Alpha") {
		t.Fatal("assistant episode marker was not recognized")
	}
	if IsText("User prefers Alpha") {
		t.Fatal("ordinary memory was recognized as an assistant episode")
	}
}
