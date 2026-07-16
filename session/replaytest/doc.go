// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package replaytest provides a reusable multi-backend replay consistency
// harness for session, memory, summary, and track services.
//
// Lightweight mode runs InMemory by default. SQLite adapters live in the
// separate session/replaytest/sqlite module so the root module does not force
// a CGO dependency. Optional backends can be gated by environment variables
// such as REPLAYTEST_REDIS_ADDR.
//
// Example:
//
//	h := replaytest.NewHarness(replaytest.DefaultHarnessOpts())
//	sess, mem, profile, err := replaytest.InMemoryFactory()()
//	if err != nil {
//		return err
//	}
//	defer sess.Close()
//	defer mem.Close()
//	h.AddBackend(replaytest.NamedBackend{
//		Name: "inmemory", Profile: profile,
//		SessionService: sess, MemoryService: mem,
//	})
//	report, err := h.Run(context.Background(), replaytest.AllCases())
//	if err != nil {
//		return err
//	}
//	_ = report
//
// Design notes: see DESIGN.md in this directory.
package replaytest
