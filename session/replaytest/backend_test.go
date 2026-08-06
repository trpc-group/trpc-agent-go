// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import "testing"

func TestInMemoryFactory(t *testing.T) {
	sess, mem, profile, err := InMemoryFactory()()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sess.Close()
		if mem != nil {
			_ = mem.Close()
		}
	})
	if profile.Name != "inmemory" {
		t.Fatalf("profile=%s", profile.Name)
	}
	if !profile.SupportsMemory || !profile.SupportsTrack {
		t.Fatalf("unexpected profile: %+v", profile)
	}
}
