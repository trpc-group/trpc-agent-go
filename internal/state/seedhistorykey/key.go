//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package seedhistorykey defines the dependency-free invocation state key used
// to share seed-history provenance across framework layers.
package seedhistorykey

// Key identifies the invocation-scoped seed-history event ID set.
const Key = "__seed_history_event_ids__"
