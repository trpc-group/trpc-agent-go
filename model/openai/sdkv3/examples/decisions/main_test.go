//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import "testing"

func TestNewDecider(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", "http://localhost/v1")
	t.Setenv("SYSTEMONE_BASE_URL", "http://localhost")
	for _, backend := range []string{"openai", "systemone"} {
		client, err := newDecider(backend, "")
		if err != nil {
			t.Fatalf("newDecider(%q): %v", backend, err)
		}
		if client == nil {
			t.Fatalf("newDecider(%q) returned a nil client", backend)
		}
	}
	if _, err := newDecider("unknown", ""); err == nil {
		t.Fatal("expected an error for an unknown backend")
	}
	t.Setenv("SYSTEMONE_BASE_URL", "")
	if _, err := newDecider("systemone", ""); err == nil {
		t.Fatal("expected an error without SYSTEMONE_BASE_URL")
	}
}
