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

func TestMemoryCaseDefined(t *testing.T) {
	if Case05_Memory == nil ||
		Case05_Memory.Name == "" ||
		len(Case05_Memory.Writes) == 0 ||
		Case05_Memory.SearchQuery == "" {
		t.Fatalf("memory case 定义不完整: %+v", Case05_Memory)
	}
}
