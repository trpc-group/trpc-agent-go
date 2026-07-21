//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package replaytest provides a replay-consistency test harness for session
// and memory backends.
//
// The same deterministic case script is replayed against multiple backends
// (an in-memory reference and one or more persistent candidates). After
// replay, each backend is read back into a snapshot covering events, state,
// memories, summaries and tracks. Snapshots are normalized to remove
// semantically irrelevant noise (timestamps, generated IDs, JSON formatting)
// and then compared field by field. Any difference outside the explicit
// allowed-diff whitelist is reported as an inconsistency.
//
// See README.md for the full design summary (~300 chars), the comparison
// rules and backend onboarding instructions.
package replaytest
