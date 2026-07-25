//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"strings"
	"testing"
)

func TestValidatePositionalArgs(t *testing.T) {
	t.Run("accepts no positional arguments", func(t *testing.T) {
		if err := validatePositionalArgs(nil); err != nil {
			t.Fatalf("validate positional arguments: %v", err)
		}
	})

	t.Run("rejects unexpected positional arguments", func(t *testing.T) {
		err := validatePositionalArgs([]string{"/tmp/accepted_prompt.txt"})
		if err == nil {
			t.Fatal("expected positional argument validation error")
		}
		if !strings.Contains(err.Error(), "unexpected positional argument") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
