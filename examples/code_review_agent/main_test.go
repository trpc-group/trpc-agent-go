//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import "testing"

func TestModelAPIKeyFromEnvironment(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	if got := modelAPIKeyFromEnvironment(); got != "" {
		t.Fatalf("modelAPIKeyFromEnvironment() = %q, want empty string", got)
	}

	const want = "test-api-key"
	t.Setenv("OPENAI_API_KEY", want)
	if got := modelAPIKeyFromEnvironment(); got != want {
		t.Fatalf("modelAPIKeyFromEnvironment() = %q, want %q", got, want)
	}
}
