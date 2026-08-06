// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package replaytest

import (
	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// BackendFactory creates session and memory services with a capability profile.
type BackendFactory func() (session.Service, memory.Service, BackendProfile, error)

// InMemoryFactory returns a factory for built-in in-memory backends with a
// deterministic FakeSummarizer installed.
func InMemoryFactory() BackendFactory {
	return func() (session.Service, memory.Service, BackendProfile, error) {
		sess := sessioninmemory.NewSessionService(
			sessioninmemory.WithSummarizer(NewFakeSummarizer()),
		)
		mem := memoryinmemory.NewMemoryService()
		return sess, mem, InMemoryProfile(), nil
	}
}
