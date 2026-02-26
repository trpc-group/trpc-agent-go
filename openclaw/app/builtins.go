//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import "trpc.group/trpc-go/trpc-agent-go/openclaw/registry"

func init() {
	must(registry.RegisterModel(modeMock, newMockModel))
	must(registry.RegisterModel(modeOpenAI, newOpenAIModel))

	must(registry.RegisterSessionBackend(
		sessionBackendInMemory,
		newInMemorySessionBackend,
	))
	must(registry.RegisterSessionBackend(
		sessionBackendRedis,
		newRedisSessionBackend,
	))
	must(registry.RegisterSessionBackend(
		sessionBackendMySQL,
		newMySQLSessionBackend,
	))
	must(registry.RegisterSessionBackend(
		sessionBackendPostgres,
		newPostgresSessionBackend,
	))
	must(registry.RegisterSessionBackend(
		sessionBackendClickHouse,
		newClickHouseSessionBackend,
	))

	must(registry.RegisterMemoryBackend(
		memoryBackendInMemory,
		newInMemoryMemoryBackend,
	))
	must(registry.RegisterMemoryBackend(
		memoryBackendRedis,
		newRedisMemoryBackend,
	))
	must(registry.RegisterMemoryBackend(
		memoryBackendMySQL,
		newMySQLMemoryBackend,
	))
	must(registry.RegisterMemoryBackend(
		memoryBackendPostgres,
		newPostgresMemoryBackend,
	))
	must(registry.RegisterMemoryBackend(
		memoryBackendPGVector,
		newPGVectorMemoryBackend,
	))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
